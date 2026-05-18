package session_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/session"
)

func user(content string) schema.Message {
	return schema.Message{Role: schema.RoleUser, Content: content}
}

func asst(content string, calls ...schema.ToolCall) schema.Message {
	return schema.Message{Role: schema.RoleAssistant, Content: content, ToolCalls: calls}
}

func toolMsg(id string, content string) schema.Message {
	return schema.Message{Role: schema.RoleTool, Content: content, ToolCallID: id}
}

func toolCall(id, name string) schema.ToolCall {
	return schema.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(`{}`)}
}

func contents(msgs []schema.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = string(m.Role) + ":" + m.Content
	}
	return out
}

func TestWindowTrimmerKeepsAllWhenUnderLimits(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	in := []schema.Message{
		user("first"),
		asst("ack"),
		user("second"),
		asst("ack"),
	}
	got := tr.Trim(in)
	if !reflect.DeepEqual(contents(got), contents(in)) {
		t.Fatalf("got %v, want %v", contents(got), contents(in))
	}
}

func TestWindowTrimmerDropsOldestUserTurns(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 2, MaxTokens: 100000})
	in := []schema.Message{
		user("u1"), asst("a1"),
		user("u2"), asst("a2"),
		user("u3"), asst("a3"),
	}
	got := tr.Trim(in)
	if !reflect.DeepEqual(contents(got), []string{"user:u2", "assistant:a2", "user:u3", "assistant:a3"}) {
		t.Fatalf("got %v", contents(got))
	}
}

func TestWindowTrimmerPreservesToolCallResultPairs(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 2, MaxTokens: 100000})
	in := []schema.Message{
		user("u1"),
		asst("", toolCall("tc1", "bash")),
		toolMsg("tc1", "result1"),
		user("u2"),
		asst("", toolCall("tc2", "bash")),
		toolMsg("tc2", "result2"),
	}
	got := tr.Trim(in)
	if len(got) != 6 {
		t.Fatalf("len = %d, want 6 (pairs preserved)", len(got))
	}
	if got[2].Role != schema.RoleTool || got[2].ToolCallID != "tc1" {
		t.Fatalf("first tool pair broken: %+v", got)
	}
}

func TestWindowTrimmerTokenCapShrinksBelowTurnLimit(t *testing.T) {
	huge := strings.Repeat("x", 1000) // ~250 tokens per content
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 600})
	in := []schema.Message{
		user(huge), asst(huge),
		user(huge), asst(huge),
		user("recent"), asst("done"),
	}
	got := tr.Trim(in)
	// All three turns together exceed 600 tokens; the trimmer should
	// drop the oldest turn(s) until the remainder fits.
	if len(got) >= len(in) {
		t.Fatalf("token cap had no effect: got len %d, in len %d", len(got), len(in))
	}
	if got[len(got)-2].Content != "recent" {
		t.Fatalf("most recent user turn dropped: %v", contents(got))
	}
}

func TestWindowTrimmerDropsOrphanToolResult(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	in := []schema.Message{
		user("u1"),
		toolMsg("orphan", "shouldnt be here"),
		asst("hi"),
	}
	got := tr.Trim(in)
	for _, m := range got {
		if m.Role == schema.RoleTool {
			t.Fatalf("orphan tool result kept: %+v", got)
		}
	}
}

func TestWindowTrimmerDropsAssistantWithDanglingToolCalls(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	in := []schema.Message{
		user("u1"),
		asst("", toolCall("tc1", "bash"), toolCall("tc2", "bash")),
		toolMsg("tc1", "ok"),
		// tc2 result missing entirely
		user("u2"),
	}
	got := tr.Trim(in)
	for _, m := range got {
		if m.Role == schema.RoleAssistant && len(m.ToolCalls) > 0 {
			t.Fatalf("assistant with dangling tool_calls retained: %+v", got)
		}
		if m.Role == schema.RoleTool {
			t.Fatalf("tool message from dropped assistant retained: %+v", got)
		}
	}
}

func TestWindowTrimmerDropsLeadingToolMessages(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	in := []schema.Message{
		toolMsg("stale", "left over from a dropped assistant"),
		user("u1"),
	}
	got := tr.Trim(in)
	if len(got) == 0 || got[0].Role == schema.RoleTool {
		t.Fatalf("leading tool message not dropped: %v", contents(got))
	}
}

func TestWindowTrimmerEmptyInput(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	if got := tr.Trim(nil); len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestWindowTrimmerSingleUserMessageUnchanged(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	in := []schema.Message{user("only")}
	got := tr.Trim(in)
	if len(got) != 1 || got[0].Content != "only" {
		t.Fatalf("got %v", contents(got))
	}
}

func TestWindowTrimmerConsecutiveUserMessagesRetained(t *testing.T) {
	// Models D12: concurrent writers can append two user messages back to back.
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	in := []schema.Message{user("a"), user("b"), asst("ack")}
	got := tr.Trim(in)
	if len(got) != 3 {
		t.Fatalf("got %v, want both user messages retained", contents(got))
	}
}

func TestWindowTrimmerRegisteredAsWindowStrategy(t *testing.T) {
	tr, err := session.Get("window", session.TrimConfig{MaxUserTurns: 6, MaxTokens: 32000})
	if err != nil {
		t.Fatalf("Get window: %v", err)
	}
	if tr.Name() != "window" {
		t.Fatalf("Name = %q, want window", tr.Name())
	}
}
