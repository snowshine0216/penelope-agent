package compact_test

import (
	"encoding/json"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

func user(c string) schema.Message { return schema.Message{Role: schema.RoleUser, Content: c} }
func asst(c string, calls ...schema.ToolCall) schema.Message {
	return schema.Message{Role: schema.RoleAssistant, Content: c, ToolCalls: calls}
}
func toolMsg(id, c string) schema.Message {
	return schema.Message{Role: schema.RoleTool, Content: c, ToolCallID: id}
}
func tc(id, name string) schema.ToolCall {
	return schema.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(`{}`)}
}

func TestDefensiveCleanupDropsOrphanToolMessage(t *testing.T) {
	in := []schema.Message{user("u1"), toolMsg("orphan", "x"), asst("hi")}
	out := compact.DefensiveCleanup(in)
	for _, m := range out {
		if m.Role == schema.RoleTool {
			t.Fatalf("orphan retained: %+v", out)
		}
	}
}

func TestDefensiveCleanupDropsAssistantWithDanglingToolCalls(t *testing.T) {
	in := []schema.Message{
		user("u1"),
		asst("", tc("a", "bash"), tc("b", "bash")),
		toolMsg("a", "ok"),
		user("u2"),
	}
	out := compact.DefensiveCleanup(in)
	for _, m := range out {
		if m.Role == schema.RoleAssistant && len(m.ToolCalls) > 0 {
			t.Fatalf("dangling tool_calls retained: %+v", out)
		}
	}
}

func TestDefensiveCleanupDropsLeadingToolMessages(t *testing.T) {
	in := []schema.Message{toolMsg("stale", "x"), user("u1")}
	out := compact.DefensiveCleanup(in)
	if len(out) == 0 || out[0].Role == schema.RoleTool {
		t.Fatalf("leading tool not dropped: %+v", out)
	}
}

func TestDefensiveCleanupKeepsValidPairs(t *testing.T) {
	in := []schema.Message{
		user("u"),
		asst("", tc("a", "bash")),
		toolMsg("a", "result"),
	}
	out := compact.DefensiveCleanup(in)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
}

func TestCloneMessagesNilInNilOut(t *testing.T) {
	if got := compact.CloneMessages(nil); got != nil {
		t.Fatalf("nil in, got %v", got)
	}
}
