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
// deadline cancels the context.
const runCtxTimeout = 5 * time.Second

// pathWithFakeCoder returns a PATH value that resolves "coder" to the fake
// script in dir while preserving access to system utilities like /bin/sleep
// that the fake script itself needs.
func pathWithFakeCoder(dir string) string {
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

func TestRunCtxTimeoutIncludesStderrTail(t *testing.T) {
	dir := writeFakeCoder(t, "boom: backend unreachable\n", 5)
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
	dir := writeFakeCoder(t, "", 5)
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
	dir := writeFakeCoder(t, long, 5)
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
