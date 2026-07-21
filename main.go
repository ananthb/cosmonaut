// cosmonaut starts or creates remote development workspaces and opens them in
// your editor (Zed or Neovim) via SSH remoting.
//
// The tool performs the following steps:
//  1. Authenticate with the selected workspace provider CLI
//  2. Resolve a target repository or workspace (interactive or from config)
//  3. Create a workspace if no match exists
//  4. Fetch the workspace's SSH config and write it to ~/.ssh/cosmonaut/
//  5. Configure editor-specific settings (e.g. Zed's settings.json)
//  6. Launch the editor with the SSH remote connection
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/editor"
	"github.com/linuskendall/cosmonaut/internal/history"
	"github.com/linuskendall/cosmonaut/internal/provider"
	"github.com/linuskendall/cosmonaut/internal/sshconfig"
	"github.com/linuskendall/cosmonaut/internal/tui"
)

const defaultConfigPath = "cosmonaut.config.json"

func main() {
	// When launched from a macOS .app bundle (double-click, Dock, Spotlight),
	// ensure the launchd agent is running rather than starting a second
	// instance. If the agent isn't registered, fall back to running inline.
	if isAppBundle() && len(os.Args) == 1 {
		if ensureLaunchdAgent() {
			return
		}
		os.Args = append(os.Args, "applet")
	}

	if err := rootCmd().Execute(); err != nil {
		tui.StatusErr("error", err.Error())
		os.Exit(1)
	}
}

// isAppBundle returns true if the running binary is inside a macOS .app bundle.
func isAppBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

func rootCmd() *cobra.Command {
	var (
		configPath    string
		codespaceName string
		editorFlag    string
		controlMaster bool
	)

	cmd := &cobra.Command{
		Use:   "cosmonaut [target]",
		Short: "Open a remote workspace (Codespaces or Coder) in your editor",
		Long: `Resolve a workspace and open it in your editor over SSH.

With a target name, settings come from the config file. Without one, the
persistent terminal applet opens — a keyboard-driven mirror of the Fyne
GUI's workspace list, per-workspace detail, and settings. See the
` + "`applet`" + ` subcommand for the tray app, ` + "`shell`" + ` for a remote SSH shell,
` + "`resolve`" + ` for a JSON dump of a workspace's SSH alias (no editor launch),
and ` + "`doctor`" + ` for environment checks.`,
		Args:              cobra.MaximumNArgs(1),
		SilenceUsage:      true,
		SilenceErrors:     true,
		ValidArgsFunction: completeTargets(&configPath),
		RunE: func(cmd *cobra.Command, args []string) error {
			var targetName string
			if len(args) > 0 {
				targetName = args[0]
			}
			var cmOverride *bool
			if cmd.Flags().Changed("control-master") {
				v := controlMaster
				cmOverride = &v
			}
			return run(configPath, targetName, codespaceName, editorFlag, cmOverride, false)
		},
	}

	cmd.PersistentFlags().StringVar(&configPath, "config", defaultConfigPath, "config file path")
	cmd.Flags().StringVar(&codespaceName, "codespace", "", "launch this codespace, skipping selection")
	cmd.Flags().StringVar(&editorFlag, "editor", "", "editor binary to launch (empty = cfg.editor, defaulting to zed with built-in settings.json integration)")
	cmd.Flags().BoolVar(&controlMaster, "control-master", true, "use SSH ControlMaster multiplexing for instant reconnects")

	_ = cmd.RegisterFlagCompletionFunc("config", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// With FilterFileExt the returned slice IS the extension list; nil
		// used to filter everything out.
		return []string{"json"}, cobra.ShellCompDirectiveFilterFileExt
	})

	cmd.AddCommand(appletCmd(&configPath))
	cmd.AddCommand(doctorCmd())
	cmd.AddCommand(launchCmd(&configPath))
	cmd.AddCommand(resolveCmd(&configPath))
	cmd.AddCommand(shellCmd(&configPath))

	return cmd
}

// completeTargets returns a ValidArgsFunction that completes target names from the config file.
func completeTargets(configPath *string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		absPath, err := filepath.Abs(*configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		cfg, err := config.LoadConfig(absPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		names := make([]string, 0, len(cfg.Targets))
		for name := range cfg.Targets {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// run is the shared core of `cosmonaut [target]` and `cosmonaut launch`.
// noPicker enforces launch's contract: every branch that would open the
// interactive applet errors instead.
func run(configPath, targetName, codespaceName, editorFlag string, controlMasterOverride *bool, noPicker bool) error {
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}

	// A malformed config is a loud error, not silently-empty defaults
	// (which surfaced as a baffling "unknown target" for every target);
	// only a genuinely missing file falls back to the zero config. Same
	// policy as `cosmonaut shell`.
	cfg, err := config.LoadConfig(absConfigPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg == nil {
		cfg = &config.Config{}
	}

	stdinTTY := term.IsTerminal(int(os.Stdin.Fd()))
	stdoutTTY := term.IsTerminal(int(os.Stdout.Fd()))
	interactive := stdinTTY && stdoutTTY

	// Bare `cosmonaut` always opens the terminal applet; pass a target
	// or --codespace for a one-shot launch.
	if interactive && targetName == "" && codespaceName == "" {
		if editorFlag != "" {
			cfg.SetEditor(editorFlag)
		}
		data := tui.NewAppletData(cfg, absConfigPath)
		return tui.RunApplet(data)
	}

	manager, err := provider.NewManager(cfg)
	if err != nil {
		return err
	}
	if err := manager.EnsurePrereqs(); err != nil {
		return err
	}

	// Authenticate.
	if interactive {
		if err := tui.RunWithSpinner("Checking "+manager.Name()+" auth", func() error {
			return manager.EnsureAuth()
		}); err != nil {
			return err
		}
	} else {
		if err := manager.EnsureAuth(); err != nil {
			return err
		}
	}

	// Resolve target metadata from the positional arg or defaultTarget.
	var target config.Target
	var resolvedTargetName string
	var selected *provider.Workspace

	switch {
	case targetName != "":
		// If the argument looks like owner/repo, treat it as a direct repo
		// name rather than a config target (used by the tray for history entries).
		if strings.Contains(targetName, "/") {
			target, resolvedTargetName = targetForRepo(cfg, targetName, manager.Name())
		} else if t, ok := cfg.Targets[targetName]; ok {
			target = t
			resolvedTargetName = targetName
		} else {
			return fmt.Errorf("unknown target %q in %s", targetName, absConfigPath)
		}
	case codespaceName != "":
		// --codespace alone launches that workspace with settings guessed
		// from the workspace itself, mirroring `cosmonaut shell
		// --codespace`. Deliberately does NOT pull in defaultTarget — the
		// named workspace may have nothing to do with it. target stays
		// zero; the flag used to be silently dropped (interactive) or
		// wrongly rejected here.
	case cfg.DefaultTarget != "":
		t, ok := cfg.Targets[cfg.DefaultTarget]
		if !ok {
			return fmt.Errorf("default target %q not found in %s", cfg.DefaultTarget, absConfigPath)
		}
		target = t
		resolvedTargetName = cfg.DefaultTarget
	case interactive && !noPicker:
		// No target, no defaultTarget, but interactive — hand off to the
		// TUI applet so the user can pick (or create). This is the case
		// where the gate above didn't fire because --editor was set; we
		// honor --editor by overriding cfg.Editor for the applet's
		// session before launching it.
		if editorFlag != "" {
			cfg.SetEditor(editorFlag)
		}
		data := tui.NewAppletData(cfg, absConfigPath)
		return tui.RunApplet(data)
	default:
		return fmt.Errorf("no target was provided and config.defaultTarget is not set")
	}

	// Direct codespace launch: bypass all selection.
	if codespaceName != "" {
		selected, err = manager.ResolveWorkspace(codespaceName)
		if err != nil {
			return fmt.Errorf("looking up workspace %q: %w", codespaceName, err)
		}
	}

	// Static-target resolution: list, then either auto-pick a single match,
	// hand off to the applet for selection (multiple matches, interactive)
	// or pre-fill the create form (no matches, interactive), or error
	// (non-interactive ambiguity / non-interactive missing match).
	if selected == nil {
		var workspaces []provider.Workspace
		if interactive {
			workspaces, err = tui.RunWithSpinnerResult("Listing workspaces", func() ([]provider.Workspace, error) {
				return manager.ListWorkspacesForTarget(target)
			})
		} else {
			workspaces, err = manager.ListWorkspacesForTarget(target)
		}
		if err != nil {
			return err
		}

		pickerOK := interactive && !noPicker
		switch {
		case len(workspaces) == 1:
			selected = &workspaces[0]
		case len(workspaces) > 1 && pickerOK:
			// Multiple matches: open the applet narrowed to the target's
			// repo so the user can pick.
			if editorFlag != "" {
				cfg.SetEditor(editorFlag)
			}
			data := tui.NewAppletData(cfg, absConfigPath)
			return tui.RunApplet(data, tui.AppletInitial{Filter: target.Repository})
		case len(workspaces) > 1:
			names := make([]string, len(workspaces))
			for i, w := range workspaces {
				names[i] = w.Name
			}
			return fmt.Errorf("ambiguous workspace match for target %q: %s (pass --codespace <name>)", targetName, strings.Join(names, ", "))
		case len(workspaces) == 0 && pickerOK:
			// No match: open the applet on the Create view with the repo
			// pre-filled so the user can confirm and create.
			if editorFlag != "" {
				cfg.SetEditor(editorFlag)
			}
			data := tui.NewAppletData(cfg, absConfigPath)
			providerName := provider.NameGitHub
			if target.Coder != nil {
				providerName = provider.NameCoder
			}
			return tui.RunApplet(data, tui.AppletInitial{Create: &tui.AppletCreateSeed{
				Provider:   providerName,
				Repository: target.Repository,
			}})
		case len(workspaces) == 0:
			return fmt.Errorf("no workspace matches target %q; pre-create it (e.g. `gh codespace create`) or pass --codespace <name>", targetName)
		}
	}

	// Record recency for the picker's sort — but only for real repo
	// launches: codespace-only launches of repo-less targets used to
	// pollute history with empty-repository entries.
	if target.Repository != "" {
		hist := history.Load()
		hist.Touch(target.Repository)
		if err := hist.Save(); err != nil {
			log.Printf("history: save: %v", err)
		}
	}

	// Resolve the editor to use (CLI flag overrides config).
	editorName := editorFlag
	if editorName == "" {
		editorName = cfg.GetEditor()
	}
	ed, err := editor.ForName(editorName)
	if err != nil {
		return err
	}

	workspacePath := provider.GuessWorkspacePath(target, selected)

	// Fast path: if the workspace is already running and we have an SSH config
	// on disk, skip the slow SSH wait + config fetch and
	// go straight to launching the editor.
	if provider.IsWorkspaceRunning(*selected) {
		paths := sshconfig.ResolvePaths()
		if alias, ok := sshconfig.ReadExistingWorkspaceAlias(paths, selected.Provider, selected.Name); ok {
			if interactive {
				tui.Status("⚡", fmt.Sprintf("Workspace already running, opening %s", ed.Name()))
			}
			return ed.LaunchRemote(alias, workspacePath)
		}
	}

	// Ensure SSH connectivity.
	if selected, err = manager.StartWorkspace(selected); err != nil {
		return err
	}
	if interactive {
		if err := tui.RunWithSpinner("Waiting for workspace SSH", func() error {
			return manager.EnsureReachable(selected)
		}); err != nil {
			return err
		}
	} else {
		if err := manager.EnsureReachable(selected); err != nil {
			return err
		}
	}

	paths := sshconfig.ResolvePaths()
	sshOpts := sshconfig.ManagedExtrasOptions{
		ControlMaster: resolveControlMaster(cfg, selected.Provider, selected.Name, controlMasterOverride),
	}
	var sshAlias string
	if interactive {
		sshAlias, err = tui.RunWithSpinnerResult("Preparing SSH config", func() (string, error) {
			return manager.PrepareSSH(paths, selected, sshOpts)
		})
	} else {
		sshAlias, err = manager.PrepareSSH(paths, selected, sshOpts)
	}
	if err != nil {
		return err
	}

	// Configure editor-specific settings (e.g. Zed's settings.json).
	nickname := editor.ResolveNickname(
		target.ZedNickname,
		target.DisplayName,
		selected.DisplayName,
		resolvedTargetName,
	)
	if err := ed.ConfigureConnection(sshAlias, workspacePath, nickname, target.UploadBinaryOverSSH); err != nil {
		return err
	}

	if interactive {
		tui.Status("✓", "SSH and editor config updated")
	}

	// Launch editor.
	if interactive {
		if err := tui.RunWithSpinner(fmt.Sprintf("Launching %s", ed.Name()), func() error {
			return ed.LaunchRemote(sshAlias, workspacePath)
		}); err != nil {
			return err
		}
	} else {
		if err := ed.LaunchRemote(sshAlias, workspacePath); err != nil {
			return err
		}
	}

	return nil
}

// targetForRepo finds a config target matching the repo, or builds a default.
func targetForRepo(cfg *config.Config, repo, _ string) (config.Target, string) {
	if cfg != nil {
		for name, t := range cfg.Targets {
			if t.Repository == repo {
				return t, name
			}
		}
	}

	parts := strings.SplitN(repo, "/", 2)
	repoName := parts[len(parts)-1]
	return config.Target{
		Repository:    repo,
		WorkspacePath: "/workspaces/" + repoName,
	}, repo
}
