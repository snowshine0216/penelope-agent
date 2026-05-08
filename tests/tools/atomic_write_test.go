package tools_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestAtomicWriteCreatesFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("seed"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := tools.AtomicWriteFile(target, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want hello", got)
	}
}

func TestAtomicWriteOverwritesContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := tools.AtomicWriteFile(target, []byte("new")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want new", got)
	}
}

func TestAtomicWritePreservesMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := tools.AtomicWriteFile(target, []byte("new")); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != fs.FileMode(0o600) {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestAtomicWriteRemovesTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := tools.AtomicWriteFile(target, []byte("new")); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".edit-") {
			t.Fatalf("temp file %q still present after success", e.Name())
		}
	}
}

func TestAtomicWriteRollsBackOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Make the directory unwritable so CreateTemp fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// Restore so t.TempDir cleanup can remove the directory.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := tools.AtomicWriteFile(target, []byte("new")); err == nil {
		t.Fatal("expected error when temp file cannot be created")
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("re-chmod: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("content = %q, want original (rollback failed)", got)
	}
}
