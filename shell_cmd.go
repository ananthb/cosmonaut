package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/provider"
	"github.com/linuskendall/cosmonaut/internal/sshconfig"
)

// shellCmd implements `cosmonaut shell [target]`. It opens an interactive SSH
// session to the resolved workspace in the current terminal, optionally
// wrapping it in `tmux new -A -s cosmonaut` so the shell survives SSH drops.
//
// Unlike the root `cosmonaut` command, shell doesn't touch editor config or
// create workspaces — the workspace must already exist and be reachable.
// This keeps the command snappy and safe to use as a fallback when an editor
// session drops mid-flight.
func shellCmd(configPath *string) *cobra.Command {
	var (
		codespaceName string
		tmux          bool
		controlMaster bool
	)
	cmd := &cobra.Command{
		Use:   "shell [target]",
		Short: "Open an SSH shell to a workspace (optionally wrapped in tmux)",
		Long: `Open an interactive SSH shell to a workspace in the current terminal.

When --tmux is set (or the workspace has tmux enabled in config), the shell
runs inside a long-lived tmux session named "cosmonaut" on the remote — so an
SSH drop, sleep, or network change can be recovered by re-running the command
and reattaching to the same shell.

ControlMaster multiplexing (enabled by default) makes repeat invocations
share a TCP connection, so reconnects feel instant.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var targetName string
			if len(args) > 0 {
				targetName = args[0]
			}
			var tmuxOverride, cmOverride *bool
			if cmd.Flags().Changed("tmux") {
				v := tmux
				tmuxOverride = &v
			}
			if cmd.Flags().Changed("control-master") {
				v := controlMaster
				cmOverride = &v
			}
			return runShell(*configPath, targetName, codespaceName, tmuxOverride, cmOverride)
		},
	}
	cmd.Flags().StringVar(&codespaceName, "codespace", "", "specific codespace/workspace name (skip selection)")
	cmd.Flags().BoolVar(&tmux, "tmux", false, "wrap the remote shell in `tmux new -A -s cosmonaut` (overrides per-workspace config)")
	cmd.Flags().BoolVar(&controlMaster, "control-master", true, "enable OpenSSH ControlMaster multiplexing (overrides per-workspace config)")
	return cmd
}

func runShell(configPath, targetName, codespaceName string, tmuxOverride, controlMasterOverride *bool) error {
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

	target, resolvedTargetName, err := resolveShellTarget(cfg, targetName, codespaceName, manager.Name())
	if err != nil {
		return err
	}
	_ = resolvedTargetName

	var selected *provider.Workspace
	switch {
	case codespaceName != "":
		selected, err = manager.ResolveWorkspace(codespaceName)
	case target.ExplicitWorkspaceName(manager.Name()) != "":
		selected, err = manager.ResolveWorkspace(target.ExplicitWorkspaceName(manager.Name()))
	default:
		matches, listErr := manager.ListWorkspacesForTarget(target)
		if listErr != nil {
			return listErr
		}
		if len(matches) == 0 {
			return fmt.Errorf("no workspace found for target %q; create one with `cosmonaut` first", targetName)
		}
		if len(matches) > 1 {
			names := make([]string, len(matches))
			for i, m := range matches {
				names[i] = m.Name
			}
			return fmt.Errorf("multiple workspaces match target %q (%s); pass --codespace <name>", targetName, strings.Join(names, ", "))
		}
		selected = &matches[0]
	}
	if err != nil {
		return err
	}
	if selected == nil {
		return fmt.Errorf("could not resolve a workspace")
	}

	if _, err := manager.StartWorkspace(selected); err != nil {
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
	useTmux := resolveTmux(cfg, selected.Provider, selected.Name, tmuxOverride)
	return execSSHShell(alias, workspacePath, useTmux)
}

// resolveShellTarget mirrors the root command's target resolution but skips
// the interactive picker — `cosmonaut shell` is expected to be scripted.
func resolveShellTarget(cfg *config.Config, targetName, codespaceName, providerName string) (config.Target, string, error) {
	if codespaceName != "" && targetName == "" {
		return config.Target{}, codespaceName, nil
	}
	if targetName == "" {
		if cfg != nil && cfg.DefaultTarget != "" {
			t, ok := cfg.Targets[cfg.DefaultTarget]
			if !ok {
				return config.Target{}, "", fmt.Errorf("default target %q not found", cfg.DefaultTarget)
			}
			return t, cfg.DefaultTarget, nil
		}
		return config.Target{}, "", fmt.Errorf("no target was provided and config.defaultTarget is not set")
	}
	if strings.Contains(targetName, "/") {
		t, name := targetForRepo(cfg, targetName, providerName)
		return t, name, nil
	}
	if cfg == nil {
		return config.Target{}, "", fmt.Errorf("target %q specified but no config file found", targetName)
	}
	t, ok := cfg.Targets[targetName]
	if !ok {
		return config.Target{}, "", fmt.Errorf("unknown target %q", targetName)
	}
	return t, targetName, nil
}

// execSSHShell replaces the current process with `ssh -t <alias> '<cmd>'`.
// On success it never returns.
func execSSHShell(alias, workspacePath string, useTmux bool) error {
	remoteCmd := buildRemoteShellCommand(workspacePath, useTmux)
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found on PATH: %w", err)
	}
	args := []string{"ssh", "-t", alias, remoteCmd}
	return syscallExec(sshBin, args, os.Environ())
}

// buildRemoteShellCommand returns the command to run on the remote.
// When useTmux is true, attach to a long-lived session named "cosmonaut"
// (creating it on first connect); the `-A` flag means `tmux new` attaches
// to an existing session instead of erroring.
func buildRemoteShellCommand(workspacePath string, useTmux bool) string {
	cd := ""
	if workspacePath != "" {
		cd = fmt.Sprintf("cd %s && ", shellQuote(workspacePath))
	}
	if useTmux {
		return cd + "tmux new -A -s cosmonaut"
	}
	return cd + "exec $SHELL -l"
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\"'$`\\!*?[]{}<>|&;()") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
