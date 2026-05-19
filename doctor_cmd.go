package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/linuskendall/cosmonaut/internal/codespace"
	"github.com/linuskendall/cosmonaut/internal/doctor"
)

func doctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose and (optionally) fix problems blocking cosmonaut",
		Long: `Run a battery of checks (gh OAuth scopes, ~/.ssh/config sanity, etc.)
and report which pass and which need attention.

With --fix, programmatic fixes are applied directly. Fixes that need a
TTY (such as gh auth refresh) are printed as commands you can copy and
run yourself.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(fix)
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "apply fixes for failing checks")
	return cmd
}

func runDoctor(applyFixes bool) error {
	if err := codespace.RequireCommand("gh"); err != nil {
		return err
	}
	runner := codespace.DefaultGHRunner{}

	// Lazy: only call gh codespace list if a check actually needs it.
	var (
		listErrCalled bool
		listErrCache  error
	)
	listErr := func() error {
		if !listErrCalled {
			_, listErrCache = codespace.ListAllCodespaces(runner)
			listErrCalled = true
		}
		return listErrCache
	}

	checks := doctor.Catalog(listErr)
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
