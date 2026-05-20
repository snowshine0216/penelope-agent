package engine

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/snowshine0216/penelope-agent/internal/compact"
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
	trimmer        agentsession.Trimmer // removed in Task 17

	// compactor, calibrator, and compactCfg are wired by SetCompactor /
	// SetCalibrator / SetCompactConfig. When nil/zero the engine falls
	// back to the old trimmer-based identity path.
	compactor    *compact.Compactor
	compactCfg   compact.Config
	calibrator   *compact.Calibrator
	modelID      string
	outputCap    int
	safetyFactor float64
	overrides    map[string]int

	// lastUsage carries the previous turn's provider-reported token
	// usage forward into the next turn's budget. Zero on first turn.
	lastUsage provider.Usage
}

// NewAgentEngine constructs an AgentEngine with the given provider, registry, and work directory.
func NewAgentEngine(p provider.LLMProvider, r tools.Registry, workDir string, enableThinking bool) *AgentEngine {
	return &AgentEngine{
		provider:       p,
		registry:       r,
		WorkDir:        workDir,
		EnableThinking: enableThinking,
		safetyFactor:   0.75,
		outputCap:      4096,
	}
}

// SetContextManager attaches a context manager that provides the system prompt.
func (e *AgentEngine) SetContextManager(manager *agentcontext.Manager) {
	e.contextManager = manager
}

// SetSession attaches the canonical history store.
func (e *AgentEngine) SetSession(s *agentsession.Session) { e.session = s }

// SetTrimmer attaches a strategy used to bound what the provider sees.
// Kept for backward compatibility; the compactor path supersedes it.
func (e *AgentEngine) SetTrimmer(t agentsession.Trimmer) { e.trimmer = t }

// SetCompactor wires the compactor that drives Layer A + B compaction.
func (e *AgentEngine) SetCompactor(c *compact.Compactor) { e.compactor = c }

// SetCalibrator wires the EWMA calibrator for token-count correction.
func (e *AgentEngine) SetCalibrator(c *compact.Calibrator) { e.calibrator = c }

// SetCompactConfig sets the compaction configuration (MaxToolBytes etc.).
func (e *AgentEngine) SetCompactConfig(c compact.Config) { e.compactCfg = c }

// SetModelID sets the model identifier used for budget calculation.
func (e *AgentEngine) SetModelID(id string) { e.modelID = id }

// SetOutputCap sets the output-token cap used for budget calculation.
func (e *AgentEngine) SetOutputCap(n int) { e.outputCap = n }

// SetSafetyFactor sets the safety factor used for budget calculation.
func (e *AgentEngine) SetSafetyFactor(f float64) { e.safetyFactor = f }

// SetModelLimitOverrides sets optional per-model context-limit overrides.
func (e *AgentEngine) SetModelLimitOverrides(m map[string]int) { e.overrides = m }

// LastUsageForTest exposes lastUsage for integration tests.
func (e *AgentEngine) LastUsageForTest() provider.Usage { return e.lastUsage }

const defaultMaxTurns = 25

// Run starts the agent loop with the given user prompt.
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
	toolSpillThisTurn := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		turnCount++
		if turnCount > maxTurns {
			return ErrMaxTurnsExceeded
		}
		log.Printf("[engine] turn %d", turnCount)

		view, stats := e.buildView(systemMsg, sess.Messages(), turnCount)
		stats.ToolOutputsSpilled = toolSpillThisTurn
		toolSpillThisTurn = 0

		if e.EnableThinking {
			log.Println("[engine] phase=think tools=disabled")
			thinkResp, err := e.provider.Generate(ctx, view, nil)
			if err != nil {
				return fmt.Errorf("think phase: %w", err)
			}
			if thinkResp != nil && thinkResp.Message != nil && thinkResp.Message.Content != "" {
				report.OnThinking(ctx)
				// Spec D17: think-phase responses are NOT persisted to the
				// session. Carry forward only inside this turn.
				view = append(view, *thinkResp.Message)
			}
		}

		log.Println("[engine] phase=act tools=enabled")
		actionResp, err := e.provider.Generate(ctx, view, availableTools)
		if err != nil {
			return fmt.Errorf("act phase: %w", err)
		}
		actionMsg := actionResp.Message
		e.lastUsage = actionResp.Usage

		if e.calibrator != nil && actionResp.Usage.InputTokens > 0 {
			e.calibrator.Observe(stats.AfterLayerB, actionResp.Usage.InputTokens)
		}

		if shouldEmit(stats) {
			report.OnCompact(ctx, stats)
		}

		if err := sess.Append(*actionMsg); err != nil {
			return fmt.Errorf("persist act response: %w", err)
		}

		if actionMsg.Content != "" {
			report.OnMessage(ctx, actionMsg.Content)
		}

		if len(actionMsg.ToolCalls) == 0 {
			log.Println("[engine] no tool calls, task complete")
			break
		}

		log.Printf("[engine] tool calls requested: %d", len(actionMsg.ToolCalls))

		if hasLoadSkillCall(actionMsg.ToolCalls) {
			results, err := e.executeLoadSkillBarrier(ctx, actionMsg.ToolCalls, report)
			if err != nil {
				return err
			}
			for _, result := range results {
				spilled, err := e.applyToolBoundaryCap(sess, &result)
				if err != nil {
					return err
				}
				if spilled {
					toolSpillThisTurn++
				}
				if err := sess.Append(toolResultMessage(result)); err != nil {
					return fmt.Errorf("persist tool result: %w", err)
				}
			}
			systemMsg.Content = e.systemPrompt()
			continue
		}

		groups := PlanToolCallGroups(actionMsg.ToolCalls, e.registry.ExecutionPolicyFor)
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
				spilled, err := e.applyToolBoundaryCap(sess, &result)
				if err != nil {
					return err
				}
				if spilled {
					toolSpillThisTurn++
				}
				report.OnToolResult(ctx, group[i].Name, result.Output, result.IsError)
				if err := sess.Append(toolResultMessage(result)); err != nil {
					return fmt.Errorf("persist tool result: %w", err)
				}
			}
		}
	}

	return nil
}

// buildView composes the provider view for one turn. When a compactor is
// configured it drives Layer A + B compaction and returns stats. When only
// a trimmer is set (legacy path), it trims instead. When neither is set,
// identity view is returned.
func (e *AgentEngine) buildView(systemMsg schema.Message, tail []schema.Message, turn int) ([]schema.Message, compact.CompactStats) {
	if e.compactor != nil {
		return e.buildCompactedView(systemMsg, tail, turn)
	}
	return e.buildTrimmedView(systemMsg, tail, turn)
}

// buildCompactedView uses the compactor pipeline.
func (e *AgentEngine) buildCompactedView(systemMsg schema.Message, tail []schema.Message, turn int) ([]schema.Message, compact.CompactStats) {
	budget := compact.Budget(compact.BudgetInput{
		Model:        e.modelID,
		LastUsage:    e.lastUsage,
		OutputCap:    e.outputCap,
		SafetyFactor: e.safetyFactor,
		Overrides:    e.overrides,
	})
	compacted, stats := e.compactor.View(tail, budget, turn, e.calibrator)
	stats.Budget = budget
	if len(compacted) == 0 && len(tail) > 0 {
		log.Printf("[engine] warning: compactor returned empty slice; emergency floor")
		compacted = []schema.Message{lastUserMessage(tail)}
	}
	view := make([]schema.Message, 0, 1+len(compacted))
	view = append(view, systemMsg)
	view = append(view, compacted...)
	return view, stats
}

// buildTrimmedView uses the legacy trimmer path (backward compatibility for
// tests that use SetTrimmer but not SetCompactor).
func (e *AgentEngine) buildTrimmedView(systemMsg schema.Message, tail []schema.Message, turn int) ([]schema.Message, compact.CompactStats) {
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
	stats := compact.NewCompactStats(turn, compact.EstimateTokens(tail))
	stats.AfterLayerA = stats.Before
	stats.AfterLayerB = stats.Before
	return view, stats
}

// shouldEmit returns true if the OnCompact callback should fire this turn.
// Emission rule per spec §4: Layer B engaged, any tool spill, or a
// non-trivial saving (>= 5% of Before tokens: Saved*20 > Before).
func shouldEmit(s compact.CompactStats) bool {
	if s.LayerBEngaged {
		return true
	}
	if s.ToolOutputsSpilled > 0 {
		return true
	}
	if s.Before > 0 && s.Saved*20 > s.Before {
		return true
	}
	return false
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
