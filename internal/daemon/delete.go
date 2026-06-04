package daemon

import (
	"fmt"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/linuskendall/cosmonaut/internal/codespace"
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
	status := d.StatusFor(providerName)
	if status.CheckedAt.IsZero() {
		return true
	}
	return status.Available && status.Err == nil
}

// deleteDisabledReason returns a short message explaining why the
// Delete UI is disabled for the given provider, or "" when delete is
// currently possible. Surfaced in the GUI next to the button so the
// user isn't left guessing.
func (d *Daemon) deleteDisabledReason(providerName string) string {
	if d.canDeleteWorkspace(providerName) {
		return ""
	}
	status := d.StatusFor(providerName)
	if !status.Available {
		switch providerName {
		case provider.NameCoder:
			return "coder CLI not installed"
		case provider.NameGitHub:
			return "gh CLI not installed"
		}
		return "CLI not available"
	}
	if status.Err != nil {
		return "auth or list call failing"
	}
	return ""
}

// confirmAndDeleteWorkspace shows a destructive confirmation dialog
// rooted at parent. On confirm, runs the provider's DeleteWorkspace in
// a goroutine and dispatches success/failure handling on the Fyne
// goroutine. On success: prunes the workspace from local caches so
// the UI reflects the deletion immediately, notifies the user, runs
// onDone, then forcePollAsync to reconcile with the backend. On
// failure: surfaces the error dialog and still runs onDone so the
// caller can refresh whatever view it was managing.
func (d *Daemon) confirmAndDeleteWorkspace(parent fyne.Window, providerName, name string, onDone func()) {
	if parent == nil {
		log.Printf("confirmAndDeleteWorkspace: nil parent for %s/%s", providerName, name)
		return
	}
	msg := fmt.Sprintf("Delete %s workspace %q? This cannot be undone.", providerLabel(providerName), name)
	dialog.ShowConfirm("Delete workspace", msg, func(ok bool) {
		if !ok {
			return
		}
		go func() {
			manager, mgrErr := d.managerForProvider(providerName)
			var err error
			if mgrErr != nil {
				err = mgrErr
			} else if delErr := manager.DeleteWorkspace(name); delErr != nil {
				err = fmt.Errorf("delete %s: %w", name, delErr)
			}
			if err == nil {
				d.pruneWorkspace(providerName, name)
				d.rebuildTrayMenu()
			}
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
			if err == nil {
				d.forcePollAsync()
			}
		}()
	}, parent)
}

// pruneWorkspace removes a single workspace from the cached lists so
// the UI reflects a just-completed delete without waiting for the
// next poll. The follow-up forcePollAsync still runs to reconcile
// with the source of truth.
func (d *Daemon) pruneWorkspace(providerName, name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.workspaces) > 0 {
		out := make([]provider.Workspace, 0, len(d.workspaces))
		for _, ws := range d.workspaces {
			if ws.Provider == providerName && ws.Name == name {
				continue
			}
			out = append(out, ws)
		}
		d.workspaces = out
	}
	if providerName == provider.NameGitHub && len(d.codespaces) > 0 {
		filtered := make([]codespace.Codespace, 0, len(d.codespaces))
		for _, c := range d.codespaces {
			if c.Name == name {
				continue
			}
			filtered = append(filtered, c)
		}
		d.codespaces = filtered
	}
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
