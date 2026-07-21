package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

func TestCanDeleteWorkspaceColdStart(t *testing.T) {
	d := &Daemon{}
	if d.canDeleteWorkspace(provider.NameGitHub) {
		t.Fatalf("canDeleteWorkspace(github) = true before first poll, want false")
	}
	if got := d.deleteDisabledReason(provider.NameGitHub); got != "checking..." {
		t.Fatalf("deleteDisabledReason(github) = %q, want %q", got, "checking...")
	}
}

func TestCanDeleteWorkspaceAvailable(t *testing.T) {
	d := &Daemon{
		providerStatus: map[string]ProviderStatus{
			provider.NameGitHub: {Available: true, CheckedAt: time.Now()},
		},
	}
	if !d.canDeleteWorkspace(provider.NameGitHub) {
		t.Fatalf("canDeleteWorkspace(github) = false, want true when CLI is available")
	}
	if got := d.deleteDisabledReason(provider.NameGitHub); got != "" {
		t.Fatalf("deleteDisabledReason(github) = %q, want empty", got)
	}
}

func TestCanDeleteWorkspaceCLIMissing(t *testing.T) {
	d := &Daemon{
		providerStatus: map[string]ProviderStatus{
			provider.NameGitHub: {Available: false, CheckedAt: time.Now()},
		},
	}
	if d.canDeleteWorkspace(provider.NameGitHub) {
		t.Fatalf("canDeleteWorkspace(github) = true, want false when CLI is missing")
	}
	if got := d.deleteDisabledReason(provider.NameGitHub); got != "gh CLI not installed" {
		t.Fatalf("deleteDisabledReason(github) = %q, want %q", got, "gh CLI not installed")
	}
}

func TestCanDeleteWorkspaceListErr(t *testing.T) {
	d := &Daemon{
		providerStatus: map[string]ProviderStatus{
			provider.NameCoder: {Available: true, Err: errors.New("auth fail"), CheckedAt: time.Now()},
		},
	}
	if d.canDeleteWorkspace(provider.NameCoder) {
		t.Fatalf("canDeleteWorkspace(coder) = true, want false when last list call failed")
	}
	if got := d.deleteDisabledReason(provider.NameCoder); got != "auth or list call failing" {
		t.Fatalf("deleteDisabledReason(coder) = %q, want %q", got, "auth or list call failing")
	}
}

func TestCanDeleteWorkspaceUnknownProvider(t *testing.T) {
	d := &Daemon{}
	if d.canDeleteWorkspace("nonsense") {
		t.Fatalf("canDeleteWorkspace(nonsense) = true, want false for unknown providers")
	}
}
