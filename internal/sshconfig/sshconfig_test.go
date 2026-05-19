package sshconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePrimaryHostAliasReturnsFirstConcreteHost(t *testing.T) {
	sshConfig := `
Host cs-demo
  HostName github.com
  User git
`
	got, err := ParsePrimaryHostAlias(sshConfig)
	if err != nil {
		t.Fatal(err)
	}
	if got != "cs-demo" {
		t.Errorf("got %q, want cs-demo", got)
	}
}

func TestEnsureIncludeLineIsIdempotent(t *testing.T) {
	once := EnsureIncludeLine("Host example\n  HostName example.com\n")
	twice := EnsureIncludeLine(once)
	if once != twice {
		t.Errorf("not idempotent:\nonce:  %q\ntwice: %q", once, twice)
	}
	if !strings.HasPrefix(once, SSHIncludeLine+"\n") {
		t.Errorf("should start with include line, got %q", once)
	}
}

func TestEnsureIncludeLineMovesExistingToTop(t *testing.T) {
	config := "Host *\n  IdentityAgent test\n" + SSHIncludeLine + "\n"
	updated := EnsureIncludeLine(config)
	if !strings.HasPrefix(updated, SSHIncludeLine+"\n") {
		t.Errorf("should start with include line, got %q", updated)
	}
	if strings.Count(updated, SSHIncludeLine) != 1 {
		t.Errorf("include line appears %d times, want 1", strings.Count(updated, SSHIncludeLine))
	}
}

func TestWriteCodespaceConfig(t *testing.T) {
	dir := t.TempDir()
	includeDir := filepath.Join(dir, "cosmonaut")
	err := WriteCodespaceConfig(includeDir, "cs-demo", "Host cs-demo\n  HostName github.com\n")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(includeDir, "cs-demo.conf"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "cs-demo") {
		t.Error("config file missing expected content")
	}
	if !strings.Contains(got, "IdentityAgent none") {
		t.Error("config file missing IdentityAgent none")
	}
	if !strings.Contains(got, managedBeginPrefix) || !strings.Contains(got, managedEndPrefix) {
		t.Error("config file missing managed-block sentinels")
	}
}

func TestApplyManagedExtrasIdempotent(t *testing.T) {
	base := "Host cs-demo\n  HostName github.com\n"
	once := applyManagedExtras(base)
	twice := applyManagedExtras(once)
	if once != twice {
		t.Errorf("not idempotent:\nonce:  %q\ntwice: %q", once, twice)
	}
	if strings.Count(once, managedBeginPrefix) != 1 {
		t.Errorf("BEGIN sentinel appears %d times, want 1", strings.Count(once, managedBeginPrefix))
	}
}

func TestRefreshManagedExtrasUpgradesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cs-demo.conf")
	// Pre-sentinel cosmonaut output: keepalive only, no IdentityAgent.
	legacy := "Host cs-demo\n  HostName github.com\n  ServerAliveInterval 15\n  ServerAliveCountMax 3\n  ConnectionAttempts 3\n"
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := RefreshManagedExtras(path)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected legacy file to be rewritten")
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "IdentityAgent none") {
		t.Error("upgraded file missing IdentityAgent none")
	}
	if strings.Count(got, "ServerAliveInterval 15") != 1 {
		t.Errorf("ServerAliveInterval appears %d times, want 1 (legacy block not stripped)", strings.Count(got, "ServerAliveInterval 15"))
	}
	// Second refresh is a no-op.
	changed, err = RefreshManagedExtras(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("expected second refresh to be a no-op")
	}
}

func TestNeedsHostStarScoping(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"bare", "Host *\n  IdentityFile ~/.ssh/foo\n", true},
		{"indented", "  Host *\n", true},
		{"already scoped", "Host * !cs-* !cs.*\n  IdentityFile ~/.ssh/foo\n", false},
		{"specific", "Host *.example.com\n", false},
		{"multi pattern", "Host * server1\n", false},
		{"no host star", "Host other\n  HostName x\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(path, []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			got := NeedsHostStarScoping(path)
			if got != tc.want {
				t.Errorf("NeedsHostStarScoping = %v, want %v", got, tc.want)
			}
		})
	}
	// Missing file is not flagged.
	if NeedsHostStarScoping(filepath.Join(t.TempDir(), "missing")) {
		t.Error("missing file should not need scoping")
	}
}

func TestScopeHostStarBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := "Include ~/.ssh/cosmonaut/*.conf\n\nHost *\n  IdentityFile ~/.ssh/yubikey\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := ScopeHostStarBlocks(path)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "Host * !cs-* !cs.*") {
		t.Errorf("Host * not scoped:\n%s", got)
	}
	if strings.Contains(string(got), "\nHost *\n") {
		t.Errorf("bare Host * still present:\n%s", got)
	}
	// Backup written.
	backup := path + MainConfigBackupSuffix
	bakData, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("backup not written: %v", err)
	}
	if string(bakData) != original {
		t.Error("backup content mismatch")
	}
	// Idempotent: second call no-ops and doesn't overwrite the backup.
	if err := os.WriteFile(backup, []byte("sentinel"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err = ScopeHostStarBlocks(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("expected idempotent second call")
	}
	bakData, _ = os.ReadFile(backup)
	if string(bakData) != "sentinel" {
		t.Error("backup got overwritten on idempotent call")
	}
}

func TestRefreshAllManagedExtras(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.conf"), []byte("Host a\n  HostName a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.conf"), []byte("Host b\n  HostName b\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("ignored"), 0644); err != nil {
		t.Fatal(err)
	}
	n, err := RefreshAllManagedExtras(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("refreshed %d files, want 2", n)
	}
	// Non-existent dir is not an error.
	if _, err := RefreshAllManagedExtras(filepath.Join(dir, "missing")); err != nil {
		t.Errorf("missing dir should be no-op, got %v", err)
	}
}
