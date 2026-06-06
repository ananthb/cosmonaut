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

func TestParsePrimaryHostAliasSkipsWildcardHosts(t *testing.T) {
	sshConfig := `
Host coder.*
  ProxyCommand coder ssh --stdio %h

Host *.coder
  ProxyCommand coder ssh --stdio %h
`
	_, err := ParsePrimaryHostAlias(sshConfig)
	if err == nil {
		t.Fatal("expected no concrete alias to be found")
	}
}

func TestReadExistingWorkspaceAliasForCoderUsesConcreteWorkspaceAlias(t *testing.T) {
	dir := t.TempDir()
	paths := SSHPaths{IncludeDir: dir}
	path := filepath.Join(dir, "coder.conf")
	body := `
Host coder.*
  ProxyCommand coder ssh --stdio %h

Host *.coder
  ProxyCommand coder ssh --stdio %h
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadExistingWorkspaceAlias(paths, "coder", "my-workspace")
	if !ok {
		t.Fatal("expected coder alias to be detected")
	}
	if got != "my-workspace.coder" {
		t.Fatalf("got %q, want %q", got, "my-workspace.coder")
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
	err := WriteCodespaceConfig(includeDir, "cs-demo", "Host cs-demo\n  HostName github.com\n", ManagedExtrasOptions{})
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
	if strings.Contains(got, "ControlMaster") {
		t.Error("ControlMaster should be absent when opts.ControlMaster=false")
	}
}

func TestWriteCodespaceConfigWithControlMaster(t *testing.T) {
	dir := t.TempDir()
	includeDir := filepath.Join(dir, "cosmonaut")
	err := WriteCodespaceConfig(includeDir, "cs-demo", "Host cs-demo\n  HostName github.com\n", ManagedExtrasOptions{ControlMaster: true})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(includeDir, "cs-demo.conf"))
	got := string(data)
	if !strings.Contains(got, "ControlMaster auto") {
		t.Error("ControlMaster auto missing when opts.ControlMaster=true")
	}
	if !strings.Contains(got, "ControlPath ~/.ssh/cosmonaut/cm-%C") {
		t.Error("ControlPath missing")
	}
	if !strings.Contains(got, "ControlPersist 10m") {
		t.Error("ControlPersist missing")
	}
}

func TestApplyManagedExtrasIdempotent(t *testing.T) {
	base := "Host cs-demo\n  HostName github.com\n"
	once := applyManagedExtras(base, ManagedExtrasOptions{})
	twice := applyManagedExtras(once, ManagedExtrasOptions{})
	if once != twice {
		t.Errorf("not idempotent:\nonce:  %q\ntwice: %q", once, twice)
	}
	if strings.Count(once, managedBeginPrefix) != 1 {
		t.Errorf("BEGIN sentinel appears %d times, want 1", strings.Count(once, managedBeginPrefix))
	}
}

func TestApplyManagedExtrasTogglesControlMaster(t *testing.T) {
	base := "Host cs-demo\n  HostName github.com\n"
	on := applyManagedExtras(base, ManagedExtrasOptions{ControlMaster: true})
	off := applyManagedExtras(on, ManagedExtrasOptions{})
	if strings.Contains(off, "ControlMaster") {
		t.Errorf("toggling ControlMaster off left a stray directive:\n%s", off)
	}
	roundTrip := applyManagedExtras(off, ManagedExtrasOptions{ControlMaster: true})
	if roundTrip != on {
		t.Errorf("on -> off -> on round-trip not stable:\non:        %q\nroundTrip: %q", on, roundTrip)
	}
}

func TestRefreshManagedExtrasUpgradesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cs-demo.conf")
	// Pre-sentinel cosmonaut output: keepalive only, no IdentityAgent.
	legacy := "Host cs-demo\n  HostName github.com\n  ServerAliveInterval 15\n  ServerAliveCountMax 3\n  ConnectionAttempts 3\n"
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := RefreshManagedExtras(path, ManagedExtrasOptions{})
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
	changed, err = RefreshManagedExtras(path, ManagedExtrasOptions{})
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
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
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
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
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
	if err := os.WriteFile(backup, []byte("sentinel"), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, "a.conf"), []byte("Host a\n  HostName a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.conf"), []byte("Host b\n  HostName b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := RefreshAllManagedExtras(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("refreshed %d files, want 2", n)
	}
	// Non-existent dir is not an error.
	if _, err := RefreshAllManagedExtras(filepath.Join(dir, "missing"), nil); err != nil {
		t.Errorf("missing dir should be no-op, got %v", err)
	}
}

func TestRefreshAllManagedExtrasAppliesPerFileOpts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "withcm.conf"), []byte("Host a\n  HostName a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain.conf"), []byte("Host b\n  HostName b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := RefreshAllManagedExtras(dir, func(name string) ManagedExtrasOptions {
		return ManagedExtrasOptions{ControlMaster: name == "withcm.conf"}
	})
	if err != nil {
		t.Fatal(err)
	}
	with, _ := os.ReadFile(filepath.Join(dir, "withcm.conf"))
	plain, _ := os.ReadFile(filepath.Join(dir, "plain.conf"))
	if !strings.Contains(string(with), "ControlMaster auto") {
		t.Error("withcm.conf should contain ControlMaster auto")
	}
	if strings.Contains(string(plain), "ControlMaster") {
		t.Error("plain.conf should not contain ControlMaster")
	}
}

func TestRefreshManagedExtrasMigratesV2ToV3PreservingControlMaster(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cs-demo.conf")
	// A v2-marked managed block as written by older cosmonaut versions.
	// Note: v2 didn't carry ControlMaster lines; the user's ControlMaster
	// preference is only known via the post-load sweep that supplies opts.
	v2 := "Host cs-demo\n  HostName github.com\n" +
		"  # BEGIN cosmonaut managed extras v2\n" +
		"  ServerAliveInterval 15\n" +
		"  ServerAliveCountMax 3\n" +
		"  ConnectionAttempts 3\n" +
		"  IdentityAgent none\n" +
		"  PKCS11Provider none\n" +
		"  # END cosmonaut managed extras v2\n"
	if err := os.WriteFile(path, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := RefreshManagedExtras(path, ManagedExtrasOptions{ControlMaster: true})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected v2 block to be rewritten")
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "# BEGIN cosmonaut managed extras v3") {
		t.Errorf("expected v3 begin marker, got:\n%s", got)
	}
	if !strings.Contains(got, "# END cosmonaut managed extras v3") {
		t.Errorf("expected v3 end marker, got:\n%s", got)
	}
	if strings.Contains(got, "extras v2") {
		t.Errorf("v2 markers should have been replaced, got:\n%s", got)
	}
	if !strings.Contains(got, "ControlMaster auto") {
		t.Errorf("expected ControlMaster auto in rewritten block, got:\n%s", got)
	}
}

func TestRefreshAllManagedExtrasOnRealFilenames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cs-abc.conf"), []byte("Host cs-abc\n  HostName github.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "coder.conf"), []byte("Host coder.*\n  ProxyCommand coder ssh --stdio %h\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	optsFor := func(name string) ManagedExtrasOptions {
		return ManagedExtrasOptions{ControlMaster: name == "cs-abc.conf"}
	}
	if _, err := RefreshAllManagedExtras(dir, optsFor); err != nil {
		t.Fatal(err)
	}
	gh, _ := os.ReadFile(filepath.Join(dir, "cs-abc.conf"))
	coder, _ := os.ReadFile(filepath.Join(dir, "coder.conf"))
	if !strings.Contains(string(gh), "ControlMaster auto") {
		t.Errorf("cs-abc.conf should contain ControlMaster auto, got:\n%s", gh)
	}
	if !strings.Contains(string(gh), "# BEGIN cosmonaut managed extras v3") {
		t.Errorf("cs-abc.conf should carry the v3 marker, got:\n%s", gh)
	}
	if strings.Contains(string(coder), "ControlMaster") {
		t.Errorf("coder.conf should not contain ControlMaster, got:\n%s", coder)
	}
	if !strings.Contains(string(coder), "# BEGIN cosmonaut managed extras v3") {
		t.Errorf("coder.conf should still carry the v3 marker, got:\n%s", coder)
	}
}

func TestProviderAndNameFromFilename(t *testing.T) {
	cases := []struct {
		filename     string
		wantProvider string
		wantName     string
	}{
		{"coder.conf", "coder", ""},
		{"cs-abc.conf", "github", "cs-abc"},
		{"cs-abc-123.conf", "github", "cs-abc-123"},
		{"my-workspace.conf", "github", "my-workspace"},
		{"plain.conf", "github", "plain"},
	}
	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			gotProvider, gotName := ProviderAndNameFromFilename(tc.filename)
			if gotProvider != tc.wantProvider || gotName != tc.wantName {
				t.Errorf("ProviderAndNameFromFilename(%q) = (%q, %q), want (%q, %q)",
					tc.filename, gotProvider, gotName, tc.wantProvider, tc.wantName)
			}
		})
	}
}
