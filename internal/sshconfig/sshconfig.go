// Package sshconfig manages the local SSH configuration for codespace
// connections. It writes per-codespace config files into
// ~/.ssh/cosmonaut/ and ensures the main ~/.ssh/config includes them.
package sshconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const SSHIncludeLine = "Include ~/.ssh/cosmonaut/*.conf"

// HostStarScopedLine is the form a bare `Host *` is rewritten to when the
// user accepts the scoping fix. The negation patterns prevent the
// catch-all block from contributing IdentityFile / IdentityAgent / etc.
// to codespace hosts (gh emits both `cs-*` and `cs.*` aliases).
const HostStarScopedLine = "Host * !cs-* !cs.*"

// MainConfigBackupSuffix is appended to the main ssh config path for the
// one-shot backup written before ScopeHostStarBlocks first modifies it.
const MainConfigBackupSuffix = ".cosmonaut.bak"

var hostAliasRe = regexp.MustCompile(`(?m)^\s*Host\s+([^\s*][^\s]*)\s*$`)

// hostStarLineRe matches lines that are exactly `Host *` (case-insensitive,
// any leading whitespace, any trailing whitespace). More complex patterns
// like `Host *.example.com`, `Host * server1`, or `Host * !already` are
// deliberately not matched — those are too risky to rewrite blindly.
var hostStarLineRe = regexp.MustCompile(`(?im)^([ \t]*)Host[ \t]+\*[ \t]*$`)

// ParsePrimaryHostAlias extracts the first concrete Host entry from SSH config text.
func ParsePrimaryHostAlias(sshConfig string) (string, error) {
	match := hostAliasRe.FindStringSubmatch(sshConfig)
	if match == nil {
		return "", fmt.Errorf("could not find a concrete Host entry in gh codespace ssh --config output")
	}
	return match[1], nil
}

// EnsureIncludeLine ensures the SSH include line is at the top of the config.
// It removes any existing copy and prepends it.
func EnsureIncludeLine(configText string) string {
	var lines []string
	for _, line := range strings.Split(configText, "\n") {
		if strings.TrimSpace(line) != SSHIncludeLine {
			lines = append(lines, line)
		}
	}
	body := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if body != "" {
		return SSHIncludeLine + "\n" + body + "\n"
	}
	return SSHIncludeLine + "\n"
}

// SSHPaths holds the resolved SSH directory paths.
type SSHPaths struct {
	MainConfigPath string
	IncludeDir     string
}

// ResolvePaths returns the SSH paths for the current platform.
func ResolvePaths() SSHPaths {
	home, _ := os.UserHomeDir()
	sshDir := filepath.Join(home, ".ssh")
	return SSHPaths{
		MainConfigPath: filepath.Join(sshDir, "config"),
		IncludeDir:     filepath.Join(sshDir, "cosmonaut"),
	}
}

// CodespaceConfigPath returns the path for a codespace-specific SSH config.
func (p SSHPaths) CodespaceConfigPath(codespaceName string) string {
	return filepath.Join(p.IncludeDir, codespaceName+".conf")
}

// EnsureConfigIncludesGenerated ensures the main SSH config includes the generated configs.
func EnsureConfigIncludesGenerated(mainConfigPath string) error {
	current, err := os.ReadFile(mainConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	currentStr := string(current)
	if strings.Contains(currentStr, SSHIncludeLine) {
		return nil
	}

	updated := EnsureIncludeLine(currentStr)
	dir := filepath.Dir(mainConfigPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(mainConfigPath, []byte(updated), 0644)
}

// NeedsHostStarScoping reports whether mainConfigPath contains any bare
// `Host *` lines that should be narrowed so the catch-all block doesn't
// apply to codespace hosts. Returns false on read errors or missing file
// (the GUI banner shouldn't pester users without an actionable fix).
func NeedsHostStarScoping(mainConfigPath string) bool {
	data, err := os.ReadFile(mainConfigPath)
	if err != nil {
		return false
	}
	return hostStarLineRe.Match(data)
}

// ScopeHostStarBlocks rewrites bare `Host *` lines in mainConfigPath to
// `Host * !cs-* !cs.*` so codespace hosts skip catch-all auth rules
// (e.g. an IdentityFile pointing at a YubiKey-resident SK key that
// blocks ssh when the device isn't plugged in). Idempotent.
//
// Writes a one-shot backup to mainConfigPath+MainConfigBackupSuffix
// before the first modification, so the user can recover the original
// if the rewrite breaks something else for them. Subsequent runs leave
// the existing backup untouched.
func ScopeHostStarBlocks(mainConfigPath string) (bool, error) {
	data, err := os.ReadFile(mainConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	updated := hostStarLineRe.ReplaceAllString(string(data), "${1}"+HostStarScopedLine)
	if updated == string(data) {
		return false, nil
	}
	backup := mainConfigPath + MainConfigBackupSuffix
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		if err := os.WriteFile(backup, data, 0644); err != nil {
			return false, fmt.Errorf("backup %s: %w", backup, err)
		}
	}
	return true, os.WriteFile(mainConfigPath, []byte(updated), 0644)
}

// ReadExistingAlias reads the SSH alias from an existing codespace config file.
// Returns the alias and true if the file exists and contains a valid Host entry,
// or empty string and false otherwise.
func ReadExistingAlias(includeDir, codespaceName string) (string, bool) {
	path := filepath.Join(includeDir, codespaceName+".conf")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	alias, err := ParsePrimaryHostAlias(string(data))
	if err != nil {
		return "", false
	}
	return alias, true
}

// managedExtrasVersion is bumped whenever managedExtrasBody changes, so
// existing on-disk confs get rewritten by RefreshAllManagedExtras on the
// next applet start.
const managedExtrasVersion = 2

// managedExtrasBody is the cosmonaut-controlled tail of every codespace
// conf. Indented two spaces so it sits inside gh's `Host cs-*` block.
//
// Keepalive: ServerAliveInterval pings every 15s, ServerAliveCountMax
// drops after 3 missed pongs (45s), ConnectionAttempts retries the
// initial connection 3x.
//
// IdentityAgent/PKCS11Provider none isolate codespace auth from the
// user's main SSH agent and PKCS#11 provider, so connections don't fail
// when a smartcard/YubiKey configured in ~/.ssh/config isn't plugged in.
// gh emits explicit IdentityFile + IdentitiesOnly yes for codespaces, so
// no agent is needed here.
const managedExtrasBody = `  ServerAliveInterval 15
  ServerAliveCountMax 3
  ConnectionAttempts 3
  IdentityAgent none
  PKCS11Provider none
`

const (
	managedBeginPrefix = "  # BEGIN cosmonaut managed extras"
	managedEndPrefix   = "  # END cosmonaut managed extras"
)

// managedExtras returns the current sentinel-bracketed managed block.
func managedExtras() string {
	return fmt.Sprintf("%s v%d\n%s%s v%d\n",
		managedBeginPrefix, managedExtrasVersion,
		managedExtrasBody,
		managedEndPrefix, managedExtrasVersion)
}

// applyManagedExtras returns content with any prior managed block (or
// legacy unmarked extras from cosmonaut < v0.8.x) replaced by the
// current managed block. Idempotent: applying twice yields the same
// output as applying once.
func applyManagedExtras(content string) string {
	content = stripManagedBlock(content)
	body := strings.TrimRight(content, "\n")
	if body == "" {
		return managedExtras()
	}
	return body + "\n" + managedExtras()
}

// stripManagedBlock removes the cosmonaut-managed tail from content.
// It handles both sentinel-bracketed blocks (current) and legacy bare
// extras starting with `  ServerAliveInterval 15` at column 0 of a
// line (pre-sentinel cosmonaut versions).
func stripManagedBlock(content string) string {
	if i := indexAtLineStart(content, managedBeginPrefix); i >= 0 {
		after := content[i:]
		if j := strings.Index(after, managedEndPrefix); j >= 0 {
			tail := after[j:]
			if eol := strings.IndexByte(tail, '\n'); eol >= 0 {
				return content[:i] + tail[eol+1:]
			}
			return content[:i]
		}
	}
	if i := indexAtLineStart(content, "  ServerAliveInterval 15"); i >= 0 {
		return content[:i]
	}
	return content
}

// indexAtLineStart finds substr in content, but only at the start of a
// line (offset 0 or right after \n). Returns -1 if not found.
func indexAtLineStart(content, substr string) int {
	from := 0
	for {
		i := strings.Index(content[from:], substr)
		if i < 0 {
			return -1
		}
		abs := from + i
		if abs == 0 || content[abs-1] == '\n' {
			return abs
		}
		from = abs + 1
	}
}

// WriteCodespaceConfig writes the SSH config for a codespace, replacing
// any prior cosmonaut-managed tail with the current one.
func WriteCodespaceConfig(includeDir, codespaceName, content string) error {
	if err := os.MkdirAll(includeDir, 0700); err != nil {
		return err
	}
	content = applyManagedExtras(content)
	path := filepath.Join(includeDir, codespaceName+".conf")
	return os.WriteFile(path, []byte(content), 0644)
}

// RefreshManagedExtras rewrites the managed block in path to the current
// version. Returns true if the file was changed. No-op if already current
// or if the file doesn't exist.
func RefreshManagedExtras(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	updated := applyManagedExtras(string(data))
	if updated == string(data) {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(updated), 0644)
}

// RefreshAllManagedExtras walks includeDir and refreshes the managed
// block in every *.conf file. Returns the number of files updated.
// Safe to call on every applet startup: idempotent and cheap.
func RefreshAllManagedExtras(includeDir string) (int, error) {
	entries, err := os.ReadDir(includeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		full := filepath.Join(includeDir, e.Name())
		changed, err := RefreshManagedExtras(full)
		if err != nil {
			return n, fmt.Errorf("%s: %w", full, err)
		}
		if changed {
			n++
		}
	}
	return n, nil
}
