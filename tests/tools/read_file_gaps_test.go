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

func TestReadFileLimitOnlyNoOffset(t *testing.T) {
	// When limit > 0 and offset == 0 (default), sliceLines must return from
	// the beginning of the file up to limit lines.
	dir := t.TempDir()
	content := "line1\nline2\nline3\nline4\nline5"
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	args, err := json.Marshal(map[string]interface{}{"path": "f.txt", "limit": 2})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	tool := tools.NewReadFileTool(dir)
	out, execErr := tool.Execute(context.Background(), args)
	if execErr != nil {
		t.Fatalf("execute: %v", execErr)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines with limit=2, got %d: %q", len(lines), out)
	}
	if lines[0] != "line1" || lines[1] != "line2" {
		t.Fatalf("unexpected lines: %v", lines)
	}
}

func TestReadFileOffsetOnlyNoLimit(t *testing.T) {
	// When offset > 0 and limit == 0, sliceLines returns from offset to EOF.
	dir := t.TempDir()
	content := "a\nb\nc\nd"
	if err := os.WriteFile(filepath.Join(dir, "g.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	args, err := json.Marshal(map[string]interface{}{"path": "g.txt", "offset": 3})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	tool := tools.NewReadFileTool(dir)
	out, execErr := tool.Execute(context.Background(), args)
	if execErr != nil {
		t.Fatalf("execute: %v", execErr)
	}

	// offset=3 means start at line index 2 (1-indexed), so expect "c\nd"
	if !strings.Contains(out, "c") {
		t.Fatalf("expected output to contain 'c' from offset=3, got: %q", out)
	}
}

func TestReadFilePaginationStartBeforeZero(t *testing.T) {
	// offset == 1 means start at line index 0 (the first line); validate
	// the clamp: start = offset - 1 = 0, which is the minimum allowed.
	dir := t.TempDir()
	content := "first\nsecond\nthird"
	if err := os.WriteFile(filepath.Join(dir, "h.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	args, err := json.Marshal(map[string]interface{}{"path": "h.txt", "offset": 1, "limit": 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	tool := tools.NewReadFileTool(dir)
	out, execErr := tool.Execute(context.Background(), args)
	if execErr != nil {
		t.Fatalf("execute: %v", execErr)
	}

	if strings.TrimRight(out, "\n") != "first" {
		t.Fatalf("expected 'first', got: %q", out)
	}
}
