package daemon

import (
	"context"
	"log"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"time"
)

// enrichPath merges the user's full shell PATH into the current PATH.
// Under launchd the default PATH is minimal (/usr/bin:/bin:/usr/sbin:/sbin).
// This uses the same approach as VS Code / Electron's fix-path: spawn
// an interactive login shell and capture its PATH.
func enrichPath() {
	usr, err := user.Current()
	if err != nil {
		log.Printf("path: cannot determine current user: %v", err)
		return
	}

	shell := userShell(usr)

	// -i (interactive) loads .zshrc/.bashrc where many users configure
	// Homebrew, nix, etc. -l (login) loads .zprofile/.bash_profile.
	shellCmd := `printf '%s\n' "$PATH"`
	if strings.HasSuffix(shell, "/fish") {
		shellCmd = `string join : $PATH`
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, "-ilc", shellCmd)
	cmd.Env = []string{
		"HOME=" + usr.HomeDir,
		"USER=" + usr.Username,
		"SHELL=" + shell,
	}

	out, err := cmd.Output()
	if err != nil {
		log.Printf("path: %s -ilc failed: %v", shell, err)
		return
	}

	shellPath := strings.TrimSpace(string(out))
	if shellPath == "" {
		return
	}

	current := os.Getenv("PATH")
	seen := make(map[string]bool)
	for _, p := range strings.Split(current, ":") {
		seen[p] = true
	}

	var extra []string
	for _, p := range strings.Split(shellPath, ":") {
		if p != "" && !seen[p] {
			seen[p] = true
			extra = append(extra, p)
		}
	}

	if len(extra) > 0 {
		merged := current + ":" + strings.Join(extra, ":")
		if err := os.Setenv("PATH", merged); err != nil {
			log.Printf("path: setenv PATH: %v", err)
			return
		}
		log.Printf("path: added %d directories from %s", len(extra), shell)
	}
}

// userShell returns the user's login shell, preferring dscl (works without
// env vars under launchd), falling back to $SHELL, then /bin/zsh.
func userShell(usr *user.User) string {
	out, err := exec.Command("dscl", ".", "-read", "/Users/"+usr.Username, "UserShell").Output()
	if err == nil {
		if _, after, ok := strings.Cut(strings.TrimSpace(string(out)), "UserShell: "); ok && after != "" {
			return after
		}
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/zsh"
}
