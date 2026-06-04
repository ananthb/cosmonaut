package daemon

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

// canDeleteWorkspace reports whether the daemon currently has the
// ability to delete a workspace for the given provider. False when the
// CLI is missing or the most recent list call failed with an auth-like
// error — both indicate the destroy call would also fail. Used to
// disable Delete UI rather than offering an action that can't succeed.
func (d *Daemon) canDeleteWorkspace(providerName string) bool {
	switch providerName {
	case provider.NameGitHub:
		if err := provider.RequireCommand("gh"); err != nil {
			return false
		}
	case provider.NameCoder:
		if err := provider.RequireCommand("coder"); err != nil {
			return false
		}
	default:
		return false
	}
	listErr := d.ListErr()
	if listErr == nil {
		return true
	}
	return !looksLikeAuthError(listErr)
}

func looksLikeAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not authenticated"),
		strings.Contains(msg, "not logged"),
		strings.Contains(msg, "authentication"),
		strings.Contains(msg, "auth status"),
		strings.Contains(msg, "unauthorized"),
		strings.Contains(msg, `needs the "codespace" scope`):
		return true
	}
	return false
}

// confirmAndDeleteWorkspace shows a destructive confirmation dialog
// rooted at parent. On confirm, runs the provider's DeleteWorkspace in
// a goroutine, notifies on success/error, refreshes the daemon's
// caches, and invokes onDone (on the Fyne goroutine) after completion.
// Pass onDone=nil if there's nothing extra to do.
func (d *Daemon) confirmAndDeleteWorkspace(parent fyne.Window, providerName, name string, onDone func()) {
	if parent == nil {
		return
	}
	msg := fmt.Sprintf("Delete %s workspace %q? This cannot be undone.", providerLabel(providerName), name)
	dialog.ShowConfirm("Delete workspace", msg, func(ok bool) {
		if !ok {
			return
		}
		go func() {
			err := d.deleteWorkspace(providerName, name)
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(err, parent)
				} else {
					d.notify(fmt.Sprintf("Deleted %s", name))
				}
				if onDone != nil {
					onDone()
				}
			})
			d.maybePollAsyncForce()
		}()
	}, parent)
}

func (d *Daemon) deleteWorkspace(providerName, name string) error {
	manager, err := d.managerForProvider(providerName)
	if err != nil {
		return err
	}
	if err := manager.DeleteWorkspace(name); err != nil {
		return fmt.Errorf("delete %s: %w", name, err)
	}
	return nil
}

// maybePollAsyncForce kicks an unconditional poll, used after a
// state-changing action like delete where the debounce isn't useful.
func (d *Daemon) maybePollAsyncForce() {
	go d.poll()
}

func providerLabel(providerName string) string {
	switch providerName {
	case provider.NameGitHub:
		return "GitHub codespace"
	case provider.NameCoder:
		return "Coder"
	default:
		return providerName
	}
}
