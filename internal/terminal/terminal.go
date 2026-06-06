// Package terminal provides shared helpers for launching commands in the
// user's platform terminal emulator, used by both the GUI and TUI.
//
// The helpers wrap two flows:
//
//   - OpenSSHInTerminal builds and launches an `ssh -t <alias> <cmd>` command
//     in the terminal. The remote working directory is shell-quoted so paths
//     containing spaces or other special characters can't be misinterpreted.
//   - OpenCommandInTerminal launches an arbitrary shell command. On macOS the
//     command is passed through osascript as an argv parameter (never
//     interpolated into the AppleScript source) to avoid AppleScript-level
//     injection. On Linux a list of well-known terminal emulators is tried in
//     order and exec'd with argv [-e sh -c <cmd>].
package terminal

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
)

// linuxTerminals lists terminal emulators tried in order on Linux. The first
// one found on PATH wins.
var linuxTerminals = []string{"ghostty", "alacritty", "kitty", "gnome-terminal", "xterm"}

// OpenSSHInTerminal opens an SSH session to the given alias in the user's
// terminal emulator. workspacePath, if non-empty, is `cd`'d into before the
// remote shell starts; it is POSIX-shell-quoted so paths with spaces, quotes,
// or shell metacharacters are safe. When useTmux is true the remote command
// is `tmux new -A -s cosmonaut`, so the shell survives an SSH drop and can
// be re-attached by re-running the same command.
func OpenSSHInTerminal(alias, workspacePath string, useTmux bool) {
	remoteCmd := "exec $SHELL -l"
	if useTmux {
		remoteCmd = "tmux new -A -s cosmonaut"
	}
	cdPrefix := ""
	if workspacePath != "" {
		cdPrefix = fmt.Sprintf("cd %s && ", ShellQuote(workspacePath))
	}
	sshCmd := fmt.Sprintf("ssh -t %s %s", alias, ShellQuote(cdPrefix+remoteCmd))
	OpenCommandInTerminal(sshCmd)
}

// OpenCommandInTerminal launches the platform's default terminal emulator
// running the given shell command. The command is never interpolated into an
// AppleScript or shell string; it is passed as an argv parameter so the
// terminal/osascript see it as a single literal value.
func OpenCommandInTerminal(shellCmd string) {
	if runtime.GOOS == "darwin" {
		// Pass shellCmd as argv to osascript so AppleScript treats it as a
		// literal string. Interpolating into the script source would allow a
		// path/command containing `"` to break out and run arbitrary
		// AppleScript. `activate` brings Terminal.app to the foreground so
		// the user actually sees the new window.
		script := `on run argv
tell application "Terminal"
activate
do script item 1 of argv
end tell
end run`
		if err := exec.Command("osascript", "-e", script, "--", shellCmd).Run(); err != nil {
			log.Printf("terminal: osascript: %v", err)
		}
		return
	}
	for _, term := range linuxTerminals {
		if _, err := exec.LookPath(term); err == nil {
			if err := exec.Command(term, "-e", "sh", "-c", shellCmd).Run(); err != nil {
				log.Printf("terminal: %s: %v", term, err)
			}
			return
		}
	}
}

// ShellQuote wraps s in POSIX single quotes so it can be safely embedded in
// a shell command. Empty strings return `”`; strings containing no shell
// metacharacters are returned unchanged for readability. Embedded single
// quotes are escaped via the `'\”` idiom.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\"'$`\\!*?[]{}<>|&;()") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
