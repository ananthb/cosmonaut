package provider

import (
	"context"
	"os/exec"
	"time"
)

// availabilityProbeTimeout caps the local auth probe so a misbehaving
// CLI (network stall, hung SSO) can't pin a tray rebuild or UI gate.
const availabilityProbeTimeout = 5 * time.Second

// IsGitHubAvailable reports whether the `gh` CLI is on PATH and the
// user is authenticated for codespace operations. Implemented as
// `gh auth status` because it's the cheapest signal that catches both
// "CLI missing" and "not logged in" without making a Codespaces API
// call. False also covers token-scope problems that gh auth surfaces.
func IsGitHubAvailable() bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), availabilityProbeTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "gh", "auth", "status").Run() == nil
}

// IsCoderAvailable reports whether the `coder` CLI is on PATH and has
// a working session. Implemented as `coder whoami` — fast, hits the
// configured Coder server only enough to validate the session token.
func IsCoderAvailable() bool {
	if _, err := exec.LookPath("coder"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), availabilityProbeTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "coder", "whoami").Run() == nil
}
