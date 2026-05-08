package tools

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicRenameFunc is the rename syscall used by AtomicWriteFile.
// It may be replaced in tests to inject rename failures.
var AtomicRenameFunc func(string, string) error = os.Rename

// AtomicWriteFile writes data to path atomically: writes to a temp file
// in the same directory, fsyncs, then renames over path. On any error,
// the temp file is removed and the original path is untouched.
// Preserves the original file's mode. Caller must ensure path exists.
func AtomicWriteFile(path string, data []byte) error {
	origInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat original: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".edit-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if anything fails before rename.
	committed := false
	defer func() {
		if !committed {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	if err := os.Chmod(tmpName, origInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod temp: %w", err)
	}

	if err := AtomicRenameFunc(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	committed = true
	return nil
}
