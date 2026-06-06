package daemon

import (
	"fmt"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/editor"
	"github.com/linuskendall/cosmonaut/internal/history"
	"github.com/linuskendall/cosmonaut/internal/provider"
	"github.com/linuskendall/cosmonaut/internal/sshconfig"
)

// showGUI opens the unified Cosmonaut window.
// Args determine initial state:
//   - no args: show the window with sidebar
//   - target name or owner/repo: open tree, expand that repo
//   - "--workspace", name, "--provider", provider, target: direct launch
//   - "--detail", "--workspace", name, "--provider", provider, target: show detail
//   - "--port-forward", "--workspace", name, "--provider", provider, target:
//     show detail and immediately open the ad-hoc port forward dialog
//   - "--delete", "--workspace", name, "--provider", provider, target:
//     show detail and immediately fire the delete confirmation dialog
func (d *Daemon) showGUI(args ...string) {
	if d.app == nil {
		log.Println("gui: app not initialized")
		return
	}

	// Parse args.
	var targetArg, workspaceName, providerName string
	detailOnly := false
	portForward := false
	deleteFlow := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--detail":
			detailOnly = true
		case args[i] == "--port-forward":
			detailOnly = true
			portForward = true
		case args[i] == "--delete":
			detailOnly = true
			deleteFlow = true
		case args[i] == "--workspace" && i+1 < len(args):
			workspaceName = args[i+1]
			i++
		case args[i] == "--provider" && i+1 < len(args):
			providerName = args[i+1]
			i++
		default:
			targetArg = args[i]
		}
	}

	fyne.Do(func() {
		uw := d.newCosmoWindow()

		if workspaceName != "" && providerName != "" {
			target, resolvedName := d.resolveGUITarget(targetArg)
			manager, err := d.managerForProvider(providerName)
			if err != nil {
				showFlowError(uw.win, err)
				return
			}
			ws, err := manager.ResolveWorkspace(workspaceName)
			if err != nil {
				showFlowError(uw.win, err)
				return
			}
			uw.win.Show()
			if detailOnly {
				uw.showDetailFor(*ws)
				// --port-forward and --delete are mutually exclusive
				// follow-ups; either fires its dialog against uw.win
				// after the detail render, and stacking both would
				// leave one buried behind the other.
				switch {
				case portForward:
					wsCopy := *ws
					uw.showAdHocPortForwardDialog(wsCopy.Provider, wsCopy.Name, func() {
						uw.showDetailFor(wsCopy)
					})
				case deleteFlow:
					wsCopy := *ws
					d.confirmAndDeleteWorkspace(uw.win, wsCopy.Provider, wsCopy.Name, func() {
						uw.tree.Refresh()
						if wsCopy.Provider == provider.NameCoder {
							uw.showCoderSummary()
						} else {
							uw.showCosmoWelcome()
						}
					})
				}
				return
			}
			d.runLaunchFlow(uw.win, target, resolvedName, ws)
		} else if targetArg != "" {
			target, _ := d.resolveGUITarget(targetArg)
			if target.Repository != "" {
				uw.tree.OpenBranch(repoNodeID(target.Repository))
			}
			uw.win.Show()
		} else {
			uw.win.Show()
		}
	})
}

// resolveGUITarget resolves a target argument to a config Target.
func (d *Daemon) resolveGUITarget(arg string) (config.Target, string) {
	if arg != "" && !isRepoLike(arg) {
		if d.Cfg != nil {
			if t, ok := d.Cfg.Targets[arg]; ok {
				return t, arg
			}
		}
	}
	return guiTargetForRepo(d.Cfg, arg)
}

func isRepoLike(s string) bool {
	for _, c := range s {
		if c == '/' {
			return true
		}
	}
	return false
}

// getEditor returns the configured editor implementation.
func (d *Daemon) getEditor() editor.Editor {
	editorName := ""
	if d.Cfg != nil {
		editorName = d.Cfg.Editor
	}
	ed, err := editor.ForName(editorName)
	if err != nil {
		log.Printf("editor: %v, falling back to zed", err)
		ed, _ = editor.ForName("zed")
	}
	return ed
}

// showFlowError displays an error dialog and closes the window once the user
// dismisses it, so a failed flow doesn't leave the UI stuck on a spinner.
func showFlowError(win fyne.Window, err error) {
	fyne.Do(func() {
		d := dialog.NewError(err, win)
		d.SetOnClosed(func() { win.Close() })
		d.Show()
	})
}

// runCreateAndLaunch creates a workspace and then launches it.
func (d *Daemon) runCreateAndLaunch(win fyne.Window, target config.Target, resolvedName string) {
	manager, err := d.managerForTarget(target)
	if err != nil {
		showFlowError(win, err)
		return
	}
	progress := newProgressScreen("Creating workspace...")
	win.SetContent(progress.canvas)

	go func() {
		ws, err := manager.CreateWorkspace(target, false)
		if err != nil {
			progress.stop()
			showFlowError(win, fmt.Errorf("creating workspace: %w", err))
			return
		}
		progress.stop()
		d.runLaunchFlow(win, target, resolvedName, ws)
	}()
}

// runLaunchFlow runs the SSH setup and editor launch sequence.
func (d *Daemon) runLaunchFlow(win fyne.Window, target config.Target, resolvedName string, selected *provider.Workspace) {
	manager, err := d.managerForTarget(target)
	if err != nil {
		showFlowError(win, err)
		return
	}
	ed := d.getEditor()
	progress := newProgressScreen("Preparing workspace...")
	fyne.Do(func() { win.SetContent(progress.canvas) })

	go func() {
		defer progress.stop()
		setStatus := func(msg string) {
			fyne.Do(func() { progress.setStatus(msg) })
		}

		// Record in history.
		hist := history.Load()
		if target.Repository != "" {
			hist.Touch(target.Repository)
			hist.Save()
		}

		workspacePath := guessWorkspacePath(target, selected)
		if isWorkspaceRunning(*selected) {
			paths := sshconfig.ResolvePaths()
			if alias, ok := sshconfig.ReadExistingWorkspaceAlias(paths, selected.Provider, selected.Name); ok {
				setStatus(fmt.Sprintf("Launching %s...", ed.Name()))
				if err := ed.LaunchRemote(alias, workspacePath); err != nil {
					showFlowError(win, err)
					return
				}
				if d.sessions != nil {
					d.sessions.TrackSession(alias)
				}
				fyne.Do(func() { win.Close() })
				return
			}
		}

		latest, err := manager.StartWorkspace(selected)
		if err != nil {
			showFlowError(win, err)
			return
		}
		selected = latest

		setStatus("Waiting for workspace SSH...")
		if err := manager.EnsureReachable(selected); err != nil {
			showFlowError(win, fmt.Errorf("SSH connectivity: %w", err))
			return
		}

		paths := sshconfig.ResolvePaths()
		setStatus("Preparing SSH config...")
		sshOpts := sshconfig.ManagedExtrasOptions{
			ControlMaster: d.Cfg.WorkspaceSSHControlMaster(selected.Provider, selected.Name),
		}
		sshAlias, err := manager.PrepareSSH(paths, selected, sshOpts)
		if err != nil {
			showFlowError(win, err)
			return
		}

		nickname := editor.ResolveNickname(
			target.ZedNickname, target.DisplayName, selected.DisplayName, resolvedName,
		)
		if err := ed.ConfigureConnection(sshAlias, workspacePath, nickname, target.UploadBinaryOverSSH); err != nil {
			showFlowError(win, err)
			return
		}

		setStatus(fmt.Sprintf("Launching %s...", ed.Name()))
		if err := ed.LaunchRemote(sshAlias, workspacePath); err != nil {
			showFlowError(win, err)
			return
		}
		if d.sessions != nil {
			d.sessions.TrackSession(sshAlias)
		}

		fyne.Do(func() { win.Close() })
		d.rebuildTrayMenu()
	}()
}

func (d *Daemon) managerForTarget(target config.Target) (provider.Manager, error) {
	if target.Coder != nil {
		return provider.NewCoderManager(d.Cfg), nil
	}
	return provider.NewGitHubManager(d.Runner), nil
}

func (d *Daemon) managerForProvider(providerName string) (provider.Manager, error) {
	switch providerName {
	case provider.NameGitHub:
		return provider.NewGitHubManager(d.Runner), nil
	case provider.NameCoder:
		return provider.NewCoderManager(d.Cfg), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", providerName)
	}
}
