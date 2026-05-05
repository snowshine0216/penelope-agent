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

// fakeProvider returns canned responses in order. Each Generate call pops
// one response off the queue. It records the messages it was given so tests
// can assert on context propagation and tool-availability behavior.
type fakeProvider struct {
	responses     []schema.Message
	calls         int
	receivedMsgs  [][]schema.Message
	receivedTools [][]schema.ToolDefinition
	err           error
}

func (f *fakeProvider) Generate(_ context.Context, msgs []schema.Message, t []schema.ToolDefinition) (*schema.Message, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.calls >= len(f.responses) {
		return nil, errors.New("fakeProvider: ran out of canned responses")
	}

	msgsCopy := append([]schema.Message(nil), msgs...)
	toolsCopy := append([]schema.ToolDefinition(nil), t...)
	f.receivedMsgs = append(f.receivedMsgs, msgsCopy)
	f.receivedTools = append(f.receivedTools, toolsCopy)

	resp := f.responses[f.calls]
	f.calls++
	return &resp, nil
}

// recordingTool captures calls so tests can verify the engine actually
// dispatched tool requests through the registry.
type recordingTool struct {
	name      string
	output    string
	err       error
	callCount int
	lastArgs  json.RawMessage
}

func (r *recordingTool) Name() string { return r.name }

func (r *recordingTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        r.name,
		Description: "test tool",
		InputSchema: map[string]interface{}{"type": "object"},
	}
}

func (r *recordingTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	r.callCount++
	r.lastArgs = args
	if r.err != nil {
		return "", r.err
	}
	return r.output, nil
}

func TestEngineExitsWhenModelReturnsNoToolCalls(t *testing.T) {
	provider := &fakeProvider{
		responses: []schema.Message{
			{Role: schema.RoleAssistant, Content: "all done"},
		},
	}
	registry := tools.NewRegistry()
	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)

	if err := eng.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider.calls = %d, want 1 (single turn, no tools)", provider.calls)
	}
}

func TestEngineExecutesToolThenContinues(t *testing.T) {
	tool := &recordingTool{name: "noop", output: "tool said hi"}
	registry := tools.NewRegistry()
	registry.Register(tool)

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "call_1", Name: "noop", Arguments: json.RawMessage(`{"x":1}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "finished"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if tool.callCount != 1 {
		t.Fatalf("tool.callCount = %d, want 1", tool.callCount)
	}
	if string(tool.lastArgs) != `{"x":1}` {
		t.Fatalf("tool.lastArgs = %s, want {\"x\":1}", tool.lastArgs)
	}
	if provider.calls != 2 {
		t.Fatalf("provider.calls = %d, want 2 (tool turn + final turn)", provider.calls)
	}
}

func TestEnginePropagatesToolResultIntoNextContext(t *testing.T) {
	tool := &recordingTool{name: "echo", output: "tool-output-marker"}
	registry := tools.NewRegistry()
	registry.Register(tool)

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "abc", Name: "echo", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "done"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The second Generate call should receive a context that includes the
	// tool's output as a message.
	if len(provider.receivedMsgs) < 2 {
		t.Fatalf("expected provider to be called twice, got %d", len(provider.receivedMsgs))
	}

	secondCallMsgs := provider.receivedMsgs[1]
	found := false
	for _, m := range secondCallMsgs {
		if m.Role == schema.RoleTool && m.ToolCallID == "abc" && m.Content == "tool-output-marker" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tool result not propagated into next turn's context: %+v", secondCallMsgs)
	}
}

func TestEngineThinkingModeCallsProviderTwicePerTurn(t *testing.T) {
	registry := tools.NewRegistry()

	provider := &fakeProvider{
		responses: []schema.Message{
			{Role: schema.RoleAssistant, Content: "thinking out loud"}, // think phase
			{Role: schema.RoleAssistant, Content: "final answer"},      // action phase
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), true)
	if err := eng.Run(context.Background(), "what?"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider.calls = %d, want 2 (think + act in one turn)", provider.calls)
	}
}

func TestEngineThinkingPhasePassesNoTools(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&recordingTool{name: "noop"})

	provider := &fakeProvider{
		responses: []schema.Message{
			{Role: schema.RoleAssistant, Content: "thought"},
			{Role: schema.RoleAssistant, Content: "answered"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), true)
	if err := eng.Run(context.Background(), "do it"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(provider.receivedTools) < 2 {
		t.Fatalf("expected 2 generate calls, got %d", len(provider.receivedTools))
	}
	if provider.receivedTools[0] != nil && len(provider.receivedTools[0]) != 0 {
		t.Fatalf("think phase should receive nil/empty tools, got %d tools", len(provider.receivedTools[0]))
	}
	if len(provider.receivedTools[1]) == 0 {
		t.Fatalf("action phase should receive the registered tools, got 0")
	}
}

func TestEngineSeedsContextWithSystemAndUserMessages(t *testing.T) {
	registry := tools.NewRegistry()
	provider := &fakeProvider{
		responses: []schema.Message{
			{Role: schema.RoleAssistant, Content: "ok"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	if err := eng.Run(context.Background(), "user-prompt-123"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	first := provider.receivedMsgs[0]
	if len(first) < 2 {
		t.Fatalf("expected at least 2 seeded messages (system + user), got %d", len(first))
	}
	if first[0].Role != schema.RoleSystem {
		t.Fatalf("first message role = %q, want system", first[0].Role)
	}
	if first[1].Role != schema.RoleUser || first[1].Content != "user-prompt-123" {
		t.Fatalf("second message = %+v, want user with prompt", first[1])
	}
}

func TestEngineSurfacesProviderError(t *testing.T) {
	provider := &fakeProvider{err: errors.New("upstream is down")}
	registry := tools.NewRegistry()

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	err := eng.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error to bubble up from provider")
	}
}

func TestEnginePropagatesToolErrorFlagToNextContext(t *testing.T) {
	tool := &recordingTool{name: "boom", err: errors.New("kaboom")}
	registry := tools.NewRegistry()
	registry.Register(tool)

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "x", Name: "boom", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "saw the error"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(provider.receivedMsgs) < 2 {
		t.Fatalf("expected provider called twice, got %d", len(provider.receivedMsgs))
	}
	second := provider.receivedMsgs[1]
	var found *schema.Message
	for i := range second {
		if second[i].Role == schema.RoleTool && second[i].ToolCallID == "x" {
			found = &second[i]
			break
		}
	}
	if found == nil {
		t.Fatal("tool result message not found in second turn context")
	}
	if !found.IsError {
		t.Fatalf("expected IsError=true on failed tool result, got false")
	}
}

// loopingProvider returns the same canned response indefinitely.
type loopingProvider struct {
	response schema.Message
	calls    int
}

func (l *loopingProvider) Generate(_ context.Context, _ []schema.Message, _ []schema.ToolDefinition) (*schema.Message, error) {
	l.calls++
	r := l.response
	return &r, nil
}

func TestEngineStopsAtMaxTurns(t *testing.T) {
	tool := &recordingTool{name: "noop", output: "ok"}
	registry := tools.NewRegistry()
	registry.Register(tool)

	provider := &loopingProvider{
		response: schema.Message{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{
				{ID: "x", Name: "noop", Arguments: json.RawMessage(`{}`)},
			},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	eng.MaxTurns = 3

	err := eng.Run(context.Background(), "loop forever")
	if err == nil {
		t.Fatal("expected MaxTurns error, got nil")
	}
	if !errors.Is(err, engine.ErrMaxTurnsExceeded) {
		t.Fatalf("expected ErrMaxTurnsExceeded, got %v", err)
	}
	if provider.calls > 4 {
		t.Fatalf("expected ~3 calls, got %d", provider.calls)
	}
}

func TestEngineHonorsContextCancellation(t *testing.T) {
	tool := &recordingTool{name: "noop", output: "ok"}
	registry := tools.NewRegistry()
	registry.Register(tool)

	provider := &loopingProvider{
		response: schema.Message{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{
				{ID: "x", Name: "noop", Arguments: json.RawMessage(`{}`)},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before Run starts

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	err := eng.Run(ctx, "go")
	if err == nil {
		t.Fatal("expected ctx error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected ctx.Canceled, got %v", err)
	}
}

func TestEngineExecutesAllParallelToolCalls(t *testing.T) {
	a := &recordingTool{name: "a", output: "from-a"}
	b := &recordingTool{name: "b", output: "from-b"}
	registry := tools.NewRegistry()
	registry.Register(a)
	registry.Register(b)

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "1", Name: "a", Arguments: json.RawMessage(`{}`)},
					{ID: "2", Name: "b", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "done"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if a.callCount != 1 || b.callCount != 1 {
		t.Fatalf("expected each tool called once, got a=%d b=%d", a.callCount, b.callCount)
	}
}
