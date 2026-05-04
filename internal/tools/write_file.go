// internal/tools/write_file.go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// WriteFileTool writes files within the workspace.
type WriteFileTool struct {
	workDir string // 工作区约束
}

func NewWriteFileTool(workDir string) *WriteFileTool {
	return &WriteFileTool{workDir: workDir}
}

func (t *WriteFileTool) Name() string {
	return "write_file"
}

func (t *WriteFileTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "Create or overwrite a file in the workspace. Parent directories are created automatically. Provide a path relative to the workspace root.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "File path relative to the workspace",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Content to write to the file",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *WriteFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input writeFileArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}

	// Resolve and sandbox the path — rejects absolute paths and traversal.
	fullPath, err := ResolveInWorkDir(t.workDir, input.Path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	// 自动创建缺失的父级目录
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "", fmt.Errorf("create parent directories: %w", err)
	}

	// 写入文件内容，权限设为 0644
	err = os.WriteFile(fullPath, []byte(input.Content), 0644)
	if err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return input.Path, nil
}
