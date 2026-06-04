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
func (d *Daemon) watchTrayOpened() {
	for {
		select {
		case <-systray.TrayOpenedCh:
			d.maybePollAsync()
		case <-d.stopCh:
			return
		}
	}
}

// maybePollAsync triggers d.poll() in a goroutine if no poll has run in
// the last autoPollMinInterval. Used by event-driven refreshers (tray
// opened, window focus) so we never refresh more than once per debounce
// window even if the user clicks the tray repeatedly.
func (d *Daemon) maybePollAsync() {
	d.mu.Lock()
	if d.pollInFlight || time.Since(d.lastPollAt) < autoPollMinInterval {
		d.mu.Unlock()
		return
	}
	d.pollInFlight = true
	d.mu.Unlock()
	go d.poll()
}

func (d *Daemon) poll() {
	d.mu.Lock()
	d.pollInFlight = true
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.pollInFlight = false
		d.lastPollAt = time.Now()
		d.mu.Unlock()
	}()
	codespaces, err := codespace.ListAllCodespaces(d.Runner)
	if err != nil {
		log.Printf("poll: %v", err)
		if d.Cfg == nil || d.Cfg.EffectiveWorkspaceProvider() == provider.NameGitHub {
			d.SetListErr(err)
		}
		codespaces = nil
	}
	var workspaces []provider.Workspace
	if len(codespaces) > 0 {
		for _, cs := range codespaces {
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
			workspaces = append(workspaces, ws)
		}
	}

	coderManager := provider.NewCoderManager(d.Cfg)
	coderWorkspaces, coderErr := coderManager.ListAllWorkspaces()
	if coderErr != nil {
		log.Printf("poll(coder): %v", coderErr)
		if d.Cfg != nil && d.Cfg.EffectiveWorkspaceProvider() == provider.NameCoder {
			d.SetListErr(coderErr)
		}
	} else {
		workspaces = append(workspaces, coderWorkspaces...)
	}

	if (d.Cfg == nil || d.Cfg.EffectiveWorkspaceProvider() == provider.NameGitHub) && err == nil {
		d.SetListErr(nil)
	}
	if d.Cfg != nil && d.Cfg.EffectiveWorkspaceProvider() == provider.NameCoder && coderErr == nil {
		d.SetListErr(nil)
	}

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
