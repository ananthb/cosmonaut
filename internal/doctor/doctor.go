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
	"os"
	"strings"

	"github.com/linuskendall/cosmonaut/internal/provider"
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
	CoderLoginID     = "coder-login"
	HostStarID       = "ssh-host-star"
	IncludeDirID     = "ssh-include-dir"
)

// Catalog returns all checks. listErr is the most recent error from a
// `gh codespace list` attempt; the daemon supplies its cached value, the
// CLI runs a fresh list at call time.
func Catalog(listErr func() error) []Check {
	return CatalogForProvider(provider.NameGitHub, listErr)
}

func CatalogForProvider(providerName string, listErr func() error) []Check {
	checks := []Check{
		sshHostStarCheck(),
		sshIncludeDirCheck(),
	}
	if providerName == provider.NameGitHub {
		checks = append([]Check{ghCodespaceScopeCheck(listErr)}, checks...)
	}
	if providerName == provider.NameCoder {
		checks = append([]Check{coderLoginCheck(listErr)}, checks...)
	}
	return checks
}

// ProviderListErr pairs a provider name with a supplier of that
// provider's most recent list error. Used by CatalogForProviders.
type ProviderListErr struct {
	Name    string
	ListErr func() error
}

// CatalogForProviders returns the shared SSH checks plus the auth/scope
// check for each provider whose error supplier is given, wired to read
// only that provider's error. Unlike CatalogForProvider (single active
// provider, used by the CLI) this lets a GUI surface both providers'
// auth problems side by side: a Coder login failure and a GitHub scope
// failure each render independently, and neither shows when its
// provider's error is nil or unrelated.
func CatalogForProviders(providers ...ProviderListErr) []Check {
	var provChecks []Check
	for _, p := range providers {
		switch p.Name {
		case provider.NameGitHub:
			provChecks = append(provChecks, ghCodespaceScopeCheck(p.ListErr))
		case provider.NameCoder:
			provChecks = append(provChecks, coderLoginCheck(p.ListErr))
		}
	}
	return append(provChecks, sshHostStarCheck(), sshIncludeDirCheck())
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

// coderLoginCheck flags the case where the local `coder` CLI has no
// usable session — typically because the session expired or `coder
// logout` was run. Without this, `coder list` errors out and the
// sidebar stays empty with no obvious cause.
func coderLoginCheck(listErr func() error) Check {
	bare := `coder login`
	return Check{
		ID:    CoderLoginID,
		Title: "Coder CLI is logged in",
		Description: "Listing workspaces requires an authenticated " +
			"`coder` session. Without one, the sidebar stays empty.",
		Status: func() *Issue {
			err := listErr()
			if err == nil || !IsCoderUnauthenticated(err) {
				return nil
			}
			return &Issue{
				Severity: SeverityError,
				Summary:  "Coder CLI is not logged in; workspaces will not load until you sign in.",
			}
		},
		FixCommand: func() string { return bare },
	}
}

// IsCoderUnauthenticated reports whether err looks like a coder CLI
// authentication failure. The coder CLI's wording has varied over
// releases ("Login required", "Unauthorized", "your session has
// expired"), so match the same set of substrings the daemon tray and
// TUI already key off so all three surfaces stay in sync.
func IsCoderUnauthenticated(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not authenticated") ||
		strings.Contains(msg, "coder login") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "session has expired") ||
		strings.Contains(msg, "login required")
}

// IsGitHubAuthIssue reports whether err from a `gh codespace list` looks
// like an auth/authorization problem the user must resolve — either the
// token is missing the codespace scope or gh is not logged in — rather
// than a transient failure (network blip, timeout). Kept next to
// IsCoderUnauthenticated so the tray, banner, and Health section all key
// off one definition per provider.
func IsGitHubAuthIssue(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, `needs the "codespace" scope`) ||
		strings.Contains(msg, "not logged") ||
		strings.Contains(msg, "authentication") ||
		strings.Contains(msg, "gh auth login")
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

// sshIncludeDirCheck verifies that the include directory cosmonaut
// writes per-workspace SSH configs into (typically ~/.ssh/cosmonaut/)
// exists and is at least 0700. The directory also hosts ControlMaster
// sockets — `ControlPath ~/.ssh/cosmonaut/cm-%C` silently fails to
// create the master socket when the parent dir is missing, so a
// missing dir manifests as slow reconnects rather than an obvious
// error.
func sshIncludeDirCheck() Check {
	return Check{
		ID:    IncludeDirID,
		Title: "SSH include directory exists",
		Description: "Cosmonaut writes per-workspace SSH configs and " +
			"ControlMaster sockets into ~/.ssh/cosmonaut/. If the " +
			"directory is missing or world-readable, ControlMaster " +
			"silently fails and reconnects feel slow.",
		Status: func() *Issue {
			paths := sshconfig.ResolvePaths()
			return includeDirIssue(paths.IncludeDir)
		},
		Fix: func() error {
			paths := sshconfig.ResolvePaths()
			if err := os.MkdirAll(paths.IncludeDir, 0o700); err != nil {
				return fmt.Errorf("create %s: %w", paths.IncludeDir, err)
			}
			// MkdirAll leaves existing dirs untouched, so tighten
			// permissions explicitly if the dir already existed with
			// a looser mode.
			if err := os.Chmod(paths.IncludeDir, 0o700); err != nil {
				return fmt.Errorf("chmod %s: %w", paths.IncludeDir, err)
			}
			return nil
		},
	}
}

// includeDirIssue returns the active issue for the include directory at
// dir, or nil if the directory exists, is a directory, and has
// permissions of 0700 or stricter. Split out so tests can drive it
// without touching the real ~/.ssh/cosmonaut.
func includeDirIssue(dir string) *Issue {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return &Issue{
				Severity: SeverityError,
				Summary:  fmt.Sprintf("%s does not exist; ControlMaster sockets will silently fail to be created.", dir),
			}
		}
		return &Issue{
			Severity: SeverityError,
			Summary:  fmt.Sprintf("cannot stat %s: %v", dir, err),
		}
	}
	if !info.IsDir() {
		return &Issue{
			Severity: SeverityError,
			Summary:  fmt.Sprintf("%s is not a directory.", dir),
		}
	}
	// Reject any bit that's looser than 0700 (group/other access).
	if info.Mode().Perm()&0o077 != 0 {
		return &Issue{
			Severity: SeverityWarning,
			Summary:  fmt.Sprintf("%s has permissions %#o; expected 0700 or stricter.", dir, info.Mode().Perm()),
		}
	}
	return nil
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
