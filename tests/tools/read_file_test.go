package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if !strings.Contains(out, "elided") {
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

func TestReadFilePaginationReturnsSubset(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(filepath.Join(dir, "lines.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	args, err := json.Marshal(map[string]interface{}{"path": "lines.txt", "offset": 5, "limit": 3})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	tool := tools.NewReadFileTool(dir)
	out, execErr := tool.Execute(context.Background(), args)
	if execErr != nil {
		t.Fatalf("execute: %v", execErr)
	}

	gotLines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(gotLines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(gotLines), out)
	}
	if gotLines[0] != "line5" {
		t.Fatalf("first line = %q, want line5", gotLines[0])
	}
	if gotLines[2] != "line7" {
		t.Fatalf("last line = %q, want line7", gotLines[2])
	}
}

func TestReadFileRejectsPathTraversal(t *testing.T) {
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
	_, err := tool.Execute(context.Background(), readArgs(t, "../outside.txt"))
	if err == nil {
		t.Fatal("expected ErrPathEscape, got nil")
	}
	if !errors.Is(err, tools.ErrPathEscape) {
		t.Fatalf("expected ErrPathEscape, got %v", err)
	}
}
