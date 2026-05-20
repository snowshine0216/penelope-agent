package compact_test

import (
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

func TestEstimateOneEmptyMessage(t *testing.T) {
	got := compact.EstimateOne(schema.Message{Role: schema.RoleUser})
	if got != compact.MessageOverhead {
		t.Fatalf("empty message tokens = %d, want overhead %d", got, compact.MessageOverhead)
	}
}

func TestEstimateOneAscii(t *testing.T) {
	msg := schema.Message{Role: schema.RoleUser, Content: "hello world"} // 11 chars
	got := compact.EstimateOne(msg)
	want := compact.MessageOverhead + (11+3)/4
	if got != want {
		t.Fatalf("ascii tokens = %d, want %d", got, want)
	}
}

func TestEstimateOneToolResultIncludesToolCallID(t *testing.T) {
	msg := schema.Message{Role: schema.RoleTool, Content: "ok", ToolCallID: "call_12345"}
	got := compact.EstimateOne(msg)
	want := compact.MessageOverhead + (2+3)/4 + (10+3)/4
	if got != want {
		t.Fatalf("tool tokens = %d, want %d", got, want)
	}
}

func TestEstimateTokensSumsAcrossMessages(t *testing.T) {
	msgs := []schema.Message{
		{Role: schema.RoleUser, Content: "a"},
		{Role: schema.RoleAssistant, Content: "bb"},
	}
	got := compact.EstimateTokens(msgs)
	want := compact.EstimateOne(msgs[0]) + compact.EstimateOne(msgs[1])
	if got != want {
		t.Fatalf("sum = %d, want %d", got, want)
	}
}
