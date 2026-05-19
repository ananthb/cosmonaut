// Package daemon implements the cosmonaut menu bar applet which
// hosts a system tray icon, global hotkey listener, and codespace
// lifecycle management.
package daemon

import (
	"log"
	"os"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/provider"
)

// Daemon is the long-running background process that hosts the system tray,
// hotkey listener, and codespace poller.
type Daemon struct {
	Cfg        *config.Config
	ConfigPath string
	Runner     codespace.GHRunner

	app fyne.App

	mu         sync.Mutex
	codespaces []codespace.Codespace
	workspaces []provider.Workspace
	portCache  map[string]portCacheEntry
	listErr    error
	stopCh     chan struct{}
	sessions   *SessionTracker
	forwards   *PortForwardManager

	dismissMu sync.Mutex
	dismissed map[string]bool

	uwMu     sync.Mutex
	activeUW *unifiedWindow
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
	if cfg != nil && cfg.Daemon != nil {
		mode = cfg.Daemon.InhibitSleep
	}
	return &Daemon{
		Cfg:        cfg,
		ConfigPath: configPath,
		Runner:     codespace.DefaultGHRunner{},
		stopCh:     make(chan struct{}),
		sessions:   newSessionTracker(mode),
		forwards:   newPortForwardManager(),
	}
}

// Run starts all applet components. It blocks until Stop is called.
// This must be called from the main OS thread.
func (d *Daemon) Run() error {
	enrichPath()

	d.app = app.NewWithID("dev.cosmonaut.applet")
	d.app.Settings().SetTheme(newCosmoTheme())
	d.app.SetIcon(appIcon())

	log.Printf("applet started (pid %d)", os.Getpid())

	// Run the initial poll synchronously so the tray menu has
	// codespace data before it is first displayed.
	d.poll()

	// Start background workers.
	go d.startPoller()
	go d.startHotkeyListener()
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
