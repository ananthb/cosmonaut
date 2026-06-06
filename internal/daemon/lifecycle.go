package daemon

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"time"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/config"
)

// checkAutoStop stops codespaces that have been idle past their configured autoStop duration.
func (d *Daemon) checkAutoStop(codespaces []codespace.Codespace) {
	if d.Cfg == nil {
		return
	}

	for _, cs := range codespaces {
		if cs.State != "Available" {
			continue
		}

		target := d.findTargetForCodespace(&cs)
		if target == nil || target.AutoStop == "" {
			continue
		}

		threshold, err := time.ParseDuration(target.AutoStop)
		if err != nil {
			log.Printf("auto-stop: invalid duration %q for %s: %v", target.AutoStop, cs.Name, err)
			continue
		}

		// gh codespace list includes lastUsedAt in gitStatus, but we don't have it
		// in the current data model. For now, rely on the configured idleTimeout on
		// the codespace itself and just send a stop command. In the future we could
		// parse lastUsedAt from the API.
		_ = threshold

		// TODO: implement idle time tracking and auto-stop
		// For now, this is a placeholder that logs when a codespace would be stopped.
	}
}

// startPreWarm schedules daily codespace creation/start for targets with a preWarm time.
func (d *Daemon) startPreWarm() {
	if d.Cfg == nil {
		return
	}

	for name, target := range d.Cfg.Targets {
		if target.PreWarm == "" {
			continue
		}

		t, err := parseTimeOfDay(target.PreWarm)
		if err != nil {
			log.Printf("pre-warm: invalid time %q for target %s: %v", target.PreWarm, name, err)
			continue
		}

		go d.preWarmLoop(name, target, t)
	}
}

func (d *Daemon) preWarmLoop(name string, target config.Target, tod timeOfDay) {
	for {
		next := nextOccurrence(tod)
		timer := time.NewTimer(time.Until(next))

		select {
		case <-timer.C:
			log.Printf("pre-warm: starting codespace for target %s", name)
			cs, err := codespace.CreateCodespace(d.Runner, target)
			if err != nil {
				log.Printf("pre-warm: create failed for %s: %v", name, err)
			} else {
				log.Printf("pre-warm: created %s for %s", cs.Name, name)
				sendNotification(fmt.Sprintf("Codespace ready: %s", name))
			}
		case <-d.stopCh:
			timer.Stop()
			return
		}
	}
}

type timeOfDay struct {
	hour, minute int
}

func parseTimeOfDay(s string) (timeOfDay, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return timeOfDay{}, fmt.Errorf("expected HH:MM format: %w", err)
	}
	return timeOfDay{hour: t.Hour(), minute: t.Minute()}, nil
}

func nextOccurrence(tod timeOfDay) time.Time {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), tod.hour, tod.minute, 0, 0, now.Location())
	if next.Before(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// sendNotification sends a desktop notification.
func sendNotification(msg string) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title "cosmonaut"`, msg)
		if err := exec.Command("osascript", "-e", script).Run(); err != nil {
			log.Printf("notify: osascript: %v", err)
		}
	case "linux":
		if err := exec.Command("notify-send", "cosmonaut", msg).Run(); err != nil {
			log.Printf("notify: notify-send: %v", err)
		}
	}
}

// detectStateChanges compares old and new codespace lists and sends notifications.
func (d *Daemon) detectStateChanges(old, new []codespace.Codespace) {
	oldMap := make(map[string]string)
	for _, cs := range old {
		oldMap[cs.Name] = cs.State
	}

	for _, cs := range new {
		oldState, existed := oldMap[cs.Name]
		if !existed {
			sendNotification(fmt.Sprintf("New codespace: %s (%s)", cs.DisplayName, cs.State))
		} else if oldState != cs.State {
			sendNotification(fmt.Sprintf("Codespace %s: %s → %s", cs.DisplayName, oldState, cs.State))
		}
	}
}

// findTargetForCodespace finds the config target that matches a codespace's repository.
func (d *Daemon) findTargetForCodespace(cs *codespace.Codespace) *config.Target {
	if d.Cfg == nil {
		return nil
	}
	repo := string(cs.Repository)
	for _, t := range d.Cfg.Targets {
		if t.Repository == repo {
			return &t
		}
	}
	return nil
}
