package compact_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

func TestShrinkSmallToolResultUnchanged(t *testing.T) {
	in := []schema.Message{
		user("u"),
		asst("", tc("a", "bash")),
		toolMsg("a", "small output"),
	}
	out, _ := compact.ShrinkApply(in, compact.ShrinkConfig{MaxToolBytes: 1024, RecentTurnsVerbatim: 0})
	if out[2].Content != "small output" {
		t.Fatalf("small tool result changed: %q", out[2].Content)
	}
}

func TestShrinkLargeToolResultTruncatedWithSpillMarker(t *testing.T) {
	huge := strings.Repeat("x", 5000)
	in := []schema.Message{
		user("u"),
		asst("", tc("a", "bash")),
		toolMsg("a", huge),
	}
	out, _ := compact.ShrinkApply(in, compact.ShrinkConfig{MaxToolBytes: 1000, RecentTurnsVerbatim: 0})
	if len(out[2].Content) >= len(huge) {
		t.Fatalf("not truncated: %d >= %d", len(out[2].Content), len(huge))
	}
	if !strings.Contains(out[2].Content, "call_id") && !strings.Contains(out[2].Content, "a") {
		t.Fatalf("marker missing call_id reference: %q", out[2].Content)
	}
}

func TestShrinkWriteFileContentArgStripped(t *testing.T) {
	bigContent := strings.Repeat("y", 10_000)
	args, _ := json.Marshal(map[string]string{"path": "x.go", "content": bigContent})
	in := []schema.Message{
		user("u1"),
		asst("", schema.ToolCall{ID: "wf", Name: "write_file", Arguments: args}),
		toolMsg("wf", "ok"),
		user("u2"),
		asst("done"),
	}
	out, _ := compact.ShrinkApply(in, compact.ShrinkConfig{MaxToolBytes: 65536, RecentTurnsVerbatim: 0})
	// Find the assistant with the write_file call.
	for _, m := range out {
		if m.Role == schema.RoleAssistant && len(m.ToolCalls) > 0 && m.ToolCalls[0].Name == "write_file" {
			if strings.Contains(string(m.ToolCalls[0].Arguments), bigContent) {
				t.Fatalf("content not stripped: %s", string(m.ToolCalls[0].Arguments))
			}
			if !strings.Contains(string(m.ToolCalls[0].Arguments), "content elided") {
				t.Fatalf("elision marker missing: %s", string(m.ToolCalls[0].Arguments))
			}
			if !strings.Contains(string(m.ToolCalls[0].Arguments), `"path":"x.go"`) {
				t.Fatalf("path lost: %s", string(m.ToolCalls[0].Arguments))
			}
			return
		}
	}
	t.Fatal("assistant message missing from output")
}

func TestShrinkRecentTurnsVerbatimSkipsWriteFileStrip(t *testing.T) {
	bigContent := strings.Repeat("y", 10_000)
	args, _ := json.Marshal(map[string]string{"path": "x.go", "content": bigContent})
	// Place write_file in the LAST turn.
	in := []schema.Message{
		user("u1"),
		asst("done"),
		user("u2"),
		asst("", schema.ToolCall{ID: "wf", Name: "write_file", Arguments: args}),
		toolMsg("wf", "ok"),
	}
	out, _ := compact.ShrinkApply(in, compact.ShrinkConfig{MaxToolBytes: 65536, RecentTurnsVerbatim: 1})
	for _, m := range out {
		if m.Role == schema.RoleAssistant && len(m.ToolCalls) > 0 && m.ToolCalls[0].Name == "write_file" {
			if !strings.Contains(string(m.ToolCalls[0].Arguments), bigContent) {
				t.Fatalf("verbatim-window write_file got stripped: %s", string(m.ToolCalls[0].Arguments))
			}
			return
		}
	}
}

func TestShrinkOtherToolCallsUnchanged(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"command": "ls"})
	in := []schema.Message{
		user("u"),
		asst("", schema.ToolCall{ID: "b", Name: "bash", Arguments: args}),
		toolMsg("b", "ok"),
	}
	out, _ := compact.ShrinkApply(in, compact.ShrinkConfig{MaxToolBytes: 65536, RecentTurnsVerbatim: 0})
	for _, m := range out {
		if m.Role == schema.RoleAssistant && len(m.ToolCalls) > 0 {
			if string(m.ToolCalls[0].Arguments) != string(args) {
				t.Fatalf("bash args mutated: %s", string(m.ToolCalls[0].Arguments))
			}
		}
	}
}

func TestShrinkUserAndAssistantTextUnchanged(t *testing.T) {
	in := []schema.Message{user("hello"), asst("world")}
	out, _ := compact.ShrinkApply(in, compact.ShrinkConfig{MaxToolBytes: 1024, RecentTurnsVerbatim: 0})
	if out[0].Content != "hello" || out[1].Content != "world" {
		t.Fatalf("text mutated: %+v", out)
	}
}

func TestShrinkIdempotent(t *testing.T) {
	in := []schema.Message{
		user("u"),
		asst("", tc("a", "bash")),
		toolMsg("a", strings.Repeat("x", 5000)),
	}
	cfg := compact.ShrinkConfig{MaxToolBytes: 1000, RecentTurnsVerbatim: 0}
	once, _ := compact.ShrinkApply(in, cfg)
	twice, _ := compact.ShrinkApply(once, cfg)
	if len(once) != len(twice) || once[2].Content != twice[2].Content {
		t.Fatalf("not idempotent: once=%q twice=%q", once[2].Content, twice[2].Content)
	}
}

// TestShrinkApplyDoesNotMutateInputToolCalls verifies the pure-function
// invariant: ShrinkApply must not write through to the caller's slice
// when stripping write_file / edit_file Arguments.
func TestShrinkApplyDoesNotMutateInputToolCalls(t *testing.T) {
	bigContent := strings.Repeat("z", 10_000)
	args, _ := json.Marshal(map[string]string{"path": "x.go", "content": bigContent})

	in := []schema.Message{
		user("u1"),
		asst("", schema.ToolCall{ID: "wf1", Name: "write_file", Arguments: args}),
		toolMsg("wf1", "ok"),
		user("u2"),
		asst("done"),
	}

	// Capture original before calling ShrinkApply.
	original := string(in[1].ToolCalls[0].Arguments)

	out, stats := compact.ShrinkApply(in, compact.ShrinkConfig{MaxToolBytes: 65536, RecentTurnsVerbatim: 0})

	// The fix should have stripped the output.
	if stats.ToolCallArgsStripped == 0 {
		t.Fatal("expected ToolCallArgsStripped > 0 — the stripping path was not exercised")
	}
	// Output must be stripped.
	if strings.Contains(string(out[1].ToolCalls[0].Arguments), bigContent) {
		t.Fatalf("output not stripped: %s", string(out[1].ToolCalls[0].Arguments))
	}
	// Input must be UNCHANGED.
	if string(in[1].ToolCalls[0].Arguments) != original {
		t.Fatalf("ShrinkApply mutated input ToolCalls[0].Arguments:\ngot:  %s\nwant: %s",
			string(in[1].ToolCalls[0].Arguments), original)
	}
}

// TestCompactorViewIsPureToolCalls strengthens the existing purity test
// to cover ToolCalls.Arguments mutation, not just text Content.
// Uses RecentTurnsVerbatim=0 so the write_file turn is inside the strip window.
func TestCompactorViewIsPureToolCalls(t *testing.T) {
	bigContent := strings.Repeat("z", 10_000)
	args, _ := json.Marshal(map[string]string{"path": "x.go", "content": bigContent})

	in := []schema.Message{
		user("u1"),
		asst("", schema.ToolCall{ID: "wf1", Name: "write_file", Arguments: args}),
		toolMsg("wf1", "ok"),
		user("u2"),
		asst("done"),
	}
	originalArgs := string(in[1].ToolCalls[0].Arguments)

	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        65536,
		RecentTurnsVerbatim: 0, // no verbatim window — write_file turn IS in the strip path
	})
	// Pass a very small budget so Layer A MUST run the strip path.
	_, _ = c.View(in, 1, 1, compact.NewCalibrator(0.3))

	if string(in[1].ToolCalls[0].Arguments) != originalArgs {
		t.Fatalf("Compactor.View mutated input ToolCalls[0].Arguments:\ngot:  %s\nwant: %s",
			string(in[1].ToolCalls[0].Arguments), originalArgs)
	}
}
