package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func editArgs(t *testing.T, path string, edits []map[string]interface{}) json.RawMessage {
	t.Helper()
	out, err := json.Marshal(map[string]interface{}{
		"path":  path,
		"edits": edits,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func TestEditFileSingleExactMatch(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(target, []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{
		{"old_text": "world", "new_text": "Go"},
	})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "edited") || !strings.Contains(out, "L1=1") {
		t.Fatalf("output = %q, want it to mention edited + L1=1", out)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "hello Go\n" {
		t.Fatalf("file = %q, want hello Go\\n", got)
	}
}

func TestEditFileMultiEditSequential(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(target, []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{
		{"old_text": "alpha", "new_text": "beta"},
		{"old_text": "beta", "new_text": "gamma"},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "gamma\n" {
		t.Fatalf("file = %q, want gamma\\n", got)
	}
}

func TestEditFileMultiEditRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	originalBytes := []byte("alpha\nbeta\n")
	if err := os.WriteFile(target, originalBytes, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{
		{"old_text": "alpha", "new_text": "ALPHA"},
		{"old_text": "missing", "new_text": "x"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error from missing old_text in edit[1]")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != string(originalBytes) {
		t.Fatalf("file changed despite rollback: got %q, want %q", got, originalBytes)
	}
}

func TestEditFileFileMissingNamesWriteFile(t *testing.T) {
	dir := t.TempDir()
	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "nope.txt", []map[string]interface{}{
		{"old_text": "a", "new_text": "b"},
	})

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "write_file") {
		t.Fatalf("error = %q, want it to mention write_file", err)
	}
}

func TestEditFileRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(target, []byte("seed"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(work)
	args := editArgs(t, "../outside.txt", []map[string]interface{}{
		{"old_text": "seed", "new_text": "leaked"},
	})

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected ErrPathEscape")
	}
	if !errors.Is(err, tools.ErrPathEscape) {
		t.Fatalf("expected ErrPathEscape, got %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "seed" {
		t.Fatal("outside file should not have been modified")
	}
}

func TestEditFileRejectsNoOpEdit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{
		{"old_text": "hello", "new_text": "hello"},
	})

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for no-op edit")
	}
	if !strings.Contains(err.Error(), "equals new_text") {
		t.Fatalf("error = %q, want it to mention no-op", err)
	}
}

func TestEditFileEmptyEditsArrayErrors(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(target, []byte("seed"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{})

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for empty edits")
	}
}

func TestEditFileMalformedArgsErrors(t *testing.T) {
	tool := tools.NewEditFileTool(t.TempDir())
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":}`))
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}

func TestEditFileIndentationHallucinationIntegration(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "server.go")
	original := "" +
		"func main() {\n" +
		"        if x {\n" +
		"                doThing()\n" +
		"        }\n" +
		"}\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "server.go", []map[string]interface{}{
		{
			"old_text": "if x {\n        doThing()\n}",
			"new_text": "if y {\n        otherThing()\n}",
		},
	})

	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "L4=1") {
		t.Fatalf("output = %q, want L4=1", out)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	want := "" +
		"func main() {\n" +
		"        if y {\n" +
		"                otherThing()\n" +
		"        }\n" +
		"}\n"
	if string(got) != want {
		t.Fatalf("file = %q\nwant = %q", got, want)
	}
}

// Gap coverage: formatEditError else branch (ambiguous match, not "not found")
func TestEditFileAmbiguousMatchError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(target, []byte("foo\nfoo\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{
		{"old_text": "foo", "new_text": "bar"}, // two occurrences → ambiguous
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for ambiguous match, got nil")
	}
	if strings.Contains(err.Error(), "not found") {
		t.Fatalf("error mentions 'not found' but should be ambiguous: %v", err)
	}
	if !strings.Contains(err.Error(), "disambiguate") {
		t.Fatalf("error = %v, want mention of 'disambiguate'", err)
	}
}

// Gap coverage: os.Stat returns a non-IsNotExist error (permission denied)
func TestEditFileStatPermissionError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	dir := t.TempDir()
	subdir := filepath.Join(dir, "locked")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(subdir, "x.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Remove execute bit so os.Stat of any path inside will fail.
	if err := os.Chmod(subdir, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(subdir, 0o755) //nolint:errcheck

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "locked/x.txt", []map[string]interface{}{
		{"old_text": "hello", "new_text": "world"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for permission-denied stat")
	}
	if strings.Contains(err.Error(), "write_file") {
		t.Fatalf("error mentions write_file but should be a stat error: %v", err)
	}
}

// Gap coverage: os.ReadFile returns an error (write-only file)
func TestEditFileReadFileFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	// 0o200 = write-only: os.Stat succeeds (checks dir entry), os.ReadFile fails.
	if err := os.WriteFile(target, []byte("hello\n"), 0o200); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{
		{"old_text": "hello", "new_text": "world"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for unreadable file")
	}
	if !strings.Contains(err.Error(), "read file") {
		t.Fatalf("error = %v, want 'read file' error", err)
	}
}

// Gap coverage: AtomicWriteFile fails (directory made read-only after seed)
func TestEditFileAtomicWriteFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Read-only dir: stat and ReadFile still succeed; CreateTemp fails.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755) //nolint:errcheck

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{
		{"old_text": "hello", "new_text": "world"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error when directory is read-only")
	}
	if !strings.Contains(err.Error(), "write file") {
		t.Fatalf("error = %v, want 'write file' error", err)
	}
}
