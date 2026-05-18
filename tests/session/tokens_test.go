package session_test

import (
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/session"
)

func TestEstimateOneEmptyMessage(t *testing.T) {
	got := session.EstimateOne(schema.Message{Role: schema.RoleUser})
	if got != session.MessageOverhead {
		t.Fatalf("empty message tokens = %d, want overhead %d", got, session.MessageOverhead)
	}
}

func TestEstimateOneAscii(t *testing.T) {
	msg := schema.Message{Role: schema.RoleUser, Content: "hello world"} // 11 chars
	got := session.EstimateOne(msg)
	want := session.MessageOverhead + (11+3)/4 // ceil division
	if got != want {
		t.Fatalf("ascii tokens = %d, want %d", got, want)
	}
}

func TestEstimateOneToolResultIncludesToolCallID(t *testing.T) {
	msg := schema.Message{Role: schema.RoleTool, Content: "ok", ToolCallID: "call_12345"}
	got := session.EstimateOne(msg)
	want := session.MessageOverhead + (2+3)/4 + (10+3)/4
	if got != want {
		t.Fatalf("tool tokens = %d, want %d (overhead + content + tool_call_id)", got, want)
	}
}

func TestEstimateTokensSumsAcrossMessages(t *testing.T) {
	msgs := []schema.Message{
		{Role: schema.RoleUser, Content: "a"},
		{Role: schema.RoleAssistant, Content: "bb"},
	}
	got := session.EstimateTokens(msgs)
	want := session.EstimateOne(msgs[0]) + session.EstimateOne(msgs[1])
	if got != want {
		t.Fatalf("sum = %d, want %d", got, want)
	}
}
