// Package daemon implements the cosmonaut menu bar applet which
// hosts a system tray icon, global hotkey listener, and codespace
// lifecycle management.
package daemon

import (
	"log"
	"os"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/provider"
)

// autoPollMinInterval is the shortest gap allowed between event-driven
// polls. Tray-open and window-focus events fire often; this debounce
// keeps us from re-hitting the backends on every interaction.
const autoPollMinInterval = 30 * time.Second

// Daemon is the long-running background process that hosts the system tray,
// hotkey listener, and codespace poller.
type Daemon struct {
	Cfg        *config.Config
	ConfigPath string
	Runner     codespace.GHRunner

	app fyne.App

	mu             sync.Mutex
	codespaces     []codespace.Codespace
	workspaces     []provider.Workspace
	portCache      map[string]portCacheEntry
	listErr        error
	providerStatus map[string]ProviderStatus
	lastPollAt     time.Time
	pollInFlight   bool
	pollCond       *sync.Cond  // signals when a poll slot frees; broadcast under mu
	trayOpenedAt   time.Time   // last time the tray menu was opened; zero before first open
	lastApplyAt    time.Time   // last time applyTrayMenu actually ran; zero before first apply
	pendingRebuild bool        // a rebuild was deferred while the tray was in-use
	rebuildTimer   *time.Timer // wakes up to retry a deferred rebuild after the gate window
	stopCh         chan struct{}
	sessions       *SessionTracker
	forwards       *PortForwardManager

	// nowFunc returns the current time. Overridable for tests so the
	// tray rebuild gate can be exercised without sleeping in real time.
	nowFunc func() time.Time
	// applyTrayMenuFunc, when non-nil, replaces the real Fyne SetSystemTrayMenu
	// call. Used by tests to observe apply events without a Fyne app.
	applyTrayMenuFunc func()

	dismissMu sync.Mutex
	dismissed map[string]bool

	uwMu     sync.Mutex
	activeUW *unifiedWindow

	// Workspace-change listeners. Fired on the Fyne main thread when a
	// poll lands and the workspace set has actually changed (see
	// workspacesDiffer). Used by the GUI sidebar to refresh its tree
	// without requiring the user to click a refresh button.
	listenersMu  sync.Mutex
	listeners    map[int]func()
	nextListener int

	// Theme-change listeners: fired when the OS flips light/dark, so
	// canvas primitives (which snapshot color at construction) get rebuilt.
	themeMu           sync.Mutex
	themeListeners    map[int]func()
	nextThemeListener int
}

// setActiveUnifiedWindow records the currently-open main window so other
// surfaces (e.g. the Settings page) can trigger its banner to refresh
// when a check passes or dismissal state changes.
func (d *Daemon) setActiveUnifiedWindow(uw *unifiedWindow) {
	d.uwMu.Lock()
	defer d.uwMu.Unlock()
	d.activeUW = uw
}

// activeUnifiedWindow returns the currently-tracked main window, or nil.
func (d *Daemon) activeUnifiedWindow() *unifiedWindow {
	d.uwMu.Lock()
	defer d.uwMu.Unlock()
	return d.activeUW
}

// DismissCheck marks a doctor check ID as dismissed for the current
// session. Banners hide it; the Settings page health section still
// shows it so the user can come back to it.
func (d *Daemon) DismissCheck(id string) {
	d.dismissMu.Lock()
	defer d.dismissMu.Unlock()
	if d.dismissed == nil {
		d.dismissed = map[string]bool{}
	}
	d.dismissed[id] = true
}

// UndismissCheck clears a previous dismissal so the banner can show again.
func (d *Daemon) UndismissCheck(id string) {
	d.dismissMu.Lock()
	defer d.dismissMu.Unlock()
	delete(d.dismissed, id)
}

// IsDismissed reports whether the given check ID has been dismissed.
func (d *Daemon) IsDismissed(id string) bool {
	d.dismissMu.Lock()
	defer d.dismissMu.Unlock()
	return d.dismissed[id]
}

// New creates a new Daemon with the given config.
func New(cfg *config.Config, configPath string) *Daemon {
	mode := "off"
	if dm := cfg.EnsureDaemon(); dm.InhibitSleep != "" {
		mode = dm.InhibitSleep
	}
	d := &Daemon{
		Cfg:        cfg,
		ConfigPath: configPath,
		Runner:     codespace.DefaultGHRunner{},
		stopCh:     make(chan struct{}),
		sessions:   newSessionTracker(mode),
		forwards:   newPortForwardManager(),
		nowFunc:    time.Now,
	}
	d.pollCond = sync.NewCond(&d.mu)
	return d
}

// now returns the current time via nowFunc when set, or time.Now otherwise.
// Tests can swap nowFunc to drive the tray rebuild gate deterministically.
func (d *Daemon) now() time.Time {
	if d.nowFunc != nil {
		return d.nowFunc()
	}
	return time.Now()
}

// Run starts all applet components. It blocks until Stop is called.
// This must be called from the main OS thread.
func (d *Daemon) Run() error {
	enrichPath()

	d.app = app.NewWithID("dev.cosmonaut.applet")
	d.app.Settings().SetTheme(newCosmoTheme())
	d.app.SetIcon(appIcon())

	prevVariant := d.app.Settings().ThemeVariant()
	d.app.Settings().AddListener(func(s fyne.Settings) {
		v := s.ThemeVariant()
		if v == prevVariant {
			return
		}
		prevVariant = v
		d.notifyThemeListeners()
	})

	log.Printf("applet started (pid %d)", os.Getpid())

	// Run the initial poll synchronously so the tray menu has
	// codespace data before it is first displayed.
	d.poll()

	// Start background workers.
	go d.startPoller()
	go d.startHotkeyListener()
	go d.watchTrayOpened()
	d.app.Lifecycle().SetOnEnteredForeground(func() {
		d.maybePollAsync()
	})
	d.startPreWarm()

	// Create a hidden master window so popover windows don't quit the app on close.
	master := d.app.NewWindow("cosmonaut-applet")
	master.SetMaster()
	master.SetCloseIntercept(func() {})

	// Set up system tray.
	if desk, ok := d.app.(desktop.App); ok {
		desk.SetSystemTrayIcon(trayIconIdle())
		desk.SetSystemTrayMenu(d.buildTrayMenu())
	}

	// Run the Fyne event loop (blocks until Quit).
	d.app.Run()

	return nil
}

// Stop signals the applet to shut down.
func (d *Daemon) Stop() {
	select {
	case <-d.stopCh:
		return
	default:
		close(d.stopCh)
	}

	if d.sessions != nil {
		d.sessions.Stop()
	}
	if d.forwards != nil {
		d.forwards.StopAll()
	}

	if d.app != nil {
		d.app.Quit()
	}

	log.Println("applet stopped")
}

// Codespaces returns the last-polled codespace list.
func (d *Daemon) Codespaces() []codespace.Codespace {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]codespace.Codespace, len(d.codespaces))
	copy(result, d.codespaces)
	return result
}

// Workspaces returns the last-polled combined workspace list.
func (d *Daemon) Workspaces() []provider.Workspace {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]provider.Workspace, len(d.workspaces))
	copy(result, d.workspaces)
	return result
}

// SetCodespaces updates the cached codespace list.
func (d *Daemon) SetCodespaces(cs []codespace.Codespace) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.codespaces = cs
}

// SetWorkspaces updates the cached combined workspace list.
func (d *Daemon) SetWorkspaces(ws []provider.Workspace) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.workspaces = ws
}

// ListErr returns the error from the most recent codespace list attempt,
// or nil on success.
func (d *Daemon) ListErr() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.listErr
}

// SetListErr stores the error from the most recent codespace list attempt.
func (d *Daemon) SetListErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.listErr = err
}

// AddWorkspaceListener registers fn to be invoked on the Fyne main
// thread after each poll where the workspace set has actually changed.
// Returns a remove function that the caller MUST invoke when the
// owning window closes — otherwise fn will keep firing into freed
// widgets.
//
// Listeners are an alternative to polling d.Workspaces() on every
// render: the GUI sidebar uses one to refresh its tree the moment new
// data lands, instead of waiting for the user to click somewhere.
func (d *Daemon) AddWorkspaceListener(fn func()) (remove func()) {
	d.listenersMu.Lock()
	defer d.listenersMu.Unlock()
	if d.listeners == nil {
		d.listeners = map[int]func(){}
	}
	d.nextListener++
	id := d.nextListener
	d.listeners[id] = fn
	return func() {
		d.listenersMu.Lock()
		delete(d.listeners, id)
		d.listenersMu.Unlock()
	}
}

// notifyWorkspaceListeners fires every registered listener on the Fyne
// main thread. Snapshots the listener map under the lock so a listener
// can safely unregister itself (or another listener) without racing the
// dispatch loop.
func (d *Daemon) notifyWorkspaceListeners() {
	d.listenersMu.Lock()
	fns := make([]func(), 0, len(d.listeners))
	for _, fn := range d.listeners {
		fns = append(fns, fn)
	}
	d.listenersMu.Unlock()
	for _, fn := range fns {
		fyne.Do(fn)
	}
}

func (d *Daemon) addThemeListener(fn func()) (remove func()) {
	d.themeMu.Lock()
	defer d.themeMu.Unlock()
	if d.themeListeners == nil {
		d.themeListeners = map[int]func(){}
	}
	d.nextThemeListener++
	id := d.nextThemeListener
	d.themeListeners[id] = fn
	return func() {
		d.themeMu.Lock()
		delete(d.themeListeners, id)
		d.themeMu.Unlock()
	}
}

func (d *Daemon) notifyThemeListeners() {
	d.themeMu.Lock()
	fns := make([]func(), 0, len(d.themeListeners))
	for _, fn := range d.themeListeners {
		fns = append(fns, fn)
	}
	d.themeMu.Unlock()
	for _, fn := range fns {
		fyne.Do(fn)
	}
}
