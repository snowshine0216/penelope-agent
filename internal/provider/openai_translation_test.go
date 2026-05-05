package provider

import (
	"encoding/json"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

func TestTranslateMessagesToOpenAISystemMessage(t *testing.T) {
	msgs := []schema.Message{{Role: schema.RoleSystem, Content: "sys"}}
	out, err := translateMessagesToOpenAI(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
}

func TestTranslateMessagesToOpenAIToolMessage(t *testing.T) {
	msgs := []schema.Message{
		{Role: schema.RoleTool, Content: "result", ToolCallID: "id1"},
	}
	out, err := translateMessagesToOpenAI(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
}

func TestTranslateMessagesToOpenAIUserMessage(t *testing.T) {
	msgs := []schema.Message{{Role: schema.RoleUser, Content: "hello"}}
	out, err := translateMessagesToOpenAI(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
}

func TestTranslateMessagesToOpenAIAssistantWithText(t *testing.T) {
	msgs := []schema.Message{{Role: schema.RoleAssistant, Content: "done"}}
	out, err := translateMessagesToOpenAI(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
}

func TestTranslateMessagesToOpenAIAssistantWithToolCalls(t *testing.T) {
	msgs := []schema.Message{
		{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{
				{ID: "c1", Name: "bash", Arguments: json.RawMessage(`{"command":"ls"}`)},
			},
		},
	}
	out, err := translateMessagesToOpenAI(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 assistant message, got %d", len(out))
	}
}

func TestTranslateToolsToOpenAIMapPath(t *testing.T) {
	defs := []schema.ToolDefinition{
		{
			Name:        "bash",
			Description: "run bash",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{"type": "string"},
				},
			},
		},
	}
	out, err := translateToolsToOpenAI(defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(out))
	}
}

func TestTranslateToolsToOpenAIJSONRoundTripPath(t *testing.T) {
	// InputSchema is not a map — triggers the JSON marshal/unmarshal slow path.
	type customSchema struct {
		Type string `json:"type"`
	}
	defs := []schema.ToolDefinition{
		{Name: "noop", Description: "noop", InputSchema: customSchema{Type: "object"}},
	}
	out, err := translateToolsToOpenAI(defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(out))
	}
}

func TestTranslateToolsToOpenAIEmptyDefs(t *testing.T) {
	out, err := translateToolsToOpenAI(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(out))
	}
}
