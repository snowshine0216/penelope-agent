package tools_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestResolveSimpleRelativePath(t *testing.T) {
	dir := t.TempDir()
	got, err := tools.ResolveInWorkDir(dir, "sub/file.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(dir, "sub", "file.txt")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveRejectsTraversalEscape(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := tools.ResolveInWorkDir(dir, "../escaped.txt")
	if err == nil {
		t.Fatal("expected ErrPathEscape, got nil")
	}
	if !errors.Is(err, tools.ErrPathEscape) {
		t.Fatalf("expected ErrPathEscape, got %v", err)
	}
}

func TestResolveRejectsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	_, err := tools.ResolveInWorkDir(dir, "/etc/passwd")
	if err == nil {
		t.Fatal("expected error for absolute path, got nil")
	}
}

func TestResolveAllowsCleanInternalPath(t *testing.T) {
	dir := t.TempDir()
	got, err := tools.ResolveInWorkDir(dir, "a/./b/../c.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, dir) {
		t.Fatalf("resolved path %q escapes workDir %q", got, dir)
	}
}
