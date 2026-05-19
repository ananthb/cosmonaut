// cosmonaut starts or creates GitHub Codespaces and opens them in your
// editor (Zed or Neovim) via SSH remoting.
//
// The tool performs the following steps:
//  1. Authenticate with GitHub via the gh CLI
//  2. Resolve a target repository and codespace (interactive or from config)
//  3. Create a codespace if no match exists
//  4. Fetch the codespace's SSH config and write it to ~/.ssh/cosmonaut/
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

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/editor"
	"github.com/linuskendall/cosmonaut/internal/history"
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
	)

	cmd := &cobra.Command{
		Use:   "cosmonaut [target]",
		Short: "Start or create GitHub Codespaces and open them in your editor",
		Long: `cosmonaut connects GitHub Codespaces to your editor via SSH remoting.

When a target name is given, its definition is read from the config file.
Without a target, an interactive TUI lets you pick a repository (with
type-ahead filtering across all your GitHub repos) and select or create
a codespace.

Config file fields:
` + config.TargetFieldsHelp(),
		Args:              cobra.MaximumNArgs(1),
		SilenceUsage:      true,
		SilenceErrors:     true,
		ValidArgsFunction: completeTargets(&configPath),
		RunE: func(cmd *cobra.Command, args []string) error {
			var targetName string
			if len(args) > 0 {
				targetName = args[0]
			}
			return run(configPath, targetName, codespaceName, editorFlag, noOpen, dryRun)
		},
	}

	cmd.PersistentFlags().StringVar(&configPath, "config", defaultConfigPath, "path to config file")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "update SSH/Zed config and print target without launching Zed")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "do not create codespace or launch Zed")
	cmd.Flags().StringVar(&codespaceName, "codespace", "", "launch a specific codespace by name (skip selection)")
	cmd.Flags().StringVar(&editorFlag, "editor", "", "editor to use: zed (default) or neovim")

	_ = cmd.RegisterFlagCompletionFunc("config", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveFilterFileExt
	})

	cmd.AddCommand(appletCmd(&configPath))
	cmd.AddCommand(doctorCmd())

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

func run(configPath, targetName, codespaceName, editorFlag string, noOpen, dryRun bool) error {
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}

	cfg, _ := config.LoadConfig(absConfigPath)

	if err := codespace.RequireCommand("gh"); err != nil {
		return err
	}

	runner := codespace.DefaultGHRunner{}
	interactive := term.IsTerminal(int(os.Stdin.Fd()))

	// Authenticate.
	if interactive {
		if err := tui.RunWithSpinner("Checking GitHub auth", func() error {
			return codespace.EnsureGHAuth(runner)
		}); err != nil {
			return err
		}
	} else {
		if err := codespace.EnsureGHAuth(runner); err != nil {
			return err
		}
	}

	// Resolve target + select codespace.
	// Dynamic mode uses a loop so the user can go back from codespace selection to repo selection.
	var target config.Target
	var resolvedTargetName string
	var selected *codespace.Codespace
	dynamicMode := false

	if targetName != "" {
		// If the argument looks like owner/repo, treat it as a direct repo
		// name rather than a config target (used by the tray for history entries).
		if strings.Contains(targetName, "/") {
			target, resolvedTargetName = targetForRepo(cfg, targetName)
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
		if target.Repository == "" {
			return fmt.Errorf("--codespace requires a target or repo argument to resolve workspace settings")
		}
		// Fetch full codespace details so we have state for the fast path.
		out, csErr := runner.Run([]string{
			"codespace", "view",
			"--codespace", codespaceName,
			"--json", "name,displayName,repository,state,gitStatus,machineName,createdAt,lastUsedAt",
		})
		if csErr != nil {
			return fmt.Errorf("looking up codespace %q: %w", codespaceName, csErr)
		}
		var cs codespace.Codespace
		if csErr := json.Unmarshal([]byte(out), &cs); csErr != nil {
			return fmt.Errorf("parsing codespace %q: %w", codespaceName, csErr)
		}
		selected = &cs
	}

	if selected != nil {
		// Already resolved (e.g. --codespace flag); skip selection.
	} else if dynamicMode {
		// Fetch all codespaces and all user repos for the repo picker.
		var allCodespaces []codespace.Codespace
		var allUserRepos []string
		allCodespaces, err = tui.RunWithSpinnerResult("Fetching your codespaces", func() ([]codespace.Codespace, error) {
			return codespace.ListAllCodespaces(runner)
		})
		if err != nil {
			return err
		}
		allUserRepos, err = tui.RunWithSpinnerResult("Fetching your repositories", func() ([]string, error) {
			return codespace.ListAllRepos(runner)
		})
		if err != nil {
			return err
		}

		repos := codespace.UniqueRepos(allCodespaces)
		repos = mergeRepos(repos, configRepos(cfg))
		repos = mergeRepos(repos, allUserRepos)

		hist := history.Load()
		sorted := hist.SortRepos(repos)
		recentCount := countRecent(sorted, hist)

		// Loop: repo selection → codespace selection (with back).
		for {
			repo, err := tui.RunRepoSelection(sorted, recentCount)
			if err != nil {
				return err
			}

			target, resolvedTargetName = targetForRepo(cfg, repo)
			repoCodespaces := codespace.FilterByRepo(allCodespaces, repo)

			if len(repoCodespaces) == 0 {
				// No existing codespaces: skip selection, go straight to creation.
				selected = nil
				break
			}

			sel, back, del, err := runSelectionTUIWithBack(repoCodespaces, target, dryRun)
			if err != nil {
				return err
			}
			if back {
				continue // go back to repo picker
			}
			if del != nil {
				if err := deleteCodespaceWithSpinner(runner, del.Name); err != nil {
					return err
				}
				// Remove from cached list and retry selection.
				allCodespaces = removeCodespace(allCodespaces, del.Name)
				repos = codespace.UniqueRepos(allCodespaces)
				sorted = hist.SortRepos(repos)
				recentCount = countRecent(sorted, hist)
				if len(repos) == 0 {
					return fmt.Errorf("no codespaces remain: create one with `gh codespace create` first")
				}
				continue
			}
			selected = sel
			break
		}
	} else {
		// Static target: list codespaces for the specific repo.
		// When using a default target interactively (no explicit target name),
		// allow the user to go back to pick a different repo.
		allowBack := interactive && targetName == ""

		var codespaces []codespace.Codespace
		if interactive {
			codespaces, err = tui.RunWithSpinnerResult("Listing codespaces for "+target.Repository, func() ([]codespace.Codespace, error) {
				return codespace.ListCodespaces(runner, target.Repository)
			})
		} else {
			codespaces, err = codespace.ListCodespaces(runner, target.Repository)
		}
		if err != nil {
			return err
		}

		wentBack := false
		if len(codespaces) > 0 {
			// Auto-select when there's only one codespace for this repo
			// in non-interactive mode (e.g. when launched from the applet).
			if len(codespaces) == 1 && !interactive {
				selected = &codespaces[0]
			} else if interactive {
				for {
					if allowBack {
						sel, back, del, selErr := runSelectionTUIWithBack(codespaces, target, dryRun)
						if selErr != nil {
							return selErr
						}
						if back {
							wentBack = true
							break
						}
						if del != nil {
							if delErr := deleteCodespaceWithSpinner(runner, del.Name); delErr != nil {
								return delErr
							}
							codespaces = removeCodespace(codespaces, del.Name)
							if len(codespaces) == 0 {
								break
							}
							continue
						}
						selected = sel
						break
					} else {
						sel, del, selErr := runSelectionTUI(codespaces, target, dryRun)
						if selErr != nil {
							return selErr
						}
						if del != nil {
							if delErr := deleteCodespaceWithSpinner(runner, del.Name); delErr != nil {
								return delErr
							}
							codespaces = removeCodespace(codespaces, del.Name)
							if len(codespaces) == 0 {
								break
							}
							continue
						}
						selected = sel
						break
					}
				}
			} else {
				selected, err = codespace.ChooseCodespace(codespaces, &target)
				if err != nil {
					return err
				}
			}
		} else if allowBack {
			// No codespaces for default target: let user pick another repo.
			wentBack = true
		}

		// If user went back from default target, fall into dynamic repo selection.
		if wentBack {
			var allCodespaces []codespace.Codespace
			var allUserRepos []string
			allCodespaces, err = tui.RunWithSpinnerResult("Fetching your codespaces", func() ([]codespace.Codespace, error) {
				return codespace.ListAllCodespaces(runner)
			})
			if err != nil {
				return err
			}
			allUserRepos, err = tui.RunWithSpinnerResult("Fetching your repositories", func() ([]string, error) {
				return codespace.ListAllRepos(runner)
			})
			if err != nil {
				return err
			}

			repos := codespace.UniqueRepos(allCodespaces)
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

				target, resolvedTargetName = targetForRepo(cfg, repo)
				repoCodespaces := codespace.FilterByRepo(allCodespaces, repo)

				if len(repoCodespaces) == 0 {
					selected = nil
					break
				}

				sel, back, del, selErr := runSelectionTUIWithBack(repoCodespaces, target, dryRun)
				if selErr != nil {
					return selErr
				}
				if back {
					continue
				}
				if del != nil {
					if delErr := deleteCodespaceWithSpinner(runner, del.Name); delErr != nil {
						return delErr
					}
					allCodespaces = removeCodespace(allCodespaces, del.Name)
					repos = codespace.UniqueRepos(allCodespaces)
					sorted = hist.SortRepos(repos)
					recentCount = countRecent(sorted, hist)
					if len(repos) == 0 {
						return fmt.Errorf("no codespaces remain: create one with `gh codespace create` first")
					}
					continue
				}
				selected = sel
				break
			}
		}
	}

	// Create codespace if needed.
	if selected == nil {
		if dryRun {
			return fmt.Errorf("no matching codespace exists and --dry-run forbids creating one")
		}

		createTarget := target
		if interactive {
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

		if interactive {
			// Run interactively (not inside a spinner) so gh can prompt
			// the user if it needs to (e.g. machine type selection).
			fmt.Fprintf(os.Stderr, "  Creating codespace…\n")
			cs, createErr := codespace.CreateCodespaceInteractive(runner, createTarget)
			if createErr != nil {
				return createErr
			}
			tui.Status("✓", "Codespace created")
			selected = cs
		} else {
			cs, createErr := codespace.CreateCodespace(runner, createTarget)
			if createErr != nil {
				return createErr
			}
			selected = cs
		}
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

	// Fast path: if the codespace is already Available and we have an
	// SSH config on disk, skip the slow SSH wait + config fetch and
	// go straight to launching the editor.
	if selected.State == "Available" {
		paths := sshconfig.ResolvePaths()
		if alias, ok := sshconfig.ReadExistingAlias(paths.IncludeDir, selected.Name); ok {
			if interactive {
				tui.Status("⚡", fmt.Sprintf("Codespace already running, opening %s", ed.Name()))
			}
			if !dryRun && !noOpen {
				return ed.LaunchRemote(alias, target.WorkspacePath)
			}
			if dryRun || noOpen {
				remoteURL := fmt.Sprintf("ssh://%s/%s", alias, strings.TrimLeft(target.WorkspacePath, "/"))
				output := map[string]string{
					"target":    resolvedTargetName,
					"codespace": selected.Name,
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
	if interactive {
		if err := tui.RunWithSpinner("Waiting for codespace SSH", func() error {
			return codespace.EnsureReachable(runner, selected.Name)
		}); err != nil {
			return err
		}
	} else {
		if err := codespace.EnsureReachable(runner, selected.Name); err != nil {
			return err
		}
	}

	// Get SSH config.
	var sshCfg string
	if interactive {
		sshCfg, err = tui.RunWithSpinnerResult("Fetching SSH config", func() (string, error) {
			return codespace.GetSSHConfig(runner, selected.Name)
		})
	} else {
		sshCfg, err = codespace.GetSSHConfig(runner, selected.Name)
	}
	if err != nil {
		return err
	}

	sshAlias, err := sshconfig.ParsePrimaryHostAlias(sshCfg)
	if err != nil {
		return err
	}

	// Write SSH config.
	paths := sshconfig.ResolvePaths()
	if err := os.MkdirAll(paths.IncludeDir, 0700); err != nil {
		return err
	}
	if err := sshconfig.EnsureConfigIncludesGenerated(paths.MainConfigPath); err != nil {
		return err
	}
	if err := sshconfig.WriteCodespaceConfig(paths.IncludeDir, selected.Name, sshCfg); err != nil {
		return err
	}

	// Configure editor-specific settings (e.g. Zed's settings.json).
	nickname := editor.ResolveNickname(
		target.ZedNickname,
		target.DisplayName,
		selected.DisplayName,
		resolvedTargetName,
	)
	if err := ed.ConfigureConnection(sshAlias, target.WorkspacePath, nickname, target.UploadBinaryOverSSH); err != nil {
		return err
	}

	if interactive {
		tui.Status("✓", "SSH and editor config updated")
	}

	if dryRun || noOpen {
		remoteURL := fmt.Sprintf("ssh://%s/%s", sshAlias, strings.TrimLeft(target.WorkspacePath, "/"))
		output := map[string]string{
			"target":    resolvedTargetName,
			"codespace": selected.Name,
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
			return ed.LaunchRemote(sshAlias, target.WorkspacePath)
		}); err != nil {
			return err
		}
	} else {
		if err := ed.LaunchRemote(sshAlias, target.WorkspacePath); err != nil {
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

// targetForRepo finds a config target matching the repo, or builds a default.
func targetForRepo(cfg *config.Config, repo string) (config.Target, string) {
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

// runSelectionTUI runs the codespace selector without back support (static target mode).
func runSelectionTUI(codespaces []codespace.Codespace, target config.Target, dryRun bool) (*codespace.Codespace, *codespace.Codespace, error) {
	model := tui.NewSelectModel(codespaces, target, dryRun, false)
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
		return nil, nil, fmt.Errorf("no matching codespace exists and --dry-run forbids creating one")
	}

	return result.Selected, nil, nil
}

// runSelectionTUIWithBack runs the codespace selector with back support (dynamic mode).
func runSelectionTUIWithBack(codespaces []codespace.Codespace, target config.Target, dryRun bool) (*codespace.Codespace, bool, *codespace.Codespace, error) {
	model := tui.NewSelectModel(codespaces, target, dryRun, true)
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
		return nil, false, nil, fmt.Errorf("no matching codespace exists and --dry-run forbids creating one")
	}

	return result.Selected, false, nil, nil
}

func deleteCodespaceWithSpinner(runner codespace.GHRunner, name string) error {
	return tui.RunWithSpinner("Deleting codespace "+name, func() error {
		return codespace.DeleteCodespace(runner, name)
	})
}

func removeCodespace(codespaces []codespace.Codespace, name string) []codespace.Codespace {
	var result []codespace.Codespace
	for _, cs := range codespaces {
		if cs.Name != name {
			result = append(result, cs)
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
