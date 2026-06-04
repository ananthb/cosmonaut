package daemon

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

// canDeleteWorkspace reports whether the daemon currently has the
// ability to delete a workspace for the given provider, based on the
// ProviderStatus snapshot from the most recent poll. The status fields
// — CLI availability and last list error — already encode the same
// signals the destroy call needs, so we don't re-check here.
//
// If we haven't polled yet (CheckedAt is zero) we treat the provider
// as deletable; the actual call will surface a real error if it isn't.
// Otherwise the button is disabled when the CLI is missing or the last
// list call failed, since both predict the delete would fail too.
func (d *Daemon) canDeleteWorkspace(providerName string) bool {
	if providerName != provider.NameGitHub && providerName != provider.NameCoder {
		return false
	}
	status := d.ProviderStatus(providerName)
	if status.CheckedAt.IsZero() {
		return true
	}
	return status.Available && status.Err == nil
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
			go d.forcePoll()
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
