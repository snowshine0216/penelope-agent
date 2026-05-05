package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestEngineDefaultMaxTurnsWhenZero(t *testing.T) {
	// MaxTurns == 0 must default to 25, not immediately return ErrMaxTurnsExceeded.
	p := &fakeProvider{
		responses: []schema.Message{
			{Role: schema.RoleAssistant, Content: "done"},
		},
	}
	registry := tools.NewRegistry()
	eng := engine.NewAgentEngine(p, registry, t.TempDir(), false)
	// MaxTurns left at 0 (struct zero value).

	if err := eng.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("expected clean exit with default MaxTurns, got: %v", err)
	}
}

func TestEngineThinkingPhaseEmptyContentNotAppendedToHistory(t *testing.T) {
	// Think phase returns empty content — the empty message must NOT be appended
	// to context history. The action-phase call (index 1) should not see an
	// empty assistant message from the think phase.
	p := &fakeProvider{
		responses: []schema.Message{
			{Role: schema.RoleAssistant, Content: ""},     // think phase: empty
			{Role: schema.RoleAssistant, Content: "done"}, // action phase
		},
	}
	registry := tools.NewRegistry()
	eng := engine.NewAgentEngine(p, registry, t.TempDir(), true)

	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(p.receivedMsgs) < 2 {
		t.Fatalf("expected 2 generate calls (think + act), got %d", len(p.receivedMsgs))
	}
	// The context passed to the action phase should not include the empty think message.
	for _, m := range p.receivedMsgs[1] {
		if m.Role == schema.RoleAssistant && m.Content == "" && len(m.ToolCalls) == 0 {
			t.Fatal("empty think-phase message was incorrectly appended to context history")
		}
	}
}

func TestEngineThinkPhaseProviderErrorPropagates(t *testing.T) {
	// When the think phase fails, Run must return a wrapped error.
	p := &fakeProvider{err: errors.New("think exploded")}
	registry := tools.NewRegistry()
	eng := engine.NewAgentEngine(p, registry, t.TempDir(), true)

	err := eng.Run(context.Background(), "go")
	if err == nil {
		t.Fatal("expected error from think phase failure, got nil")
	}
	if !strings.Contains(err.Error(), "think phase") {
		t.Fatalf("expected 'think phase' in error, got: %v", err)
	}
}
