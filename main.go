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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/editor"
	"github.com/linuskendall/cosmonaut/internal/history"
	"github.com/linuskendall/cosmonaut/internal/provider"
	"github.com/linuskendall/cosmonaut/internal/slug"
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
		noOpen        bool
		dryRun        bool
		codespaceName string
		editorFlag    string
		controlMaster bool
	)

	cmd := &cobra.Command{
		Use:   "cosmonaut [target]",
		Short: "Open a remote workspace (Codespaces or Coder) in your editor",
		Long: `Resolve a workspace and open it in your editor over SSH.

With a target name, settings come from the config file. Without one, an
interactive picker lets you choose (or create) a workspace. See the
` + "`applet`" + ` subcommand for the tray app, ` + "`tui`" + ` for the same surfaces in
the terminal, ` + "`shell`" + ` for a remote SSH shell, and ` + "`doctor`" + ` for
environment checks.`,
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
			return run(configPath, targetName, codespaceName, editorFlag, noOpen, dryRun, cmOverride)
		},
	}

	cmd.PersistentFlags().StringVar(&configPath, "config", defaultConfigPath, "config file path")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "prepare SSH config and print target; don't launch the editor")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "don't create a workspace or launch the editor")
	cmd.Flags().StringVar(&codespaceName, "codespace", "", "launch this codespace, skipping selection")
	cmd.Flags().StringVar(&editorFlag, "editor", "", "zed (default) or neovim")
	cmd.Flags().BoolVar(&controlMaster, "control-master", true, "use SSH ControlMaster multiplexing for instant reconnects")

	_ = cmd.RegisterFlagCompletionFunc("config", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveFilterFileExt
	})

	cmd.AddCommand(appletCmd(&configPath))
	cmd.AddCommand(doctorCmd())
	cmd.AddCommand(shellCmd(&configPath))
	cmd.AddCommand(tuiCmd(&configPath))

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

func run(configPath, targetName, codespaceName, editorFlag string, noOpen, dryRun bool, controlMasterOverride *bool) error {
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}

	cfg, _ := config.LoadConfig(absConfigPath)
	if cfg == nil {
		cfg = &config.Config{}
	}
	manager, err := provider.NewManager(cfg)
	if err != nil {
		return err
	}
	if err := manager.EnsurePrereqs(); err != nil {
		return err
	}
	interactive := term.IsTerminal(int(os.Stdin.Fd()))

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

	// Resolve target + select workspace.
	// Dynamic mode uses a loop so the user can go back from workspace selection to repo selection.
	var target config.Target
	var resolvedTargetName string
	var selected *provider.Workspace
	dynamicMode := false

	if targetName != "" {
		// If the argument looks like owner/repo, treat it as a direct repo
		// name rather than a config target (used by the tray for history entries).
		if strings.Contains(targetName, "/") {
			target, resolvedTargetName = targetForRepo(cfg, targetName, manager.Name())
		} else if cfg == nil {
			return fmt.Errorf("target %q specified but no config file found at %s", targetName, absConfigPath)
		} else if t, ok := cfg.Targets[targetName]; ok {
			target = t
			resolvedTargetName = targetName
		} else {
			return fmt.Errorf("unknown target %q in %s", targetName, absConfigPath)
		}
	} else if cfg != nil && cfg.DefaultTarget != "" {
		t, ok := cfg.Targets[cfg.DefaultTarget]
		if !ok {
			return fmt.Errorf("default target %q not found in %s", cfg.DefaultTarget, absConfigPath)
		}
		target = t
		resolvedTargetName = cfg.DefaultTarget
	} else if interactive {
		dynamicMode = true
	} else {
		return fmt.Errorf("no target was provided and config.defaultTarget is not set")
	}

	// Direct codespace launch: bypass all TUI selection.
	if codespaceName != "" {
		if target.Repository == "" && target.ExplicitWorkspaceName(manager.Name()) == "" {
			return fmt.Errorf("--codespace requires a target or repo argument to resolve workspace settings")
		}
		selected, err = manager.ResolveWorkspace(codespaceName)
		if err != nil {
			return fmt.Errorf("looking up workspace %q: %w", codespaceName, err)
		}
	}

	if selected != nil {
		// Already resolved (e.g. --codespace flag); skip selection.
	} else if dynamicMode {
		if manager.Name() == provider.NameCoder {
			var allWorkspaces []provider.Workspace
			allWorkspaces, err = tui.RunWithSpinnerResult("Fetching your coder workspaces", func() ([]provider.Workspace, error) {
				return manager.ListAllWorkspaces()
			})
			if err != nil {
				return err
			}
			if len(allWorkspaces) == 0 {
				return fmt.Errorf("no coder workspaces found and no target was provided")
			}
			target = config.Target{WorkspacePath: guessWorkspacePath(target, nil)}
			resolvedTargetName = allWorkspaces[0].Name
			sel, del, selErr := runSelectionTUI(allWorkspaces, target, dryRun)
			if selErr != nil {
				return selErr
			}
			if del != nil {
				return fmt.Errorf("workspace deletion is not supported for provider %q", manager.Name())
			}
			selected = sel
			if selected != nil {
				target = applyWorkspaceDefaults(target, *selected)
				resolvedTargetName = selected.Name
			}
		} else {
			// Fetch all workspaces and all user repos for the repo picker.
			var allWorkspaces []provider.Workspace
			var allUserRepos []string
			allWorkspaces, err = tui.RunWithSpinnerResult("Fetching your workspaces", func() ([]provider.Workspace, error) {
				return manager.ListAllWorkspaces()
			})
			if err != nil {
				return err
			}
			allUserRepos, err = tui.RunWithSpinnerResult("Fetching your repositories", func() ([]string, error) {
				return manager.ListRepositories()
			})
			if err != nil {
				return err
			}

			repos := provider.UniqueRepos(allWorkspaces)
			repos = mergeRepos(repos, configRepos(cfg))
			repos = mergeRepos(repos, allUserRepos)

			hist := history.Load()
			sorted := hist.SortRepos(repos)
			recentCount := countRecent(sorted, hist)

			// Loop: repo selection → workspace selection (with back).
			for {
				repo, err := tui.RunRepoSelection(sorted, recentCount)
				if err != nil {
					return err
				}

				target, resolvedTargetName = targetForRepo(cfg, repo, manager.Name())
				repoWorkspaces := provider.FilterByRepo(allWorkspaces, repo)

				if len(repoWorkspaces) == 0 {
					selected = nil
					break
				}

				sel, back, del, err := runSelectionTUIWithBack(repoWorkspaces, target, dryRun)
				if err != nil {
					return err
				}
				if back {
					continue
				}
				if del != nil {
					if err := deleteWorkspaceWithSpinner(manager, del.Name); err != nil {
						return err
					}
					allWorkspaces = removeWorkspace(allWorkspaces, del.Name)
					repos = provider.UniqueRepos(allWorkspaces)
					sorted = hist.SortRepos(repos)
					recentCount = countRecent(sorted, hist)
					if len(repos) == 0 {
						return fmt.Errorf("no workspaces remain")
					}
					continue
				}
				selected = sel
				break
			}
		}
	} else {
		// Static target: list workspaces for the specific target.
		allowBack := interactive && targetName == ""

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

		wentBack := false
		if len(workspaces) > 0 {
			if len(workspaces) == 1 && !interactive {
				selected = &workspaces[0]
			} else if interactive {
				for {
					if allowBack {
						sel, back, del, selErr := runSelectionTUIWithBack(workspaces, target, dryRun)
						if selErr != nil {
							return selErr
						}
						if back {
							wentBack = true
							break
						}
						if del != nil {
							if delErr := deleteWorkspaceWithSpinner(manager, del.Name); delErr != nil {
								return delErr
							}
							workspaces = removeWorkspace(workspaces, del.Name)
							if len(workspaces) == 0 {
								break
							}
							continue
						}
						selected = sel
						break
					} else {
						sel, del, selErr := runSelectionTUI(workspaces, target, dryRun)
						if selErr != nil {
							return selErr
						}
						if del != nil {
							if delErr := deleteWorkspaceWithSpinner(manager, del.Name); delErr != nil {
								return delErr
							}
							workspaces = removeWorkspace(workspaces, del.Name)
							if len(workspaces) == 0 {
								break
							}
							continue
						}
						selected = sel
						break
					}
				}
			} else {
				selected, err = provider.ChooseWorkspace(workspaces, &target)
				if err != nil {
					return err
				}
			}
		} else if allowBack {
			wentBack = true
		}

		if wentBack {
			var allWorkspaces []provider.Workspace
			var allUserRepos []string
			allWorkspaces, err = tui.RunWithSpinnerResult("Fetching your workspaces", func() ([]provider.Workspace, error) {
				return manager.ListAllWorkspaces()
			})
			if err != nil {
				return err
			}
			allUserRepos, err = tui.RunWithSpinnerResult("Fetching your repositories", func() ([]string, error) {
				return manager.ListRepositories()
			})
			if err != nil {
				return err
			}

			repos := provider.UniqueRepos(allWorkspaces)
			repos = mergeRepos(repos, configRepos(cfg))
			repos = mergeRepos(repos, allUserRepos)

			hist := history.Load()
			sorted := hist.SortRepos(repos)
			recentCount := countRecent(sorted, hist)

			for {
				repo, repoErr := tui.RunRepoSelection(sorted, recentCount)
				if repoErr != nil {
					return repoErr
				}

				target, resolvedTargetName = targetForRepo(cfg, repo, manager.Name())
				repoWorkspaces := provider.FilterByRepo(allWorkspaces, repo)

				if len(repoWorkspaces) == 0 {
					selected = nil
					break
				}

				sel, back, del, selErr := runSelectionTUIWithBack(repoWorkspaces, target, dryRun)
				if selErr != nil {
					return selErr
				}
				if back {
					continue
				}
				if del != nil {
					if delErr := deleteWorkspaceWithSpinner(manager, del.Name); delErr != nil {
						return delErr
					}
					allWorkspaces = removeWorkspace(allWorkspaces, del.Name)
					repos = provider.UniqueRepos(allWorkspaces)
					sorted = hist.SortRepos(repos)
					recentCount = countRecent(sorted, hist)
					if len(repos) == 0 {
						return fmt.Errorf("no workspaces remain")
					}
					continue
				}
				selected = sel
				break
			}
		}
	}

	// Create workspace if needed.
	if selected == nil {
		if dryRun {
			return fmt.Errorf("no matching workspace exists and --dry-run forbids creating one")
		}

		createTarget := target
		if interactive && manager.Name() == provider.NameGitHub {
			workLabel, err := runWorkLabelTUI()
			if err != nil {
				return err
			}
			if workLabel != "" {
				createTarget.DisplayName = slug.BuildDisplayName(
					target.Repository,
					target.Branch,
					workLabel,
					target.DisplayName,
				)
			}
		}

		if interactive && manager.Name() == provider.NameGitHub {
			fmt.Fprintf(os.Stderr, "  Creating workspace…\n")
		}
		ws, createErr := manager.CreateWorkspace(createTarget, interactive)
		if createErr != nil {
			return createErr
		}
		if interactive {
			tui.Status("✓", "Workspace created")
		}
		selected = ws
	}

	// Record repo in history.
	hist := history.Load()
	hist.Touch(target.Repository)
	hist.Save()

	// Resolve the editor to use (CLI flag overrides config).
	editorName := editorFlag
	if editorName == "" && cfg != nil {
		editorName = cfg.Editor
	}
	ed, err := editor.ForName(editorName)
	if err != nil {
		return err
	}

	workspacePath := guessWorkspacePath(target, selected)

	// Fast path: if the workspace is already running and we have an SSH config
	// on disk, skip the slow SSH wait + config fetch and
	// go straight to launching the editor.
	if isWorkspaceRunning(*selected) {
		paths := sshconfig.ResolvePaths()
		if alias, ok := sshconfig.ReadExistingWorkspaceAlias(paths, selected.Provider, selected.Name); ok {
			if interactive {
				tui.Status("⚡", fmt.Sprintf("Workspace already running, opening %s", ed.Name()))
			}
			if !dryRun && !noOpen {
				return ed.LaunchRemote(alias, workspacePath)
			}
			if dryRun || noOpen {
				remoteURL := fmt.Sprintf("ssh://%s/%s", alias, strings.TrimLeft(workspacePath, "/"))
				output := map[string]string{
					"target":    resolvedTargetName,
					"workspace": selected.Name,
					"provider":  selected.Provider,
					"sshAlias":  alias,
					"remoteUrl": remoteURL,
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(output)
			}
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

	if dryRun || noOpen {
		remoteURL := fmt.Sprintf("ssh://%s/%s", sshAlias, strings.TrimLeft(workspacePath, "/"))
		output := map[string]string{
			"target":    resolvedTargetName,
			"workspace": selected.Name,
			"provider":  selected.Provider,
			"sshAlias":  sshAlias,
			"remoteUrl": remoteURL,
			"editor":    ed.Name(),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
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

func countRecent(sorted []string, hist *history.History) int {
	n := 0
	for _, repo := range sorted {
		found := false
		for _, e := range hist.Entries {
			if e.Repository == repo {
				found = true
				break
			}
		}
		if !found {
			break
		}
		n++
	}
	return n
}

// configRepos returns the unique repository names from config targets.
func configRepos(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	seen := make(map[string]bool)
	var repos []string
	for _, t := range cfg.Targets {
		if t.Repository != "" && !seen[t.Repository] {
			seen[t.Repository] = true
			repos = append(repos, t.Repository)
		}
	}
	return repos
}

// mergeRepos adds extra repos to the list, skipping duplicates.
func mergeRepos(base, extra []string) []string {
	seen := make(map[string]bool, len(base))
	for _, r := range base {
		seen[r] = true
	}
	result := make([]string, len(base))
	copy(result, base)
	for _, r := range extra {
		if !seen[r] {
			seen[r] = true
			result = append(result, r)
		}
	}
	return result
}

func applyWorkspaceDefaults(target config.Target, ws provider.Workspace) config.Target {
	if target.Repository == "" && ws.Repository != "" {
		target.Repository = ws.Repository
	}
	if target.WorkspacePath == "" {
		target.WorkspacePath = guessWorkspacePath(target, &ws)
	}
	return target
}

func guessWorkspacePath(target config.Target, ws *provider.Workspace) string {
	if target.WorkspacePath != "" {
		return target.WorkspacePath
	}
	if ws != nil && ws.Provider == provider.NameCoder {
		return "/workspaces/" + ws.Name
	}
	if target.Repository != "" {
		parts := strings.SplitN(target.Repository, "/", 2)
		return "/workspaces/" + parts[len(parts)-1]
	}
	if ws != nil && ws.Name != "" {
		return "/workspaces/" + ws.Name
	}
	return "/workspaces"
}

func isWorkspaceRunning(ws provider.Workspace) bool {
	state := strings.ToLower(ws.State)
	return state == "available" || state == "ready" || state == "running" || state == "connected"
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

// runSelectionTUI runs the workspace selector without back support (static target mode).
func runSelectionTUI(workspaces []provider.Workspace, target config.Target, dryRun bool) (*provider.Workspace, *provider.Workspace, error) {
	model := tui.NewSelectModel(workspaces, target, dryRun, false)
	p := tea.NewProgram(model, tea.WithMouseCellMotion())
	finalModel, err := p.Run()
	if err != nil {
		return nil, nil, err
	}

	result := finalModel.(tui.SelectModel).Result()
	if result.Quit {
		os.Exit(0)
	}
	if result.Delete != nil {
		return nil, result.Delete, nil
	}

	if result.Selected == nil && dryRun {
		return nil, nil, fmt.Errorf("no matching workspace exists and --dry-run forbids creating one")
	}

	return result.Selected, nil, nil
}

// runSelectionTUIWithBack runs the workspace selector with back support (dynamic mode).
func runSelectionTUIWithBack(workspaces []provider.Workspace, target config.Target, dryRun bool) (*provider.Workspace, bool, *provider.Workspace, error) {
	model := tui.NewSelectModel(workspaces, target, dryRun, true)
	p := tea.NewProgram(model, tea.WithMouseCellMotion())
	finalModel, err := p.Run()
	if err != nil {
		return nil, false, nil, err
	}

	result := finalModel.(tui.SelectModel).Result()
	if result.Quit {
		os.Exit(0)
	}
	if result.Back {
		return nil, true, nil, nil
	}
	if result.Delete != nil {
		return nil, false, result.Delete, nil
	}

	if result.Selected == nil && dryRun {
		return nil, false, nil, fmt.Errorf("no matching workspace exists and --dry-run forbids creating one")
	}

	return result.Selected, false, nil, nil
}

func deleteWorkspaceWithSpinner(manager provider.Manager, name string) error {
	return tui.RunWithSpinner("Deleting workspace "+name, func() error {
		return manager.DeleteWorkspace(name)
	})
}

func removeWorkspace(workspaces []provider.Workspace, name string) []provider.Workspace {
	var result []provider.Workspace
	for _, ws := range workspaces {
		if ws.Name != name {
			result = append(result, ws)
		}
	}
	return result
}

func runWorkLabelTUI() (string, error) {
	model := tui.NewWorkLabelModel()
	p := tea.NewProgram(model)
	finalModel, err := p.Run()
	if err != nil {
		return "", err
	}

	result := finalModel.(tui.WorkLabelModel).Result()
	if result.Quit {
		os.Exit(0)
	}

	return result.Label, nil
}
