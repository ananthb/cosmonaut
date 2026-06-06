package editor

import (
	"fmt"
	"os/exec"
	"strings"
)

// GenericEditor wraps any user-supplied editor binary. It runs the
// binary against an `ssh://<alias>/<path>` URI and lets the editor's own
// remote handler take it from there. Suitable for Zed-likes that
// understand the URI (Cursor, etc.), or for user wrapper scripts that
// translate it to whatever their editor wants.
type GenericEditor struct {
	// Command is the binary or wrapper script to execute. May be a bare
	// name (looked up on PATH) or an absolute path.
	Command string
}

// Name returns the user-supplied binary name so settings UI can display
// it back to the user verbatim.
func (g *GenericEditor) Name() string { return g.Command }

// FindBinary resolves the command on PATH, or returns it as-is when it's
// already absolute (since the user pointed straight at a wrapper script).
func (g *GenericEditor) FindBinary() (string, error) {
	if g.Command == "" {
		return "", fmt.Errorf("editor command is empty")
	}
	if strings.ContainsRune(g.Command, '/') {
		return g.Command, nil
	}
	p, err := exec.LookPath(g.Command)
	if err != nil {
		return "", fmt.Errorf("%q not found on PATH", g.Command)
	}
	return p, nil
}

// ConfigureConnection is a no-op for generic editors: we don't know how
// their settings file is shaped, and the caller's editor / wrapper is
// expected to handle whatever it needs from the URI alone.
func (g *GenericEditor) ConfigureConnection(_, _, _ string, _ *bool) error {
	return nil
}

// LaunchRemote invokes the editor with the SSH URI. The URI form
// (ssh://<alias>/<path>) is what Zed-compatible editors expect; wrapper
// scripts can parse it apart if they need to call a non-URI-aware editor.
func (g *GenericEditor) LaunchRemote(sshAlias, workspacePath string) error {
	bin, err := g.FindBinary()
	if err != nil {
		return err
	}
	uri := fmt.Sprintf("ssh://%s/%s", sshAlias, strings.TrimLeft(workspacePath, "/"))
	return exec.Command(bin, uri).Run()
}
