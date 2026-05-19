// Package doctor centralizes diagnostic checks and their fixes for
// problems that block cosmonaut from working: missing GitHub token
// scopes, hostile SSH config, etc.
//
// The same catalog drives:
//   - GUI banners on the main window
//   - the Health section in the settings page
//   - the `cosmonaut doctor` CLI subcommand
//
// so a check added here surfaces in all three places without duplication.
package doctor

import (
	"fmt"
	"strings"

	"github.com/linuskendall/cosmonaut/internal/sshconfig"
)

// Severity controls how a UI surfaces an active issue.
type Severity int

const (
	SeverityWarning Severity = iota
	SeverityError
)

// Check is a single diagnostic, optionally with a fix.
type Check struct {
	ID          string
	Title       string
	Description string

	// Status returns the active issue, or nil if the check passes.
	Status func() *Issue

	// FixCommand returns the bare shell command the user should run in a
	// TTY to apply the fix (e.g. `gh auth refresh ...`). Empty when the
	// fix is fully programmatic.
	FixCommand func() string

	// Fix applies the fix in-process. Used for fixes that don't need a
	// terminal. Returns an error on failure.
	Fix func() error
}

// HasInProcessFix reports whether the check can be fixed without a TTY.
func (c Check) HasInProcessFix() bool { return c.Fix != nil }

// HasTerminalFix reports whether the fix needs a TTY.
func (c Check) HasTerminalFix() bool {
	if c.FixCommand == nil {
		return false
	}
	return c.FixCommand() != ""
}

// Issue describes an active problem.
type Issue struct {
	Severity Severity
	Summary  string
}

// Stable IDs for checks. Exported so dismissal state and other call
// sites can refer to a check without string-matching the title.
const (
	CodespaceScopeID = "gh-codespace-scope"
	HostStarID       = "ssh-host-star"
)

// Catalog returns all checks. listErr is the most recent error from a
// `gh codespace list` attempt; the daemon supplies its cached value, the
// CLI runs a fresh list at call time.
func Catalog(listErr func() error) []Check {
	return []Check{
		ghCodespaceScopeCheck(listErr),
		sshHostStarCheck(),
	}
}

// FindByID returns the check with the given ID, or nil.
func FindByID(checks []Check, id string) *Check {
	for i := range checks {
		if checks[i].ID == id {
			return &checks[i]
		}
	}
	return nil
}

func ghCodespaceScopeCheck(listErr func() error) Check {
	bare := `gh auth refresh -h github.com -s codespace`
	return Check{
		ID:    CodespaceScopeID,
		Title: "GitHub token has the codespace scope",
		Description: "Listing codespaces requires the codespace scope on " +
			"your gh OAuth token. Without it, the sidebar stays empty.",
		Status: func() *Issue {
			err := listErr()
			if err == nil || !strings.Contains(err.Error(), `needs the "codespace" scope`) {
				return nil
			}
			return &Issue{
				Severity: SeverityError,
				Summary:  "GitHub token is missing the codespace scope; codespaces will not load until granted.",
			}
		},
		FixCommand: func() string { return bare },
	}
}

func sshHostStarCheck() Check {
	return Check{
		ID:    HostStarID,
		Title: "SSH config Host * does not match codespaces",
		Description: "A bare `Host *` rule in ~/.ssh/config matches codespace " +
			"hosts, which can break SSH when an IdentityFile points at a " +
			"YubiKey/SK key and the device isn't plugged in.",
		Status: func() *Issue {
			paths := sshconfig.ResolvePaths()
			if !sshconfig.NeedsHostStarScoping(paths.MainConfigPath) {
				return nil
			}
			return &Issue{
				Severity: SeverityWarning,
				Summary:  "~/.ssh/config has a `Host *` rule that also matches codespace hosts.",
			}
		},
		Fix: func() error {
			paths := sshconfig.ResolvePaths()
			if _, err := sshconfig.ScopeHostStarBlocks(paths.MainConfigPath); err != nil {
				return fmt.Errorf("scope Host *: %w", err)
			}
			return nil
		},
	}
}
