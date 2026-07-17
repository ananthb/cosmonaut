package provider

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/linuskendall/cosmonaut/internal/config"
)

// writeFakeCoder creates a script named "coder" in a temp dir that emits the
// given stderr text and then sleeps for sleepSeconds. It returns the absolute
// directory containing the script.
func writeFakeCoder(t *testing.T, stderr string, sleepSeconds int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake coder script requires a POSIX shell")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"printf '%s' " + shellQuote(stderr) + " 1>&2\n" +
		"sleep " + strconv.Itoa(sleepSeconds) + "\n"
	path := filepath.Join(dir, "coder")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake coder: %v", err)
	}
	return dir
}

func shellQuote(s string) string {
	// Wrap in single quotes and escape any embedded single quotes.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runCtxTimeout is generous enough that the fake shell has time to fork,
// write to stderr, and start sleeping on slow CI runners before the deadline
// fires. Under heavy parallel test load (go test ./... at full
// concurrency), sh fork latency can exceed 1s; the value is set high
// enough that printf reliably reaches the stderr pipe before the
// deadline cancels the context. The fake script's sleep duration must
// also exceed this so the context (not the script's normal exit) wins
// the race.
const runCtxTimeout = 5 * time.Second

// pathWithFakeCoder returns a PATH value that resolves "coder" to the fake
// script in dir while preserving access to system utilities like /bin/sleep
// that the fake script itself needs.
func pathWithFakeCoder(dir string) string {
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

func TestRunCtxTimeoutIncludesStderrTail(t *testing.T) {
	dir := writeFakeCoder(t, "boom: backend unreachable\n", 30)
	t.Setenv("PATH", pathWithFakeCoder(dir))

	m := &CoderManager{}
	ctx, cancel := context.WithTimeout(context.Background(), runCtxTimeout)
	defer cancel()

	_, err := m.runCtx(ctx, "delete", "--yes", "--", "ws")
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "timed out") {
		t.Fatalf("expected 'timed out' in error, got %q", msg)
	}
	if !strings.Contains(msg, "boom: backend unreachable") {
		t.Fatalf("expected stderr tail in error, got %q", msg)
	}
}

func TestRunCtxTimeoutWithoutStderrOmitsTail(t *testing.T) {
	dir := writeFakeCoder(t, "", 30)
	t.Setenv("PATH", pathWithFakeCoder(dir))

	m := &CoderManager{}
	ctx, cancel := context.WithTimeout(context.Background(), runCtxTimeout)
	defer cancel()

	_, err := m.runCtx(ctx, "delete", "--yes", "--", "ws")
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "timed out") {
		t.Fatalf("expected 'timed out' in error, got %q", msg)
	}
	// No stderr means no trailing ": <tail>"; format should match exactly.
	if strings.Contains(msg, "timed out:") {
		t.Fatalf("expected no tail suffix, got %q", msg)
	}
}

func TestRunCtxTimeoutTrimsLongStderr(t *testing.T) {
	long := strings.Repeat("x", 500) + "TAIL_MARKER"
	dir := writeFakeCoder(t, long, 30)
	t.Setenv("PATH", pathWithFakeCoder(dir))

	m := &CoderManager{}
	ctx, cancel := context.WithTimeout(context.Background(), runCtxTimeout)
	defer cancel()

	_, err := m.runCtx(ctx, "delete", "--yes", "--", "ws")
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "TAIL_MARKER") {
		t.Fatalf("expected trailing portion to appear in error, got %q", msg)
	}
	if !strings.Contains(msg, "...") {
		t.Fatalf("expected '...' prefix for trimmed stderr, got %q", msg)
	}
	// The error must not embed the full 500-char stderr — only the trimmed tail.
	if strings.Count(msg, "x") > 210 {
		t.Fatalf("error message contains untrimmed stderr (%d x chars): %q", strings.Count(msg, "x"), msg)
	}
}

func TestDeleteWorkspaceRequiresName(t *testing.T) {
	m := &CoderManager{}
	if err := m.DeleteWorkspace("   "); err == nil {
		t.Fatalf("expected error for blank workspace name, got nil")
	}
}

func TestFilterCoderWorkspacesForTarget(t *testing.T) {
	all := []Workspace{
		{Name: "api", Repository: ""},
		{Name: "my-api-old", Repository: ""},
		{Name: "web", Repository: "acme/web"},
		{Name: "unrelated", Repository: ""},
	}

	t.Run("workspace name constraint matches exactly", func(t *testing.T) {
		got := filterCoderWorkspacesForTarget(all, config.Target{
			Coder: &config.CoderTargetConfig{WorkspaceName: "api"},
		})
		if len(got) != 1 || got[0].Name != "api" {
			t.Errorf("got %v", got)
		}
	})

	t.Run("workspace name constraint with no match returns empty, never all", func(t *testing.T) {
		got := filterCoderWorkspacesForTarget(all, config.Target{
			Coder: &config.CoderTargetConfig{WorkspaceName: "nope"},
		})
		if len(got) != 0 {
			t.Errorf("constrained no-match must be empty, got %v", got)
		}
	})

	t.Run("repository constraint matches repo and derived name only", func(t *testing.T) {
		got := filterCoderWorkspacesForTarget(all, config.Target{Repository: "acme/api"})
		if len(got) != 1 || got[0].Name != "api" {
			t.Errorf("want exact-name match only (no substring tier), got %v", got)
		}
	})

	t.Run("repository constraint with no match returns empty, never all", func(t *testing.T) {
		got := filterCoderWorkspacesForTarget(all, config.Target{Repository: "acme/missing"})
		if len(got) != 0 {
			t.Errorf("constrained no-match must be empty, got %v", got)
		}
	})

	t.Run("unconstrained target lists everything", func(t *testing.T) {
		got := filterCoderWorkspacesForTarget(all, config.Target{})
		if len(got) != len(all) {
			t.Errorf("got %d workspaces, want %d", len(got), len(all))
		}
	})
}

func TestCoderStateClassifiers(t *testing.T) {
	for state, want := range map[string]struct{ busy, deleting bool }{
		"stopping":   {busy: true},
		"Canceling":  {busy: true},
		"cancelling": {busy: true},
		"deleting":   {deleting: true},
		"deleted":    {deleting: true},
		"running":    {},
		"failed":     {},
	} {
		if got := isCoderBusyState(state); got != want.busy {
			t.Errorf("isCoderBusyState(%q) = %v, want %v", state, got, want.busy)
		}
		if got := isCoderDeletingState(state); got != want.deleting {
			t.Errorf("isCoderDeletingState(%q) = %v, want %v", state, got, want.deleting)
		}
	}
}

func TestCreateWorkspaceRejectsInvalidName(t *testing.T) {
	m := NewCoderManager(nil)
	for _, name := range []string{"-leading-dash", "has/slash", "has space", "..", "-"} {
		_, err := m.CreateWorkspace(config.Target{
			Coder: &config.CoderTargetConfig{Template: "tpl", WorkspaceName: name},
		}, false)
		if err == nil || !strings.Contains(err.Error(), "invalid coder workspace name") {
			t.Errorf("CreateWorkspace(%q): expected name validation error, got %v", name, err)
		}
	}
}
