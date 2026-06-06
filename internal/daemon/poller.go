package daemon

import (
	"fmt"
	"log"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/systray"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/provider"
)

// pollerBackstopInterval is the periodic refresh used as a fallback when
// no user interaction is happening. Event-driven refreshes (tray opened,
// app foregrounded) cover the responsive case; this just keeps state
// from going indefinitely stale when the user leaves the menu alone.
const pollerBackstopInterval = 15 * time.Minute

func (d *Daemon) startPoller() {
	ticker := time.NewTicker(pollerBackstopInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.poll()
		case <-d.stopCh:
			return
		}
	}
}

// watchTrayOpened listens for system tray menu opens and triggers a
// debounced refresh. Wakes on macOS (NSMenu menuWillOpen:) and Linux
// (DBusMenu "opened"); does nothing on platforms where the underlying
// systray library doesn't drive the channel.
//
// TODO(systray): fyne.io/systray does not currently expose a tray-closed
// channel. When/if one is added, hook it here to reset d.trayOpenedAt
// on close and immediately flush any pending rebuild. Until then, the
// interaction-window timeout in rebuildTrayMenu's gate is the only
// signal we have that the user has finished navigating the menu.
func (d *Daemon) watchTrayOpened() {
	for {
		select {
		case <-systray.TrayOpenedCh:
			d.onTrayOpened()
		case <-d.stopCh:
			return
		}
	}
}

// onTrayOpened records the open timestamp (used as the start of the
// interaction window during which rebuildTrayMenu defers) and kicks
// off a fresh poll. It does NOT flush any pendingRebuild here: applying
// the menu as the tray opens still dismisses the user's first click on
// some platforms. Instead, rebuildTrayMenu's timer fires after the
// interaction window elapses, by which point the user is no longer
// expected to be navigating the freshly opened menu.
func (d *Daemon) onTrayOpened() {
	d.mu.Lock()
	d.trayOpenedAt = d.now()
	// If a rebuild is pending, make sure the retry timer is armed for
	// at least one full interaction window from now — otherwise an
	// earlier timer could fire mid-interaction.
	if d.pendingRebuild {
		if d.rebuildTimer != nil {
			d.rebuildTimer.Stop()
			d.rebuildTimer = nil
		}
		d.armRebuildTimerLocked(trayInteractionWindow)
	}
	d.mu.Unlock()
	d.maybePollAsync()
}

// maybePollAsync spawns a poll goroutine when a poll hasn't run in the
// last autoPollMinInterval and none is currently in flight. The check
// is advisory: two callers that pass the check concurrently can both
// spawn goroutines, but poll()'s single-flight gate (tryAcquirePoll)
// ensures only one actually runs. This wrapper just avoids the cost of
// spawning goroutines that would immediately no-op.
func (d *Daemon) maybePollAsync() {
	d.mu.Lock()
	if d.pollInFlight || time.Since(d.lastPollAt) < autoPollMinInterval {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()
	go d.poll()
}

// poll runs a single refresh, skipping if another poll is already in
// flight. Single-flight prevents concurrent triggers (ticker, foreground
// event, initial Run) from racing on the workspace caches.
func (d *Daemon) poll() {
	if !d.tryAcquirePoll() {
		return
	}
	defer d.releasePoll()
	d.runPoll()
}

// forcePollAsync spawns a goroutine that waits for any in-flight poll
// to finish and then runs a fresh one. Used after state-changing
// actions (e.g. delete) where the in-flight poll's data predates the
// action and would clobber the post-action state on completion. Always
// async because the wait can be long if the in-flight call hangs; the
// caller never wants to block on it.
func (d *Daemon) forcePollAsync() {
	go func() {
		d.acquirePoll()
		defer d.releasePoll()
		d.runPoll()
	}()
}

// tryAcquirePoll claims the in-flight slot if free. Returns false when
// another poll already holds it.
func (d *Daemon) tryAcquirePoll() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pollInFlight {
		return false
	}
	d.pollInFlight = true
	return true
}

// acquirePoll blocks until the in-flight slot is free, then claims it.
func (d *Daemon) acquirePoll() {
	d.mu.Lock()
	for d.pollInFlight {
		d.pollCond.Wait()
	}
	d.pollInFlight = true
	d.mu.Unlock()
}

// releasePoll frees the in-flight slot and wakes any forcePoll waiters.
func (d *Daemon) releasePoll() {
	d.mu.Lock()
	d.pollInFlight = false
	d.lastPollAt = time.Now()
	d.pollCond.Broadcast()
	d.mu.Unlock()
}

// runPoll is the actual refresh body. Caller must hold the in-flight
// slot via tryAcquirePoll or acquirePoll.
func (d *Daemon) runPoll() {
	var codespaces []codespace.Codespace
	ghWorkspaces := d.pollProvider(provider.NameGitHub, "gh", func() ([]provider.Workspace, error) {
		cs, err := codespace.ListAllCodespaces(d.Runner)
		if err != nil {
			return nil, err
		}
		codespaces = cs
		return codespacesToWorkspaces(cs), nil
	})

	var coderWorkspaces []provider.Workspace
	if d.Cfg != nil && d.Cfg.IsCoderConfigured() {
		coderWorkspaces = d.pollProvider(provider.NameCoder, "coder", func() ([]provider.Workspace, error) {
			return provider.NewCoderManager(d.Cfg).ListAllWorkspaces()
		})
	}

	workspaces := append(ghWorkspaces, coderWorkspaces...)

	log.Printf("poll: fetched %d github codespaces and %d total workspaces", len(codespaces), len(workspaces))

	old := d.Codespaces()
	d.SetCodespaces(codespaces)
	d.SetWorkspaces(workspaces)

	if len(old) > 0 {
		d.detectStateChanges(old, codespaces)
	}
	d.checkAutoStop(codespaces)
	d.updateTrayIcon(workspaces)
	d.rebuildTrayMenu()
}

// pollProvider does the CLI presence check + list call for one
// provider, records the resulting ProviderStatus, and propagates
// listErr when the provider is the effective default. Returns the
// listed workspaces, or nil on failure.
func (d *Daemon) pollProvider(name, cli string, list func() ([]provider.Workspace, error)) []provider.Workspace {
	if err := provider.RequireCommand(cli); err != nil {
		log.Printf("poll(%s): %v", name, err)
		d.setProviderStatus(name, ProviderStatus{Available: false, Err: err})
		d.updateEffectiveListErr(name, err)
		return nil
	}
	workspaces, err := list()
	if err != nil {
		log.Printf("poll(%s): %v", name, err)
	}
	d.setProviderStatus(name, ProviderStatus{Available: true, Err: err})
	d.updateEffectiveListErr(name, err)
	if err != nil {
		return nil
	}
	return workspaces
}

// updateEffectiveListErr writes listErr only when the named provider
// is the user's effective default — otherwise the shared listErr would
// reflect a non-default provider's errors and confuse banner UI.
func (d *Daemon) updateEffectiveListErr(providerName string, err error) {
	effective := provider.NameGitHub
	if d.Cfg != nil {
		effective = d.Cfg.EffectiveWorkspaceProvider()
	}
	if effective != providerName {
		return
	}
	d.SetListErr(err)
}

func codespacesToWorkspaces(items []codespace.Codespace) []provider.Workspace {
	out := make([]provider.Workspace, 0, len(items))
	for _, cs := range items {
		ws := provider.Workspace{
			Provider:    provider.NameGitHub,
			Name:        cs.Name,
			DisplayName: cs.DisplayName,
			Repository:  string(cs.Repository),
			State:       cs.State,
			MachineName: cs.MachineName,
			CreatedAt:   cs.CreatedAt,
			LastUsedAt:  cs.LastUsedAt,
		}
		if cs.GitStatus != nil {
			ws.Branch = cs.GitStatus.Ref
			if ws.Branch == "" {
				ws.Branch = cs.GitStatus.Branch
			}
		}
		out = append(out, ws)
	}
	return out
}

func (d *Daemon) refreshCoderWorkspacesAsync(done func()) {
	go func() {
		manager := provider.NewCoderManager(d.Cfg)
		workspaces, err := manager.ListAllWorkspaces()
		if err != nil {
			log.Printf("refresh(coder): %v", err)
			if d.Cfg != nil && d.Cfg.EffectiveWorkspaceProvider() == provider.NameCoder {
				d.SetListErr(err)
			}
			d.notify(fmt.Sprintf("Refreshing Coder workspaces failed: %v", err))
		} else {
			d.SetWorkspaces(replaceWorkspacesByProvider(d.Workspaces(), provider.NameCoder, workspaces))
			if d.Cfg != nil && d.Cfg.EffectiveWorkspaceProvider() == provider.NameCoder {
				d.SetListErr(nil)
			}
			d.notify(fmt.Sprintf("Refreshed %d Coder workspace(s)", len(workspaces)))
		}
		d.updateTrayIcon(d.Workspaces())
		d.rebuildTrayMenu()
		if done != nil {
			fyne.Do(done)
		}
	}()
}

func replaceWorkspacesByProvider(current []provider.Workspace, providerName string, replacement []provider.Workspace) []provider.Workspace {
	result := make([]provider.Workspace, 0, len(current)+len(replacement))
	for _, ws := range current {
		if ws.Provider != providerName {
			result = append(result, ws)
		}
	}
	result = append(result, replacement...)
	return result
}

// updateTrayIcon switches tray icon based on aggregate workspace state.
func (d *Daemon) updateTrayIcon(workspaces []provider.Workspace) {
	hasAvailable := false
	hasStarting := false
	for _, ws := range workspaces {
		switch ws.State {
		case "Available", "ready", "running", "connected":
			hasAvailable = true
		case "Starting", "starting", "pending":
			hasStarting = true
		}
	}

	fyne.Do(func() {
		desk, ok := d.app.(desktop.App)
		if !ok {
			return
		}
		switch {
		case hasStarting:
			desk.SetSystemTrayIcon(trayIconStarting())
		case hasAvailable:
			desk.SetSystemTrayIcon(trayIconActive())
		default:
			desk.SetSystemTrayIcon(trayIconIdle())
		}
	})
}
