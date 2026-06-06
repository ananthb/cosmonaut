package daemon

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

// TestPollProviderTimesOutBlockedListCall verifies that pollProvider
// cancels a list call that exceeds pollListTimeout, so a hung `gh` or
// `coder` can't pin the in-flight poll slot. Without the timeout,
// forcePollAsync (which `cond.Wait`s on the slot) would leak goroutines.
func TestPollProviderTimesOutBlockedListCall(t *testing.T) {
	// Need a CLI that exists on PATH so provider.RequireCommand passes;
	// `sh` is universally present on Unix builders, including Nix CI.
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH; skipping")
	}

	// Shorten the cap so the test completes in well under a second
	// rather than the production 30s.
	orig := pollListTimeout
	pollListTimeout = 100 * time.Millisecond
	t.Cleanup(func() { pollListTimeout = orig })

	d := &Daemon{}
	d.pollCond = sync.NewCond(&d.mu)

	done := make(chan struct{})
	var listErr error
	start := time.Now()
	go func() {
		defer close(done)
		d.pollProvider(provider.NameGitHub, "sh", func(ctx context.Context) ([]provider.Workspace, error) {
			// Block until the context's deadline fires (or absurdly long).
			select {
			case <-ctx.Done():
				listErr = ctx.Err()
				return nil, ctx.Err()
			case <-time.After(time.Hour):
				return nil, errors.New("unreachable")
			}
		})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pollProvider did not return within 2s; expected timeout to fire")
	}

	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("pollProvider took %v, expected to time out near %v", elapsed, pollListTimeout)
	}

	if !errors.Is(listErr, context.DeadlineExceeded) {
		t.Fatalf("list ctx err = %v, want context.DeadlineExceeded", listErr)
	}

	status := d.StatusFor(provider.NameGitHub)
	if status.Err == nil {
		t.Fatal("ProviderStatus.Err = nil, want timeout error")
	}
	if !errors.Is(status.Err, context.DeadlineExceeded) {
		t.Fatalf("ProviderStatus.Err = %v, want context.DeadlineExceeded", status.Err)
	}

	if got := d.ListErr(); !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("Daemon.ListErr() = %v, want context.DeadlineExceeded", got)
	}
}

func TestReplaceWorkspacesByProvider(t *testing.T) {
	current := []provider.Workspace{
		{Provider: provider.NameGitHub, Name: "gh-one"},
		{Provider: provider.NameCoder, Name: "coder-old"},
	}
	replacement := []provider.Workspace{
		{Provider: provider.NameCoder, Name: "coder-new"},
	}

	got := replaceWorkspacesByProvider(current, provider.NameCoder, replacement)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Provider != provider.NameGitHub || got[0].Name != "gh-one" {
		t.Fatalf("github workspace was not preserved: %#v", got)
	}
	if got[1].Provider != provider.NameCoder || got[1].Name != "coder-new" {
		t.Fatalf("coder replacement not applied: %#v", got)
	}
}
