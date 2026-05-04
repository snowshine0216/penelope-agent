package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func writeArgs(t *testing.T, path, content string) json.RawMessage {
	t.Helper()

	out, err := json.Marshal(map[string]string{"path": path, "content": content})
	if err != nil {
		t.Fatalf("marshal write args: %v", err)
	}
	return out
}

func TestWriteFileCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	tool := tools.NewWriteFileTool(dir)

	if _, err := tool.Execute(context.Background(), writeArgs(t, "out.txt", "payload")); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("file content = %q, want payload", got)
	}
}

func TestWriteFileCreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	tool := tools.NewWriteFileTool(dir)

	if _, err := tool.Execute(context.Background(), writeArgs(t, "a/b/c/deep.txt", "hi")); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "a", "b", "c", "deep.txt"))
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "hi" {
		t.Fatalf("file content = %q, want hi", got)
	}
}

func TestWriteFileOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := tools.NewWriteFileTool(dir)
	if _, err := tool.Execute(context.Background(), writeArgs(t, "x.txt", "new")); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("file content = %q, want new (overwrite)", got)
	}
}

func TestWriteFileMalformedArgsReturnsError(t *testing.T) {
	tool := tools.NewWriteFileTool(t.TempDir())

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":}`))
	if err == nil {
		t.Fatal("expected JSON parse error for malformed args")
	}
}

// TestWriteFilePathTraversalCurrentlyEscapesWorkDir mirrors the read_file
// known-bug test. write_file joins workDir + path without normalization,
// so the model can write outside the workspace. Flip the assertions when
// the bug is fixed.
func TestWriteFilePathTraversalCurrentlyEscapesWorkDir(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tool := tools.NewWriteFileTool(work)
	if _, err := tool.Execute(context.Background(), writeArgs(t, "../escaped.txt", "leak")); err != nil {
		t.Fatalf("execute (current behavior should NOT error): %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "escaped.txt"))
	if err != nil {
		t.Fatalf("expected file to land outside workDir under current buggy behavior: %v", err)
	}
	if string(got) != "leak" {
		t.Fatalf("escaped file content = %q, want leak", got)
	}
	t.Log("KNOWN BUG: write_file allows path traversal outside workDir; fix by resolving abs path and asserting HasPrefix(absWorkDir).")
}
