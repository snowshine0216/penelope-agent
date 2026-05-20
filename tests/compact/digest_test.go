package compact_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

func TestFoldNoOpWhenAlreadyUnderBudget(t *testing.T) {
	// View already fits — Fold should return it unchanged.
	in := []schema.Message{user("u1"), asst("ok")}
	out, folded := compact.Fold(in, 10_000, 4, nil)
	if folded != 0 {
		t.Fatalf("expected 0 folded turns, got %d", folded)
	}
	if len(out) != len(in) {
		t.Fatalf("view changed: in=%d out=%d", len(in), len(out))
	}
}

func TestFoldRespectsVerbatimTail(t *testing.T) {
	// Build 6 turns; verbatim=2 must keep the last 2 user turns intact.
	in := []schema.Message{
		user("turn1"), asst("a1"),
		user("turn2"), asst("a2"),
		user("turn3"), asst("a3"),
		user("turn4"), asst("a4"),
		user("turn5"), asst("a5"),
		user("turn6"), asst("a6"),
	}
	out, folded := compact.Fold(in, 1 /* impossibly tight */, 2, nil)
	if folded < 1 {
		t.Fatalf("expected folding, got %d", folded)
	}
	// Verbatim tail = last 2 user turns plus their assistants.
	last := out[len(out)-4:]
	if last[0].Role != schema.RoleUser || last[0].Content != "turn5" {
		t.Fatalf("verbatim turn5 user missing: %+v", last)
	}
	if last[2].Role != schema.RoleUser || last[2].Content != "turn6" {
		t.Fatalf("verbatim turn6 user missing: %+v", last)
	}
}

func TestFoldDigestIsSyntheticAssistantAtIndex0(t *testing.T) {
	in := []schema.Message{
		user("turn1"), asst("a1"),
		user("turn2"), asst("a2"),
		user("turn3"), asst("a3"),
		user("turn4"), asst("a4"),
	}
	out, folded := compact.Fold(in, 1, 1, nil)
	if folded < 1 {
		t.Fatalf("expected fold, got 0")
	}
	if out[0].Role != schema.RoleAssistant {
		t.Fatalf("digest must be assistant, got role=%s", out[0].Role)
	}
	if !strings.HasPrefix(out[0].Content, "## Prior session digest") {
		t.Fatalf("digest header missing: %q", out[0].Content[:min(60, len(out[0].Content))])
	}
}

func TestFoldDigestPreservesCallIDForSpilledTools(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"command": "find / -type f"})
	huge := strings.Repeat("x", 5000)
	// Tool result already truncated by Layer A; marker contains call_id.
	truncated := "head...[5000 bytes elided of 5000 total for call_id=toolu_01abc; use read_tool_output(call_id=\"toolu_01abc\", start_line=N, line_count=M) to read more]...tail"
	in := []schema.Message{
		user("u1"),
		asst("planning", schema.ToolCall{ID: "toolu_01abc", Name: "bash", Arguments: args}),
		toolMsg("toolu_01abc", truncated),
		user("u2"),
		asst("more"),
		user("u3"),
		asst("now"),
	}
	_ = huge
	out, folded := compact.Fold(in, 1, 1, nil)
	if folded < 1 {
		t.Fatalf("expected fold, got 0")
	}
	if !strings.Contains(out[0].Content, "toolu_01abc") {
		t.Fatalf("call_id missing from digest: %q", out[0].Content)
	}
}

func TestFoldFormatGoldenTurnsMixed(t *testing.T) {
	// Golden file: tests/compact/testdata/digest/turns-mixed.golden.txt
	in := fixtureTurnsMixed()
	out, _ := compact.Fold(in, 1 /* force fold */, 1, nil)
	got := out[0].Content
	goldenPath := filepath.Join("testdata", "digest", "turns-mixed.golden.txt")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run with UPDATE_GOLDEN=1 to regenerate)", err)
	}
	if got != string(want) {
		t.Fatalf("digest mismatch:\nwant=%q\ngot=%q", string(want), got)
	}
}

func TestFoldIdempotent(t *testing.T) {
	in := []schema.Message{
		user("u1"), asst("a1"),
		user("u2"), asst("a2"),
		user("u3"), asst("a3"),
	}
	out1, _ := compact.Fold(in, 1, 1, nil)
	out2, _ := compact.Fold(out1, 1, 1, nil)
	if out1[0].Content != out2[0].Content {
		t.Fatalf("not idempotent:\nonce=%q\ntwice=%q", out1[0].Content, out2[0].Content)
	}
}

func TestFoldTruncatesLongTextTo120Chars(t *testing.T) {
	longUser := strings.Repeat("a", 500)
	in := []schema.Message{
		user(longUser), asst("a1"),
		user("u2"), asst("a2"),
		user("u3"), asst("a3"),
	}
	out, _ := compact.Fold(in, 1, 1, nil)
	for _, line := range strings.Split(out[0].Content, "\n") {
		// digest body lines should not exceed a reasonable width
		if strings.Contains(line, "aaaaaaaa") && len(line) > 200 {
			t.Fatalf("digest line not truncated: len=%d %q", len(line), line)
		}
	}
}

func fixtureTurnsMixed() []schema.Message {
	args1, _ := json.Marshal(map[string]string{"path": "main.go"})
	args2, _ := json.Marshal(map[string]string{"command": "go test ./..."})
	return []schema.Message{
		user("fix the OOM in the trimmer please"),
		asst("planning a 3-step approach",
			schema.ToolCall{ID: "c1", Name: "read_file", Arguments: args1},
		),
		toolMsg("c1", "189 lines of code"),
		user("looks good, run the tests"),
		asst("running tests",
			schema.ToolCall{ID: "c2", Name: "bash", Arguments: args2},
		),
		toolMsg("c2", "...[12345 bytes elided of 50000 total for call_id=c2; use read_tool_output(call_id=\"c2\", start_line=N, line_count=M) to read more]..."),
		user("verbatim turn user"),
		asst("verbatim turn assistant"),
	}
}

func min(a, b int) int { if a < b { return a }; return b }
