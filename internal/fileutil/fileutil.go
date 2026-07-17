// Package fileutil provides crash-safe file writing for the user-owned
// files cosmonaut manages (~/.ssh/config, cosmonaut config, Zed settings,
// history). A plain os.WriteFile truncates before writing, so a crash or
// full disk mid-write destroys the file — unacceptable for a file like
// ~/.ssh/config whose loss locks the user out of every SSH host they use.
package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path via a temp file in the same
// directory followed by fsync and rename, so the destination always
// contains either the old contents or the new contents, never a torn
// write. If the destination already exists its file mode is preserved;
// otherwise perm is used.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup on any failure path; no-op after rename.
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpName, path, err)
	}
	return nil
}
