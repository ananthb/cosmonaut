package daemon

import (
	"log"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/provider"
)

func (d *Daemon) startPoller() {
	interval := 5 * time.Minute
	if d.Cfg != nil && d.Cfg.Daemon != nil && d.Cfg.Daemon.PollInterval != "" {
		if parsed, err := time.ParseDuration(d.Cfg.Daemon.PollInterval); err == nil {
			interval = parsed
		}
	}

	ticker := time.NewTicker(interval)
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

func (d *Daemon) poll() {
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
