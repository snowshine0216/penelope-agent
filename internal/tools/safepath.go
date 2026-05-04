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

	return abs, nil
}
