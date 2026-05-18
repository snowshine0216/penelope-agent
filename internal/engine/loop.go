package engine

import (
	"context"
	"errors"
	"fmt"
	"log"

	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
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
	session        *agentsession.Session
	trimmer        agentsession.Trimmer
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

// SetSession attaches the canonical history store. The engine appends
// the user prompt, every act-phase assistant message, and every tool
// result to the session; think-phase responses are intentionally not
// persisted (spec D17).
func (e *AgentEngine) SetSession(s *agentsession.Session) {
	e.session = s
}

// SetTrimmer attaches a strategy used to bound what the provider sees.
// If nil, the engine uses an identity trimmer that returns the full
// session history (matches today's unbounded behavior for tests that
// don't care).
func (e *AgentEngine) SetTrimmer(t agentsession.Trimmer) {
	e.trimmer = t
}

const defaultMaxTurns = 25

// Run starts the agent loop with the given user prompt and returns an error if the context is cancelled or MaxTurns is exceeded.
func (e *AgentEngine) Run(ctx context.Context, userPrompt string, report Reporter) error {
	log.Printf("[engine] starting, workdir=%s thinking=%v", e.WorkDir, e.EnableThinking)

	maxTurns := e.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}

	sess := e.session
	if sess == nil {
		sess = agentsession.NewInMemory()
	}
	if userPrompt != "" {
		if err := sess.Append(schema.Message{Role: schema.RoleUser, Content: userPrompt}); err != nil {
			return fmt.Errorf("append user prompt: %w", err)
		}
	}

	systemMsg := schema.Message{Role: schema.RoleSystem, Content: e.systemPrompt()}
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

		view := e.providerView(systemMsg, sess.Messages())

		if e.EnableThinking {
			log.Println("[engine] phase=think tools=disabled")
			thinkResp, err := e.provider.Generate(ctx, view, nil)
			if err != nil {
				return fmt.Errorf("think phase: %w", err)
			}
			if thinkResp.Content != "" {
				report.OnThinking(ctx)
				// Spec D17: think-phase responses are NOT persisted to
				// the session. Carry it forward only inside this turn
				// so the act-phase call can see the reasoning.
				view = append(view, *thinkResp)
			}
		}

		log.Println("[engine] phase=act tools=enabled")
		actionResp, err := e.provider.Generate(ctx, view, availableTools)
		if err != nil {
			return fmt.Errorf("act phase: %w", err)
		}

		if err := sess.Append(*actionResp); err != nil {
			return fmt.Errorf("persist act response: %w", err)
		}

		if actionResp.Content != "" {
			report.OnMessage(ctx, actionResp.Content)
		}

		if len(actionResp.ToolCalls) == 0 {
			log.Println("[engine] no tool calls, task complete")
			break
		}

		log.Printf("[engine] tool calls requested: %d", len(actionResp.ToolCalls))

		if hasLoadSkillCall(actionResp.ToolCalls) {
			results, err := e.executeLoadSkillBarrier(ctx, actionResp.ToolCalls, report)
			if err != nil {
				return err
			}
			for _, result := range results {
				if err := sess.Append(toolResultMessage(result)); err != nil {
					return fmt.Errorf("persist tool result: %w", err)
				}
			}
			systemMsg.Content = e.systemPrompt()
			continue
		}

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
				if err := sess.Append(toolResultMessage(result)); err != nil {
					return fmt.Errorf("persist tool result: %w", err)
				}
			}
		}
	}

	return nil
}

// providerView composes the slice handed to provider.Generate: the
// system message at index 0 followed by the trimmed session tail. If
// the trimmer returns an empty slice (token cap so low that even the
// latest user turn does not fit) we substitute the last user message
// so the model still receives a valid prompt.
func (e *AgentEngine) providerView(systemMsg schema.Message, tail []schema.Message) []schema.Message {
	var trimmed []schema.Message
	if e.trimmer != nil {
		trimmed = e.trimmer.Trim(tail)
	} else {
		trimmed = tail
	}
	if len(trimmed) == 0 && len(tail) > 0 {
		log.Printf("[engine] warning: trimmer returned empty slice; applying emergency floor")
		trimmed = []schema.Message{lastUserMessage(tail)}
	}
	view := make([]schema.Message, 0, 1+len(trimmed))
	view = append(view, systemMsg)
	view = append(view, trimmed...)
	return view
}

func lastUserMessage(tail []schema.Message) schema.Message {
	for i := len(tail) - 1; i >= 0; i-- {
		if tail[i].Role == schema.RoleUser {
			return tail[i]
		}
	}
	return tail[len(tail)-1]
}

func (e *AgentEngine) systemPrompt() string {
	if e.contextManager == nil {
		return agentcontext.DefaultBaseInstructions
	}
	return e.contextManager.SystemPrompt()
}

func hasLoadSkillCall(calls []schema.ToolCall) bool {
	for _, call := range calls {
		if call.Name == agentcontext.LoadSkillToolName {
			return true
		}
	}
	return false
}

func deferToolResult(call schema.ToolCall) schema.ToolResult {
	return schema.ToolResult{
		ToolCallID: call.ID,
		Output:     fmt.Sprintf("tool %q deferred until after skill loading; request it again if still needed", call.Name),
		IsError:    false,
	}
}

func (e *AgentEngine) executeLoadSkillBarrier(ctx context.Context, calls []schema.ToolCall, report Reporter) ([]schema.ToolResult, error) {
	results := make([]schema.ToolResult, len(calls))
	for i, call := range calls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if call.Name != agentcontext.LoadSkillToolName {
			results[i] = deferToolResult(call)
			continue
		}
		report.OnToolCall(ctx, call.Name, string(call.Arguments))
		result := executeToolCall(ctx, e.registry, call)
		report.OnToolResult(ctx, call.Name, result.Output, result.IsError)
		results[i] = result
	}
	return results, nil
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
