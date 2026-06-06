// Package editor abstracts editor-specific operations for launching and
// configuring remote connections to codespaces. Zed is built in as the
// default since cosmonaut writes Zed-flavored config files when it
// detects Zed; every other editor is launched as a plain external
// command and is expected to understand the `ssh://alias/path` URI.
package editor

import "fmt"

// Editor abstracts editor-specific operations.
type Editor interface {
	// Name returns the editor identifier (e.g. "zed", or the user's
	// custom binary name).
	Name() string
	// FindBinary locates the editor's CLI binary on PATH.
	FindBinary() (string, error)
	// ConfigureConnection sets up editor-specific config for the SSH
	// connection (e.g. Zed's settings.json). Generic editors return nil.
	ConfigureConnection(sshAlias, workspacePath, nickname string, uploadBinary *bool) error
	// LaunchRemote opens the editor connected to the remote workspace.
	LaunchRemote(sshAlias, workspacePath string) error
}

// ForName returns an Editor implementation for the given name. An empty
// name (or "zed" / "zeditor") returns the Zed editor with its
// settings.json plumbing; any other name is wired through GenericEditor
// — which just runs `<name> ssh://<alias>/<path>` and lets the caller's
// editor handle the URI.
func ForName(name string) (Editor, error) {
	switch name {
	case "", "zed", "zeditor":
		return &ZedEditor{}, nil
	}
	if name == "" {
		return nil, fmt.Errorf("editor name must not be empty")
	}
	return &GenericEditor{Command: name}, nil
}

// ResolveNickname determines the nickname for a connection.
// Checks zedNickname, targetDisplayName, codespaceDisplayName, then targetName.
func ResolveNickname(zedNickname, targetDisplayName, codespaceDisplayName, targetName string) string {
	if zedNickname != "" {
		return zedNickname
	}
	if targetDisplayName != "" {
		return targetDisplayName
	}
	if codespaceDisplayName != "" {
		return codespaceDisplayName
	}
	return targetName
}
