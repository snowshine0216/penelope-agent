package engine

import (
	"context"
	"errors"
	"fmt"
	"log"

	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// ErrMaxTurnsExceeded is returned when Run exhausts the MaxTurns budget.
var ErrMaxTurnsExceeded = errors.New("agent engine exceeded MaxTurns")

// AgentEngine drives the agent's main loop.
type AgentEngine struct {
	provider provider.LLMProvider
	registry tools.Registry

	WorkDir        string
	EnableThinking bool

	// MaxTurns caps the number of model turns per Run. 0 means use the
	// default (25).
	MaxTurns int

	// MaxParallelToolCalls caps concurrently executing parallel-safe tools.
	// 0 means use the default (4).
	MaxParallelToolCalls int

	contextManager *agentcontext.Manager
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

// SetContextManager attaches a context manager that provides the system prompt.
func (e *AgentEngine) SetContextManager(manager *agentcontext.Manager) {
	e.contextManager = manager
}

const defaultMaxTurns = 25

// Run starts the agent loop with the given user prompt and returns an error if the context is cancelled or MaxTurns is exceeded.
func (e *AgentEngine) Run(ctx context.Context, userPrompt string, report Reporter) error {
	log.Printf("[engine] starting, workdir=%s thinking=%v", e.WorkDir, e.EnableThinking)

	maxTurns := e.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}

	contextHistory := []schema.Message{
		{
			Role:    schema.RoleSystem,
			Content: e.systemPrompt(),
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

		if e.EnableThinking {
			log.Println("[engine] phase=think tools=disabled")

			// Think phase: pass nil tools so the model is forced to produce plain-text reasoning.
			thinkResp, err := e.provider.Generate(ctx, contextHistory, nil)
			if err != nil {
				return fmt.Errorf("think phase: %w", err)
			}

			// Append the model's thinking trace to context as an assistant message.
			if thinkResp.Content != "" {
				report.OnThinking(ctx)
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
			report.OnMessage(ctx, actionResp.Content)
		}

		if len(actionResp.ToolCalls) == 0 {
			log.Println("[engine] no tool calls, task complete")
			break
		}

		log.Printf("[engine] tool calls requested: %d", len(actionResp.ToolCalls))

		groups := PlanToolCallGroups(actionResp.ToolCalls, e.registry.ExecutionPolicyFor)
		for _, group := range groups {
			if err := ctx.Err(); err != nil {
				return err
			}

			for _, call := range group {
				report.OnToolCall(ctx, call.Name, string(call.Arguments))
			}

			results, err := executeToolCallGroup(ctx, e.registry, group, e.toolGroupLimit(group))
			if err != nil {
				return err
			}

			for i, result := range results {
				report.OnToolResult(ctx, group[i].Name, result.Output, result.IsError)
			}

			contextHistory = appendToolResultMessages(contextHistory, results)
		}
	}

	return nil
}

func (e *AgentEngine) systemPrompt() string {
	if e.contextManager == nil {
		return agentcontext.DefaultBaseInstructions
	}
	return e.contextManager.SystemPrompt()
}

func (e *AgentEngine) toolGroupLimit(group []schema.ToolCall) int {
	limit := e.MaxParallelToolCalls
	if limit <= 0 {
		limit = defaultParallelToolConcurrency
	}

	for _, call := range group {
		policy := e.registry.ExecutionPolicyFor(call)
		if !policy.ParallelSafe || policy.MaxConcurrency <= 0 {
			continue
		}
		if policy.MaxConcurrency < limit {
			limit = policy.MaxConcurrency
		}
	}

	return limit
}
