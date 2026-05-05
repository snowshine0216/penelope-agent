package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// cancellingTool cancels the supplied context the moment it is executed so that
// the engine's per-tool ctx.Err() check fires on the next iteration.
type cancellingTool struct {
	cancel context.CancelFunc
	calls  int
}

func (c *cancellingTool) Name() string { return "canceller" }
func (c *cancellingTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{Name: "canceller", Description: "cancels ctx", InputSchema: map[string]interface{}{"type": "object"}}
}
func (c *cancellingTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	c.calls++
	c.cancel() // cancel the context after first tool execution
	return "done", nil
}

// TestEngineHonorsContextCancellationDuringToolLoop verifies that the engine
// checks ctx.Err() between tool calls and aborts when the context is cancelled.
func TestEngineHonorsContextCancellationDuringToolLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ct := &cancellingTool{cancel: cancel}

	registry := tools.NewRegistry()
	registry.Register(ct)

	// Provide two tool calls in one response so the second iteration of the
	// per-tool loop will see the cancelled context.
	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "1", Name: "canceller", Arguments: json.RawMessage(`{}`)},
					{ID: "2", Name: "canceller", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "should not reach here"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	err := eng.Run(ctx, "go")
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	// Only the first tool should have been called; the second iteration aborts.
	if ct.calls != 1 {
		t.Fatalf("expected 1 tool call before cancellation, got %d", ct.calls)
	}
}

// TestEngineActPhaseProviderErrorPropagates covers the act-phase error return
// when thinking mode is disabled (separate from the think-phase test in loop_gaps_test.go).
func TestEngineActPhaseProviderErrorPropagates(t *testing.T) {
	p := &fakeProvider{err: errors.New("act exploded")}
	registry := tools.NewRegistry()
	eng := engine.NewAgentEngine(p, registry, t.TempDir(), false)

	err := eng.Run(context.Background(), "go")
	if err == nil {
		t.Fatal("expected error from act phase failure, got nil")
	}
	if !errors.Is(err, p.err) {
		t.Fatalf("expected error wrapping %v, got: %v", p.err, err)
	}
}
