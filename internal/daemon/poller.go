package daemon

import (
	"log"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"github.com/linuskendall/cosmonaut/internal/codespace"
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
		d.SetListErr(err)
		return
	}
	d.SetListErr(nil)

	log.Printf("poll: fetched %d codespaces", len(codespaces))

	old := d.Codespaces()
	d.SetCodespaces(codespaces)

	if len(old) > 0 {
		d.detectStateChanges(old, codespaces)
	}
	d.checkAutoStop(codespaces)
	d.updateTrayIcon(codespaces)
	d.rebuildTrayMenu()
}

// updateTrayIcon switches tray icon based on aggregate codespace state.
func (d *Daemon) updateTrayIcon(codespaces []codespace.Codespace) {
	hasAvailable := false
	hasStarting := false
	for _, cs := range codespaces {
		switch cs.State {
		case "Available":
			hasAvailable = true
		case "Starting":
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
