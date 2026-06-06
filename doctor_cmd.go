package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/linuskendall/cosmonaut/internal/config"
	"github.com/linuskendall/cosmonaut/internal/doctor"
	"github.com/linuskendall/cosmonaut/internal/provider"
)

func doctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the local setup and optionally fix what's broken",
		Long: `Check gh OAuth scopes, ~/.ssh/config, CLI presence, and other
prerequisites. With --fix, in-process fixes are applied automatically;
fixes that need a TTY are printed as commands to run yourself.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, err := cmd.Flags().GetString("config")
			if err != nil {
				return err
			}
			return runDoctor(configPath, fix)
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "apply in-process fixes for failing checks")
	return cmd
}

func runDoctor(configPath string, applyFixes bool) error {
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

	// Lazy: only call provider list if a check actually needs it.
	var (
		listErrCalled bool
		listErrCache  error
	)
	listErr := func() error {
		if !listErrCalled {
			_, listErrCache = manager.ListAllWorkspaces()
			listErrCalled = true
		}
		return listErrCache
	}

	checks := doctor.CatalogForProvider(manager.Name(), listErr)
	out := os.Stdout
	failures := 0

	for _, c := range checks {
		issue := c.Status()
		if issue == nil {
			fmt.Fprintf(out, "  ok    %s\n", c.Title)
			continue
		}
		failures++
		fmt.Fprintf(out, "  fail  %s\n", c.Title)
		fmt.Fprintf(out, "        %s\n", issue.Summary)

		if !applyFixes {
			if c.HasInProcessFix() {
				fmt.Fprintln(out, "        rerun with --fix to apply automatically")
			} else if c.HasTerminalFix() {
				fmt.Fprintf(out, "        run: %s\n", c.FixCommand())
			}
			continue
		}

		switch {
		case c.HasInProcessFix():
			if err := c.Fix(); err != nil {
				fmt.Fprintf(out, "        fix failed: %v\n", err)
			} else {
				fmt.Fprintln(out, "        fix applied")
			}
		case c.HasTerminalFix():
			fmt.Fprintf(out, "        run: %s\n", c.FixCommand())
		default:
			fmt.Fprintln(out, "        no automatic fix available")
		}
	}

	if failures == 0 {
		fmt.Fprintln(out, "\nAll checks passed.")
		return nil
	}
	fmt.Fprintf(out, "\n%d check(s) need attention.\n", failures)
	return nil
}
