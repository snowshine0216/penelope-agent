package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestWriteFileMkdirAllFailsOnReadOnlyParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits differ on Windows")
	}

	root := t.TempDir()
	// Make root read-only so MkdirAll cannot create subdirectories.
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	tool := tools.NewWriteFileTool(root)
	args, err := json.Marshal(map[string]string{"path": "newdir/file.txt", "content": "x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	_, execErr := tool.Execute(context.Background(), args)
	if execErr == nil {
		t.Fatal("expected error when parent directory creation fails, got nil")
	}
}

func TestWriteFileSuccessMessageContainsPath(t *testing.T) {
	dir := t.TempDir()
	tool := tools.NewWriteFileTool(dir)

	out, err := tool.Execute(context.Background(), writeArgs(t, "result.txt", "data"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty success message")
	}

	// Verify the file was actually written.
	got, readErr := os.ReadFile(filepath.Join(dir, "result.txt"))
	if readErr != nil {
		t.Fatalf("readback: %v", readErr)
	}
	if string(got) != "data" {
		t.Fatalf("file content = %q, want data", got)
	}
}
