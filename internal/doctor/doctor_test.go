package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

func TestIncludeDirIssueMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	issue := includeDirIssue(dir)
	if issue == nil {
		t.Fatal("expected issue for missing dir, got nil")
	}
	if issue.Severity != SeverityError {
		t.Errorf("missing dir should be an error, got severity %v", issue.Severity)
	}
}

func TestIncludeDirIssueNotADir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(path, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	issue := includeDirIssue(path)
	if issue == nil {
		t.Fatal("expected issue for non-dir path, got nil")
	}
	if issue.Severity != SeverityError {
		t.Errorf("non-dir should be an error, got severity %v", issue.Severity)
	}
}

func TestIncludeDirIssueTooPermissive(t *testing.T) {
	dir := t.TempDir()
	// MkdirAll under TempDir respects umask, so make the loose perms
	// explicit here.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	issue := includeDirIssue(dir)
	if issue == nil {
		t.Fatal("expected issue for 0755 dir, got nil")
	}
	if issue.Severity != SeverityWarning {
		t.Errorf("loose perms should be a warning, got severity %v", issue.Severity)
	}
}

func TestIncludeDirIssueOK(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cosmonaut")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// MkdirAll honors umask; force the exact mode.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if issue := includeDirIssue(dir); issue != nil {
		t.Errorf("expected no issue for 0700 dir, got %+v", issue)
	}
}

func TestIncludeDirIssueStricterThan0700OK(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cosmonaut")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// 0600 is stricter than 0700 (no exec/traverse on the dir, which
	// would actually break things in practice — but it's not _looser_,
	// so the check should still pass).
	if err := os.Chmod(dir, 0o600); err != nil {
		t.Fatal(err)
	}
	if issue := includeDirIssue(dir); issue != nil {
		t.Errorf("expected no issue for 0600 dir, got %+v", issue)
	}
}

func TestCatalogIncludesSSHIncludeDirCheck(t *testing.T) {
	for _, name := range []string{provider.NameGitHub, provider.NameCoder} {
		t.Run(name, func(t *testing.T) {
			checks := CatalogForProvider(name, func() error { return nil })
			if FindByID(checks, IncludeDirID) == nil {
				t.Errorf("catalog for %q is missing %q", name, IncludeDirID)
			}
		})
	}
}
