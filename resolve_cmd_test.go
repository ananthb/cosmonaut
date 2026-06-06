package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

// TestBuildResolveOutput pins the JSON shape that `cosmonaut resolve` emits.
// Scripts that consumed the old --no-open / --dry-run output (or new scripts
// written against this subcommand) depend on these field names and the
// ssh:// URL composition rule.
func TestBuildResolveOutput(t *testing.T) {
	ws := &provider.Workspace{
		Provider: provider.NameGitHub,
		Name:     "cs-abcdef",
	}
	got := buildResolveOutput("owner/repo", ws, "cs.cs-abcdef.github.dev", "/workspaces/repo")

	want := resolveOutput{
		Target:        "owner/repo",
		Workspace:     "cs-abcdef",
		Provider:      "github",
		SSHAlias:      "cs.cs-abcdef.github.dev",
		WorkspacePath: "/workspaces/repo",
		RemoteURL:     "ssh://cs.cs-abcdef.github.dev//workspaces/repo",
	}
	if got != want {
		t.Fatalf("buildResolveOutput mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

// TestBuildResolveOutputCoder confirms the Coder provider name and a
// path-style workspace location compose into a valid ssh:// URL.
func TestBuildResolveOutputCoder(t *testing.T) {
	ws := &provider.Workspace{
		Provider: provider.NameCoder,
		Name:     "dev",
	}
	got := buildResolveOutput("dev-target", ws, "dev.coder", "/home/coder/project")

	if got.Provider != "coder" {
		t.Errorf("Provider = %q, want %q", got.Provider, "coder")
	}
	if got.RemoteURL != "ssh://dev.coder//home/coder/project" {
		t.Errorf("RemoteURL = %q, want %q", got.RemoteURL, "ssh://dev.coder//home/coder/project")
	}
}

// TestResolveOutputJSONShape asserts the marshaled field names match the
// documented contract — these key names are part of the public CLI surface.
func TestResolveOutputJSONShape(t *testing.T) {
	out := resolveOutput{
		Target:        "owner/repo",
		Workspace:     "cs-abcdef",
		Provider:      "github",
		SSHAlias:      "cs.cs-abcdef.github.dev",
		WorkspacePath: "/workspaces/repo",
		RemoteURL:     "ssh://cs.cs-abcdef.github.dev//workspaces/repo",
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	wantKeys := []string{"target", "workspace", "provider", "sshAlias", "workspacePath", "remoteUrl"}
	for _, k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q in JSON output: %s", k, data)
		}
	}
	if len(got) != len(wantKeys) {
		t.Errorf("unexpected key count: got %d (%v), want %d", len(got), got, len(wantKeys))
	}
}

// TestResolveCmdRegistered checks the subcommand wires up under the root,
// so `cosmonaut resolve` is discoverable.
func TestResolveCmdRegistered(t *testing.T) {
	root := rootCmd()
	found := false
	for _, sub := range root.Commands() {
		if sub.Name() == "resolve" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("resolve subcommand not registered on root cmd")
	}
}

// TestResolveCmdHelp confirms `cosmonaut resolve --help` runs without
// touching gh / coder, by checking the rendered help contains key flags.
func TestResolveCmdHelp(t *testing.T) {
	var cfg string
	cmd := resolveCmd(&cfg)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("help should not error: %v", err)
	}
	help := buf.String()
	for _, want := range []string{"--codespace", "--control-master", "JSON"} {
		if !bytes.Contains([]byte(help), []byte(want)) {
			t.Errorf("help missing %q in:\n%s", want, help)
		}
	}
}
