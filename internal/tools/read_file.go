package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// ReadFileTool reads files within the workspace.
type ReadFileTool struct {
	workDir string
}

func NewReadFileTool(workDir string) *ReadFileTool {
	return &ReadFileTool{workDir: workDir}
}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

func (t *ReadFileTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "Read the contents of a file in the workspace. Provide a path relative to the workspace root.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "File path relative to the workspace, e.g. cmd/claw/main.go",
				},
				"offset": map[string]interface{}{
					"type":        "integer",
					"description": "Optional 1-indexed line number to start reading from",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Optional max number of lines to return",
				},
			},
			"required": []string{"path"},
		},
	}
}

type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func (t *ReadFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input readFileArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}

	// Resolve and sandbox the path — rejects absolute paths and traversal.
	fullPath, err := ResolveInWorkDir(t.workDir, input.Path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	const maxFileBytes = 10 << 20 // 10 MB hard cap to prevent OOM on large files

	info, err := os.Stat(fullPath)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	if info.Size() > maxFileBytes {
		return "", fmt.Errorf("file too large (%d bytes, limit %d): use offset/limit to read specific sections", info.Size(), maxFileBytes)
	}

	file, err := os.Open(fullPath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	// Optional line-based pagination; otherwise head+tail truncation.
	text := string(content)
	if input.Offset > 0 || input.Limit > 0 {
		text = sliceLines(text, input.Offset, input.Limit)
		return text, nil
	}
	return TruncateForLLM(text, 8000), nil
}

func sliceLines(s string, offset, limit int) string {
	lines := strings.Split(s, "\n")
	start := offset - 1
	if start < 0 {
		start = 0
	}
	if start >= len(lines) {
		return ""
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	return strings.Join(lines[start:end], "\n")
}
