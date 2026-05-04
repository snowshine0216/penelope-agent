// internal/tools/bash.go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"time"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// BashTool executes shell commands in the workspace.
type BashTool struct {
	workDir string // 工作区约束
}

func NewBashTool(workDir string) *BashTool {
	return &BashTool{workDir: workDir}
}

func (t *BashTool) Name() string {
	return "bash"
}

func (t *BashTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "Execute a bash command in the workspace. Supports chained commands (&&). Returns combined stdout and stderr.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "The bash command to run, e.g. ls -la or go test ./...",
				},
				"timeout_s": map[string]interface{}{
					"type":        "integer",
					"description": "Optional command timeout in seconds (default 30, max 600)",
				},
			},
			"required": []string{"command"},
		},
	}
}

type bashArgs struct {
	Command  string `json:"command"`
	TimeoutS int    `json:"timeout_s,omitempty"`
}

func (t *BashTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input bashArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}

	const defaultTimeout = 30 * time.Second
	const maxTimeout = 600 * time.Second

	timeout := defaultTimeout
	if input.TimeoutS > 0 {
		timeout = time.Duration(input.TimeoutS) * time.Second
		if timeout > maxTimeout {
			timeout = maxTimeout
		}
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 在 macOS/Linux 下，我们通过将指令包裹在 `bash -c` 中执行，以支持环境变量、管道和逻辑与(&&)等复杂 Shell 语法。
	cmd := exec.CommandContext(timeoutCtx, "bash", "-c", input.Command)

	// 【驾驭底线 2】：绑定执行的工作区目录
	// 确保命令默认在用户指定的 WorkDir 下执行，而不是引擎启动时的绝对路径。
	cmd.Dir = t.workDir

	log.Printf("[bash] dir=%s cmd=%s", t.workDir, input.Command)
	out, err := cmd.CombinedOutput()
	outputStr := string(out)

	// If the command timed out, return a warning so the model knows.
	if timeoutCtx.Err() == context.DeadlineExceeded {
		return outputStr + "\n[warning: command timed out and was killed]", nil
	}

	// Self-correction mechanism: never return a Go error for bash failures.
	// Instead, pass the combined error + output back to the model so it can self-correct.
	if err != nil {
		return fmt.Sprintf("execution error: %v\noutput:\n%s", err, outputStr), nil
	}

	// If there is no output (e.g. mkdir), give the model an explicit success signal.
	if outputStr == "" {
		return "command finished with no output.", nil
	}

	return TruncateForLLM(outputStr, 8000), nil
}
