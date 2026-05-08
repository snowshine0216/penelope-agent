package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// EditFileTool applies one or more string replacements to an existing
// file in the workspace, using the L1->L4 fuzzy match chain. Multi-edit
// is atomic: any failure rolls back to the original file content.
type EditFileTool struct {
	workDir string
}

func NewEditFileTool(workDir string) *EditFileTool {
	return &EditFileTool{workDir: workDir}
}

func (t *EditFileTool) Name() string {
	return "edit_file"
}

func (t *EditFileTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name: t.Name(),
		Description: "Apply one or more string replacements to an existing file. " +
			"Each edit finds old_text in the file and replaces it with new_text. " +
			"Edits apply sequentially against the in-memory result; if any edit " +
			"fails, none are applied and the file is unchanged. Set replace_all=true " +
			"to replace every occurrence; otherwise old_text must match exactly one " +
			"location after fuzzy normalization. Use read_file first to obtain exact " +
			"contents. Use write_file to create new files.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "File path relative to the workspace",
				},
				"edits": map[string]interface{}{
					"type":        "array",
					"description": "One or more string replacements to apply atomically",
					"minItems":    1,
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"old_text": map[string]interface{}{
								"type":        "string",
								"description": "Text to find (matched via fuzzy chain)",
							},
							"new_text": map[string]interface{}{
								"type":        "string",
								"description": "Replacement text",
							},
							"replace_all": map[string]interface{}{
								"type":        "boolean",
								"description": "If true, replace every occurrence (default false)",
							},
						},
						"required": []string{"old_text", "new_text"},
					},
				},
			},
			"required": []string{"path", "edits"},
		},
	}
}

type editFileArgs struct {
	Path  string     `json:"path"`
	Edits []editSpec `json:"edits"`
}

type editSpec struct {
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (t *EditFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input editFileArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	if len(input.Edits) == 0 {
		return "", fmt.Errorf("edit_file: edits array is empty")
	}

	fullPath, err := ResolveInWorkDir(t.workDir, input.Path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("edit_file: %q does not exist; use write_file to create it", input.Path)
		}
		return "", fmt.Errorf("stat file: %w", err)
	}

	original, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	current := string(original)
	var levelCounts [4]int
	for i, e := range input.Edits {
		if e.OldText == e.NewText {
			return "", fmt.Errorf("edit_file: edit[%d] old_text equals new_text", i)
		}
		next, level, ferr := FuzzyReplace(current, e.OldText, e.NewText, e.ReplaceAll)
		if ferr != nil {
			return "", formatEditError(i, input.Path, ferr)
		}
		current = next
		levelCounts[level-1]++ // level is 1-based; shift to 0-based index
	}

	if err := AtomicWriteFile(fullPath, []byte(current)); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf(
		"edited %q (%d edits applied: L1=%d L2=%d L3=%d L4=%d)",
		input.Path, len(input.Edits),
		levelCounts[0], levelCounts[1], levelCounts[2], levelCounts[3],
	), nil
}

func formatEditError(editIndex int, path string, ferr error) error {
	msg := ferr.Error()
	if strings.Contains(msg, "not found") {
		return fmt.Errorf(
			"edit_file: edit[%d] old_text not found in %q; re-read the file and check exact contents (incl. whitespace and line endings)",
			editIndex, path,
		)
	}
	return fmt.Errorf(
		"edit_file: edit[%d] %s in %q; provide more surrounding context to disambiguate, or set replace_all=true",
		editIndex, msg, path,
	)
}
