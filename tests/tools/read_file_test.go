package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func readArgs(t *testing.T, path string) json.RawMessage {
	t.Helper()

	out, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatalf("marshal read args: %v", err)
	}
	return out
}

func TestReadFileReturnsContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := tools.NewReadFileTool(dir)
	out, err := tool.Execute(context.Background(), readArgs(t, "hello.txt"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "hello world" {
		t.Fatalf("output = %q, want %q", out, "hello world")
	}
}

func TestReadFileMissingPathReturnsError(t *testing.T) {
	tool := tools.NewReadFileTool(t.TempDir())

	_, err := tool.Execute(context.Background(), readArgs(t, "does-not-exist.txt"))
	if err == nil {
		t.Fatal("expected error reading missing file")
	}
}

func TestReadFileMalformedArgsReturnsError(t *testing.T) {
	tool := tools.NewReadFileTool(t.TempDir())

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":}`))
	if err == nil {
		t.Fatal("expected JSON parse error for malformed args")
	}
}

func TestReadFileTruncatesLongContent(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("a", 12000)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := tools.NewReadFileTool(dir)
	out, err := tool.Execute(context.Background(), readArgs(t, "big.txt"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "已被系统截断") {
		t.Fatalf("expected truncation marker in output")
	}
}

func TestReadFileResolvesRelativeToWorkDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "x.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := tools.NewReadFileTool(dir)
	out, err := tool.Execute(context.Background(), readArgs(t, filepath.Join("nested", "x.txt")))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "nested" {
		t.Fatalf("output = %q, want nested", out)
	}
}

// TestReadFilePathTraversalCurrentlyEscapesWorkDir documents a known bug:
// the tool joins workDir + path without normalizing, so "../" segments
// escape the workspace. When the bug is fixed (path resolution + prefix
// check), this test should be flipped to expect an error.
func TestReadFilePathTraversalCurrentlyEscapesWorkDir(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tool := tools.NewReadFileTool(work)
	out, err := tool.Execute(context.Background(), readArgs(t, "../outside.txt"))
	if err != nil {
		t.Fatalf("execute (current behavior should NOT error): %v", err)
	}
	if out != "secret" {
		t.Fatalf("expected current-behavior escape to read 'secret', got %q", out)
	}
	t.Log("KNOWN BUG: read_file allows path traversal outside workDir; fix by resolving abs path and asserting HasPrefix(absWorkDir).")
}
