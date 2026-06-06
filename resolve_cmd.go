package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/provider"
	"github.com/linuskendall/cosmonaut/internal/sshconfig"
)

// resolveOutput is the JSON payload that `cosmonaut resolve` prints to stdout.
// Field names match what the now-removed `--no-open` / `--dry-run` modes
// emitted, so existing scripts only need to change the invocation.
type resolveOutput struct {
	Target        string `json:"target"`
	Workspace     string `json:"workspace"`
	Provider      string `json:"provider"`
	SSHAlias      string `json:"sshAlias"`
	WorkspacePath string `json:"workspacePath"`
	RemoteURL     string `json:"remoteUrl"`
}

// resolveCmd implements `cosmonaut resolve [target]`. It resolves a workspace
// to its SSH alias and emits a JSON document on stdout. It deliberately does
// NOT launch the editor; the previous `--no-open` / `--dry-run` flags on the
// root command were removed (commit fc56da3) but the JSON-extraction
// capability is the only scriptable way to fetch a workspace's SSH alias
// without ssh'ing or parsing ~/.ssh/cosmonaut/*.conf. This subcommand
// restores that capability with a cleaner surface area.
//
// All informational output (spinners, status lines) goes to stderr so
// `cosmonaut resolve work | jq` Just Works.
func resolveCmd(configPath *string) *cobra.Command {
	var (
		codespaceName string
		editorFlag    string
		controlMaster bool
	)
	cmd := &cobra.Command{
		Use:   "resolve [target]",
		Short: "Resolve a workspace and print its SSH alias as JSON",
		Long: `Resolve a workspace (same target / --codespace selection as ` + "`cosmonaut shell`" + `)
and print a JSON document describing how to reach it over SSH.

Unlike the root command this does NOT launch an editor; it only writes
~/.ssh/cosmonaut/<workspace>.conf (so the printed alias is usable) and
emits the alias + remote URL on stdout. All logs go to stderr.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
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
			return runResolve(*configPath, targetName, codespaceName, editorFlag, cmOverride, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&codespaceName, "codespace", "", "specific workspace name, skipping selection")
	cmd.Flags().StringVar(&editorFlag, "editor", "", "editor name (reserved; reported in output, does not launch the editor)")
	cmd.Flags().BoolVar(&controlMaster, "control-master", true, "use SSH ControlMaster multiplexing in the written SSH config")
	return cmd
}

func runResolve(configPath, targetName, codespaceName, _ string, controlMasterOverride *bool, stdout *os.File) error {
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
	if err := manager.EnsureAuth(); err != nil {
		return err
	}

	target, err := resolveShellTarget(cfg, targetName, codespaceName, manager.Name())
	if err != nil {
		return err
	}
	resolvedTargetName := targetName
	if resolvedTargetName == "" && cfg != nil {
		resolvedTargetName = cfg.DefaultTarget
	}

	selected, err := selectResolveWorkspace(manager, target, targetName, codespaceName)
	if err != nil {
		return err
	}

	if selected, err = manager.StartWorkspace(selected); err != nil {
		return err
	}
	if err := manager.EnsureReachable(selected); err != nil {
		return err
	}

	paths := sshconfig.ResolvePaths()
	sshOpts := sshconfig.ManagedExtrasOptions{
		ControlMaster: resolveControlMaster(cfg, selected.Provider, selected.Name, controlMasterOverride),
	}
	alias, err := manager.PrepareSSH(paths, selected, sshOpts)
	if err != nil {
		return err
	}

	workspacePath := guessWorkspacePath(target, selected)

	out := buildResolveOutput(resolvedTargetName, selected, alias, workspacePath)
	return writeResolveJSON(stdout, out)
}

// selectResolveWorkspace picks a single workspace for resolve, mirroring the
// non-interactive selection logic in runShell. It deliberately errors on
// ambiguity rather than prompting, because resolve is intended for scripts.
func selectResolveWorkspace(manager provider.Manager, target config.Target, targetName, codespaceName string) (*provider.Workspace, error) {
	switch {
	case codespaceName != "":
		ws, err := manager.ResolveWorkspace(codespaceName)
		if err != nil {
			return nil, err
		}
		if ws == nil {
			return nil, fmt.Errorf("workspace %q not found", codespaceName)
		}
		return ws, nil
	case target.ExplicitWorkspaceName(manager.Name()) != "":
		ws, err := manager.ResolveWorkspace(target.ExplicitWorkspaceName(manager.Name()))
		if err != nil {
			return nil, err
		}
		if ws == nil {
			return nil, fmt.Errorf("workspace %q not found", target.ExplicitWorkspaceName(manager.Name()))
		}
		return ws, nil
	default:
		matches, err := manager.ListWorkspacesForTarget(target)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no workspace found for target %q; create one with `cosmonaut` first", targetName)
		}
		if len(matches) > 1 {
			names := make([]string, len(matches))
			for i, m := range matches {
				names[i] = m.Name
			}
			return nil, fmt.Errorf("multiple workspaces match target %q (%s); pass --codespace <name>", targetName, strings.Join(names, ", "))
		}
		return &matches[0], nil
	}
}

// buildResolveOutput assembles the JSON payload from a resolved workspace.
// Split out so resolve_cmd_test.go can pin the shape without touching real
// provider plumbing.
//
// The remoteUrl uses standard ssh:// URI syntax: an absolute remote path
// becomes `ssh://host//abs/path` (two slashes — the first separates host
// from path, the second is the leading `/` of the absolute path). For
// relative paths the result is the more conventional `ssh://host/rel`.
func buildResolveOutput(targetName string, ws *provider.Workspace, alias, workspacePath string) resolveOutput {
	remoteURL := fmt.Sprintf("ssh://%s/%s", alias, workspacePath)
	providerName := ""
	workspaceName := ""
	if ws != nil {
		providerName = ws.Provider
		workspaceName = ws.Name
	}
	return resolveOutput{
		Target:        targetName,
		Workspace:     workspaceName,
		Provider:      providerName,
		SSHAlias:      alias,
		WorkspacePath: workspacePath,
		RemoteURL:     remoteURL,
	}
}

func writeResolveJSON(w *os.File, out resolveOutput) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
