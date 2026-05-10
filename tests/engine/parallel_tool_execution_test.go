package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

type activityTracker struct {
	mu        sync.Mutex
	active    int
	maxActive int
	order     []string
}

func (t *activityTracker) enter(name string) func() {
	t.mu.Lock()
	t.active++
	if t.active > t.maxActive {
		t.maxActive = t.active
	}
	t.order = append(t.order, name)
	t.mu.Unlock()

	return func() {
		t.mu.Lock()
		t.active--
		t.mu.Unlock()
	}
}

func (t *activityTracker) snapshot() (int, []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.maxActive, append([]string(nil), t.order...)
}

type delayedPolicyTool struct {
	name    string
	output  string
	delay   time.Duration
	policy  tools.ExecutionPolicy
	tracker *activityTracker
}

func (d *delayedPolicyTool) Name() string { return d.name }

func (d *delayedPolicyTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        d.name,
		Description: "delayed test tool",
		InputSchema: map[string]interface{}{"type": "object"},
	}
}

func (d *delayedPolicyTool) ExecutionPolicy() tools.ExecutionPolicy {
	return d.policy
}

func (d *delayedPolicyTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	if d.tracker != nil {
		leave := d.tracker.enter(d.name)
		defer leave()
	}

	select {
	case <-time.After(d.delay):
		return d.output, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func toolObservationIDs(msgs []schema.Message) []string {
	ids := []string{}
	for _, msg := range msgs {
		if msg.Role == schema.RoleTool {
			ids = append(ids, msg.ToolCallID)
		}
	}
	return ids
}

func TestEngineRunsParallelSafeToolsConcurrently(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&delayedPolicyTool{
		name:   "parallel_a",
		output: "a-output",
		delay:  180 * time.Millisecond,
		policy: tools.ExecutionPolicy{ParallelSafe: true},
	})
	registry.Register(&delayedPolicyTool{
		name:   "parallel_b",
		output: "b-output",
		delay:  180 * time.Millisecond,
		policy: tools.ExecutionPolicy{ParallelSafe: true},
	})

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "a-call", Name: "parallel_a", Arguments: json.RawMessage(`{}`)},
					{ID: "b-call", Name: "parallel_b", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "done"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	start := time.Now()
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed >= 320*time.Millisecond {
		t.Fatalf("elapsed = %s, want parallel execution under 320ms", elapsed)
	}
}

func TestEnginePreservesObservationOrderWhenParallelToolsFinishOutOfOrder(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&delayedPolicyTool{
		name:   "slow",
		output: "slow-output",
		delay:  180 * time.Millisecond,
		policy: tools.ExecutionPolicy{ParallelSafe: true},
	})
	registry.Register(&delayedPolicyTool{
		name:   "fast",
		output: "fast-output",
		delay:  20 * time.Millisecond,
		policy: tools.ExecutionPolicy{ParallelSafe: true},
	})

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "slow-call", Name: "slow", Arguments: json.RawMessage(`{}`)},
					{ID: "fast-call", Name: "fast", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "done"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := toolObservationIDs(provider.receivedMsgs[1])
	want := []string{"slow-call", "fast-call"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool observation IDs = %#v, want %#v", got, want)
	}
}

func TestEngineRunsSerialToolsOneAtATimeInOrder(t *testing.T) {
	tracker := &activityTracker{}
	registry := tools.NewRegistry()
	registry.Register(&delayedPolicyTool{name: "first", output: "1", delay: 60 * time.Millisecond, tracker: tracker})
	registry.Register(&delayedPolicyTool{name: "second", output: "2", delay: 60 * time.Millisecond, tracker: tracker})

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "1", Name: "first", Arguments: json.RawMessage(`{}`)},
					{ID: "2", Name: "second", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "done"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	maxActive, order := tracker.snapshot()
	if maxActive != 1 {
		t.Fatalf("maxActive = %d, want 1 for serial tools", maxActive)
	}
	if !reflect.DeepEqual(order, []string{"first", "second"}) {
		t.Fatalf("order = %#v, want first then second", order)
	}
}

func TestEngineHonorsPerToolMaxConcurrency(t *testing.T) {
	tracker := &activityTracker{}
	registry := tools.NewRegistry()
	registry.Register(&delayedPolicyTool{
		name:    "limited_a",
		output:  "a",
		delay:   80 * time.Millisecond,
		policy:  tools.ExecutionPolicy{ParallelSafe: true, MaxConcurrency: 1},
		tracker: tracker,
	})
	registry.Register(&delayedPolicyTool{
		name:    "limited_b",
		output:  "b",
		delay:   80 * time.Millisecond,
		policy:  tools.ExecutionPolicy{ParallelSafe: true},
		tracker: tracker,
	})

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "a", Name: "limited_a", Arguments: json.RawMessage(`{}`)},
					{ID: "b", Name: "limited_b", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "done"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	maxActive, _ := tracker.snapshot()
	if maxActive != 1 {
		t.Fatalf("maxActive = %d, want 1 from per-tool cap", maxActive)
	}
}

type cancelOnExecuteTool struct {
	cancel context.CancelFunc
}

func (c *cancelOnExecuteTool) Name() string { return "cancel_now" }
func (c *cancelOnExecuteTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{Name: "cancel_now", Description: "cancels", InputSchema: map[string]interface{}{"type": "object"}}
}
func (c *cancelOnExecuteTool) ExecutionPolicy() tools.ExecutionPolicy {
	return tools.ExecutionPolicy{ParallelSafe: true}
}
func (c *cancelOnExecuteTool) Execute(context.Context, json.RawMessage) (string, error) {
	c.cancel()
	return "cancelled", nil
}

func TestEngineCancellationDuringParallelGroupDoesNotCallProviderAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	registry := tools.NewRegistry()
	registry.Register(&cancelOnExecuteTool{cancel: cancel})
	registry.Register(&delayedPolicyTool{
		name:   "waiter",
		output: "waited",
		delay:  200 * time.Millisecond,
		policy: tools.ExecutionPolicy{ParallelSafe: true},
	})

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "cancel", Name: "cancel_now", Arguments: json.RawMessage(`{}`)},
					{ID: "wait", Name: "waiter", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "should not be requested"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	err := eng.Run(ctx, "go")

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider.calls = %d, want 1", provider.calls)
	}
}
