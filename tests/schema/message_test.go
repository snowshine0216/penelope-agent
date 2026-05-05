package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

func TestMessageMarshalsBasicTextMessage(t *testing.T) {
	msg := schema.Message{
		Role:    schema.RoleUser,
		Content: "hello",
	}

	out, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"role":"user","content":"hello"}`
	if string(out) != want {
		t.Fatalf("marshal output = %s, want %s", out, want)
	}
}

func TestMessageOmitsEmptyToolFields(t *testing.T) {
	out, err := json.Marshal(schema.Message{Role: schema.RoleAssistant, Content: "ok"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got := string(out)
	if got != `{"role":"assistant","content":"ok"}` {
		t.Fatalf("expected omitempty for tool_calls / tool_call_id, got: %s", got)
	}
}

func TestMessageRoundTripPreservesToolCalls(t *testing.T) {
	original := schema.Message{
		Role:    schema.RoleAssistant,
		Content: "calling bash",
		ToolCalls: []schema.ToolCall{
			{
				ID:        "call_1",
				Name:      "bash",
				Arguments: json.RawMessage(`{"command":"ls -la"}`),
			},
		},
	}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded schema.Message
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(decoded.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call after round trip, got %d", len(decoded.ToolCalls))
	}
	if decoded.ToolCalls[0].Name != "bash" {
		t.Fatalf("name = %q, want bash", decoded.ToolCalls[0].Name)
	}
	if string(decoded.ToolCalls[0].Arguments) != `{"command":"ls -la"}` {
		t.Fatalf("arguments = %s, want raw JSON preserved", decoded.ToolCalls[0].Arguments)
	}
}

func TestToolCallArgumentsAreLazilyParsed(t *testing.T) {
	tc := schema.ToolCall{
		ID:        "x",
		Name:      "noop",
		Arguments: json.RawMessage(`{"deeply":{"nested":[1,2,3]}}`),
	}

	var args struct {
		Deeply struct {
			Nested []int `json:"nested"`
		} `json:"deeply"`
	}
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		t.Fatalf("nested unmarshal: %v", err)
	}

	if len(args.Deeply.Nested) != 3 || args.Deeply.Nested[2] != 3 {
		t.Fatalf("nested args not preserved: %+v", args)
	}
}

func TestRoleConstantsMatchWireFormat(t *testing.T) {
	cases := map[schema.Role]string{
		schema.RoleSystem:    "system",
		schema.RoleUser:      "user",
		schema.RoleAssistant: "assistant",
		schema.RoleTool:      "tool",
	}
	for role, want := range cases {
		if string(role) != want {
			t.Errorf("Role %q != wire %q", role, want)
		}
	}
}
