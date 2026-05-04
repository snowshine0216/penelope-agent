package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// fakeTool is a minimal tools.BaseTool used to exercise the registry without
// touching the filesystem or shell.
type fakeTool struct {
	name        string
	description string
	exec        func(ctx context.Context, args json.RawMessage) (string, error)
}

func (f *fakeTool) Name() string { return f.name }

func (f *fakeTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        f.name,
		Description: f.description,
		InputSchema: map[string]interface{}{"type": "object"},
	}
}

func (f *fakeTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return f.exec(ctx, args)
}

func newFake(name, desc string, exec func(ctx context.Context, args json.RawMessage) (string, error)) *fakeTool {
	return &fakeTool{name: name, description: desc, exec: exec}
}

func okExec(out string) func(context.Context, json.RawMessage) (string, error) {
	return func(context.Context, json.RawMessage) (string, error) { return out, nil }
}

func TestRegistryRegisterMakesToolAvailable(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(newFake("noop", "does nothing", okExec("ok")))

	defs := r.GetAvailableTools()
	if len(defs) != 1 {
		t.Fatalf("GetAvailableTools length = %d, want 1", len(defs))
	}
	if defs[0].Name != "noop" {
		t.Fatalf("definition name = %q, want noop", defs[0].Name)
	}
}

func TestRegistryRegisterTwiceOverwrites(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(newFake("noop", "v1", okExec("first")))
	r.Register(newFake("noop", "v2", okExec("second")))

	result := r.Execute(context.Background(), schema.ToolCall{ID: "x", Name: "noop"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
	if result.Output != "second" {
		t.Fatalf("output = %q, want second (overwritten)", result.Output)
	}
}

func TestRegistryExecuteRoutesByName(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(newFake("alpha", "", okExec("from-alpha")))
	r.Register(newFake("beta", "", okExec("from-beta")))

	for _, tc := range []struct {
		name string
		want string
	}{
		{"alpha", "from-alpha"},
		{"beta", "from-beta"},
	} {
		got := r.Execute(context.Background(), schema.ToolCall{ID: "1", Name: tc.name})
		if got.Output != tc.want {
			t.Errorf("route %q -> %q, want %q", tc.name, got.Output, tc.want)
		}
	}
}

func TestRegistryExecuteUnknownToolReturnsError(t *testing.T) {
	r := tools.NewRegistry()

	result := r.Execute(context.Background(), schema.ToolCall{ID: "x", Name: "ghost"})
	if !result.IsError {
		t.Fatalf("expected IsError=true for unknown tool")
	}
	if result.ToolCallID != "x" {
		t.Fatalf("ToolCallID lost: got %q", result.ToolCallID)
	}
}

func TestRegistryExecutePropagatesToolError(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(newFake("boom", "", func(context.Context, json.RawMessage) (string, error) {
		return "", errors.New("kaboom")
	}))

	result := r.Execute(context.Background(), schema.ToolCall{ID: "y", Name: "boom"})
	if !result.IsError {
		t.Fatalf("expected IsError=true when tool errors")
	}
	if result.Output == "" {
		t.Fatalf("expected error message in Output")
	}
}

func TestRegistryExecutePassesArgumentsThrough(t *testing.T) {
	var seen json.RawMessage
	r := tools.NewRegistry()
	r.Register(newFake("capture", "", func(_ context.Context, args json.RawMessage) (string, error) {
		seen = args
		return "ok", nil
	}))

	args := json.RawMessage(`{"key":"value"}`)
	r.Execute(context.Background(), schema.ToolCall{ID: "z", Name: "capture", Arguments: args})

	if string(seen) != `{"key":"value"}` {
		t.Fatalf("arguments lost: got %s", seen)
	}
}
