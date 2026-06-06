package tui

import (
	"strings"
	"testing"

	"github.com/linuskendall/cosmonaut/internal/provider"
)

// TestSSHOptionRowsForCoder asserts the Coder detail view omits the
// ControlMaster toggle — coder.conf is shared across workspaces so a
// per-workspace toggle would be last-writer-wins.
func TestSSHOptionRowsForCoder(t *testing.T) {
	rows := sshOptionRowsFor(provider.NameCoder)
	if len(rows) != 1 {
		t.Fatalf("Coder should expose exactly one SSH option (tmux), got %d rows: %#v", len(rows), rows)
	}
	if rows[0].kind != sshOptionTmux {
		t.Fatalf("Coder's only SSH option should be tmux, got kind=%d", rows[0].kind)
	}
	note := sshOptionsSharedConfNote(provider.NameCoder)
	if note == "" {
		t.Fatalf("Coder should provide a shared-conf note")
	}
	if !strings.Contains(note, "coder.conf") {
		t.Fatalf("shared-conf note should mention coder.conf, got %q", note)
	}
}

// TestSSHOptionRowsForGitHub asserts GitHub keeps both toggles. Each
// GitHub codespace has its own conf file so ControlMaster is coherent.
func TestSSHOptionRowsForGitHub(t *testing.T) {
	rows := sshOptionRowsFor(provider.NameGitHub)
	if len(rows) != 2 {
		t.Fatalf("GitHub should expose 2 SSH options, got %d", len(rows))
	}
	sawCM, sawTmux := false, false
	for _, r := range rows {
		switch r.kind {
		case sshOptionControlMaster:
			sawCM = true
		case sshOptionTmux:
			sawTmux = true
		}
	}
	if !sawCM || !sawTmux {
		t.Fatalf("GitHub should have both ControlMaster and tmux rows, got %#v", rows)
	}
	if sshOptionsSharedConfNote(provider.NameGitHub) != "" {
		t.Fatalf("GitHub should not emit a shared-conf note")
	}
}
