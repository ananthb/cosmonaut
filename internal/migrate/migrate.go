// Package migrate handles one-time migration of config, history, and SSH
// paths from the old "codespace-zed" name to "cosmonaut".
package migrate

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/xdg"

	"github.com/linuskendall/cosmonaut/internal/sshconfig"
)

const (
	oldName = "codespace-zed"
	newName = "cosmonaut"

	oldSSHDir  = "codespaces-zed"
	oldSSHIncl = "Include ~/.ssh/codespaces-zed/*.conf"
	newSSHIncl = "Include ~/.ssh/cosmonaut/*.conf"
)

// Run migrates old paths to the new cosmonaut locations.
// Safe to call multiple times: it's a no-op if migration is already done.
func Run() {
	migrateDir(
		filepath.Join(xdg.ConfigHome, oldName),
		filepath.Join(xdg.ConfigHome, newName),
	)
	migrateDir(
		filepath.Join(xdg.StateHome, oldName),
		filepath.Join(xdg.StateHome, newName),
	)

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	migrateDir(
		filepath.Join(home, ".ssh", oldSSHDir),
		filepath.Join(home, ".ssh", newName),
	)
	migrateSSHInclude(filepath.Join(home, ".ssh", "config"))
	refreshSSHExtras()
}

// refreshSSHExtras rewrites the cosmonaut-managed tail of every codespace
// conf in ~/.ssh/cosmonaut/ so option additions (e.g. IdentityAgent none
// for YubiKey users) take effect for codespaces created on older versions.
// Idempotent: a no-op once every conf is at the current version.
func refreshSSHExtras() {
	paths := sshconfig.ResolvePaths()
	n, err := sshconfig.RefreshAllManagedExtras(paths.IncludeDir)
	if err != nil {
		log.Printf("migrate: refresh ssh extras: %v", err)
		return
	}
	if n > 0 {
		log.Printf("migrate: refreshed ssh extras in %d codespace conf(s)", n)
	}
}

// migrateDir copies oldDir to newDir if oldDir exists and newDir does not.
func migrateDir(oldDir, newDir string) {
	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		return
	}
	if _, err := os.Stat(newDir); err == nil {
		return // new dir already exists
	}

	// Create a symlink so both old and new paths work.
	if err := os.MkdirAll(filepath.Dir(newDir), 0755); err != nil {
		log.Printf("migrate: mkdir %s: %v", filepath.Dir(newDir), err)
		return
	}
	if err := os.Symlink(oldDir, newDir); err != nil {
		log.Printf("migrate: symlink %s -> %s: %v", newDir, oldDir, err)
	} else {
		log.Printf("migrate: linked %s -> %s", newDir, oldDir)
	}
}

// migrateSSHInclude updates the SSH config Include line from old to new.
func migrateSSHInclude(sshConfigPath string) {
	data, err := os.ReadFile(sshConfigPath)
	if err != nil {
		return
	}

	content := string(data)
	if !strings.Contains(content, oldSSHIncl) {
		return
	}

	// Replace old include with new. If the new include already exists, just
	// remove the old one to avoid duplicates.
	if strings.Contains(content, newSSHIncl) {
		content = strings.Replace(content, oldSSHIncl+"\n", "", 1)
	} else {
		content = strings.Replace(content, oldSSHIncl, newSSHIncl, 1)
	}

	if err := os.WriteFile(sshConfigPath, []byte(content), 0644); err != nil {
		log.Printf("migrate: update %s: %v", sshConfigPath, err)
	} else {
		log.Printf("migrate: updated SSH include in %s", sshConfigPath)
	}
}
