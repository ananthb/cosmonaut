package daemon

import (
	"fmt"
	"log"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/editor"
	"github.com/linuskendall/cosmonaut/internal/history"
	"github.com/linuskendall/cosmonaut/internal/sshconfig"
)

// showGUI opens the unified Cosmonaut window.
// Args determine initial state:
//   - no args: show the window with sidebar
//   - target name or owner/repo: open tree, expand that repo
//   - "--codespace", csName, target: direct launch with progress
func (d *Daemon) showGUI(args ...string) {
	if d.app == nil {
		log.Println("gui: app not initialized")
		return
	}

	// Parse args.
	var targetArg, codespaceName string
	for i := 0; i < len(args); i++ {
		if args[i] == "--codespace" && i+1 < len(args) {
			codespaceName = args[i+1]
			i++
		} else {
			targetArg = args[i]
		}
	}

	fyne.Do(func() {
		uw := d.newCosmoWindow()

		if codespaceName != "" && targetArg != "" {
			// Direct codespace launch: show progress immediately.
			target, resolvedName := d.resolveGUITarget(targetArg)
			cs := &codespace.Codespace{Name: codespaceName, Repository: codespace.RepoField(target.Repository)}
			uw.win.Show()
			d.runLaunchFlow(uw.win, target, resolvedName, cs)
		} else if targetArg != "" {
			// Open with a specific repo expanded.
			target, _ := d.resolveGUITarget(targetArg)
			uw.tree.OpenBranch(repoNodeID(target.Repository))
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

// runCreateAndLaunch creates a codespace and then launches it.
func (d *Daemon) runCreateAndLaunch(win fyne.Window, target config.Target, resolvedName string) {
	progress := newProgressScreen("Creating codespace...")
	win.SetContent(progress.canvas)

	go func() {
		cs, err := codespace.CreateCodespace(d.Runner, target)
		if err != nil {
			progress.stop()
			showFlowError(win, fmt.Errorf("creating codespace: %w", err))
			return
		}
		// runLaunchFlow installs its own progress screen; stop ours so the
		// first animation goroutine doesn't leak.
		progress.stop()
		d.runLaunchFlow(win, target, resolvedName, cs)
	}()
}

// runLaunchFlow runs the SSH setup and editor launch sequence.
func (d *Daemon) runLaunchFlow(win fyne.Window, target config.Target, resolvedName string, selected *codespace.Codespace) {
	ed := d.getEditor()
	progress := newProgressScreen("Preparing codespace...")
	fyne.Do(func() { win.SetContent(progress.canvas) })

	go func() {
		defer progress.stop()
		setStatus := func(msg string) {
			fyne.Do(func() { progress.setStatus(msg) })
		}

		// Record in history.
		hist := history.Load()
		hist.Touch(target.Repository)
		hist.Save()

		// Fast path: if already Available with existing SSH config.
		if selected.State == "Available" {
			paths := sshconfig.ResolvePaths()
			if alias, ok := sshconfig.ReadExistingAlias(paths.IncludeDir, selected.Name); ok {
				setStatus(fmt.Sprintf("Launching %s...", ed.Name()))
				if err := ed.LaunchRemote(alias, target.WorkspacePath); err != nil {
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

		// Ensure SSH connectivity.
		setStatus("Waiting for codespace SSH...")
		if err := codespace.EnsureReachable(d.Runner, selected.Name); err != nil {
			showFlowError(win, fmt.Errorf("SSH connectivity: %w", err))
			return
		}

		// Get SSH config.
		setStatus("Fetching SSH config...")
		sshCfg, err := codespace.GetSSHConfig(d.Runner, selected.Name)
		if err != nil {
			showFlowError(win, err)
			return
		}

		sshAlias, err := sshconfig.ParsePrimaryHostAlias(sshCfg)
		if err != nil {
			showFlowError(win, err)
			return
		}

		// Write SSH config.
		paths := sshconfig.ResolvePaths()
		if err := os.MkdirAll(paths.IncludeDir, 0700); err != nil {
			showFlowError(win, err)
			return
		}
		if err := sshconfig.EnsureConfigIncludesGenerated(paths.MainConfigPath); err != nil {
			showFlowError(win, err)
			return
		}
		if err := sshconfig.WriteCodespaceConfig(paths.IncludeDir, selected.Name, sshCfg); err != nil {
			showFlowError(win, err)
			return
		}

		// Configure editor-specific settings.
		nickname := editor.ResolveNickname(
			target.ZedNickname, target.DisplayName, selected.DisplayName, resolvedName,
		)
		if err := ed.ConfigureConnection(sshAlias, target.WorkspacePath, nickname, target.UploadBinaryOverSSH); err != nil {
			showFlowError(win, err)
			return
		}

		// Launch editor.
		setStatus(fmt.Sprintf("Launching %s...", ed.Name()))
		if err := ed.LaunchRemote(sshAlias, target.WorkspacePath); err != nil {
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
