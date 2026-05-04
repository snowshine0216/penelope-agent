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
		Description: "在当前工作区执行任意的 bash 命令。支持链式命令(如 &&)。返回标准输出(stdout)和标准错误(stderr)。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "要执行的 bash 命令，例如: ls -la 或 go test ./...",
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
		return "", fmt.Errorf("参数解析失败: %w", err)
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

	// 如果命令执行超时，返回警告信息让模型知晓
	if timeoutCtx.Err() == context.DeadlineExceeded {
		return outputStr + "\n[警告: 命令执行超时，已被系统强制终止。如果是启动常驻服务，请尝试将其转入后台。]", nil
	}

	// 【驾驭底线 3】：错误原样回传 (Self-Correction 自愈机制)
	// 当 bash 报错时（err != nil），我们绝对不能返回 Go 的 error 阻断程序！
	// 我们必须把 err 和 outputStr 拼接成字符串返回，利用大模型的自纠错能力自己分析报错！
	if err != nil {
		return fmt.Sprintf("执行报错: %v\n输出:\n%s", err, outputStr), nil
	}

	// 如果没有终端输出（比如仅仅执行了 mkdir），给模型一个明确的执行成功的反馈
	if outputStr == "" {
		return "命令执行成功，无终端输出。", nil
	}

	return TruncateForLLM(outputStr, 8000), nil
}
