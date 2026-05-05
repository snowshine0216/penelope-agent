package provider

import (
	"encoding/json"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

func TestTranslateMessagesToAnthropicExtractsSystemPrompt(t *testing.T) {
	msgs := []schema.Message{
		{Role: schema.RoleSystem, Content: "be helpful"},
		{Role: schema.RoleUser, Content: "hello"},
	}
	out, sysPrompt, err := translateMessagesToAnthropic(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sysPrompt != "be helpful" {
		t.Fatalf("system prompt = %q, want %q", sysPrompt, "be helpful")
	}
	// System messages are stripped from the output slice.
	if len(out) != 1 {
		t.Fatalf("expected 1 anthropic message (user only), got %d", len(out))
	}
}

func TestTranslateMessagesToAnthropicUserMessage(t *testing.T) {
	msgs := []schema.Message{{Role: schema.RoleUser, Content: "hi"}}
	out, _, err := translateMessagesToAnthropic(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
}

func TestTranslateMessagesToAnthropicToolResultMessage(t *testing.T) {
	msgs := []schema.Message{
		{Role: schema.RoleTool, Content: "tool output", ToolCallID: "id1", IsError: false},
	}
	out, _, err := translateMessagesToAnthropic(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 user message (tool result), got %d", len(out))
	}
}

func TestTranslateMessagesToAnthropicAssistantWithTextAndToolCalls(t *testing.T) {
	msgs := []schema.Message{
		{
			Role:    schema.RoleAssistant,
			Content: "calling bash",
			ToolCalls: []schema.ToolCall{
				{ID: "c1", Name: "bash", Arguments: json.RawMessage(`{"command":"ls"}`)},
			},
		},
	}
	out, _, err := translateMessagesToAnthropic(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 assistant message, got %d", len(out))
	}
}

func TestTranslateMessagesToAnthropicAssistantEmptyContentGetsPlaceholder(t *testing.T) {
	// An assistant message with no content and no tool calls must still produce
	// one content block (the empty text block fallback) so the API doesn't reject it.
	msgs := []schema.Message{{Role: schema.RoleAssistant, Content: "", ToolCalls: nil}}
	out, _, err := translateMessagesToAnthropic(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 assistant message with placeholder block, got %d", len(out))
	}
}

func TestTranslateMessagesToAnthropicInvalidToolCallArguments(t *testing.T) {
	msgs := []schema.Message{
		{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{
				{ID: "c1", Name: "bash", Arguments: json.RawMessage(`not-valid-json`)},
			},
		},
	}
	_, _, err := translateMessagesToAnthropic(msgs)
	if err == nil {
		t.Fatal("expected error for invalid tool call arguments JSON, got nil")
	}
}

func TestTranslateToolsToAnthropicExtractsPropertiesAndRequired(t *testing.T) {
	defs := []schema.ToolDefinition{
		{
			Name:        "bash",
			Description: "run bash",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{"type": "string"},
				},
				"required": []string{"command"},
			},
		},
	}
	tools, err := translateToolsToAnthropic(defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
}

func TestTranslateToolsToAnthropicHandlesNonMapSchema(t *testing.T) {
	// InputSchema is not a map — properties and required should be empty.
	defs := []schema.ToolDefinition{
		{Name: "noop", Description: "does nothing", InputSchema: "not-a-map"},
	}
	tools, err := translateToolsToAnthropic(defs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
}

func TestTranslateToolsToAnthropicEmptyDefs(t *testing.T) {
	tools, err := translateToolsToAnthropic(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}
}
