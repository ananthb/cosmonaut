package daemon

import (
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/linuskendall/cosmonaut/internal/sshconfig"
)

// buildWorkspaceSSHSection renders the per-workspace SSH option toggles
// (ControlMaster persistent connection, tmux session wrapping) for the
// detail view of a single workspace.
//
// Both toggles write to Config.WorkspaceSSH keyed by provider:name, so each
// workspace owns its own settings — toggling tmux on cs-A does not affect
// cs-B. Defaults match the package-wide defaults: ControlMaster on, tmux
// off.
//
// refresh is invoked after a toggle changes so the caller can re-render the
// detail panel (e.g. so ControlMaster info reflects in any sub-sections).
// rebuildTrayMenu is also called so the tray reflects the new state on the
// next menu open.
func (uw *unifiedWindow) buildWorkspaceSSHSection(providerName, workspaceName string, refresh func()) fyne.CanvasObject {
	title := caption("SSH OPTIONS")

	cfg := uw.daemon.Cfg

	cmCheck := widget.NewCheck("Persistent SSH (ControlMaster)", func(on bool) {
		v := on
		cfg.SetWorkspaceSSHControlMaster(providerName, workspaceName, &v)
		uw.daemon.persistConfig()
		// Rewrite this workspace's conf so the new managed-extras block
		// takes effect immediately — otherwise the next SSH wouldn't pick
		// up the change until the workspace is re-prepared.
		uw.daemon.applyWorkspaceSSHOptions(providerName, workspaceName)
		if refresh != nil {
			refresh()
		}
	})
	cmCheck.SetChecked(cfg.WorkspaceSSHControlMaster(providerName, workspaceName))
	cmHint := mutedHint("Multiplex extra sessions over one TCP connection — instant reconnects.")

	tmuxCheck := widget.NewCheck("Wrap shell in tmux", func(on bool) {
		v := on
		cfg.SetWorkspaceSSHTmux(providerName, workspaceName, &v)
		uw.daemon.persistConfig()
		if refresh != nil {
			refresh()
		}
	})
	tmuxCheck.SetChecked(cfg.WorkspaceSSHTmux(providerName, workspaceName))
	tmuxHint := mutedHint("The SSH button (and `cosmonaut shell`) attach to a persistent tmux session that survives disconnects.")

	return container.NewVBox(
		title,
		cmCheck,
		container.NewPadded(cmHint),
		tmuxCheck,
		container.NewPadded(tmuxHint),
	)
}

// mutedHint returns a wrapped label styled as secondary help text.
func mutedHint(s string) fyne.CanvasObject {
	lbl := widget.NewLabel(s)
	lbl.Wrapping = fyne.TextWrapWord
	lbl.Importance = widget.LowImportance
	return lbl
}

// applyWorkspaceSSHOptions rewrites the on-disk SSH conf for a workspace so
// the latest ControlMaster setting takes effect without waiting for the next
// PrepareSSH call. A no-op for workspaces whose conf doesn't exist yet
// (a launch will create one with the right options).
func (d *Daemon) applyWorkspaceSSHOptions(providerName, workspaceName string) {
	paths := sshconfig.ResolvePaths()
	confPath := paths.WorkspaceConfigPath(providerName, workspaceName)
	opts := sshconfig.ManagedExtrasOptions{
		ControlMaster: d.Cfg.WorkspaceSSHControlMaster(providerName, workspaceName),
	}
	if _, err := sshconfig.RefreshManagedExtras(confPath, opts); err != nil {
		log.Printf("ssh options: refresh %s: %v", confPath, err)
	}
}
