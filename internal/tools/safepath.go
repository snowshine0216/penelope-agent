package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrPathEscape signals that a relative path resolves outside the workdir.
var ErrPathEscape = errors.New("path escapes workdir")

// ResolveInWorkDir joins relPath onto workDir, resolves the result to an
// absolute path, and asserts that the result remains inside workDir.
// Absolute relPaths and paths that traverse outside via "../" are rejected
// with ErrPathEscape.
func ResolveInWorkDir(workDir, relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("%w: absolute path %q not allowed", ErrPathEscape, relPath)
	}

	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve workdir: %w", err)
	}

	joined := filepath.Join(absWorkDir, relPath)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	sep := string(os.PathSeparator)
	if abs != absWorkDir && !strings.HasPrefix(abs, absWorkDir+sep) {
		return "", fmt.Errorf("%w: %q resolves outside %q", ErrPathEscape, relPath, absWorkDir)
	}

	// For files that already exist, resolve symlinks and re-check containment.
	// This prevents a symlink inside the workspace from pointing outside it.
	if realAbs, err := filepath.EvalSymlinks(abs); err == nil {
		realWorkDir := absWorkDir
		if r, err2 := filepath.EvalSymlinks(absWorkDir); err2 == nil {
			realWorkDir = r
		}
		if realAbs != realWorkDir && !strings.HasPrefix(realAbs, realWorkDir+sep) {
			return "", fmt.Errorf("%w: %q resolves outside %q via symlink", ErrPathEscape, relPath, absWorkDir)
		}
	}

	return abs, nil
}
