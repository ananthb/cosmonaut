package codespace

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/linuskendall/cosmonaut/internal/config"
)

func TestMatchesTargetComparesRepoBranchAndDisplayName(t *testing.T) {
	cs := Codespace{
		Name:        "demo-abc123",
		DisplayName: "demo-main",
		Repository:  "acme/demo",
		GitStatus:   &GitStatus{Ref: "main"},
	}

	target := config.Target{
		Repository:  "acme/demo",
		Branch:      "main",
		DisplayName: "demo-main",
	}
	if !MatchesTarget(&cs, &target) {
		t.Error("expected match")
	}

	noMatch := config.Target{
		Repository: "acme/demo",
		Branch:     "develop",
	}
	if MatchesTarget(&cs, &noMatch) {
		t.Error("expected no match for different branch")
	}
}

func TestMatchesTargetAcceptsStringRepositoryShape(t *testing.T) {
	cs := Codespace{
		Name:        "demo-abc123",
		DisplayName: "demo-main",
		Repository:  "acme/demo",
		GitStatus:   &GitStatus{Ref: "main"},
	}

	target := config.Target{
		Repository: "acme/demo",
		Branch:     "main",
	}
	if !MatchesTarget(&cs, &target) {
		t.Error("expected match with string repository")
	}
}

func TestChooseCodespaceErrorsOnAmbiguous(t *testing.T) {
	codespaces := []Codespace{
		{Name: "demo-a", Repository: "acme/demo"},
		{Name: "demo-b", Repository: "acme/demo"},
	}
	target := config.Target{Repository: "acme/demo"}

	_, err := ChooseCodespace(codespaces, &target)
	if err == nil {
		t.Fatal("expected error for ambiguous match")
	}
	if got := err.Error(); !strings.Contains(got, "Ambiguous") && !strings.Contains(got, "ambiguous") {
		t.Errorf("error = %q, want ambiguous", got)
	}
}

func TestBuildCreateArgsMapsOptionalFields(t *testing.T) {
	target := config.Target{
		Repository:       "acme/demo",
		Branch:           "main",
		DisplayName:      "demo-main",
		Machine:          "standardLinux32gb",
		Location:         "EastUs",
		DevcontainerPath: ".devcontainer/devcontainer.json",
		IdleTimeout:      "30m",
		RetentionPeriod:  "72h",
	}

	got := BuildCreateArgs(target)
	want := []string{
		"gh", "codespace", "create", "--repo", "acme/demo",
		"--branch", "main",
		"--display-name", "demo-main",
		"--machine", "standardLinux32gb",
		"--location", "EastUs",
		"--devcontainer-path", ".devcontainer/devcontainer.json",
		"--idle-timeout", "30m",
		"--retention-period", "72h",
	}

	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBuildCreateAPIBodyMapsAndConvertsDurations(t *testing.T) {
	target := config.Target{
		Repository:       "acme/demo",
		Branch:           "main",
		DisplayName:      "demo-main",
		Machine:          "standardLinux32gb",
		Location:         "EastUs",
		DevcontainerPath: ".devcontainer/devcontainer.json",
		IdleTimeout:      "30m",
		RetentionPeriod:  "72h",
	}

	raw, err := buildCreateAPIBody(target)
	if err != nil {
		t.Fatalf("buildCreateAPIBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	cases := map[string]any{
		"ref":                      "main",
		"display_name":             "demo-main",
		"machine":                  "standardLinux32gb",
		"location":                 "EastUs",
		"devcontainer_path":        ".devcontainer/devcontainer.json",
		"idle_timeout_minutes":     float64(30),
		"retention_period_minutes": float64(72 * 60),
	}
	for k, want := range cases {
		if got := body[k]; got != want {
			t.Errorf("body[%q] = %v (%T), want %v (%T)", k, got, got, want, want)
		}
	}
	if _, ok := body["ref"]; !ok {
		t.Error("ref missing")
	}
	if len(body) != len(cases) {
		t.Errorf("unexpected keys in body: %v", body)
	}
}

func TestBuildCreateAPIBodyOmitsEmptyFields(t *testing.T) {
	raw, err := buildCreateAPIBody(config.Target{Repository: "acme/demo"})
	if err != nil {
		t.Fatalf("buildCreateAPIBody: %v", err)
	}
	if string(raw) != "{}" {
		t.Errorf("body = %s, want {}", raw)
	}
}

func TestSplitRepoRejectsBadInput(t *testing.T) {
	for _, bad := range []string{"", "acme", "acme/", "/demo", "/"} {
		if _, _, err := splitRepo(bad); err == nil {
			t.Errorf("splitRepo(%q) expected error", bad)
		}
	}
	owner, name, err := splitRepo("acme/demo")
	if err != nil || owner != "acme" || name != "demo" {
		t.Errorf("splitRepo(acme/demo) = %q,%q,%v", owner, name, err)
	}
}

func TestDescribeCodespaceMarksMatchingEntry(t *testing.T) {
	cs := Codespace{
		Name:        "demo-a",
		DisplayName: "rpcpool-main",
		State:       "Available",
		GitStatus:   &GitStatus{Ref: "main"},
	}

	desc := DescribeCodespace(&cs, true)
	if !strings.Contains(desc, "[matches config]") {
		t.Errorf("missing [matches config] in %q", desc)
	}
	if !strings.Contains(desc, "branch=main") {
		t.Errorf("missing branch=main in %q", desc)
	}
}
