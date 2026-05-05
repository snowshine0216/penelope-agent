package engine

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/snowshine0216/penelope-agent/internal/provider"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// ErrMaxTurnsExceeded is returned when Run exhausts the MaxTurns budget.
var ErrMaxTurnsExceeded = errors.New("agent engine exceeded MaxTurns")

// AgentEngine 是微型 OS 的核心驱动
type AgentEngine struct {
	provider provider.LLMProvider
	registry tools.Registry

	// WorkDir (工作区): 借鉴 OpenClaw 的理念，Agent 必须有一个明确的物理边界
	WorkDir        string
	EnableThinking bool // 【新增】慢思考模式开关

	// MaxTurns caps the number of model turns per Run. 0 means use the
	// default (25).
	MaxTurns int
}

// NewAgentEngine constructs an AgentEngine with the given provider, registry, and work directory.
func NewAgentEngine(p provider.LLMProvider, r tools.Registry, workDir string, enableThinking bool) *AgentEngine {
	return &AgentEngine{
		provider:       p,
		registry:       r,
		WorkDir:        workDir,
		EnableThinking: enableThinking,
	}
}

const defaultMaxTurns = 25

// Run starts the agent loop with the given user prompt and returns an error if the context is cancelled or MaxTurns is exceeded.
func (e *AgentEngine) Run(ctx context.Context, userPrompt string) error {
	log.Printf("[engine] starting, workdir=%s thinking=%v", e.WorkDir, e.EnableThinking)

	maxTurns := e.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}

	contextHistory := []schema.Message{
		{
			Role:    schema.RoleSystem,
			Content: "You are penelope-agent, an expert coding assistant. You have full access to tools in the workspace.",
		},
		{
			Role:    schema.RoleUser,
			Content: userPrompt,
		},
	}

	availableTools := e.registry.GetAvailableTools()
	turnCount := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		turnCount++
		if turnCount > maxTurns {
			return ErrMaxTurnsExceeded
		}
		log.Printf("[engine] turn %d", turnCount)

		// ====================================================================
		// Phase 1: 慢思考阶段 (Thinking) - 剥夺工具，强制规划
		// ====================================================================
		if e.EnableThinking {
			log.Println("[engine] phase=think tools=disabled")

			// Think phase: pass nil tools so the model is forced to produce plain-text reasoning.
			thinkResp, err := e.provider.Generate(ctx, contextHistory, nil)
			if err != nil {
				return fmt.Errorf("think phase: %w", err)
			}

			// Append the model's thinking trace to context as an assistant message.
			if thinkResp.Content != "" {
				fmt.Printf("[think] %s\n", thinkResp.Content)
				contextHistory = append(contextHistory, *thinkResp)
			}
		}

		log.Println("[engine] phase=act tools=enabled")

		// Context history now includes the thinking trace from the previous phase (if any).
		// The model continues its reasoning and issues precise tool calls.
		actionResp, err := e.provider.Generate(ctx, contextHistory, availableTools)
		if err != nil {
			return fmt.Errorf("act phase: %w", err)
		}

		contextHistory = append(contextHistory, *actionResp)

		if actionResp.Content != "" {
			fmt.Printf("[reply] %s\n", actionResp.Content)
		}

		if len(actionResp.ToolCalls) == 0 {
			log.Println("[engine] no tool calls, task complete")
			break
		}

		log.Printf("[engine] tool calls requested: %d", len(actionResp.ToolCalls))

		for _, toolCall := range actionResp.ToolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}
			log.Printf("[engine] executing tool=%s args=%s", toolCall.Name, string(toolCall.Arguments))

			result := e.registry.Execute(ctx, toolCall)

			if result.IsError {
				log.Printf("[engine] tool error: %s", result.Output)
			} else {
				log.Printf("[engine] tool ok: %d bytes", len(result.Output))
			}

			// Append the tool result as an observation for the next turn.
			observationMsg := schema.Message{
				Role:       schema.RoleTool,
				Content:    result.Output,
				ToolCallID: toolCall.ID,
				IsError:    result.IsError,
			}
			contextHistory = append(contextHistory, observationMsg)
		}
	}

	return nil
}
