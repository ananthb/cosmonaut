package main

import (
	"errors"
	"fmt"
	"io/fs"
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
	// Malformed config: fail loudly (same policy as shell/run); only a
	// missing file falls back to the zero config.
	cfg, err := config.LoadConfig(absConfigPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("loading config: %w", err)
	}
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

	printf := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format, args...) }
	println := func(args ...any) { _, _ = fmt.Fprintln(out, args...) }
	for _, c := range checks {
		issue := c.Status()
		if issue == nil {
			printf("  ok    %s\n", c.Title)
			continue
		}
		failures++
		printf("  fail  %s\n", c.Title)
		printf("        %s\n", issue.Summary)

		if !applyFixes {
			switch {
			case c.HasInProcessFix():
				println("        rerun with --fix to apply automatically")
			case c.HasTerminalFix():
				printf("        run: %s\n", c.FixCommand())
			}
			continue
		}

		switch {
		case c.HasInProcessFix():
			if err := c.Fix(); err != nil {
				printf("        fix failed: %v\n", err)
			} else {
				println("        fix applied")
			}
		case c.HasTerminalFix():
			printf("        run: %s\n", c.FixCommand())
		default:
			println("        no automatic fix available")
		}
	}

	if failures == 0 {
		println("\nAll checks passed.")
		return nil
	}
	printf("\n%d check(s) need attention.\n", failures)
	// Non-zero exit so scripts/CI can gate on `cosmonaut doctor`. The
	// message above already explains everything; cobra's SilenceErrors
	// setup means this just sets the exit code.
	return fmt.Errorf("%d doctor check(s) failing", failures)
}
