package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestReadFilePaginationOffsetBeyondFileLength(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "short.txt"), []byte("line1\nline2"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	args, err := json.Marshal(map[string]interface{}{"path": "short.txt", "offset": 100, "limit": 5})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	tool := tools.NewReadFileTool(dir)
	out, execErr := tool.Execute(context.Background(), args)
	if execErr != nil {
		t.Fatalf("execute: %v", execErr)
	}
	if out != "" {
		t.Fatalf("expected empty string when offset is beyond end, got: %q", out)
	}
}
