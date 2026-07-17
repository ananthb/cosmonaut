package codespace

import (
	"fmt"
	"strings"

	"github.com/linuskendall/cosmonaut/internal/config"
)

// MatchesTarget checks whether a codespace matches a config target.
// Repository comparison is case-insensitive: GitHub repo names are, and a
// config that says "Acme/Demo" against an API that reports "acme/demo"
// used to silently create a duplicate codespace on every launch.
func MatchesTarget(cs *Codespace, t *config.Target) bool {
	if !strings.EqualFold(string(cs.Repository), t.Repository) {
		return false
	}

	if t.CodespaceName != "" && cs.Name != t.CodespaceName {
		return false
	}

	if t.DisplayName != "" && cs.DisplayName != t.DisplayName {
		return false
	}

	if t.Branch != "" && cs.GitStatus != nil {
		ref := cs.GitStatus.Ref
		if ref == "" {
			ref = cs.GitStatus.Branch
		}
		if ref != "" && ref != t.Branch {
			return false
		}
	}

	return true
}

// FindMatching returns all codespaces matching the target.
func FindMatching(codespaces []Codespace, t *config.Target) []Codespace {
	var matches []Codespace
	for i := range codespaces {
		if MatchesTarget(&codespaces[i], t) {
			matches = append(matches, codespaces[i])
		}
	}
	return matches
}

// ChooseCodespace auto-selects a codespace in non-interactive mode.
// Returns nil if no match, errors if ambiguous.
func ChooseCodespace(codespaces []Codespace, t *config.Target) (*Codespace, error) {
	matches := FindMatching(codespaces, t)
	if len(matches) > 1 {
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.Name
		}
		return nil, fmt.Errorf("ambiguous codespace match for %s: %s", t.Repository, strings.Join(names, ", "))
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	return nil, nil
}

// BuildCreateArgs builds the gh codespace create command arguments.
func BuildCreateArgs(target config.Target) []string {
	args := []string{"gh", "codespace", "create", "--repo", target.Repository}

	flags := []struct {
		value string
		flag  string
	}{
		{target.Branch, "--branch"},
		{target.DisplayName, "--display-name"},
		{target.Machine, "--machine"},
		{target.Location, "--location"},
		{target.DevcontainerPath, "--devcontainer-path"},
		{target.IdleTimeout, "--idle-timeout"},
		{target.RetentionPeriod, "--retention-period"},
	}

	for _, f := range flags {
		if f.value != "" {
			args = append(args, f.flag, f.value)
		}
	}
	return args
}

// DescribeCodespace returns a human-readable description.
func DescribeCodespace(cs *Codespace, recommended bool) string {
	var branch string
	if cs.GitStatus != nil {
		ref := cs.GitStatus.Ref
		if ref == "" {
			ref = cs.GitStatus.Branch
		}
		if ref != "" {
			branch = fmt.Sprintf(", branch=%s", ref)
		}
	}

	state := cs.State
	if state == "" {
		state = "unknown"
	}

	label := cs.Name
	if cs.DisplayName != "" {
		label += fmt.Sprintf(" (%s)", cs.DisplayName)
	}
	label += fmt.Sprintf(", state=%s%s", state, branch)
	if recommended {
		label += " [matches config]"
	}
	return label
}
