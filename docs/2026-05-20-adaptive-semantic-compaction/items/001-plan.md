# Adaptive Semantic Compaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current `internal/session/{window,trim,tokens}.go` "drop oldest user turn" trimmer with a deterministic, semantics-preserving compactor (Layer A structural shrink + Layer B rolling digest), cap large tool outputs at the engine boundary with disk spill + paged retrieval, plumb real provider Usage into an adaptive budget with EWMA calibration, and surface per-turn compaction stats via a new `OnCompact` reporter callback.

**Architecture:** A new `internal/compact` package owns a pure `Compactor.View(history, budget, calibrator)` that returns a `[]schema.Message` view + `CompactStats`. The session JSONL stays append-only and complete — the compactor produces a read-time view, never mutating history. Large tool outputs are intercepted at the engine boundary, spilled to `.claw/sessions/<id>/tool-outputs/<call-id>.txt`, and replaced with a marker pointing at a new `read_tool_output` tool. The provider interface is widened to return `*Response{Message, Usage}` so an EWMA calibrator can self-correct the local chars/4 estimator. A new `Reporter.OnCompact` callback prints `[compact]` lines on stderr and appends to a session-scoped `compact-events.jsonl` audit log.

**Tech Stack:** Go 1.26.2, existing `schema.Message` / `tools.Tool` / `provider.LLMProvider` interfaces, `bufio.Scanner` with bumped buffer for paged retrieval, `gopkg.in/yaml.v3` (already in `go.sum`) for the optional model-limits override, `syscall.Flock` for the audit log (same pattern as `session.go`), `go test ./...` plus a `live_provider` build tag for the opt-in smoke test.

**Spec deviations noted up front:** None. All design decisions were resolved during brainstorming and the spec leaves only implementation-level details to this plan.

**User-supplied load-bearing constraint (verbatim):**

> "make sure keep the real e2e test case for me to verify also"

This plan honours that constraint with two mandatory tasks that MUST land — they are not optional, not future work, and not "if time permits":

- **Task 18 (Layer 4 — Real-case verification)** ships committed `testdata/compact/*.jsonl` fixtures and corresponding `testdata/compact/golden/*` files, plus `tests/engine/compact_realcase_test.go` that loads each fixture, runs the compactor, and asserts against the goldens. Supports `-update` for golden regeneration. `session-pathological.jsonl` is synthesized at test-time by a `TestMain` helper (so the 200 MB blob never hits the git index).
- **Task 19 (Layer 5 — Opt-in live-provider smoke)** ships `tests/engine/compact_live_test.go` guarded by `//go:build live_provider`, with `TestCompact_LiveClaude` that skips on missing `ANTHROPIC_API_KEY`, elicits a huge tool output (`find / -type f 2>/dev/null | head -50000`), and asserts no OOM, `OnCompact` fired with `LayerBEngaged=true`, and a follow-up turn calls `read_tool_output` successfully. The invocation `go test -tags=live_provider ./tests/engine -run TestCompact_LiveClaude` is documented in `CLAUDE.md`.

**"Done means" / acceptance gate:**

- `go test ./... -count=1` passes (default tags, no live provider).
- `go vet -tags=live_provider ./tests/engine` succeeds (Layer 5 compiles).
- `go test ./tests/engine -run TestCompact_RealCase -count=1` passes with committed goldens.
- `CLAUDE.md` documents the `live_provider` invocation.
- `CHANGELOG.md` has a new entry for this feature; `TODOS.md` removes any items closed by this work.
- `internal/compact/...` line coverage ≥ 85% (`go test -cover ./internal/compact/...`).

---

## File Structure

**New files (in dependency order — earlier files have no dependency on later ones):**

```
internal/compact/
├── tokens.go              EstimateOne, EstimateTokens, MessageOverhead (moved from internal/session/tokens.go)
├── cleanup.go             defensiveCleanup, matchingCallExists, cloneMessages (moved from internal/session/window.go)
├── modellimits.go         defaultModelLimits map, lookupModelLimit, FallbackContextLimit, LoadOverridesYAML
├── budget.go              BudgetInput, Budget()
├── calibration.go         Calibrator{ratio, alpha}, Observe, Predict, NewCalibrator
├── shrink.go              Layer A: ShrinkConfig, Apply(history, cfg) ([]schema.Message, perMessageStats)
├── digest.go              Layer B: Fold(viewA, budget, recentTurnsVerbatim, calibrator) ([]schema.Message, foldedTurns int)
├── stats.go               CompactStats struct
└── compactor.go           Config, Compactor, NewCompactor, View(history, budget, calibrator) ([]schema.Message, CompactStats)

internal/session/
└── tool_spill.go          SpillToolOutput(id, body) (path, lines, err), ReadToolOutputChunk(id, startLine, lineCount) (string, totalLines, err), ToolOutputPath(id)

internal/tools/
└── read_tool_output.go    NewReadToolOutputTool(session), Tool implementing the read_tool_output schema

cmd/claw/
└── (main.go modified — no new files)

tests/compact/             (new dir mirroring tests/session/ convention)
├── tokens_test.go
├── cleanup_test.go
├── modellimits_test.go
├── budget_test.go
├── calibration_test.go
├── shrink_test.go
├── digest_test.go
├── compactor_test.go
└── properties_test.go     Layer-3 property tests (tool-call pairing, budget invariant)

tests/session/
└── tool_spill_test.go     (new file in existing tests/session)

tests/tools/
└── read_tool_output_test.go

tests/engine/
├── compact_integration_test.go    Layer-2 engine + compact + spill + read_tool_output round-trip
├── compact_realcase_test.go       Layer-4 committed-fixture goldens (REQUIRED — load-bearing user constraint)
└── compact_live_test.go           Layer-5 //go:build live_provider, TestCompact_LiveClaude (REQUIRED)

testdata/compact/                  Committed fixtures + goldens for Layer 4
├── session-huge-bash.jsonl
├── session-many-edits.jsonl
├── session-mixed-tools.jsonl
└── golden/
    ├── session-huge-bash.view.txt
    ├── session-huge-bash.digest.txt
    ├── session-huge-bash.stats.json
    ├── session-many-edits.view.txt
    ├── session-many-edits.digest.txt
    ├── session-many-edits.stats.json
    ├── session-mixed-tools.view.txt
    ├── session-mixed-tools.digest.txt
    └── session-mixed-tools.stats.json
```

**Modified files:**

```
internal/tools/truncate.go            Add TruncateWithMarker(s, maxBytes, marker); refactor TruncateForLLM as thin wrapper.
internal/provider/interface.go        Add Usage, Response; change LLMProvider.Generate signature.
internal/provider/claude.go           Generate returns *Response{Message, Usage}; populate Usage from resp.Usage.
internal/provider/openai.go           Generate returns *Response{Message, Usage}; populate Usage from resp.Usage.{Prompt,Completion}Tokens.
internal/engine/loop.go               Drop trimmer field, attach *compact.Compactor + *compact.Calibrator, thread lastUsage, replace providerView() with compactor.View(), emit OnCompact per emission rule.
internal/engine/tool_execution.go     Add tool-output boundary cap + spill before sess.Append (single & parallel paths).
internal/engine/reporter.go           Add OnCompact(ctx, stats) method to Reporter interface.
internal/engine/terminal_reporter.go  Implement OnCompact: print [compact] line in spec format.
cmd/claw/main.go                      Remove --max-context-turns/--max-context-tokens/--trim-strategy; add --compact-recent-turns / --compact-fallback-limit / --compact-safety-factor / --compact-max-tool-bytes; hard-error on removed flags; register read_tool_output tool.
CLAUDE.md                             Document live_provider build-tag invocation.
CHANGELOG.md                          Add v0.6.0.0 entry summarising the feature.
TODOS.md                              Drop any items closed by this work (none currently match exactly; leave as-is unless one applies).
```

**Files DELETED at the end of the plan (after every caller has migrated):**

```
internal/session/window.go     (defensiveCleanup moved to internal/compact/cleanup.go; everything else gone)
internal/session/trim.go       (Trimmer interface and registry gone)
internal/session/tokens.go     (moved to internal/compact/tokens.go)
tests/session/trim_test.go     (interface gone)
tests/session/trim_window_test.go (strategy gone)
tests/session/tokens_test.go   (moved to tests/compact/tokens_test.go)
```

**Package name:** `internal/compact` is package `compact`. Import alias not needed at engine callsite (`import "github.com/snowshine0216/penelope-agent/internal/compact"`), and there is no local-variable collision with the existing `agentcontext` / `agentsession` aliases. Tests live in package `compact_test` to exercise the public surface only.

---

### Task 1: Bootstrap `internal/compact` package by moving the token estimator

**Files:**
- Create: `internal/compact/tokens.go`
- Create: `tests/compact/tokens_test.go`

This task creates the new package and moves the existing chars/4 estimator verbatim. The session-package copy stays for now so nothing else breaks; later tasks migrate callers and delete the original.

- [ ] **Step 1: Write the failing tests**

Create `tests/compact/tokens_test.go`:

```go
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
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./tests/compact -run TestEstimate -count=1`
Expected: FAIL because the `compact` package does not exist yet.

- [ ] **Step 3: Create the package by moving the estimator verbatim**

Create `internal/compact/tokens.go`:

```go
package compact

import (
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// MessageOverhead approximates the per-message envelope cost a provider
// incurs beyond the literal content (role marker, separator tokens).
// 8 roughly matches OpenAI's documented per-message overhead so the
// total estimate is conservative.
const MessageOverhead = 8

// EstimateOne returns a chars/4 estimate of one message's token cost,
// rounding up so a 1-char message still counts as 1 content token.
func EstimateOne(msg schema.Message) int {
	tokens := MessageOverhead + ceilDiv4(len(msg.Content))
	if msg.ToolCallID != "" {
		tokens += ceilDiv4(len(msg.ToolCallID))
	}
	for _, call := range msg.ToolCalls {
		tokens += ceilDiv4(len(call.ID))
		tokens += ceilDiv4(len(call.Name))
		tokens += ceilDiv4(len(call.Arguments))
	}
	return tokens
}

// EstimateTokens sums EstimateOne across every message in the slice.
func EstimateTokens(msgs []schema.Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateOne(m)
	}
	return total
}

func ceilDiv4(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + 3) / 4
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run: `go test ./tests/compact -run TestEstimate -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/compact/tokens.go tests/compact/tokens_test.go
git commit -m "feat(compact): bootstrap package with chars/4 token estimator"
```

---

### Task 2: Move `defensiveCleanup` to `internal/compact/cleanup.go`

**Files:**
- Create: `internal/compact/cleanup.go`
- Create: `tests/compact/cleanup_test.go`

The cleanup helper is a pure function with no session-specific state. Moving it here lets every later task (shrink, digest, compactor) call it without an import cycle through `internal/session`.

- [ ] **Step 1: Write the failing tests**

Create `tests/compact/cleanup_test.go`:

```go
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
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./tests/compact -run 'TestDefensiveCleanup|TestCloneMessages' -count=1`
Expected: FAIL because `compact.DefensiveCleanup` does not exist.

- [ ] **Step 3: Create the file by moving the helpers verbatim and exporting them**

Create `internal/compact/cleanup.go`:

```go
package compact

import (
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// DefensiveCleanup removes orphan tool results, assistants whose
// tool_calls do not all have matching tool results immediately
// following, and any leading tool messages exposed by those drops.
// The result is always a provider-valid slice even if concurrent
// writers interleaved a JSONL file. Pure function: input is not
// mutated.
func DefensiveCleanup(messages []schema.Message) []schema.Message {
	pass1 := make([]schema.Message, 0, len(messages))
	for _, m := range messages {
		if m.Role == schema.RoleTool {
			if !matchingCallExists(pass1, m.ToolCallID) {
				continue
			}
		}
		pass1 = append(pass1, m)
	}

	keep := make([]bool, len(pass1))
	for i := range keep {
		keep[i] = true
	}
	for i, m := range pass1 {
		if m.Role != schema.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		expected := map[string]bool{}
		for _, c := range m.ToolCalls {
			expected[c.ID] = false
		}
		j := i + 1
		for j < len(pass1) && pass1[j].Role == schema.RoleTool {
			if _, ok := expected[pass1[j].ToolCallID]; ok {
				expected[pass1[j].ToolCallID] = true
			}
			j++
		}
		allSatisfied := true
		for _, seen := range expected {
			if !seen {
				allSatisfied = false
				break
			}
		}
		if allSatisfied {
			continue
		}
		keep[i] = false
		for k := i + 1; k < j; k++ {
			keep[k] = false
		}
	}
	pass2 := make([]schema.Message, 0, len(pass1))
	for i, m := range pass1 {
		if keep[i] {
			pass2 = append(pass2, m)
		}
	}

	start := 0
	for start < len(pass2) && pass2[start].Role == schema.RoleTool {
		start++
	}
	return pass2[start:]
}

// CloneMessages returns a fresh slice with the same elements. Returns
// nil for empty input so downstream `len(x) == 0` checks behave the
// same as on the original.
func CloneMessages(messages []schema.Message) []schema.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]schema.Message, len(messages))
	copy(out, messages)
	return out
}

func matchingCallExists(prefix []schema.Message, toolCallID string) bool {
	for i := len(prefix) - 1; i >= 0; i-- {
		m := prefix[i]
		if m.Role == schema.RoleAssistant {
			for _, c := range m.ToolCalls {
				if c.ID == toolCallID {
					return true
				}
			}
			return false
		}
		if m.Role == schema.RoleUser {
			return false
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run: `go test ./tests/compact -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/compact/cleanup.go tests/compact/cleanup_test.go
git commit -m "feat(compact): move defensive cleanup helper into compact package"
```

---

### Task 3: Model-limit registry + optional YAML override

**Files:**
- Create: `internal/compact/modellimits.go`
- Create: `tests/compact/modellimits_test.go`

- [ ] **Step 1: Write the failing tests**

Create `tests/compact/modellimits_test.go`:

```go
package compact_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
)

func TestLookupModelLimitKnownClaudeOpus(t *testing.T) {
	got, ok := compact.LookupModelLimit("claude-opus-4-7", nil)
	if !ok || got != 200_000 {
		t.Fatalf("claude-opus-4-7 = (%d, %v), want (200000, true)", got, ok)
	}
}

func TestLookupModelLimitOneMillionVariant(t *testing.T) {
	got, ok := compact.LookupModelLimit("claude-opus-4-7[1m]", nil)
	if !ok || got != 1_000_000 {
		t.Fatalf("claude-opus-4-7[1m] = (%d, %v), want (1000000, true)", got, ok)
	}
}

func TestLookupModelLimitUnknownModelReturnsFalse(t *testing.T) {
	_, ok := compact.LookupModelLimit("nonexistent-model-x", nil)
	if ok {
		t.Fatalf("unknown model should not be found")
	}
}

func TestLookupModelLimitOverrideWins(t *testing.T) {
	overrides := map[string]int{"claude-opus-4-7": 999}
	got, ok := compact.LookupModelLimit("claude-opus-4-7", overrides)
	if !ok || got != 999 {
		t.Fatalf("override should win, got (%d, %v)", got, ok)
	}
}

func TestLookupModelLimitOverrideAddsNewModel(t *testing.T) {
	overrides := map[string]int{"my-custom-model": 50_000}
	got, ok := compact.LookupModelLimit("my-custom-model", overrides)
	if !ok || got != 50_000 {
		t.Fatalf("override-only model not found: (%d, %v)", got, ok)
	}
}

func TestLoadOverridesYAMLMissingFileReturnsEmpty(t *testing.T) {
	overrides, err := compact.LoadOverridesYAML(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if len(overrides) != 0 {
		t.Fatalf("missing file overrides = %v, want empty", overrides)
	}
}

func TestLoadOverridesYAMLValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model-limits.yaml")
	content := "claude-opus-4-7: 999\nmy-model: 1234\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	overrides, err := compact.LoadOverridesYAML(path)
	if err != nil {
		t.Fatalf("LoadOverridesYAML: %v", err)
	}
	if overrides["claude-opus-4-7"] != 999 {
		t.Fatalf("override = %d, want 999", overrides["claude-opus-4-7"])
	}
	if overrides["my-model"] != 1234 {
		t.Fatalf("override my-model = %d, want 1234", overrides["my-model"])
	}
}

func TestLoadOverridesYAMLMalformedFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("this: is: not: yaml::\n"), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	if _, err := compact.LoadOverridesYAML(path); err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

func TestFallbackContextLimitConstant(t *testing.T) {
	if compact.FallbackContextLimit != 32_000 {
		t.Fatalf("FallbackContextLimit = %d, want 32000", compact.FallbackContextLimit)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./tests/compact -run 'TestLookupModelLimit|TestLoadOverridesYAML|TestFallbackContextLimitConstant' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement model-limit lookup and YAML loader**

Create `internal/compact/modellimits.go`:

```go
package compact

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// FallbackContextLimit is used when a model is in neither the built-in
// registry nor the user-supplied override map. 32k is conservative
// enough that even the smallest legacy models won't trip a 4xx.
const FallbackContextLimit = 32_000

// defaultModelLimits maps model id -> total context-window size in
// tokens. Add new entries here as providers ship new models. Keep keys
// matching the exact strings the CLI passes via --model.
var defaultModelLimits = map[string]int{
	"claude-opus-4-7":           200_000,
	"claude-opus-4-7[1m]":       1_000_000,
	"claude-sonnet-4-6":         200_000,
	"claude-haiku-4-5-20251001": 200_000,
	"gpt-4o":                    128_000,
	"gpt-4o-mini":               128_000,
}

// LookupModelLimit resolves the total context-window for the given
// model id. Overrides win over the built-in registry. The second
// return is false iff the model is in neither map; callers fall back
// to FallbackContextLimit.
func LookupModelLimit(model string, overrides map[string]int) (int, bool) {
	if v, ok := overrides[model]; ok {
		return v, true
	}
	v, ok := defaultModelLimits[model]
	return v, ok
}

// LoadOverridesYAML parses a YAML file shaped as `{model_id: limit}`.
// A missing file is NOT an error (returns empty map) so the override
// file is genuinely optional. Malformed YAML IS an error, surfaced at
// NewCompactor time so silent ignore-and-continue cannot drop user
// intent.
func LoadOverridesYAML(path string) (map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]int{}, nil
		}
		return nil, fmt.Errorf("read model-limits override %q: %w", path, err)
	}
	out := map[string]int{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse model-limits override %q: %w", path, err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run: `go test ./tests/compact -run 'TestLookupModelLimit|TestLoadOverridesYAML|TestFallbackContextLimitConstant' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/compact/modellimits.go tests/compact/modellimits_test.go
git commit -m "feat(compact): per-model context-limit registry with YAML override"
```

---

### Task 4: Budget function

**Files:**
- Create: `internal/compact/budget.go`
- Create: `tests/compact/budget_test.go`

- [ ] **Step 1: Write the failing tests**

Create `tests/compact/budget_test.go`:

```go
package compact_test

import (
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/provider"
)

func TestBudgetKnownModelSubtractsOutputCap(t *testing.T) {
	got := compact.Budget(compact.BudgetInput{
		Model:        "claude-opus-4-7",
		OutputCap:    4096,
		SafetyFactor: 0.75,
	})
	// 200000*0.75 - 4096 = 150000 - 4096 = 145904
	if got != 145_904 {
		t.Fatalf("budget = %d, want 145904", got)
	}
}

func TestBudgetUnknownModelUsesFallback(t *testing.T) {
	got := compact.Budget(compact.BudgetInput{
		Model:        "wat",
		OutputCap:    1000,
		SafetyFactor: 0.75,
	})
	// 32000*0.75 - 1000 = 24000 - 1000 = 23000
	if got != 23_000 {
		t.Fatalf("budget = %d, want 23000", got)
	}
}

func TestBudgetOverrideMapUsed(t *testing.T) {
	got := compact.Budget(compact.BudgetInput{
		Model:        "tiny",
		OutputCap:    0,
		SafetyFactor: 1.0,
		Overrides:    map[string]int{"tiny": 1000},
	})
	if got != 1000 {
		t.Fatalf("budget = %d, want 1000", got)
	}
}

func TestBudgetUsageIgnoredForReservation(t *testing.T) {
	// LastUsage is informational only; the budget reserves OutputCap
	// for the worst case we'll request next turn.
	a := compact.Budget(compact.BudgetInput{
		Model:        "claude-opus-4-7",
		OutputCap:    4096,
		SafetyFactor: 0.75,
		LastUsage:    provider.Usage{InputTokens: 50_000, OutputTokens: 2000},
	})
	b := compact.Budget(compact.BudgetInput{
		Model:        "claude-opus-4-7",
		OutputCap:    4096,
		SafetyFactor: 0.75,
		LastUsage:    provider.Usage{},
	})
	if a != b {
		t.Fatalf("LastUsage changed budget: %d vs %d", a, b)
	}
}

func TestBudgetClampsNegativeToZero(t *testing.T) {
	// Hostile inputs: tiny model, huge output cap -> negative naive result.
	got := compact.Budget(compact.BudgetInput{
		Model:        "wat",
		OutputCap:    1_000_000,
		SafetyFactor: 0.75,
	})
	if got != 0 {
		t.Fatalf("budget = %d, want clamp-to-zero", got)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./tests/compact -run TestBudget -count=1`
Expected: FAIL — `BudgetInput`, `Budget` don't exist; also `provider.Usage` doesn't exist yet either, so this test temporarily uses a stub. NOTE: Task 5 below introduces `provider.Usage`; if the test won't even compile, add a temporary local stub by creating a one-line file `tests/compact/_provider_stub_test.go` that declares a local Usage type — but the cleaner sequencing is to skip the `provider.Usage` import in this task and re-add the test case in Task 5. The implementation below uses `provider.Usage` so the package itself compiles. The simplest path: do Task 5 immediately before running Step 2; the tests above already cite `provider.Usage`.

Resolution: this plan executes tasks in order. Run Task 5's "Step 3: change `provider/interface.go`" BEFORE running Step 2 of Task 4. The two tasks are interleaved in the natural dependency order.

- [ ] **Step 3: Implement Budget**

Create `internal/compact/budget.go`:

```go
package compact

import (
	"github.com/snowshine0216/penelope-agent/internal/provider"
)

// BudgetInput captures the inputs to Budget. LastUsage is informational
// only — the budget reserves OutputCap for the worst case we may
// request next turn rather than the last actual output count.
type BudgetInput struct {
	Model        string
	LastUsage    provider.Usage     // zero on first turn
	OutputCap    int                // == --max-tokens (default 4096 for Claude)
	SafetyFactor float64            // default 0.75
	Overrides    map[string]int     // optional model -> limit override
}

// Budget returns the input-token ceiling the compactor must keep below.
// The provider hard-fails if `input + output` exceeds the window so we
// subtract OutputCap (worst-case output we will request) rather than
// LastUsage.OutputTokens (what we got back last turn).
//
// Negative results are clamped to zero so a hostile flag combination
// (huge output cap on a tiny model) yields a documented "everything
// gets compacted" rather than a Go-side negative-int landmine.
func Budget(in BudgetInput) int {
	limit, ok := LookupModelLimit(in.Model, in.Overrides)
	if !ok {
		limit = FallbackContextLimit
	}
	safety := in.SafetyFactor
	if safety <= 0 {
		safety = 0.75
	}
	v := int(float64(limit)*safety) - in.OutputCap
	if v < 0 {
		return 0
	}
	return v
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run: `go test ./tests/compact -run TestBudget -count=1`
Expected: PASS (requires Task 5 to be merged first so `provider.Usage` exists).

- [ ] **Step 5: Commit**

```bash
git add internal/compact/budget.go tests/compact/budget_test.go
git commit -m "feat(compact): adaptive token budget derived from model limit"
```

---

### Task 5: Provider interface returns `*Response{Message, Usage}`

**Files:**
- Modify: `internal/provider/interface.go`
- Modify: `internal/provider/claude.go`
- Modify: `internal/provider/openai.go`
- Modify: `internal/engine/loop.go` (callsite only — minimal: unwrap `.Message`)
- Modify: `tests/engine/loop_test.go` (the `fakeProvider` test double)
- Modify: every other test file that defines a fake provider
- Create: `tests/provider/usage_test.go`

This is a breaking change to the provider contract. Touching it now (before the compactor uses it) means later tasks don't need adapter shims.

- [ ] **Step 1: Write failing test that asserts on Usage round-trip via the fake provider**

Create `tests/provider/usage_test.go`:

```go
package provider_test

import (
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/provider"
)

func TestUsageZeroValueIsAllZeros(t *testing.T) {
	u := provider.Usage{}
	if u.InputTokens != 0 || u.OutputTokens != 0 {
		t.Fatalf("zero Usage non-zero: %+v", u)
	}
}

func TestResponseHoldsBothMessageAndUsage(t *testing.T) {
	r := &provider.Response{
		Usage: provider.Usage{InputTokens: 1234, OutputTokens: 56},
	}
	if r.Usage.InputTokens != 1234 || r.Usage.OutputTokens != 56 {
		t.Fatalf("usage round-trip failed: %+v", r.Usage)
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

Run: `go test ./tests/provider -run TestUsage -count=1`
Expected: FAIL — `provider.Usage` and `provider.Response` don't exist.

- [ ] **Step 3: Rewrite the interface**

Replace `internal/provider/interface.go` with:

```go
package provider

import (
	"context"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// Usage captures per-request token counts surfaced by the provider.
// Zero values mean the provider did not return usage; callers must
// treat that as "unknown" rather than "zero".
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Response is the full Generate return: the next assistant message
// plus the provider-reported token usage.
type Response struct {
	Message *schema.Message
	Usage   Usage
}

// LLMProvider is the provider-neutral contract. Implementations must
// always return a non-nil Response on success (Message may be empty
// if the model produced no content).
type LLMProvider interface {
	Generate(ctx context.Context, messages []schema.Message, availableTools []schema.ToolDefinition) (*Response, error)
}
```

- [ ] **Step 4: Update `internal/provider/claude.go` Generate to populate Usage**

In `internal/provider/claude.go`, change the `Generate` signature and body:

```go
func (p *ClaudeProvider) Generate(ctx context.Context, msgs []schema.Message, availableTools []schema.ToolDefinition) (*Response, error) {
	anthropicMsgs, systemPrompt, err := translateMessagesToAnthropic(msgs)
	if err != nil {
		return nil, err
	}

	anthropicTools, err := translateToolsToAnthropic(availableTools)
	if err != nil {
		return nil, err
	}

	maxTokens := p.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: maxTokens,
		Messages:  anthropicMsgs,
	}

	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{{Text: systemPrompt}}
	}
	if len(anthropicTools) > 0 {
		params.Tools = anthropicTools
	}

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("claude API request failed: %w", err)
	}

	resultMsg := &schema.Message{Role: schema.RoleAssistant}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			resultMsg.Content += block.Text
		case "tool_use":
			argsBytes, err := json.Marshal(block.Input)
			if err != nil {
				return nil, fmt.Errorf("encode tool call %s input: %w", block.Name, err)
			}
			resultMsg.ToolCalls = append(resultMsg.ToolCalls, schema.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: argsBytes,
			})
		}
	}

	return &Response{
		Message: resultMsg,
		Usage: Usage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
		},
	}, nil
}
```

- [ ] **Step 5: Update `internal/provider/openai.go` Generate to populate Usage**

In `internal/provider/openai.go`, change the `Generate` signature and body. The OpenAI SDK's `resp.Usage` exposes `PromptTokens` / `CompletionTokens`:

```go
func (p *OpenAIProvider) Generate(ctx context.Context, msgs []schema.Message, availableTools []schema.ToolDefinition) (*Response, error) {
	openaiMsgs, err := translateMessagesToOpenAI(msgs)
	if err != nil {
		return nil, err
	}

	openaiTools, err := translateToolsToOpenAI(availableTools)
	if err != nil {
		return nil, err
	}

	params := openai.ChatCompletionNewParams{
		Model:    p.model,
		Messages: openaiMsgs,
	}
	if len(openaiTools) > 0 {
		params.Tools = openaiTools
	}

	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai API request failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai API returned empty choices")
	}

	choice := resp.Choices[0].Message
	resultMsg := &schema.Message{
		Role:    schema.RoleAssistant,
		Content: choice.Content,
	}

	for _, tc := range choice.ToolCalls {
		switch tc.Type {
		case "function":
			resultMsg.ToolCalls = append(resultMsg.ToolCalls, schema.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: []byte(tc.Function.Arguments),
			})
		default:
			return nil, fmt.Errorf("unsupported tool call type from OpenAI: %q", tc.Type)
		}
	}

	return &Response{
		Message: resultMsg,
		Usage: Usage{
			InputTokens:  int(resp.Usage.PromptTokens),
			OutputTokens: int(resp.Usage.CompletionTokens),
		},
	}, nil
}
```

- [ ] **Step 6: Update every engine callsite that calls `.Generate` to unwrap `.Message`**

In `internal/engine/loop.go`, every `provider.Generate` call must change. Find each `resp, err := e.provider.Generate(...)` (or similar) and replace the downstream usage. There are two callsites today (think phase and act phase):

```go
// THINK PHASE
thinkResp, err := e.provider.Generate(ctx, view, nil)
if err != nil {
	return fmt.Errorf("think phase: %w", err)
}
if thinkResp.Message != nil && thinkResp.Message.Content != "" {
	report.OnThinking(ctx)
	view = append(view, *thinkResp.Message)
}

// ACT PHASE
actionResp, err := e.provider.Generate(ctx, view, availableTools)
if err != nil {
	return fmt.Errorf("act phase: %w", err)
}
actionMsg := actionResp.Message
e.lastUsage = actionResp.Usage // (lastUsage field added in Task 11)

if err := sess.Append(*actionMsg); err != nil {
	return fmt.Errorf("persist act response: %w", err)
}

if actionMsg.Content != "" {
	report.OnMessage(ctx, actionMsg.Content)
}

if len(actionMsg.ToolCalls) == 0 {
	log.Println("[engine] no tool calls, task complete")
	break
}
// ... etc
```

(The `e.lastUsage` field is introduced in Task 11; for this task, store it in a temporary local var or leave a `_ = actionResp.Usage` placeholder until Task 11 lands.)

- [ ] **Step 7: Update every fake-provider test double**

Find every `fakeProvider`-shaped type with a `Generate` method. The current `tests/engine/loop_test.go` has one; check whether `tests/engine/parallel_tool_execution_test.go`, `tests/engine/loop_gaps_test.go`, `tests/engine/engine_gaps_test.go`, `tests/engine/session_integration_test.go`, and `tests/engine/reporter_test.go` define more.

For each, change:

```go
func (f *fakeProvider) Generate(...) (*schema.Message, error) {
```

to:

```go
func (f *fakeProvider) Generate(_ context.Context, msgs []schema.Message, t []schema.ToolDefinition) (*provider.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.calls >= len(f.responses) {
		return nil, errors.New("fakeProvider: ran out of canned responses")
	}
	msgsCopy := append([]schema.Message(nil), msgs...)
	toolsCopy := append([]schema.ToolDefinition(nil), t...)
	f.receivedMsgs = append(f.receivedMsgs, msgsCopy)
	f.receivedTools = append(f.receivedTools, toolsCopy)
	resp := f.responses[f.calls]
	f.calls++
	return &provider.Response{Message: &resp, Usage: provider.Usage{}}, nil
}
```

Add `"github.com/snowshine0216/penelope-agent/internal/provider"` to the import block.

- [ ] **Step 8: Verify everything builds and existing tests still pass**

Run:

```bash
go build ./...
go test ./tests/provider -count=1
go test ./tests/engine -count=1
go test ./... -count=1
```

Expected: PASS across every package. Any compile error is a missed test double — fix it before committing.

- [ ] **Step 9: Commit**

```bash
git add internal/provider/interface.go internal/provider/claude.go internal/provider/openai.go internal/engine/loop.go tests/engine tests/provider/usage_test.go
git commit -m "feat(provider): Generate returns Response with Usage"
```

---

### Task 6: EWMA Calibrator

**Files:**
- Create: `internal/compact/calibration.go`
- Create: `tests/compact/calibration_test.go`

- [ ] **Step 1: Write the failing tests**

Create `tests/compact/calibration_test.go`:

```go
package compact_test

import (
	"math"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
)

func TestNewCalibratorStartsAtRatioOne(t *testing.T) {
	c := compact.NewCalibrator(0.3)
	if c.Ratio() != 1.0 {
		t.Fatalf("initial ratio = %v, want 1.0", c.Ratio())
	}
}

func TestCalibratorPredictAtRatioOne(t *testing.T) {
	c := compact.NewCalibrator(0.3)
	if c.Predict(1000) != 1000 {
		t.Fatalf("predict at ratio 1 = %d, want 1000", c.Predict(1000))
	}
}

func TestCalibratorObserveZeroProviderIsIgnored(t *testing.T) {
	c := compact.NewCalibrator(0.3)
	c.Observe(1000, 0)
	if c.Ratio() != 1.0 {
		t.Fatalf("ratio after zero observe = %v, want 1.0", c.Ratio())
	}
}

func TestCalibratorObserveZeroLocalIsIgnored(t *testing.T) {
	c := compact.NewCalibrator(0.3)
	c.Observe(0, 1500)
	if c.Ratio() != 1.0 {
		t.Fatalf("ratio after zero local observe = %v, want 1.0", c.Ratio())
	}
}

func TestCalibratorEWMAConverges(t *testing.T) {
	c := compact.NewCalibrator(0.3)
	// Local estimate is consistently low: provider reports 1.5x more.
	for range 20 {
		c.Observe(1000, 1500)
	}
	if math.Abs(c.Ratio()-1.5) > 0.01 {
		t.Fatalf("ratio after 20 obs = %v, want ~1.5", c.Ratio())
	}
}

func TestCalibratorPredictUsesRatio(t *testing.T) {
	c := compact.NewCalibrator(1.0) // alpha=1 means each observation overwrites
	c.Observe(1000, 1500)
	if c.Ratio() != 1.5 {
		t.Fatalf("ratio = %v, want 1.5", c.Ratio())
	}
	if c.Predict(2000) != 3000 {
		t.Fatalf("predict(2000) = %d, want 3000", c.Predict(2000))
	}
}

func TestCalibratorDefaultAlpha(t *testing.T) {
	c := compact.NewCalibrator(0) // 0 -> default 0.3
	if c.Alpha() != 0.3 {
		t.Fatalf("alpha = %v, want 0.3 default", c.Alpha())
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./tests/compact -run TestCalibrator -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement the calibrator**

Create `internal/compact/calibration.go`:

```go
package compact

// Calibrator tracks the EWMA-smoothed ratio between the provider's
// reported InputTokens and our local chars/4 estimate. The ratio
// resets to 1.0 every session (the spec is explicit that cross-session
// learning is a non-goal) and converges in 2-3 turns under steady
// observation. NOT goroutine-safe — call from a single goroutine
// (the engine main loop in practice).
type Calibrator struct {
	ratio float64
	alpha float64
}

// NewCalibrator returns a calibrator with the supplied EWMA weight.
// alpha=0 (or negative) falls back to 0.3, which gives ~95% influence
// after ~10 observations — fast enough to track a real tokenizer,
// slow enough to ignore a single outlier turn.
func NewCalibrator(alpha float64) *Calibrator {
	if alpha <= 0 {
		alpha = 0.3
	}
	return &Calibrator{ratio: 1.0, alpha: alpha}
}

// Ratio returns the current EWMA-smoothed multiplier.
func (c *Calibrator) Ratio() float64 { return c.ratio }

// Alpha returns the EWMA weight; exposed for tests / debugging.
func (c *Calibrator) Alpha() float64 { return c.alpha }

// Observe folds a single (localEstimate, providerActual) sample into
// the running ratio. Zeros on either side are ignored — they mean
// "no signal this turn" rather than "the real ratio is zero".
func (c *Calibrator) Observe(localEst, providerActual int) {
	if localEst <= 0 || providerActual <= 0 {
		return
	}
	sample := float64(providerActual) / float64(localEst)
	c.ratio = c.alpha*sample + (1-c.alpha)*c.ratio
}

// Predict converts a local estimate into a predicted provider count.
// Rounded to nearest int.
func (c *Calibrator) Predict(localEst int) int {
	return int(float64(localEst)*c.ratio + 0.5)
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run: `go test ./tests/compact -run TestCalibrator -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/compact/calibration.go tests/compact/calibration_test.go
git commit -m "feat(compact): EWMA calibrator for local vs provider token drift"
```

---

### Task 7: `tools.TruncateWithMarker` refactor

**Files:**
- Modify: `internal/tools/truncate.go`
- Modify: `tests/tools/truncate_test.go`

`TruncateForLLM` today hard-codes its elision marker. We refactor it minimally to accept a caller-supplied marker so the engine boundary can use a spill-aware marker. Existing callers and their tests must still pass unchanged.

- [ ] **Step 1: Add failing test for the new function**

Append to `tests/tools/truncate_test.go`:

```go
func TestTruncateWithMarkerCustomMarker(t *testing.T) {
	in := strings.Repeat("x", 2000)
	out := tools.TruncateWithMarker(in, 200, "...[SPILLED to /tmp/foo.txt]...")
	if !strings.Contains(out, "SPILLED to /tmp/foo.txt") {
		t.Fatalf("marker missing from output: %q", out)
	}
	if len(out) >= len(in) {
		t.Fatalf("output not truncated: %d >= %d", len(out), len(in))
	}
}

func TestTruncateWithMarkerShortInputUnchanged(t *testing.T) {
	in := "small"
	out := tools.TruncateWithMarker(in, 200, "ignored")
	if out != in {
		t.Fatalf("short input changed: %q -> %q", in, out)
	}
}

func TestTruncateForLLMStillWorksAsThinWrapper(t *testing.T) {
	in := strings.Repeat("x", 2000)
	out := tools.TruncateForLLM(in, 200)
	if !strings.Contains(out, "elided") {
		t.Fatalf("default marker missing: %q", out)
	}
}
```

If the `strings` import is not yet in the test file, add it.

- [ ] **Step 2: Run tests and verify the new ones fail**

Run: `go test ./tests/tools -run TestTruncateWithMarker -count=1`
Expected: FAIL — `TruncateWithMarker` doesn't exist.

- [ ] **Step 3: Refactor truncate.go**

Replace `internal/tools/truncate.go` with:

```go
package tools

import (
	"fmt"
	"unicode/utf8"
)

// TruncateForLLM returns s unchanged when it fits in maxBytes, otherwise
// returns a head+tail elision with the project's standard marker.
// Existing callers rely on the standard marker text; new callers that
// need a different marker should use TruncateWithMarker directly.
func TruncateForLLM(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	standardMarker := func(elided int) string {
		return fmt.Sprintf("\n\n...[%d bytes elided of %d total]...\n\n", elided, len(s))
	}
	return truncateInternal(s, maxBytes, standardMarker)
}

// TruncateWithMarker is the general form: caller supplies the marker
// string. The marker is inserted between the head and tail slices, and
// the head/tail cuts are backed up to the nearest valid UTF-8 rune
// boundary so the result is always valid UTF-8.
func TruncateWithMarker(s string, maxBytes int, marker string) string {
	if len(s) <= maxBytes {
		return s
	}
	return truncateInternal(s, maxBytes, func(_ int) string { return marker })
}

func truncateInternal(s string, maxBytes int, markerFn func(elided int) string) string {
	if maxBytes <= 0 {
		return markerFn(len(s))
	}
	half := maxBytes / 2
	headEnd := safeRuneBoundaryDown(s, half)
	tailStart := safeRuneBoundaryUp(s, len(s)-half)
	if tailStart <= headEnd {
		tailStart = headEnd
	}
	elided := tailStart - headEnd
	return s[:headEnd] + markerFn(elided) + s[tailStart:]
}

func safeRuneBoundaryDown(s string, max int) int {
	if max >= len(s) {
		return len(s)
	}
	for i := max; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return i
		}
	}
	return 0
}

func safeRuneBoundaryUp(s string, min int) int {
	if min <= 0 {
		return 0
	}
	if min >= len(s) {
		return len(s)
	}
	for i := min; i < len(s); i++ {
		if utf8.RuneStart(s[i]) {
			return i
		}
	}
	return len(s)
}
```

- [ ] **Step 4: Run all tools tests and verify they pass**

Run: `go test ./tests/tools -count=1`
Expected: PASS — old `TruncateForLLM` behaviour preserved by the wrapper.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/truncate.go tests/tools/truncate_test.go
git commit -m "refactor(tools): split TruncateWithMarker from TruncateForLLM"
```

---

### Task 8: Layer A — structural shrink

**Files:**
- Create: `internal/compact/shrink.go`
- Create: `tests/compact/shrink_test.go`

Layer A is the per-message structural shrink: tool results over `MaxToolBytes` get head/tail truncation with a spill-aware marker; `write_file` / `edit_file` `tool_calls` arguments outside the verbatim window get their large fields stripped. Pure function; no I/O.

- [ ] **Step 1: Write the failing tests**

Create `tests/compact/shrink_test.go`:

```go
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
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./tests/compact -run TestShrink -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement Layer A**

Create `internal/compact/shrink.go`:

```go
package compact

import (
	"encoding/json"
	"fmt"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// ShrinkConfig parameterises Layer A.
type ShrinkConfig struct {
	MaxToolBytes        int // tool result truncation threshold (default 65536)
	RecentTurnsVerbatim int // last N turns skip write_file / edit_file arg stripping
}

// ShrinkStats summarises what Layer A did. The Compactor wraps this
// into the public CompactStats.
type ShrinkStats struct {
	ToolResultsTruncated int
	ToolCallArgsStripped int
}

// ShrinkApply runs the Layer A passes. Pure: input is not mutated.
// Returns a fresh slice.
func ShrinkApply(history []schema.Message, cfg ShrinkConfig) ([]schema.Message, ShrinkStats) {
	if cfg.MaxToolBytes <= 0 {
		cfg.MaxToolBytes = 65536
	}
	out := make([]schema.Message, len(history))
	copy(out, history)

	// Determine which message indices are inside the verbatim tail.
	// A "turn" starts at each user message. We walk backwards and
	// count user messages until we have RecentTurnsVerbatim.
	verbatimStart := verbatimStartIndex(out, cfg.RecentTurnsVerbatim)

	stats := ShrinkStats{}
	for i := range out {
		switch out[i].Role {
		case schema.RoleTool:
			if len(out[i].Content) > cfg.MaxToolBytes {
				marker := fmt.Sprintf(
					"\n\n...[%d bytes elided of %d total for call_id=%s; "+
						"use read_tool_output(call_id=%q, start_line=N, line_count=M) to read more]...\n\n",
					len(out[i].Content), len(out[i].Content), out[i].ToolCallID, out[i].ToolCallID,
				)
				out[i].Content = tools.TruncateWithMarker(out[i].Content, cfg.MaxToolBytes, marker)
				stats.ToolResultsTruncated++
			}
		case schema.RoleAssistant:
			if i >= verbatimStart {
				continue
			}
			for j := range out[i].ToolCalls {
				if !isLargeArgTool(out[i].ToolCalls[j].Name) {
					continue
				}
				stripped, changed := stripLargeArgs(out[i].ToolCalls[j].Arguments)
				if changed {
					out[i].ToolCalls[j].Arguments = stripped
					stats.ToolCallArgsStripped++
				}
			}
		}
	}
	return out, stats
}

func isLargeArgTool(name string) bool {
	return name == "write_file" || name == "edit_file"
}

// stripLargeArgs reads the raw JSON args, replaces `content`,
// `new_string`, `old_string` with `"<content elided: N bytes>"` if
// they are larger than 256 bytes, and re-marshals. Returns the new
// raw bytes and whether anything changed.
func stripLargeArgs(args json.RawMessage) (json.RawMessage, bool) {
	const threshold = 256
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return args, false
	}
	changed := false
	for _, key := range []string{"content", "new_string", "old_string"} {
		v, ok := m[key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		if len(s) > threshold {
			m[key] = fmt.Sprintf("<content elided: %d bytes>", len(s))
			changed = true
		}
	}
	if !changed {
		return args, false
	}
	out, err := json.Marshal(m)
	if err != nil {
		return args, false
	}
	return out, true
}

// verbatimStartIndex returns the index of the first message inside
// the verbatim tail (recentTurns user-message-bounded turns from the
// end). Returns len(history) if recentTurns <= 0 (no verbatim window).
func verbatimStartIndex(history []schema.Message, recentTurns int) int {
	if recentTurns <= 0 {
		return len(history)
	}
	userCount := 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == schema.RoleUser {
			userCount++
			if userCount == recentTurns {
				return i
			}
		}
	}
	return 0
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run: `go test ./tests/compact -run TestShrink -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/compact/shrink.go tests/compact/shrink_test.go
git commit -m "feat(compact): Layer A structural shrink"
```

---

### Task 9: Layer B — rolling deterministic digest

**Files:**
- Create: `internal/compact/digest.go`
- Create: `tests/compact/digest_test.go`
- Create: `tests/compact/testdata/digest/turns-mixed.golden.txt`

Layer B engages only when Layer A is not enough. It folds the oldest non-verbatim turns into a single synthetic assistant message inserted at position 1 (after system, before the verbatim tail). The digest grows backward — oldest first — until the view fits. Pure function: no I/O, no model, no allocation of shared state.

The output format must match the spec §1 Layer B example verbatim so the model has a stable contract: one line per user/assistant turn (truncated to ~120 chars), one indented bullet per tool call with name, key arg, outcome, and (for spilled outputs) `call_id` so the model can fetch the body via `read_tool_output`.

- [ ] **Step 1: Write the failing tests**

Create `tests/compact/digest_test.go`:

```go
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
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./tests/compact -run TestFold -count=1`
Expected: FAIL — `compact.Fold` does not exist; the golden file is not yet present.

- [ ] **Step 3: Implement Layer B**

Create `internal/compact/digest.go`:

```go
package compact

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// digestTextLimit caps per-turn text bodies in the digest. The model
// only needs the gist; a 120-char head + ellipsis preserves enough to
// recall what happened without bloating the digest.
const digestTextLimit = 120

// callIDRe matches the call_id=... marker that Layer A embeds in
// truncated tool results. The digest re-extracts it so the model can
// see the call_id again at-a-glance.
var callIDRe = regexp.MustCompile(`call_id=([A-Za-z0-9_\-]+)`)

// Fold runs Layer B. It folds the oldest non-verbatim turns into a
// single synthetic assistant message inserted at position 1 (after the
// system message, which lives at index 0 when the engine assembles the
// final view; from this function's perspective the slice has no system
// message and the digest is inserted at index 0). Returns the new
// slice and the number of turns folded.
//
// recentTurnsVerbatim turns at the tail are preserved unchanged. The
// digest grows backward — oldest first — until the result fits the
// budget. If even (digest + verbatim tail) exceeds the budget the
// caller (Compactor.View) is responsible for the emergency floor.
//
// Pure: input is never mutated. calibrator may be nil; if provided it
// converts local estimates to predicted provider counts before the
// budget check, giving the fold loop a tighter target.
func Fold(viewA []schema.Message, budget int, recentTurnsVerbatim int, calibrator *Calibrator) ([]schema.Message, int) {
	if len(viewA) == 0 {
		return nil, 0
	}
	if predict(calibrator, EstimateTokens(viewA)) <= budget {
		return CloneMessages(viewA), 0
	}

	turns := splitIntoTurns(viewA)
	if len(turns) == 0 {
		return CloneMessages(viewA), 0
	}

	// Determine where the verbatim window begins (by turn index).
	verbatimStart := len(turns) - recentTurnsVerbatim
	if verbatimStart < 0 {
		verbatimStart = 0
	}

	// Grow the fold window backward: fold turns [0..foldEnd).
	for foldEnd := 1; foldEnd <= verbatimStart; foldEnd++ {
		folded := assembleFolded(turns, foldEnd, verbatimStart)
		if predict(calibrator, EstimateTokens(folded)) <= budget {
			return folded, foldEnd
		}
	}
	// Could not fit even after folding every non-verbatim turn.
	// Return the maximally-folded view; emergency floor is the
	// Compactor's job, not the digest's.
	if verbatimStart > 0 {
		return assembleFolded(turns, verbatimStart, verbatimStart), verbatimStart
	}
	return CloneMessages(viewA), 0
}

// assembleFolded builds a view = [digest, ...turns[verbatimStart:]].
// turns[0..foldEnd) become the digest body; turns[foldEnd..verbatimStart)
// are dropped (they would have been folded but the caller chose a
// smaller window); turns[verbatimStart:] are preserved verbatim.
func assembleFolded(turns [][]schema.Message, foldEnd, verbatimStart int) []schema.Message {
	digest := buildDigest(turns[:foldEnd])
	out := []schema.Message{{Role: schema.RoleAssistant, Content: digest}}
	for _, t := range turns[verbatimStart:] {
		out = append(out, t...)
	}
	return out
}

// splitIntoTurns groups messages into turn boundaries. A turn starts
// at each user message and includes every following non-user message
// until the next user message. The leading slice before the first
// user message (system-pre-pended views never hit this path, but
// belt-and-suspenders) becomes turn 0.
func splitIntoTurns(msgs []schema.Message) [][]schema.Message {
	var turns [][]schema.Message
	var cur []schema.Message
	for _, m := range msgs {
		if m.Role == schema.RoleUser && len(cur) > 0 {
			turns = append(turns, cur)
			cur = nil
		}
		cur = append(cur, m)
	}
	if len(cur) > 0 {
		turns = append(turns, cur)
	}
	return turns
}

// buildDigest renders the spec §1 Layer B example format.
func buildDigest(turns [][]schema.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Prior session digest (turns 1..%d, compacted)\n\n", len(turns))
	for i, t := range turns {
		turnNum := i + 1
		for _, m := range t {
			switch m.Role {
			case schema.RoleUser:
				fmt.Fprintf(&b, "Turn %d — user: %q\n", turnNum, clipText(m.Content))
			case schema.RoleAssistant:
				if m.Content != "" {
					fmt.Fprintf(&b, "Turn %d — assistant: %s\n", turnNum, clipText(m.Content))
				}
				if len(m.ToolCalls) > 0 {
					fmt.Fprintf(&b, "Turn %d — assistant: tools:\n", turnNum)
					for _, call := range m.ToolCalls {
						fmt.Fprintf(&b, "  • %s\n", formatCall(call, t))
					}
				}
			}
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// formatCall renders one tool call as "name(key=value) → outcome [call_id=...]".
// outcome is derived from the matching tool result in the same turn.
func formatCall(call schema.ToolCall, turn []schema.Message) string {
	key := summariseArgs(call.Name, call.Arguments)
	outcome := lookupOutcome(call.ID, turn)
	if id := callIDFromOutcome(outcome); id != "" {
		return fmt.Sprintf("%s(%s) → %s; call_id=%s", call.Name, key, summariseOutcome(outcome), id)
	}
	return fmt.Sprintf("%s(%s) → %s", call.Name, key, summariseOutcome(outcome))
}

// summariseArgs picks the most-informative single field from the
// arguments JSON: path / command / pattern. Returns "" if none apply.
func summariseArgs(name string, args json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	for _, key := range []string{"path", "command", "pattern", "url"} {
		if v, ok := m[key]; ok {
			s := fmt.Sprintf("%v", v)
			return clipText(s)
		}
	}
	return ""
}

// lookupOutcome returns the tool-result Content for the given call ID
// within the same turn, or "" if not present.
func lookupOutcome(id string, turn []schema.Message) string {
	for _, m := range turn {
		if m.Role == schema.RoleTool && m.ToolCallID == id {
			return m.Content
		}
	}
	return ""
}

// summariseOutcome produces a short one-line summary of the tool result.
// For typical small outputs we return the first line clipped; for
// truncated outputs Layer A's marker provides "lines spilled" detail.
func summariseOutcome(outcome string) string {
	if outcome == "" {
		return "(no result)"
	}
	if strings.Contains(outcome, "bytes elided") {
		return "(elided; see call_id)"
	}
	line := outcome
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	return clipText(line)
}

// callIDFromOutcome extracts call_id=... from a truncated outcome
// marker if Layer A left one. Returns "" when absent.
func callIDFromOutcome(outcome string) string {
	matches := callIDRe.FindStringSubmatch(outcome)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// clipText caps a body line to digestTextLimit runes with an ellipsis
// suffix when truncated.
func clipText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= digestTextLimit {
		return s
	}
	return s[:digestTextLimit] + "..."
}

// predict applies the calibrator's ratio to a local estimate. nil
// calibrator means ratio=1.0 (no calibration data yet).
func predict(c *Calibrator, localEst int) int {
	if c == nil {
		return localEst
	}
	return c.Predict(localEst)
}
```

- [ ] **Step 4: Generate the golden file**

```bash
UPDATE_GOLDEN=1 go test ./tests/compact -run TestFoldFormatGoldenTurnsMixed -count=1
```

Inspect `tests/compact/testdata/digest/turns-mixed.golden.txt` and confirm it looks like the spec §1 example (one line per user/assistant turn, indented bullets per tool call, `call_id=c2` present on the elided result).

- [ ] **Step 5: Run all digest tests and verify they pass**

Run: `go test ./tests/compact -run TestFold -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/compact/digest.go tests/compact/digest_test.go tests/compact/testdata/digest/turns-mixed.golden.txt
git commit -m "feat(compact): Layer B rolling deterministic digest"
```

---

### Task 10: `CompactStats` type

**Files:**
- Create: `internal/compact/stats.go`
- Create: `tests/compact/stats_test.go`

A tiny carrier type for what one compaction did. Lives in its own file so other packages (`engine/reporter`, `session/audit`) can import it without dragging in shrink/digest dependencies. No logic — just the struct, a constructor helper, and a JSON round-trip test so the audit log format is locked in early.

- [ ] **Step 1: Write the failing test**

Create `tests/compact/stats_test.go`:

```go
package compact_test

import (
	"encoding/json"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
)

func TestCompactStatsJSONRoundTrip(t *testing.T) {
	in := compact.CompactStats{
		Turn:               7,
		Before:             48_210,
		AfterLayerA:        48_000,
		AfterLayerB:        47_920,
		Budget:             100_000,
		Saved:              290,
		LayerBEngaged:      true,
		TurnsFolded:        3,
		ToolOutputsSpilled: 2,
		CalibratorRatio:    1.07,
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out compact.CompactStats
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round trip mismatch:\nin =%+v\nout=%+v", in, out)
	}
}

func TestNewCompactStatsDefaults(t *testing.T) {
	s := compact.NewCompactStats(3, 1000)
	if s.Turn != 3 {
		t.Fatalf("turn = %d, want 3", s.Turn)
	}
	if s.Before != 1000 {
		t.Fatalf("before = %d, want 1000", s.Before)
	}
	if s.AfterLayerA != 1000 || s.AfterLayerB != 1000 {
		t.Fatalf("after = (%d, %d), want both 1000 (default = no-op)", s.AfterLayerA, s.AfterLayerB)
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

Run: `go test ./tests/compact -run TestCompactStats -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement the type**

Create `internal/compact/stats.go`:

```go
package compact

// CompactStats captures what one compaction did for a single turn.
// Serialised verbatim to .claw/sessions/<id>/compact-events.jsonl when
// emission fires. Field names use JSON-friendly snake_case so the
// audit log is grep-friendly without a tags table.
type CompactStats struct {
	Turn               int     `json:"turn"`
	Before             int     `json:"before"`
	AfterLayerA        int     `json:"after_layer_a"`
	AfterLayerB        int     `json:"after_layer_b"`
	Budget             int     `json:"budget"`
	Saved              int     `json:"saved"`
	LayerBEngaged      bool    `json:"layer_b_engaged"`
	TurnsFolded        int     `json:"turns_folded"`
	ToolOutputsSpilled int     `json:"tool_outputs_spilled"`
	CalibratorRatio    float64 `json:"calibrator_ratio"`
}

// NewCompactStats returns a stats baseline that represents "no-op
// compaction at this token count". Compactor.View populates the rest
// as Layer A and Layer B run.
func NewCompactStats(turn, before int) CompactStats {
	return CompactStats{
		Turn:        turn,
		Before:      before,
		AfterLayerA: before,
		AfterLayerB: before,
	}
}
```

- [ ] **Step 4: Run test and verify it passes**

Run: `go test ./tests/compact -run TestCompactStats -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/compact/stats.go tests/compact/stats_test.go
git commit -m "feat(compact): CompactStats carrier type with JSON round-trip"
```

---

### Task 11: `Compactor.View` orchestration

**Files:**
- Create: `internal/compact/compactor.go`
- Create: `tests/compact/compactor_test.go`
- Modify: `internal/engine/loop.go` (add `lastUsage` field; do NOT wire compactor in this task — Task 14 does that)

This is the public surface of the package: a pure function that runs Layer A, checks the budget, runs Layer B if needed, applies an emergency floor if still over. Returns `(view []schema.Message, stats CompactStats)`. The function is the engine's only entry point into the compact package.

The engine needs an `e.lastUsage provider.Usage` field so Task 14 can thread the previous turn's input/output tokens into the next turn's budget. We add the field in this task as a forward-declaration so later tasks compile without churn.

- [ ] **Step 1: Write the failing tests**

Create `tests/compact/compactor_test.go`:

```go
package compact_test

import (
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

func TestCompactorViewUnderBudgetIsNoOp(t *testing.T) {
	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        65536,
		RecentTurnsVerbatim: 4,
	})
	in := []schema.Message{user("hi"), asst("hello")}
	view, stats := c.View(in, 100_000, 1 /* turn */, compact.NewCalibrator(0.3))
	if len(view) != len(in) {
		t.Fatalf("view changed under budget: %d vs %d", len(view), len(in))
	}
	if stats.LayerBEngaged {
		t.Fatalf("Layer B engaged under budget")
	}
	if stats.Before == 0 {
		t.Fatalf("stats.Before unset")
	}
}

func TestCompactorViewLayerASufficient(t *testing.T) {
	// A huge tool result fits the budget after Layer A truncates it.
	huge := strings.Repeat("x", 200_000)
	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        1000,
		RecentTurnsVerbatim: 4,
	})
	in := []schema.Message{
		user("u"),
		asst("", tc("a", "bash")),
		toolMsg("a", huge),
	}
	view, stats := c.View(in, 10_000, 1, compact.NewCalibrator(0.3))
	if stats.LayerBEngaged {
		t.Fatalf("Layer B engaged when A was sufficient: %+v", stats)
	}
	if stats.AfterLayerA >= stats.Before {
		t.Fatalf("Layer A did not shrink: before=%d after=%d", stats.Before, stats.AfterLayerA)
	}
	if len(view[2].Content) >= len(huge) {
		t.Fatalf("tool result not truncated")
	}
}

func TestCompactorViewLayerBEngaged(t *testing.T) {
	// Many turns, none too large individually, but the sum exceeds budget.
	in := []schema.Message{}
	for i := range 20 {
		in = append(in, user("u"+string(rune('0'+i))), asst("a"+string(rune('0'+i))))
	}
	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        65536,
		RecentTurnsVerbatim: 2,
	})
	view, stats := c.View(in, 50 /* very tight */, 5, compact.NewCalibrator(0.3))
	if !stats.LayerBEngaged {
		t.Fatalf("Layer B not engaged: %+v", stats)
	}
	if stats.TurnsFolded < 1 {
		t.Fatalf("no turns folded: %+v", stats)
	}
	// View[0] should now be the digest (after engine adds system at 0;
	// here we get view without system).
	if view[0].Role != schema.RoleAssistant || !strings.Contains(view[0].Content, "Prior session digest") {
		t.Fatalf("digest missing from view: %+v", view[0])
	}
}

func TestCompactorViewEmergencyFloorOverBudget(t *testing.T) {
	// Tight budget that even digest+verbatim cannot meet.
	huge := strings.Repeat("x", 1_000_000)
	in := []schema.Message{
		user("u1"), asst("a1"),
		user("u2"),
		asst("", tc("a", "bash")),
		toolMsg("a", huge),
	}
	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        100_000,
		RecentTurnsVerbatim: 1,
	})
	view, stats := c.View(in, 100 /* impossible */, 1, compact.NewCalibrator(0.3))
	// Emergency floor: at minimum the last user message is present.
	found := false
	for _, m := range view {
		if m.Role == schema.RoleUser && m.Content == "u2" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("emergency floor lost last user message: %+v", view)
	}
	// Stats still populated.
	if stats.Before == 0 {
		t.Fatalf("stats.Before unset: %+v", stats)
	}
}

func TestCompactorViewIsPure(t *testing.T) {
	in := []schema.Message{user("u"), asst("a")}
	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        65536,
		RecentTurnsVerbatim: 4,
	})
	_, _ = c.View(in, 100_000, 1, compact.NewCalibrator(0.3))
	if in[0].Content != "u" || in[1].Content != "a" {
		t.Fatalf("View mutated input: %+v", in)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./tests/compact -run TestCompactorView -count=1`
Expected: FAIL — `compact.Compactor`, `compact.Config`, `compact.NewCompactor` do not exist.

- [ ] **Step 3: Implement the compactor**

Create `internal/compact/compactor.go`:

```go
package compact

import (
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// Config carries the user-tunable knobs into the compactor. Pure
// data — no I/O handles here.
type Config struct {
	MaxToolBytes        int // tool-result truncation threshold (default 65536)
	RecentTurnsVerbatim int // last N user turns skip Layer A arg stripping (default 4)
	Overrides           map[string]int // optional model -> limit override map
}

// Compactor produces a read-time provider view from the canonical
// session history. View is the only public method.
type Compactor struct {
	cfg Config
}

// NewCompactor returns a Compactor with the given configuration.
// Defaults are filled in for any zero-valued field.
func NewCompactor(cfg Config) *Compactor {
	if cfg.MaxToolBytes <= 0 {
		cfg.MaxToolBytes = 65536
	}
	if cfg.RecentTurnsVerbatim <= 0 {
		cfg.RecentTurnsVerbatim = 4
	}
	return &Compactor{cfg: cfg}
}

// Config returns the active configuration (read-only; safe to share).
func (c *Compactor) Config() Config { return c.cfg }

// View runs the full pipeline:
//   - Layer A structural shrink (always).
//   - Budget check via calibrator.Predict.
//   - Layer B rolling digest if Layer A was not enough.
//   - Emergency floor: drop everything except the verbatim tail (and
//     synthesise a tail from the last user message if even that is gone)
//     when the digest + tail still exceeds budget.
//
// Pure: input history is never mutated; calibrator is consulted via
// Predict only — Observe is the caller's job after provider.Generate.
func (c *Compactor) View(history []schema.Message, budget, turn int, cal *Calibrator) ([]schema.Message, CompactStats) {
	cleaned := DefensiveCleanup(history)
	before := EstimateTokens(cleaned)
	stats := NewCompactStats(turn, before)
	if cal != nil {
		stats.CalibratorRatio = cal.Ratio()
	}

	// Layer A.
	shrunk, _ := ShrinkApply(cleaned, ShrinkConfig{
		MaxToolBytes:        c.cfg.MaxToolBytes,
		RecentTurnsVerbatim: c.cfg.RecentTurnsVerbatim,
	})
	stats.AfterLayerA = EstimateTokens(shrunk)

	if predict(cal, stats.AfterLayerA) <= budget {
		stats.AfterLayerB = stats.AfterLayerA
		stats.Saved = stats.Before - stats.AfterLayerB
		return shrunk, stats
	}

	// Layer B.
	folded, foldedTurns := Fold(shrunk, budget, c.cfg.RecentTurnsVerbatim, cal)
	stats.AfterLayerB = EstimateTokens(folded)
	stats.LayerBEngaged = foldedTurns > 0
	stats.TurnsFolded = foldedTurns
	stats.Saved = stats.Before - stats.AfterLayerB

	if predict(cal, stats.AfterLayerB) <= budget {
		return folded, stats
	}

	// Emergency floor: halve MaxToolBytes for the verbatim tail and
	// retry once with the same fold target. If still over budget, send
	// what we have — the provider will surface a clean 4xx.
	tightened := *c
	tightened.cfg.MaxToolBytes = c.cfg.MaxToolBytes / 2
	if tightened.cfg.MaxToolBytes < 1024 {
		tightened.cfg.MaxToolBytes = 1024
	}
	shrunk2, _ := ShrinkApply(cleaned, ShrinkConfig{
		MaxToolBytes:        tightened.cfg.MaxToolBytes,
		RecentTurnsVerbatim: c.cfg.RecentTurnsVerbatim,
	})
	folded2, foldedTurns2 := Fold(shrunk2, budget, c.cfg.RecentTurnsVerbatim, cal)
	stats.AfterLayerB = EstimateTokens(folded2)
	stats.LayerBEngaged = foldedTurns2 > 0 || stats.LayerBEngaged
	if foldedTurns2 > stats.TurnsFolded {
		stats.TurnsFolded = foldedTurns2
	}
	stats.Saved = stats.Before - stats.AfterLayerB

	if len(folded2) == 0 {
		// Last-ditch fallback: synthesise the last user message so the
		// model still receives a valid prompt (mirrors the
		// engine.providerView emergency floor in the old code path).
		if last, ok := lastUserMessage(cleaned); ok {
			return []schema.Message{last}, stats
		}
	}
	return folded2, stats
}

func lastUserMessage(msgs []schema.Message) (schema.Message, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == schema.RoleUser {
			return msgs[i], true
		}
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1], true
	}
	return schema.Message{}, false
}
```

- [ ] **Step 4: Add `lastUsage` field to AgentEngine (forward declaration for Task 14)**

In `internal/engine/loop.go`, add the field alongside the existing private fields on `AgentEngine`:

```go
type AgentEngine struct {
	provider provider.LLMProvider
	registry tools.Registry

	WorkDir        string
	EnableThinking bool

	MaxTurns             int
	MaxParallelToolCalls int

	contextManager *agentcontext.Manager
	session        *agentsession.Session
	trimmer        agentsession.Trimmer // removed in Task 17

	// lastUsage carries the previous turn's provider-reported token
	// usage forward into the next turn's budget. Zero on first turn.
	// Wired up in Task 14.
	lastUsage provider.Usage
}
```

(Do not touch the rest of `loop.go` in this task — Task 14 does the actual wiring.)

- [ ] **Step 5: Run all tests and verify they pass**

Run:

```bash
go build ./...
go test ./tests/compact -count=1
go test ./... -count=1
```

Expected: PASS. The new `lastUsage` field is unused for now and that is fine — Go is happy with unused struct fields.

- [ ] **Step 6: Commit**

```bash
git add internal/compact/compactor.go tests/compact/compactor_test.go internal/engine/loop.go
git commit -m "feat(compact): Compactor.View orchestrates Layer A + B with emergency floor"
```

---

### Task 12: Session tool-output spill helpers

**Files:**
- Create: `internal/session/tool_spill.go`
- Create: `tests/session/tool_spill_test.go`

Helpers the engine calls when a tool result exceeds `MaxToolBytes`. `SpillToolOutput(callID, body) -> (path, lineCount, err)` writes to `.claw/sessions/<sid>/tool-outputs/<callID>.txt`. `ReadToolOutputChunk(callID, startLine, lineCount) -> (chunk, totalLines, err)` reads a chunked window with `bufio.Scanner.Buffer` bumped so realistic-length lines fit. Both attach to the `*Session` so they share the session id and base dir.

- [ ] **Step 1: Write the failing tests**

Create `tests/session/tool_spill_test.go`:

```go
package session_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/session"
)

func TestSpillRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	body := "line 1\nline 2\nline 3\n"
	path, lines, err := sess.SpillToolOutput("call_abc", body)
	if err != nil {
		t.Fatalf("spill: %v", err)
	}
	if lines != 3 {
		t.Fatalf("lines = %d, want 3", lines)
	}
	if !strings.Contains(path, "call_abc.txt") {
		t.Fatalf("path = %q, want suffix call_abc.txt", path)
	}

	chunk, total, err := sess.ReadToolOutputChunk("call_abc", 1, 200)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if total != 3 {
		t.Fatalf("totalLines = %d, want 3", total)
	}
	if !strings.Contains(chunk, "line 1") || !strings.Contains(chunk, "line 3") {
		t.Fatalf("chunk missing data: %q", chunk)
	}
}

func TestSpillChunkBoundaries(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	var b strings.Builder
	for i := 1; i <= 100; i++ {
		b.WriteString("line ")
		b.WriteString(itoa(i))
		b.WriteByte('\n')
	}
	if _, _, err := sess.SpillToolOutput("c", b.String()); err != nil {
		t.Fatalf("spill: %v", err)
	}

	chunk, total, err := sess.ReadToolOutputChunk("c", 50, 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if total != 100 {
		t.Fatalf("totalLines = %d, want 100", total)
	}
	if !strings.Contains(chunk, "line 50") {
		t.Fatalf("missing line 50: %q", chunk)
	}
	if !strings.Contains(chunk, "line 59") {
		t.Fatalf("missing line 59: %q", chunk)
	}
	if strings.Contains(chunk, "line 60") {
		t.Fatalf("contains line 60: %q", chunk)
	}
}

func TestSpillMissingCallID(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	if _, _, err := sess.ReadToolOutputChunk("never_spilled", 1, 200); err == nil {
		t.Fatal("expected error for unknown call_id")
	}
}

func TestSpillVeryLongLine(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	huge := strings.Repeat("x", 200_000) + "\n"
	if _, _, err := sess.SpillToolOutput("big", huge); err != nil {
		t.Fatalf("spill: %v", err)
	}
	chunk, total, err := sess.ReadToolOutputChunk("big", 1, 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if total != 1 {
		t.Fatalf("totalLines = %d, want 1", total)
	}
	if len(chunk) < 100_000 {
		t.Fatalf("chunk truncated: len=%d", len(chunk))
	}
}

func TestSpillFileLocation(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	if _, _, err := sess.SpillToolOutput("c1", "x"); err != nil {
		t.Fatalf("spill: %v", err)
	}
	expected := filepath.Join(dir, sess.ID()+"-tool-outputs", "c1.txt")
	if _, err := os.Stat(expected); err != nil {
		// also accept the documented layout below: <dir>/<sid>/tool-outputs/c1.txt
		alt := filepath.Join(dir, sess.ID(), "tool-outputs", "c1.txt")
		if _, err2 := os.Stat(alt); err2 != nil {
			t.Fatalf("spill file at neither %q nor %q: %v / %v", expected, alt, err, err2)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	out := []byte{}
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./tests/session -run TestSpill -count=1`
Expected: FAIL — `SpillToolOutput` / `ReadToolOutputChunk` do not exist.

- [ ] **Step 3: Implement the spill helpers**

Create `internal/session/tool_spill.go`:

```go
package session

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxToolBytesScannerBuffer caps the per-line buffer the spill reader
// uses. Aligned with the engine's MaxToolBytes default so any line up
// to that size is read in a single Scanner call. Single lines larger
// than this fall back to a byte-range read in ReadToolOutputChunk.
const MaxToolBytesScannerBuffer = 65536

// toolOutputsDir returns the directory where this session keeps its
// spilled tool outputs. Adjacent to the JSONL file rather than nested
// inside it so a future cleanup can `rm -rf` the dir without touching
// the canonical history.
func (s *Session) toolOutputsDir() string {
	if s.file == nil {
		return ""
	}
	sessionDir := filepath.Dir(s.file.Name())
	return filepath.Join(sessionDir, s.id+"-tool-outputs")
}

// ToolOutputPath returns the canonical on-disk path for a spilled
// tool output. Public so the digest can refer to it in markers.
func (s *Session) ToolOutputPath(callID string) string {
	return filepath.Join(s.toolOutputsDir(), callID+".txt")
}

// SpillToolOutput writes body to the per-session tool-outputs dir,
// keyed by callID. Returns the path, the number of lines written, and
// any error. In-memory sessions return an error because there is no
// disk to spill to.
func (s *Session) SpillToolOutput(callID, body string) (string, int, error) {
	if s.file == nil {
		return "", 0, fmt.Errorf("spill: in-memory session has no spill directory")
	}
	if callID == "" {
		return "", 0, fmt.Errorf("spill: empty call_id")
	}
	dir := s.toolOutputsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("spill mkdir %q: %w", dir, err)
	}
	path := filepath.Join(dir, callID+".txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", 0, fmt.Errorf("spill write %q: %w", path, err)
	}
	return path, countLines(body), nil
}

// ReadToolOutputChunk reads lines [startLine, startLine+lineCount) from
// the spilled tool output for callID. startLine is 1-indexed; out-of-
// range bounds are clamped. Returns the chunk content, the total line
// count of the underlying file, and any error.
//
// If a single line exceeds MaxToolBytesScannerBuffer the function
// falls back to returning that line via a byte-range read so a
// pathological log file does not crash the reader.
func (s *Session) ReadToolOutputChunk(callID string, startLine, lineCount int) (string, int, error) {
	if callID == "" {
		return "", 0, fmt.Errorf("read: empty call_id")
	}
	path := s.ToolOutputPath(callID)
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("read spill %q: %w", path, err)
	}
	defer f.Close()

	if startLine < 1 {
		startLine = 1
	}
	if lineCount < 1 {
		lineCount = 200
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), MaxToolBytesScannerBuffer)

	var b strings.Builder
	totalLines := 0
	captured := 0
	for scanner.Scan() {
		totalLines++
		if totalLines < startLine || captured >= lineCount {
			continue
		}
		b.WriteString(scanner.Text())
		b.WriteByte('\n')
		captured++
	}
	if err := scanner.Err(); err != nil {
		if err == bufio.ErrTooLong {
			// Fallback: at least one line is larger than the buffer.
			// Re-open and stream the whole file as a single chunk; the
			// caller decides what to do with it. Document this in the
			// returned chunk header (the read_tool_output tool wraps).
			full, ferr := readAllWithCap(path, 8*1024*1024)
			if ferr != nil {
				return "", 0, fmt.Errorf("oversize-line fallback: %w", ferr)
			}
			return full, 1, nil
		}
		return "", 0, fmt.Errorf("scan spill %q: %w", path, err)
	}
	return b.String(), totalLines, nil
}

// readAllWithCap reads up to cap bytes from path. Used as the
// oversize-line fallback for ReadToolOutputChunk.
func readAllWithCap(path string, cap int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	r := io.LimitReader(f, cap)
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run: `go test ./tests/session -run TestSpill -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/session/tool_spill.go tests/session/tool_spill_test.go
git commit -m "feat(session): tool-output spill and chunked retrieval helpers"
```

---

### Task 13: `read_tool_output` built-in tool

**Files:**
- Create: `internal/tools/read_tool_output.go`
- Create: `tests/tools/read_tool_output_test.go`

The model-facing side of the spill system. Wraps `session.ReadToolOutputChunk` with a tool schema, argument validation, header/footer markers, and the same `MaxToolBytes` cap any other tool result is subject to.

The tool registers in `cmd/claw/main.go` once the CLI flag plumbing lands in Task 16 — this task just builds the tool and tests it against an in-memory `*session.Session`.

- [ ] **Step 1: Write the failing tests**

Create `tests/tools/read_tool_output_test.go`:

```go
package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestReadToolOutputBasic(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	body := "line 1\nline 2\nline 3\nline 4\n"
	if _, _, err := sess.SpillToolOutput("c1", body); err != nil {
		t.Fatalf("spill: %v", err)
	}
	tool := tools.NewReadToolOutputTool(sess, 65536)
	args, _ := json.Marshal(map[string]any{"call_id": "c1", "start_line": 2, "line_count": 2})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "lines 2-3 of 4") {
		t.Fatalf("header missing: %q", out)
	}
	if !strings.Contains(out, "line 2") || !strings.Contains(out, "line 3") {
		t.Fatalf("body missing: %q", out)
	}
}

func TestReadToolOutputDefaultArgs(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	if _, _, err := sess.SpillToolOutput("c1", "a\nb\nc\n"); err != nil {
		t.Fatalf("spill: %v", err)
	}
	tool := tools.NewReadToolOutputTool(sess, 65536)
	args, _ := json.Marshal(map[string]any{"call_id": "c1"})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "lines 1-3 of 3") {
		t.Fatalf("default header missing: %q", out)
	}
}

func TestReadToolOutputUnknownCallID(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	tool := tools.NewReadToolOutputTool(sess, 65536)
	args, _ := json.Marshal(map[string]any{"call_id": "ghost"})
	out, err := tool.Execute(context.Background(), args)
	if err == nil && !strings.Contains(out, "not found") && !strings.Contains(out, "no such") {
		t.Fatalf("expected error or not-found marker, got: %q / %v", out, err)
	}
}

func TestReadToolOutputArgValidation(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	tool := tools.NewReadToolOutputTool(sess, 65536)

	// Empty call_id.
	args, _ := json.Marshal(map[string]any{"call_id": ""})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatalf("empty call_id should error")
	}
	// Negative start_line accepted (clamped to 1) — should not panic.
	if _, _, err := sess.SpillToolOutput("c", "x\n"); err != nil {
		t.Fatalf("spill: %v", err)
	}
	args, _ = json.Marshal(map[string]any{"call_id": "c", "start_line": -3})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("negative start_line should clamp, got: %v", err)
	}
	// line_count > 1000 clamped to 1000.
	args, _ = json.Marshal(map[string]any{"call_id": "c", "line_count": 99999})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("huge line_count should clamp, got: %v", err)
	}
}

func TestReadToolOutputTailMarkerWhenMore(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	body := ""
	for i := 1; i <= 50; i++ {
		body += "line\n"
	}
	if _, _, err := sess.SpillToolOutput("c", body); err != nil {
		t.Fatalf("spill: %v", err)
	}
	tool := tools.NewReadToolOutputTool(sess, 65536)
	args, _ := json.Marshal(map[string]any{"call_id": "c", "start_line": 1, "line_count": 10})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "more remaining") && !strings.Contains(out, "40 more") {
		t.Fatalf("tail marker missing: %q", out)
	}
}

func TestReadToolOutputCapByMaxToolBytes(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	huge := strings.Repeat("x", 100_000) + "\n"
	if _, _, err := sess.SpillToolOutput("c", huge); err != nil {
		t.Fatalf("spill: %v", err)
	}
	tool := tools.NewReadToolOutputTool(sess, 4096)
	args, _ := json.Marshal(map[string]any{"call_id": "c"})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(out) > 8192 {
		t.Fatalf("output not capped: len=%d", len(out))
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./tests/tools -run TestReadToolOutput -count=1`
Expected: FAIL — `tools.NewReadToolOutputTool` does not exist.

- [ ] **Step 3: Implement the tool**

Create `internal/tools/read_tool_output.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/session"
)

// ReadToolOutputTool exposes session.ReadToolOutputChunk to the model.
// The model uses it to retrieve a chunk of a previously-spilled tool
// output after seeing the elision marker that Layer A leaves behind.
type ReadToolOutputTool struct {
	sess         *session.Session
	maxToolBytes int
}

// NewReadToolOutputTool wires the tool to a session and a byte cap.
// The byte cap matches the engine's MaxToolBytes so the chunk
// returned here is governed by the same boundary as any other tool
// result.
func NewReadToolOutputTool(sess *session.Session, maxToolBytes int) *ReadToolOutputTool {
	if maxToolBytes <= 0 {
		maxToolBytes = 65536
	}
	return &ReadToolOutputTool{sess: sess, maxToolBytes: maxToolBytes}
}

func (t *ReadToolOutputTool) Name() string { return "read_tool_output" }

func (t *ReadToolOutputTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name: "read_tool_output",
		Description: "Read a chunk of a previously-spilled tool output by its tool_call_id. " +
			"Use this when an earlier tool result was too large and was elided. " +
			"The elision marker in the original result shows the call_id and total lines.",
		InputSchema: schema.InputSchema{
			Type: "object",
			Properties: map[string]schema.Property{
				"call_id":    {Type: "string", Description: "The tool_call_id of the original call."},
				"start_line": {Type: "integer", Description: "1-indexed line to start at (default 1)."},
				"line_count": {Type: "integer", Description: "Number of lines to read (default 200, max 1000)."},
			},
			Required: []string{"call_id"},
		},
	}
}

type readToolOutputArgs struct {
	CallID    string `json:"call_id"`
	StartLine int    `json:"start_line"`
	LineCount int    `json:"line_count"`
}

func (t *ReadToolOutputTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var args readToolOutputArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("read_tool_output: invalid arguments: %w", err)
	}
	if args.CallID == "" {
		return "", fmt.Errorf("read_tool_output: call_id is required")
	}
	if args.StartLine < 1 {
		args.StartLine = 1
	}
	if args.LineCount < 1 {
		args.LineCount = 200
	}
	if args.LineCount > 1000 {
		args.LineCount = 1000
	}

	chunk, total, err := t.sess.ReadToolOutputChunk(args.CallID, args.StartLine, args.LineCount)
	if err != nil {
		return fmt.Sprintf("read_tool_output: %v (call_id=%q not found in tool-outputs dir)", err, args.CallID), nil
	}

	endLine := args.StartLine + args.LineCount - 1
	if endLine > total {
		endLine = total
	}
	header := fmt.Sprintf("lines %d-%d of %d (call_id=%s)\n", args.StartLine, endLine, total, args.CallID)
	body := chunk
	if endLine < total {
		body += fmt.Sprintf("...[%d more remaining; call read_tool_output with start_line=%d]\n", total-endLine, endLine+1)
	}
	combined := header + body
	if len(combined) > t.maxToolBytes {
		return TruncateWithMarker(combined, t.maxToolBytes,
			fmt.Sprintf("\n...[chunk capped at %d bytes; lower line_count or use a tighter start_line]...\n", t.maxToolBytes)), nil
	}
	return combined, nil
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run: `go test ./tests/tools -run TestReadToolOutput -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/read_tool_output.go tests/tools/read_tool_output_test.go
git commit -m "feat(tools): read_tool_output built-in for paged spill retrieval"
```

---

### Task 14: Engine integration — boundary cap, spill, and compactor in the turn loop

**Files:**
- Modify: `internal/engine/loop.go`
- Modify: `internal/engine/tool_execution.go`
- Create: `tests/engine/compact_integration_test.go`

This is the wiring task. Three discrete changes:

1. **`loop.go`** — Replace the `providerView` helper. Call `compact.Compactor.View(history, budget, turn, calibrator)` once per turn before `provider.Generate`. Thread `actionResp.Usage` into `e.lastUsage` so the next turn's budget reflects the previous turn's real input/output count. Call `calibrator.Observe(stats.AfterLayerB, actionResp.Usage.InputTokens)` after the act response. Call `report.OnCompact(ctx, stats)` per the emission rule.
2. **`tool_execution.go`** — Before any `sess.Append(toolResultMessage(result))`, if `len(result.Output) > cfg.MaxToolBytes`, call `sess.SpillToolOutput`, build a spill-aware marker via `tools.TruncateWithMarker`, and replace `result.Output`. The exact code snippet is the one in spec §2.
3. **Integration test** — Fake provider returning a huge tool call; verify the spill file exists, `read_tool_output` retrieves the right chunk, `lastUsage` threads correctly across turns, `OnCompact` fires with expected stats.

The `OnCompact` callback itself lands in Task 15; this task uses a no-op stub on the reporter so the engine compiles. (Reporter interface gets `OnCompact` formally in Task 15.) To avoid a forward-reference, this task adds the method to `TerminalReporter` as an empty no-op and to the `Reporter` interface; Task 15 fills in the body and audit-log emission.

- [ ] **Step 1: Write the failing integration test**

Create `tests/engine/compact_integration_test.go`:

```go
package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// fakeBashTool returns a fixed output; useful to simulate a huge bash result.
type fakeBashTool struct{ output string }

func (t *fakeBashTool) Name() string { return "bash" }
func (t *fakeBashTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{Name: "bash"}
}
func (t *fakeBashTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return t.output, nil
}

type compactCapturingReporter struct {
	statsSeen []compact.CompactStats
	messages  []string
}

func (r *compactCapturingReporter) OnThinking(_ context.Context)                                {}
func (r *compactCapturingReporter) OnToolCall(_ context.Context, _, _ string)                   {}
func (r *compactCapturingReporter) OnToolResult(_ context.Context, _, _ string, _ bool)         {}
func (r *compactCapturingReporter) OnMessage(_ context.Context, c string)                       { r.messages = append(r.messages, c) }
func (r *compactCapturingReporter) OnCompact(_ context.Context, s compact.CompactStats)         { r.statsSeen = append(r.statsSeen, s) }

func TestCompactIntegrationHugeToolOutputSpills(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer sess.Close()

	huge := strings.Repeat("x", 200_000)
	registry := tools.NewRegistry()
	registry.Register(&fakeBashTool{output: huge})

	args, _ := json.Marshal(map[string]string{"command": "find / -type f"})
	fp := &fakeProvider{
		responses: []schema.Message{
			{Role: schema.RoleAssistant, ToolCalls: []schema.ToolCall{{ID: "tool_huge", Name: "bash", Arguments: args}}},
			{Role: schema.RoleAssistant, Content: "done"},
		},
		usage: provider.Usage{InputTokens: 1234, OutputTokens: 56},
	}

	cfg := compact.Config{MaxToolBytes: 4096, RecentTurnsVerbatim: 4}
	c := compact.NewCompactor(cfg)
	cal := compact.NewCalibrator(0.3)

	eng := engine.NewAgentEngine(fp, registry, dir, false)
	eng.SetSession(sess)
	eng.SetCompactor(c)
	eng.SetCalibrator(cal)
	eng.SetCompactConfig(cfg)
	eng.SetModelID("claude-opus-4-7")
	eng.SetOutputCap(4096)
	eng.SetSafetyFactor(0.75)

	rep := &compactCapturingReporter{}
	if err := eng.Run(context.Background(), "find huge files", rep); err != nil {
		if !errors.Is(err, engine.ErrMaxTurnsExceeded) {
			t.Fatalf("run: %v", err)
		}
	}

	// Spill file exists.
	spillPath := filepath.Join(dir, sess.ID()+"-tool-outputs", "tool_huge.txt")
	if _, err := os.Stat(spillPath); err != nil {
		// alt layout
		spillPath = filepath.Join(dir, sess.ID(), "tool-outputs", "tool_huge.txt")
		if _, err := os.Stat(spillPath); err != nil {
			t.Fatalf("spill file missing: %v", err)
		}
	}

	// Marker in the session's tool message refers to call_id.
	found := false
	for _, m := range sess.Messages() {
		if m.Role == schema.RoleTool && m.ToolCallID == "tool_huge" {
			if !strings.Contains(m.Content, "tool_huge") {
				t.Fatalf("marker missing call_id reference: %q", m.Content)
			}
			if len(m.Content) >= len(huge) {
				t.Fatalf("tool output not capped in session: len=%d", len(m.Content))
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("tool result for tool_huge missing from session")
	}

	// read_tool_output retrieves a chunk.
	rt := tools.NewReadToolOutputTool(sess, 65536)
	a, _ := json.Marshal(map[string]any{"call_id": "tool_huge", "start_line": 1, "line_count": 10})
	out, err := rt.Execute(context.Background(), a)
	if err != nil {
		t.Fatalf("read_tool_output: %v", err)
	}
	if !strings.Contains(out, "tool_huge") {
		t.Fatalf("retrieved chunk missing call_id header: %q", out)
	}
}

func TestCompactIntegrationLastUsageThreadsForward(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	registry := tools.NewRegistry()
	fp := &fakeProvider{
		responses: []schema.Message{
			{Role: schema.RoleAssistant, Content: "first"},
		},
		usage: provider.Usage{InputTokens: 9000, OutputTokens: 200},
	}
	cfg := compact.Config{MaxToolBytes: 65536, RecentTurnsVerbatim: 4}
	eng := engine.NewAgentEngine(fp, registry, dir, false)
	eng.SetSession(sess)
	eng.SetCompactor(compact.NewCompactor(cfg))
	eng.SetCalibrator(compact.NewCalibrator(0.3))
	eng.SetCompactConfig(cfg)
	eng.SetModelID("claude-opus-4-7")
	eng.SetOutputCap(4096)
	eng.SetSafetyFactor(0.75)

	rep := &compactCapturingReporter{}
	if err := eng.Run(context.Background(), "hi", rep); err != nil {
		t.Fatalf("run: %v", err)
	}
	if eng.LastUsageForTest().InputTokens != 9000 {
		t.Fatalf("lastUsage not threaded: %+v", eng.LastUsageForTest())
	}
}
```

(`fakeProvider` already exists in tests/engine; this task only adds a `usage` field to it. If not already there, extend it: `type fakeProvider struct { responses []schema.Message; usage provider.Usage; ... }` and in `Generate` return `&provider.Response{Message: &resp, Usage: f.usage}`.)

- [ ] **Step 2: Run test and verify it fails**

Run: `go test ./tests/engine -run TestCompactIntegration -count=1`
Expected: FAIL — engine setters (`SetCompactor`, `SetCalibrator`, `SetCompactConfig`, `SetModelID`, `SetOutputCap`, `SetSafetyFactor`, `LastUsageForTest`) and the OnCompact reporter method do not exist.

- [ ] **Step 3: Wire compactor into the engine**

Modify `internal/engine/loop.go`. Replace the existing `AgentEngine` definition with one that holds the new fields, and replace `providerView` with a `compactedView` helper that calls `compact.Compactor.View` and emits stats. Use the spec §0 per-turn flow as the canonical snippet.

```go
package engine

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

var ErrMaxTurnsExceeded = errors.New("agent engine exceeded MaxTurns")

type AgentEngine struct {
	provider provider.LLMProvider
	registry tools.Registry

	WorkDir              string
	EnableThinking       bool
	MaxTurns             int
	MaxParallelToolCalls int

	contextManager *agentcontext.Manager
	session        *agentsession.Session

	compactor    *compact.Compactor
	compactCfg   compact.Config
	calibrator   *compact.Calibrator
	modelID      string
	outputCap    int
	safetyFactor float64
	overrides    map[string]int

	lastUsage provider.Usage
}

func NewAgentEngine(p provider.LLMProvider, r tools.Registry, workDir string, enableThinking bool) *AgentEngine {
	return &AgentEngine{
		provider:       p,
		registry:       r,
		WorkDir:        workDir,
		EnableThinking: enableThinking,
		safetyFactor:   0.75,
		outputCap:      4096,
	}
}

func (e *AgentEngine) SetContextManager(m *agentcontext.Manager) { e.contextManager = m }
func (e *AgentEngine) SetSession(s *agentsession.Session)        { e.session = s }
func (e *AgentEngine) SetCompactor(c *compact.Compactor)         { e.compactor = c }
func (e *AgentEngine) SetCalibrator(c *compact.Calibrator)       { e.calibrator = c }
func (e *AgentEngine) SetCompactConfig(c compact.Config)         { e.compactCfg = c }
func (e *AgentEngine) SetModelID(id string)                      { e.modelID = id }
func (e *AgentEngine) SetOutputCap(n int)                        { e.outputCap = n }
func (e *AgentEngine) SetSafetyFactor(f float64)                 { e.safetyFactor = f }
func (e *AgentEngine) SetModelLimitOverrides(m map[string]int)   { e.overrides = m }
func (e *AgentEngine) LastUsageForTest() provider.Usage          { return e.lastUsage }

const defaultMaxTurns = 25

func (e *AgentEngine) Run(ctx context.Context, userPrompt string, report Reporter) error {
	log.Printf("[engine] starting, workdir=%s thinking=%v", e.WorkDir, e.EnableThinking)

	maxTurns := e.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}

	sess := e.session
	if sess == nil {
		sess = agentsession.NewInMemory()
	}
	if userPrompt != "" {
		if err := sess.Append(schema.Message{Role: schema.RoleUser, Content: userPrompt}); err != nil {
			return fmt.Errorf("append user prompt: %w", err)
		}
	}

	systemMsg := schema.Message{Role: schema.RoleSystem, Content: e.systemPrompt()}
	availableTools := e.registry.GetAvailableTools()
	turnCount := 0
	toolSpillThisTurn := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		turnCount++
		if turnCount > maxTurns {
			return ErrMaxTurnsExceeded
		}
		log.Printf("[engine] turn %d", turnCount)

		view, stats := e.buildCompactedView(systemMsg, sess.Messages(), turnCount)
		stats.ToolOutputsSpilled = toolSpillThisTurn
		toolSpillThisTurn = 0

		if e.EnableThinking {
			log.Println("[engine] phase=think tools=disabled")
			thinkResp, err := e.provider.Generate(ctx, view, nil)
			if err != nil {
				return fmt.Errorf("think phase: %w", err)
			}
			if thinkResp != nil && thinkResp.Message != nil && thinkResp.Message.Content != "" {
				report.OnThinking(ctx)
				view = append(view, *thinkResp.Message)
			}
		}

		log.Println("[engine] phase=act tools=enabled")
		actionResp, err := e.provider.Generate(ctx, view, availableTools)
		if err != nil {
			return fmt.Errorf("act phase: %w", err)
		}
		actionMsg := actionResp.Message
		e.lastUsage = actionResp.Usage
		if e.calibrator != nil && actionResp.Usage.InputTokens > 0 {
			e.calibrator.Observe(stats.AfterLayerB, actionResp.Usage.InputTokens)
		}

		if shouldEmit(stats) {
			report.OnCompact(ctx, stats)
		}

		if err := sess.Append(*actionMsg); err != nil {
			return fmt.Errorf("persist act response: %w", err)
		}
		if actionMsg.Content != "" {
			report.OnMessage(ctx, actionMsg.Content)
		}
		if len(actionMsg.ToolCalls) == 0 {
			log.Println("[engine] no tool calls, task complete")
			break
		}

		if hasLoadSkillCall(actionMsg.ToolCalls) {
			results, err := e.executeLoadSkillBarrier(ctx, actionMsg.ToolCalls, report)
			if err != nil {
				return err
			}
			for _, result := range results {
				spilled, err := e.applyToolBoundaryCap(sess, &result)
				if err != nil {
					return err
				}
				if spilled {
					toolSpillThisTurn++
				}
				if err := sess.Append(toolResultMessage(result)); err != nil {
					return fmt.Errorf("persist tool result: %w", err)
				}
			}
			systemMsg.Content = e.systemPrompt()
			continue
		}

		groups := PlanToolCallGroups(actionMsg.ToolCalls, e.registry.ExecutionPolicyFor)
		for _, group := range groups {
			if err := ctx.Err(); err != nil {
				return err
			}
			for _, call := range group {
				report.OnToolCall(ctx, call.Name, string(call.Arguments))
			}
			results, err := executeToolCallGroup(ctx, e.registry, group, e.toolGroupLimit(group))
			if err != nil {
				return err
			}
			for i, result := range results {
				spilled, err := e.applyToolBoundaryCap(sess, &result)
				if err != nil {
					return err
				}
				if spilled {
					toolSpillThisTurn++
				}
				report.OnToolResult(ctx, group[i].Name, result.Output, result.IsError)
				if err := sess.Append(toolResultMessage(result)); err != nil {
					return fmt.Errorf("persist tool result: %w", err)
				}
			}
		}
	}

	return nil
}

func (e *AgentEngine) buildCompactedView(systemMsg schema.Message, tail []schema.Message, turn int) ([]schema.Message, compact.CompactStats) {
	if e.compactor == nil {
		// Test-only path: no compactor configured. Identity view.
		view := append([]schema.Message{systemMsg}, tail...)
		return view, compact.NewCompactStats(turn, compact.EstimateTokens(tail))
	}
	budget := compact.Budget(compact.BudgetInput{
		Model:        e.modelID,
		LastUsage:    e.lastUsage,
		OutputCap:    e.outputCap,
		SafetyFactor: e.safetyFactor,
		Overrides:    e.overrides,
	})
	compacted, stats := e.compactor.View(tail, budget, turn, e.calibrator)
	stats.Budget = budget
	if len(compacted) == 0 && len(tail) > 0 {
		log.Printf("[engine] warning: compactor returned empty slice; emergency floor")
		compacted = []schema.Message{lastUserMessage(tail)}
	}
	view := make([]schema.Message, 0, 1+len(compacted))
	view = append(view, systemMsg)
	view = append(view, compacted...)
	return view, stats
}

// shouldEmit returns true if the OnCompact callback should fire this turn.
// Matches the spec §4 emission rule: Layer B engaged, any tool spill, or
// a non-trivial saving (>= 5% of Before).
func shouldEmit(s compact.CompactStats) bool {
	if s.LayerBEngaged {
		return true
	}
	if s.ToolOutputsSpilled > 0 {
		return true
	}
	if s.Before > 0 && s.Saved*20 > s.Before {
		return true
	}
	return false
}

func lastUserMessage(tail []schema.Message) schema.Message {
	for i := len(tail) - 1; i >= 0; i-- {
		if tail[i].Role == schema.RoleUser {
			return tail[i]
		}
	}
	return tail[len(tail)-1]
}

func (e *AgentEngine) systemPrompt() string {
	if e.contextManager == nil {
		return agentcontext.DefaultBaseInstructions
	}
	return e.contextManager.SystemPrompt()
}

func hasLoadSkillCall(calls []schema.ToolCall) bool {
	for _, call := range calls {
		if call.Name == agentcontext.LoadSkillToolName {
			return true
		}
	}
	return false
}

func deferToolResult(call schema.ToolCall) schema.ToolResult {
	return schema.ToolResult{
		ToolCallID: call.ID,
		Output:     fmt.Sprintf("tool %q deferred until after skill loading; request it again if still needed", call.Name),
		IsError:    false,
	}
}

func (e *AgentEngine) executeLoadSkillBarrier(ctx context.Context, calls []schema.ToolCall, report Reporter) ([]schema.ToolResult, error) {
	results := make([]schema.ToolResult, len(calls))
	for i, call := range calls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if call.Name != agentcontext.LoadSkillToolName {
			results[i] = deferToolResult(call)
			continue
		}
		report.OnToolCall(ctx, call.Name, string(call.Arguments))
		result := executeToolCall(ctx, e.registry, call)
		report.OnToolResult(ctx, call.Name, result.Output, result.IsError)
		results[i] = result
	}
	return results, nil
}

func (e *AgentEngine) toolGroupLimit(group []schema.ToolCall) int {
	limit := e.MaxParallelToolCalls
	if limit <= 0 {
		limit = defaultParallelToolConcurrency
	}
	for _, call := range group {
		policy := e.registry.ExecutionPolicyFor(call)
		if !policy.ParallelSafe || policy.MaxConcurrency <= 0 {
			continue
		}
		if policy.MaxConcurrency < limit {
			limit = policy.MaxConcurrency
		}
	}
	return limit
}
```

- [ ] **Step 4: Add the tool-output boundary cap helper**

Modify `internal/engine/tool_execution.go`. Add `applyToolBoundaryCap` as a method on `*AgentEngine` so it has access to `e.compactCfg.MaxToolBytes`. It is called once per tool result before `sess.Append`. Mutates `result.Output` in place and returns whether a spill happened.

```go
package engine

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

const defaultParallelToolConcurrency = 4

type indexedToolCall struct {
	index int
	call  schema.ToolCall
}

type indexedToolResult struct {
	index  int
	result schema.ToolResult
}

// applyToolBoundaryCap is the engine's tool-output boundary cap from
// spec §2. Called once per tool result before sess.Append. If the
// output exceeds MaxToolBytes, spill the full body to the session's
// tool-outputs dir and replace result.Output with a head+tail
// truncation that carries a spill-aware marker. Returns true iff a
// spill happened (so the caller can increment the per-turn count).
func (e *AgentEngine) applyToolBoundaryCap(sess *agentsession.Session, result *schema.ToolResult) (bool, error) {
	max := e.compactCfg.MaxToolBytes
	if max <= 0 {
		max = 65536
	}
	if len(result.Output) <= max {
		return false, nil
	}
	path, lines, err := sess.SpillToolOutput(result.ToolCallID, result.Output)
	if err != nil {
		return false, fmt.Errorf("spill tool output for call %s: %w", result.ToolCallID, err)
	}
	marker := fmt.Sprintf(
		"\n\n...[%d bytes / %d lines spilled to %s; "+
			"use read_tool_output(call_id=%q, start_line=N, line_count=M) to read more]...\n\n",
		len(result.Output), lines, path, result.ToolCallID,
	)
	result.Output = tools.TruncateWithMarker(result.Output, max, marker)
	return true, nil
}

func executeToolCallGroup(
	ctx context.Context,
	registry tools.Registry,
	group []schema.ToolCall,
	limit int,
) ([]schema.ToolResult, error) {
	if len(group) == 0 {
		return nil, nil
	}

	if len(group) == 1 {
		result := executeToolCall(ctx, registry, group[0])
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return []schema.ToolResult{result}, nil
	}

	workerCount := boundedWorkerCount(limit, len(group))
	jobs := make(chan indexedToolCall)
	resultCh := make(chan indexedToolResult, len(group))
	var wg sync.WaitGroup

	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				resultCh <- indexedToolResult{
					index:  job.index,
					result: executeToolCall(ctx, registry, job.call),
				}
			}
		}()
	}

	for i, call := range group {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(resultCh)
			return nil, ctx.Err()
		case jobs <- indexedToolCall{index: i, call: call}:
		}
	}

	close(jobs)
	wg.Wait()
	close(resultCh)

	results := make([]schema.ToolResult, len(group))
	for item := range resultCh {
		results[item.index] = item.result
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func executeToolCall(ctx context.Context, registry tools.Registry, call schema.ToolCall) schema.ToolResult {
	log.Printf("[engine] executing tool=%s args=%s", call.Name, string(call.Arguments))
	result := registry.Execute(ctx, call)
	if result.IsError {
		log.Printf("[engine] tool error: %s", result.Output)
	} else {
		log.Printf("[engine] tool ok: %d bytes", len(result.Output))
	}
	return result
}

func boundedWorkerCount(limit, groupSize int) int {
	if groupSize <= 0 {
		return 0
	}
	if limit <= 0 || limit > groupSize {
		return groupSize
	}
	return limit
}

func toolResultMessage(result schema.ToolResult) schema.Message {
	return schema.Message{
		Role:       schema.RoleTool,
		Content:    result.Output,
		ToolCallID: result.ToolCallID,
		IsError:    result.IsError,
	}
}
```

- [ ] **Step 5: Stub OnCompact on Reporter + TerminalReporter**

In `internal/engine/reporter.go`, add the method:

```go
type Reporter interface {
	OnThinking(ctx context.Context)
	OnToolCall(ctx context.Context, toolName string, args string)
	OnToolResult(ctx context.Context, toolName string, result string, isError bool)
	OnMessage(ctx context.Context, content string)
	// OnCompact is fired once per turn when compaction stats merit
	// surfacing. Emission rule lives in the engine (see shouldEmit).
	// Task 15 implements the body; this task only needs the stub.
	OnCompact(ctx context.Context, stats compact.CompactStats)
}
```

Add the import `"github.com/snowshine0216/penelope-agent/internal/compact"`. In `internal/engine/terminal_reporter.go`, add a no-op stub for now:

```go
func (r *TerminalReporter) OnCompact(_ context.Context, _ compact.CompactStats) {
	// Task 15 implements this. The integration test uses a custom
	// reporter, so a no-op terminal stub is fine for this task.
}
```

Update every other Reporter implementor in the repo (tests/engine fakes etc.) to add the same no-op method. Run `go build ./...` and fix any "does not implement Reporter" errors by adding `OnCompact` stubs to the offending types.

- [ ] **Step 6: Run tests and verify they pass**

Run:

```bash
go build ./...
go test ./tests/engine -run TestCompactIntegration -count=1
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/engine/loop.go internal/engine/tool_execution.go internal/engine/reporter.go internal/engine/terminal_reporter.go tests/engine/compact_integration_test.go tests/engine
git commit -m "feat(engine): wire compactor and tool-output boundary cap into turn loop"
```

---

### Task 15: Reporter `OnCompact` callback + audit log

**Files:**
- Modify: `internal/engine/terminal_reporter.go`
- Create: `internal/session/audit.go`
- Create: `tests/engine/terminal_reporter_test.go` (extend if exists)
- Create: `tests/session/audit_test.go`

The interface stub from Task 14 gets a real body: a one-line `[compact] turn N: ...` print to stderr in the spec §4 format, plus an append-only `.claw/sessions/<id>/compact-events.jsonl` audit log written under the same per-session flock pattern as `session.go:71-92`.

The audit log lives in `internal/session` because that is where the flock primitives are. The reporter calls `sess.AppendCompactEvent(stats)` after printing.

- [ ] **Step 1: Write the failing tests**

Create `tests/session/audit_test.go`:

```go
package session_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/session"
)

func TestAppendCompactEventRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer sess.Close()

	stats := compact.CompactStats{
		Turn:               7,
		Before:             48_210,
		AfterLayerA:        48_000,
		AfterLayerB:        47_920,
		Budget:             100_000,
		Saved:              290,
		LayerBEngaged:      true,
		TurnsFolded:        3,
		ToolOutputsSpilled: 2,
		CalibratorRatio:    1.07,
	}
	if err := sess.AppendCompactEvent(stats); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := sess.AppendCompactEvent(stats); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	path := filepath.Join(dir, sess.ID()+"-compact-events.jsonl")
	if _, err := os.Stat(path); err != nil {
		// alt layout
		path = filepath.Join(dir, sess.ID(), "compact-events.jsonl")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("audit log missing at either location: %v", err)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	lines := 0
	for scanner.Scan() {
		lines++
		var got compact.CompactStats
		if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal audit line: %v", err)
		}
		if got.Turn != 7 {
			t.Fatalf("audit turn = %d, want 7", got.Turn)
		}
	}
	if lines != 2 {
		t.Fatalf("audit lines = %d, want 2", lines)
	}
}
```

Append to (or create) `tests/engine/terminal_reporter_test.go`:

```go
package engine_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/engine"
)

func TestTerminalReporterOnCompactPrintsShortForm(t *testing.T) {
	stderr := captureStderr(t, func() {
		rep := engine.NewTerminalReporter()
		stats := compact.CompactStats{
			Turn: 7, Before: 48_210, AfterLayerA: 48_000, AfterLayerB: 47_920,
			Budget: 100_000, Saved: 290, ToolOutputsSpilled: 2,
		}
		rep.OnCompact(context.Background(), stats)
	})
	if !strings.Contains(stderr, "[compact]") {
		t.Fatalf("missing [compact] prefix: %q", stderr)
	}
	if !strings.Contains(stderr, "turn 7") {
		t.Fatalf("missing turn: %q", stderr)
	}
	if !strings.Contains(stderr, "saved 290") {
		t.Fatalf("missing saved: %q", stderr)
	}
	if !strings.Contains(stderr, "2 tool outputs spilled") {
		t.Fatalf("missing spill count: %q", stderr)
	}
}

func TestTerminalReporterOnCompactLayerBLine(t *testing.T) {
	stderr := captureStderr(t, func() {
		rep := engine.NewTerminalReporter()
		stats := compact.CompactStats{
			Turn: 12, Before: 192_430, AfterLayerB: 11_930, Budget: 144_000,
			Saved: 180_500, LayerBEngaged: true, TurnsFolded: 8, ToolOutputsSpilled: 1,
		}
		rep.OnCompact(context.Background(), stats)
	})
	if !strings.Contains(stderr, "folded turns") {
		t.Fatalf("missing folded marker: %q", stderr)
	}
	if !strings.Contains(stderr, "budget 144000") && !strings.Contains(stderr, "budget 144,000") {
		t.Fatalf("missing budget: %q", stderr)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = buf.ReadFrom(r)
		close(done)
	}()
	fn()
	_ = w.Close()
	<-done
	return buf.String()
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./tests/session -run TestAppendCompactEvent -count=1
go test ./tests/engine -run TestTerminalReporterOnCompact -count=1
```

Expected: FAIL.

- [ ] **Step 3: Implement the audit log**

Create `internal/session/audit.go`:

```go
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/snowshine0216/penelope-agent/internal/compact"
)

// compactEventsPath returns the per-session audit log path. Located
// adjacent to the JSONL history rather than nested so a `ls` of the
// sessions dir surfaces every artefact for one session.
func (s *Session) compactEventsPath() string {
	if s.file == nil {
		return ""
	}
	dir := filepath.Dir(s.file.Name())
	return filepath.Join(dir, s.id+"-compact-events.jsonl")
}

// AppendCompactEvent appends one CompactStats line to the per-session
// audit log. Uses the same per-write flock pattern as Session.persist
// so concurrent processes serialize at the kernel level. In-memory
// sessions are a no-op (audit logging requires a stable on-disk path).
func (s *Session) AppendCompactEvent(stats compact.CompactStats) error {
	if s.file == nil {
		return nil
	}
	path := s.compactEventsPath()
	data, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("marshal compact event: %w", err)
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open compact audit %q: %w", path, err)
	}
	defer f.Close()
	if err := lockExclusive(f.Fd()); err != nil {
		return err
	}
	defer func() { _ = unlock(f.Fd()) }()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write compact audit: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Implement the reporter body**

Replace the Task-14 no-op `TerminalReporter.OnCompact` with the real body in `internal/engine/terminal_reporter.go`:

```go
package engine

import (
	"context"
	"fmt"
	"os"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
)

type TerminalReporter struct {
	sess *agentsession.Session
}

func NewTerminalReporter() *TerminalReporter { return &TerminalReporter{} }

// AttachSession lets the engine give the reporter a session handle so
// OnCompact can persist to the audit log. Optional — the reporter
// degrades to stderr-only when nil.
func (r *TerminalReporter) AttachSession(s *agentsession.Session) { r.sess = s }

func (r *TerminalReporter) OnThinking(_ context.Context) { fmt.Println("[thinking]") }

func (r *TerminalReporter) OnToolCall(_ context.Context, toolName string, args string) {
	fmt.Printf("[tool] %s args=%s\n", toolName, args)
}

func (r *TerminalReporter) OnToolResult(_ context.Context, toolName string, result string, isError bool) {
	if isError {
		fmt.Printf("[tool:error] %s result=%s\n", toolName, result)
		return
	}
	fmt.Printf("[tool:ok] %s result=%s\n", toolName, result)
}

func (r *TerminalReporter) OnMessage(_ context.Context, content string) {
	fmt.Println(content)
}

func (r *TerminalReporter) OnCompact(_ context.Context, s compact.CompactStats) {
	line := formatCompactLine(s)
	fmt.Fprintln(os.Stderr, line)
	if r.sess != nil {
		_ = r.sess.AppendCompactEvent(s) // audit is best-effort
	}
}

// formatCompactLine implements spec §4. Three shapes:
//   - Short: turn N: B → A tokens (saved X) | <extras>
//   - Layer B: turn N: B → A tokens (saved X, P%) | budget Y | folded turns 1..F into digest | <spill>
//   - Calibrator warming: turn N: A → A tokens (saved 0) | calibrator ratio R (warming)
func formatCompactLine(s compact.CompactStats) string {
	if s.LayerBEngaged {
		pct := 0.0
		if s.Before > 0 {
			pct = float64(s.Saved) / float64(s.Before) * 100
		}
		extras := ""
		if s.ToolOutputsSpilled > 0 {
			extras = fmt.Sprintf(" | %d tool output%s spilled", s.ToolOutputsSpilled, plural(s.ToolOutputsSpilled))
		}
		return fmt.Sprintf("[compact] turn %d: %d → %d tokens (saved %d, %.1f%%) | budget %d | folded turns 1..%d into digest%s",
			s.Turn, s.Before, s.AfterLayerB, s.Saved, pct, s.Budget, s.TurnsFolded, extras)
	}
	extras := ""
	if s.ToolOutputsSpilled > 0 {
		extras = fmt.Sprintf(" | %d tool output%s spilled", s.ToolOutputsSpilled, plural(s.ToolOutputsSpilled))
	}
	return fmt.Sprintf("[compact] turn %d: %d → %d tokens (saved %d)%s",
		s.Turn, s.Before, s.AfterLayerB, s.Saved, extras)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
```

- [ ] **Step 5: Run tests and verify they pass**

Run:

```bash
go test ./tests/session -run TestAppendCompactEvent -count=1
go test ./tests/engine -run TestTerminalReporterOnCompact -count=1
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/session/audit.go internal/engine/terminal_reporter.go tests/session/audit_test.go tests/engine/terminal_reporter_test.go
git commit -m "feat(engine): OnCompact reporter prints stderr line + audits to JSONL"
```

---

### Task 16: CLI flag changes

**Files:**
- Modify: `cmd/claw/main.go`
- Create: `tests/cmd/main_flags_test.go`

The migration moment. Three removed flags, four new ones, plus the engine setters wired up. `--trim-strategy` is a hard error that points to the new flags so an existing CI invocation gets a clear migration message.

- [ ] **Step 1: Write the failing tests**

Create `tests/cmd/main_flags_test.go`:

```go
package cmd_test

// Black-box test: invoke the built binary with various flag combinations
// and assert exit code + stderr substring.

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "claw")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/claw")
	cmd.Dir = repoRoot(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	b, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func runFlag(t *testing.T, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	return stderr.String(), exit
}

func TestRemovedTrimStrategyFlagHardErrors(t *testing.T) {
	bin := buildBinary(t)
	stderr, exit := runFlag(t, bin, "--trim-strategy=window", "--prompt=hi")
	if exit == 0 {
		t.Fatalf("expected non-zero exit, got 0")
	}
	if !strings.Contains(stderr, "--trim-strategy") || !strings.Contains(stderr, "removed") {
		t.Fatalf("stderr missing migration message: %q", stderr)
	}
	if !strings.Contains(stderr, "--compact-") {
		t.Fatalf("stderr missing new-flag hint: %q", stderr)
	}
}

func TestRemovedMaxContextTurnsFlagHardErrors(t *testing.T) {
	bin := buildBinary(t)
	stderr, exit := runFlag(t, bin, "--max-context-turns=6", "--prompt=hi")
	if exit == 0 {
		t.Fatalf("expected non-zero exit")
	}
	if !strings.Contains(stderr, "--compact-recent-turns") {
		t.Fatalf("stderr missing replacement-flag hint: %q", stderr)
	}
}

func TestRemovedMaxContextTokensFlagHardErrors(t *testing.T) {
	bin := buildBinary(t)
	stderr, exit := runFlag(t, bin, "--max-context-tokens=32000", "--prompt=hi")
	if exit == 0 {
		t.Fatalf("expected non-zero exit")
	}
	if !strings.Contains(stderr, "--compact-fallback-limit") {
		t.Fatalf("stderr missing replacement-flag hint: %q", stderr)
	}
}

func TestNewCompactFlagsAccepted(t *testing.T) {
	bin := buildBinary(t)
	// We can't actually call a live provider, but flag-parse alone
	// should not fail. Use --help to bypass provider init.
	cmd := exec.Command(bin, "--compact-recent-turns=4",
		"--compact-fallback-limit=32000", "--compact-safety-factor=0.75",
		"--compact-max-tool-bytes=65536", "--help")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run() // --help exits 0 or 2 depending; just assert flag-parse didn't break
	if strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("new flags missing: %q", stderr.String())
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./tests/cmd -run TestRemoved -count=1 && go test ./tests/cmd -run TestNew -count=1`
Expected: FAIL — the old flags still exist; the new ones do not.

- [ ] **Step 3: Rewrite main.go**

Replace `cmd/claw/main.go` with:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// removedFlag intercepts flags that were removed in the adaptive-compaction
// migration. flag.Set on it always returns a hard error with a hint at the
// replacement.
type removedFlag struct {
	name        string
	replacement string
}

func (r *removedFlag) String() string { return "" }
func (r *removedFlag) Set(string) error {
	return fmt.Errorf("--%s was removed; use --%s (adaptive semantic compaction)", r.name, r.replacement)
}

func main() {
	prompt := flag.String("prompt", "", "user prompt; if empty, read from stdin")
	think := flag.Bool("think", false, "enable thinking phase before each action")
	providerName := flag.String("provider", "openai", "provider: openai or claude")
	model := flag.String("model", "", "model id; defaults to LLM_MODEL env or provider default")
	maxTurns := flag.Int("max-turns", 25, "max engine turns per run")
	maxTokens := flag.Int("max-tokens", 4096, "max output tokens (claude only); also used as Budget OutputCap")
	workDir := flag.String("workdir", "", "workspace root; defaults to cwd")
	sessionID := flag.String("session", "", "resume the named session; empty creates a fresh one")
	sessionsDir := flag.String("sessions-dir", "", "directory for session files; defaults to <workdir>/.claw/sessions")

	// New compact flags.
	compactRecentTurns := flag.Int("compact-recent-turns", 4, "verbatim window: keep this many recent user turns un-stripped")
	compactFallbackLimit := flag.Int("compact-fallback-limit", 32000, "context limit when --model is unknown to the registry")
	compactSafetyFactor := flag.Float64("compact-safety-factor", 0.75, "fraction of the model's context window to consume")
	compactMaxToolBytes := flag.Int("compact-max-tool-bytes", 65536, "tool result boundary cap; over this triggers disk spill")

	// Removed flags — hard error pointing at the replacement.
	flag.Var(&removedFlag{name: "trim-strategy", replacement: "compact-* (the trim strategy registry was removed)"}, "trim-strategy", "REMOVED: see --compact-* flags")
	flag.Var(&removedFlag{name: "max-context-turns", replacement: "compact-recent-turns"}, "max-context-turns", "REMOVED: use --compact-recent-turns")
	flag.Var(&removedFlag{name: "max-context-tokens", replacement: "compact-fallback-limit"}, "max-context-tokens", "REMOVED: use --compact-fallback-limit")

	flag.Parse()

	cwd := *workDir
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			log.Fatalf("get cwd: %v", err)
		}
	}

	userPrompt := *prompt
	if userPrompt == "" && !isTerminal(os.Stdin) {
		stdin, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatalf("read stdin: %v", err)
		}
		userPrompt = string(stdin)
	}
	if userPrompt == "" {
		fmt.Fprintln(os.Stderr, "no prompt provided (use --prompt or pipe to stdin)")
		os.Exit(2)
	}

	llm, err := newProvider(*providerName, *model, *maxTokens)
	if err != nil {
		log.Fatalf("init provider: %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(cwd))
	registry.Register(tools.NewWriteFileTool(cwd))
	registry.Register(tools.NewEditFileTool(cwd))
	registry.Register(tools.NewBashTool(cwd))

	contextManager, err := agentcontext.NewManager(cwd)
	if err != nil {
		log.Fatalf("init context: %v", err)
	}
	if contextManager.HasSkills() {
		registry.Register(agentcontext.NewLoadSkillTool(contextManager))
	}

	resolvedSessionsDir := *sessionsDir
	if resolvedSessionsDir == "" {
		resolvedSessionsDir = filepath.Join(cwd, ".claw", "sessions")
	}

	// Load optional model-limits override.
	overridesPath := filepath.Join(cwd, ".claw", "model-limits.yaml")
	overrides, err := compact.LoadOverridesYAML(overridesPath)
	if err != nil {
		log.Fatalf("model-limits override: %v", err)
	}

	sess, resumed, err := openOrCreateSession(*sessionID, resolvedSessionsDir)
	if err != nil {
		log.Fatalf("session: %v", err)
	}
	defer sess.Close()
	if resumed {
		fmt.Fprintf(os.Stderr, "session: %s (resumed, %d messages)\n", sess.ID(), len(sess.Messages()))
	} else {
		fmt.Fprintf(os.Stderr, "session: %s\n", sess.ID())
	}

	// Register read_tool_output now that the session exists.
	registry.Register(tools.NewReadToolOutputTool(sess, *compactMaxToolBytes))

	resolvedModel := *model
	if resolvedModel == "" {
		resolvedModel = os.Getenv("LLM_MODEL")
	}
	compactCfg := compact.Config{
		MaxToolBytes:        *compactMaxToolBytes,
		RecentTurnsVerbatim: *compactRecentTurns,
		Overrides:           overrides,
	}
	if _, ok := compact.LookupModelLimit(resolvedModel, overrides); !ok && resolvedModel != "" {
		// Documented in spec §0: unknown model => fallback limit; we
		// inject it into the overrides map so Budget() picks it up.
		overrides[resolvedModel] = *compactFallbackLimit
	}

	eng := engine.NewAgentEngine(llm, registry, cwd, *think)
	eng.SetContextManager(contextManager)
	eng.SetSession(sess)
	eng.SetCompactor(compact.NewCompactor(compactCfg))
	eng.SetCalibrator(compact.NewCalibrator(0.3))
	eng.SetCompactConfig(compactCfg)
	eng.SetModelID(resolvedModel)
	eng.SetOutputCap(*maxTokens)
	eng.SetSafetyFactor(*compactSafetyFactor)
	eng.SetModelLimitOverrides(overrides)
	eng.MaxTurns = *maxTurns

	reporter := engine.NewTerminalReporter()
	reporter.AttachSession(sess)

	if err := eng.Run(context.Background(), userPrompt, reporter); err != nil {
		log.Fatalf("engine: %v", err)
	}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func newProvider(name, model string, maxTokens int) (provider.LLMProvider, error) {
	switch name {
	case "openai":
		return provider.NewOpenAIProvider(model)
	case "claude":
		p, err := provider.NewClaudeProvider(model)
		if err != nil {
			return nil, err
		}
		if maxTokens > 0 {
			p.MaxTokens = int64(maxTokens)
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unknown provider %q (supported: openai, claude)", name)
	}
}

func openOrCreateSession(id string, dir string) (*agentsession.Session, bool, error) {
	if id == "" {
		s, err := agentsession.NewSession(dir)
		if err != nil {
			return nil, false, err
		}
		return s, false, nil
	}
	if !agentsession.IsValidID(id) {
		return nil, false, fmt.Errorf("invalid session id %q (must match YYYYMMDD-HHMMSS-XXXXXX)", id)
	}
	s, err := agentsession.OpenSession(id, dir)
	if err != nil {
		return nil, false, err
	}
	return s, true, nil
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run:

```bash
go build ./...
go test ./tests/cmd -count=1
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/claw/main.go tests/cmd/main_flags_test.go
git commit -m "feat(cli): replace trim flags with adaptive compaction flags"
```

---

### Task 17: Delete legacy session trimmer files

**Files:**
- Delete: `internal/session/window.go`
- Delete: `internal/session/trim.go`
- Delete: `internal/session/tokens.go`
- Delete: `tests/session/trim_test.go` (if it exists)
- Delete: `tests/session/trim_window_test.go` (if it exists)
- Delete: `tests/session/tokens_test.go` (if it exists)

A pure deletion + verification task. Every caller migrated in Tasks 1–16; this is the cleanup. By the end of this task `rg -l 'WindowTrimmer|TrimStrategy|defensiveCleanup' internal/session/` returns nothing.

- [ ] **Step 1: Verify no live callers remain**

Run:

```bash
rg -l 'WindowTrimmer|TrimConfig|session\.Trimmer|session\.Register|session\.Get\b' internal/ cmd/ tests/
```

Expected: no matches in `internal/engine/`, no matches in `cmd/claw/`. The only acceptable matches are inside the files we're about to delete. If a match shows up elsewhere, STOP and migrate that caller before deleting.

- [ ] **Step 2: Delete the files**

```bash
rm internal/session/window.go
rm internal/session/trim.go
rm internal/session/tokens.go
rm -f tests/session/trim_test.go
rm -f tests/session/trim_window_test.go
rm -f tests/session/tokens_test.go
```

- [ ] **Step 3: Verify the build is still green**

Run:

```bash
go build ./...
go vet ./...
go test ./... -count=1
```

Expected: PASS. If a "package not used" or "undefined: session.Trimmer" error appears, restore the file and re-verify Step 1 — a caller was missed.

- [ ] **Step 4: Verify no symbol references remain**

Run:

```bash
rg -l 'WindowTrimmer|TrimStrategy|defensiveCleanup|EstimateTokens' internal/session/ tests/session/
```

Expected: no matches in `internal/session/` (the compact package owns these now). `EstimateTokens` may legitimately appear in `tests/session/` only via a session-public helper if any survived — verify it does not.

- [ ] **Step 5: Commit**

```bash
git add -A internal/session tests/session
git commit -m "refactor(session): delete legacy WindowTrimmer + tokens (superseded by compact pkg)"
```

---

### Task 18: Layer 4 — committed real-case fixtures + goldens + test

**Files:**
- Create: `testdata/compact/session-huge-bash.jsonl`
- Create: `testdata/compact/session-many-edits.jsonl`
- Create: `testdata/compact/session-mixed-tools.jsonl`
- Create: `testdata/compact/golden/session-huge-bash.view.txt`
- Create: `testdata/compact/golden/session-huge-bash.digest.txt`
- Create: `testdata/compact/golden/session-huge-bash.stats.json`
- Create: `testdata/compact/golden/session-many-edits.view.txt`
- Create: `testdata/compact/golden/session-many-edits.digest.txt`
- Create: `testdata/compact/golden/session-many-edits.stats.json`
- Create: `testdata/compact/golden/session-mixed-tools.view.txt`
- Create: `testdata/compact/golden/session-mixed-tools.digest.txt`
- Create: `testdata/compact/golden/session-mixed-tools.stats.json`
- Create: `tests/engine/gen_fixtures/main.go`
- Create: `tests/engine/compact_realcase_test.go`

**MANDATORY USER REQUIREMENT** — load-bearing real-case e2e verification. Do NOT trim.

Three committed JSONL fixtures, three sets of goldens (compacted view as plain text, the synthetic digest content, and the resulting `CompactStats` JSON), plus a small generator script so the fixtures can be regenerated deterministically. `session-pathological.jsonl` (~200 MB) is **synthesized at test-time by `TestMain`** so the git index stays small.

Size choice for the committed fixture (documented in the file's first comment line): we ship a representative ~200 KB single-message blow-up rather than the 8 MB referenced in the spec — enough to trigger Layer B reliably under the default budget but small enough that `git diff` and CI checkouts stay fast.

- [ ] **Step 1: Write the fixture generator**

Create `tests/engine/gen_fixtures/main.go`:

```go
// gen_fixtures regenerates testdata/compact/*.jsonl from a deterministic
// seed. Committed to the repo so the fixtures can be reproduced without
// trusting whatever shell+jq invocations produced them originally.
//
// Run from the repo root:
//   go run ./tests/engine/gen_fixtures
//
// The script always writes the same bytes for the same seed. If you
// need to evolve a fixture, change the seed AND the golden files in
// the same commit so the test catches drift.

package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

const fixturesDir = "testdata/compact"

func main() {
	if err := os.MkdirAll(fixturesDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	writeFixture(fixturesDir+"/session-huge-bash.jsonl", genHugeBash())
	writeFixture(fixturesDir+"/session-many-edits.jsonl", genManyEdits())
	writeFixture(fixturesDir+"/session-mixed-tools.jsonl", genMixedTools())
	fmt.Println("regenerated fixtures in", filepath.Clean(fixturesDir))
}

func writeFixture(path string, msgs []schema.Message) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	// First line is a JSON-comment-ish stub the loader tolerates as
	// "skip if it doesn't parse".
	fmt.Fprintf(f, "// generated by tests/engine/gen_fixtures; do not edit by hand\n")
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			panic(err)
		}
		f.Write(b)
		f.WriteString("\n")
	}
}

func genHugeBash() []schema.Message {
	r := rand.New(rand.NewSource(1))
	msgs := []schema.Message{}
	for i := 1; i <= 30; i++ {
		msgs = append(msgs, schema.Message{Role: schema.RoleUser, Content: fmt.Sprintf("turn %d question", i)})
		if i == 15 {
			// One huge bash result. ~200 KB so the file stays git-friendly
			// but Layer B reliably engages under default budgets.
			args, _ := json.Marshal(map[string]string{"command": "find / -type f 2>/dev/null"})
			msgs = append(msgs, schema.Message{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{{
					ID:        fmt.Sprintf("call_huge_%d", i),
					Name:      "bash",
					Arguments: args,
				}},
			})
			body := strings.Repeat(randLine(r)+"\n", 4000) // ~200 KB
			msgs = append(msgs, schema.Message{
				Role:       schema.RoleTool,
				Content:    body,
				ToolCallID: fmt.Sprintf("call_huge_%d", i),
			})
		} else {
			msgs = append(msgs, schema.Message{Role: schema.RoleAssistant, Content: fmt.Sprintf("turn %d answer", i)})
		}
	}
	return msgs
}

func genManyEdits() []schema.Message {
	r := rand.New(rand.NewSource(2))
	msgs := []schema.Message{}
	for i := 1; i <= 50; i++ {
		msgs = append(msgs, schema.Message{Role: schema.RoleUser, Content: fmt.Sprintf("edit %d please", i)})
		args, _ := json.Marshal(map[string]string{
			"path":       fmt.Sprintf("file_%d.go", i),
			"new_string": strings.Repeat(randLine(r)+"\n", 200), // ~10 KB
		})
		msgs = append(msgs, schema.Message{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{{
				ID:        fmt.Sprintf("call_edit_%d", i),
				Name:      "edit_file",
				Arguments: args,
			}},
		})
		msgs = append(msgs, schema.Message{
			Role: schema.RoleTool, Content: "ok",
			ToolCallID: fmt.Sprintf("call_edit_%d", i),
		})
	}
	return msgs
}

func genMixedTools() []schema.Message {
	r := rand.New(rand.NewSource(3))
	msgs := []schema.Message{}
	tools := []string{"read_file", "bash", "edit_file", "write_file"}
	for i := 1; i <= 80; i++ {
		msgs = append(msgs, schema.Message{Role: schema.RoleUser, Content: fmt.Sprintf("turn %d", i)})
		tool := tools[i%len(tools)]
		args, _ := json.Marshal(map[string]string{"path": fmt.Sprintf("f%d.go", i)})
		msgs = append(msgs, schema.Message{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{{
				ID:        fmt.Sprintf("call_%s_%d", tool, i),
				Name:      tool,
				Arguments: args,
			}},
		})
		msgs = append(msgs, schema.Message{
			Role:       schema.RoleTool,
			Content:    randLine(r) + " " + randLine(r),
			ToolCallID: fmt.Sprintf("call_%s_%d", tool, i),
		})
	}
	return msgs
}

func randLine(r *rand.Rand) string {
	const alpha = "abcdefghijklmnopqrstuvwxyz "
	b := make([]byte, 50)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}
```

Run it once to populate `testdata/compact/`:

```bash
go run ./tests/engine/gen_fixtures
```

- [ ] **Step 2: Write the real-case test (failing — goldens not yet present)**

Create `tests/engine/compact_realcase_test.go`:

```go
package engine_test

import (
	"bufio"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/compact/golden/* from current behaviour")

var pathologicalOnce sync.Once
var pathologicalPath string

// TestMain synthesises session-pathological.jsonl at test-time (single
// tool output ~200 MB). We never commit it to git — the helper creates
// the file in TempDir and a global path holds it for the test cases.
func TestMain(m *testing.M) {
	flag.Parse()
	pathologicalOnce.Do(func() {
		dir, err := os.MkdirTemp("", "claw-pathological-*")
		if err != nil {
			panic(err)
		}
		path := filepath.Join(dir, "session-pathological.jsonl")
		f, err := os.Create(path)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		writeMsg := func(m schema.Message) {
			b, _ := json.Marshal(m)
			f.Write(b)
			f.WriteString("\n")
		}
		writeMsg(schema.Message{Role: schema.RoleUser, Content: "find everything"})
		args, _ := json.Marshal(map[string]string{"command": "find /"})
		writeMsg(schema.Message{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{{ID: "call_path_huge", Name: "bash", Arguments: args}},
		})
		huge := strings.Repeat("x", 200*1024*1024) // 200 MB
		writeMsg(schema.Message{Role: schema.RoleTool, Content: huge, ToolCallID: "call_path_huge"})
		writeMsg(schema.Message{Role: schema.RoleUser, Content: "summarise"})
		writeMsg(schema.Message{Role: schema.RoleAssistant, Content: "many files"})
		pathologicalPath = path
	})
	os.Exit(m.Run())
}

func loadFixture(t *testing.T, path string) []schema.Message {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %q: %v", path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), 256*1024*1024) // accommodate pathological
	var out []schema.Message
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "//") || line == "" {
			continue
		}
		var m schema.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("fixture %q: bad json: %v", path, err)
		}
		out = append(out, m)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("fixture %q: scan: %v", path, err)
	}
	return out
}

func budgetForClaudeOpus47() int {
	return compact.Budget(compact.BudgetInput{
		Model:        "claude-opus-4-7",
		OutputCap:    4096,
		SafetyFactor: 0.75,
	})
}

func TestCompact_RealCase(t *testing.T) {
	cases := []struct {
		name              string
		fixture           string
		wantLayerB        bool
		wantSpilledAtLeast int
		budget            int
	}{
		{"huge-bash", "testdata/compact/session-huge-bash.jsonl", true, 0, budgetForClaudeOpus47()},
		{"many-edits", "testdata/compact/session-many-edits.jsonl", false, 0, budgetForClaudeOpus47()},
		{"mixed-tools", "testdata/compact/session-mixed-tools.jsonl", false, 0, budgetForClaudeOpus47()},
		{"pathological", pathologicalPath, true, 0, budgetForClaudeOpus47()},
	}

	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        65536,
		RecentTurnsVerbatim: 4,
	})
	cal := compact.NewCalibrator(0.3)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fixture == "" {
				t.Skip("fixture path unset; TestMain may have failed")
			}
			history := loadFixture(t, tc.fixture)
			view, stats := c.View(history, tc.budget, 10, cal)

			if tc.wantLayerB && !stats.LayerBEngaged {
				t.Fatalf("Layer B expected but not engaged: %+v", stats)
			}
			if !tc.wantLayerB && stats.LayerBEngaged {
				t.Fatalf("Layer B engaged but not expected: %+v", stats)
			}

			if tc.name == "pathological" {
				return // do not write goldens for the 200 MB case
			}

			viewPath := filepath.Join("testdata", "compact", "golden", tc.name+".view.txt")
			digestPath := filepath.Join("testdata", "compact", "golden", tc.name+".digest.txt")
			statsPath := filepath.Join("testdata", "compact", "golden", tc.name+".stats.json")
			actualView := renderView(view)
			actualDigest := extractDigest(view)
			actualStats, _ := json.MarshalIndent(stats, "", "  ")

			if *updateGolden {
				_ = os.MkdirAll(filepath.Dir(viewPath), 0o755)
				_ = os.WriteFile(viewPath, []byte(actualView), 0o600)
				_ = os.WriteFile(digestPath, []byte(actualDigest), 0o600)
				_ = os.WriteFile(statsPath, actualStats, 0o600)
				return
			}

			assertFileEquals(t, viewPath, actualView)
			assertFileEquals(t, digestPath, actualDigest)
			assertFileEquals(t, statsPath, string(actualStats))
		})
	}
}

func renderView(view []schema.Message) string {
	var b strings.Builder
	for _, m := range view {
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		if len(m.ToolCalls) > 0 {
			b.WriteString(" [calls=")
			for i, c := range m.ToolCalls {
				if i > 0 {
					b.WriteString(",")
				}
				b.WriteString(c.Name)
			}
			b.WriteString("]")
		}
		b.WriteString("\n---\n")
	}
	return b.String()
}

func extractDigest(view []schema.Message) string {
	for _, m := range view {
		if m.Role == schema.RoleAssistant && strings.HasPrefix(m.Content, "## Prior session digest") {
			return m.Content
		}
	}
	return "(no digest)"
}

func assertFileEquals(t *testing.T, path, got string) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %q: %v (run go test -run TestCompact_RealCase -update to regenerate)", path, err)
	}
	if string(want) != got {
		t.Fatalf("golden mismatch at %s:\nwant=%q\ngot=%q", path, string(want), got)
	}
}
```

- [ ] **Step 3: Run test once with `-update` to populate goldens**

```bash
go test ./tests/engine -run TestCompact_RealCase -update -count=1
```

Inspect every file under `testdata/compact/golden/` and confirm:

- `huge-bash.digest.txt` starts with `## Prior session digest` and references the `call_huge_*` IDs.
- `many-edits.stats.json` shows `"layer_b_engaged": false` (the 50 edit_file calls together fit under the Claude Opus default budget; Layer A is enough).
- `mixed-tools.view.txt` has no digest header (Layer A only).

If `many-edits` unexpectedly engages Layer B, lower the per-edit content size in `genManyEdits` and regenerate fixtures + goldens; the test name encodes the contract.

- [ ] **Step 4: Run test without `-update` and verify it passes**

```bash
go test ./tests/engine -run TestCompact_RealCase -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit fixtures, goldens, generator, and test together**

```bash
git add testdata/compact tests/engine/gen_fixtures tests/engine/compact_realcase_test.go
git commit -m "test(compact): Layer 4 real-case fixtures, goldens, and -update support"
```

---

### Task 19: Layer 5 — opt-in live-provider smoke test

**Files:**
- Create: `tests/engine/compact_live_test.go`
- Modify: `CLAUDE.md`

**MANDATORY USER REQUIREMENT** — load-bearing e2e verification against a real provider. Do NOT trim.

Guarded by `//go:build live_provider`. Default `go test ./...` skips it. Invoked with `go test -tags=live_provider ./tests/engine -run TestCompact_LiveClaude -count=1`; requires `ANTHROPIC_API_KEY`. Validates three things end-to-end: no Go-process OOM during a multi-MB tool result, `OnCompact` fires with `LayerBEngaged=true`, and a follow-up turn successfully calls `read_tool_output` and gets a non-empty chunk.

The plan-step that mentions this MUST instruct the implementer to verify the file **compiles** under the live_provider tag via `go vet -tags=live_provider ./tests/engine` — even when not running the test.

- [ ] **Step 1: Write the test**

Create `tests/engine/compact_live_test.go`:

```go
//go:build live_provider

package engine_test

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	"github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// liveReporter captures stats for assertion and forwards messages.
type liveReporter struct {
	stats           []compact.CompactStats
	layerBFired     atomic.Bool
	readToolUsed    atomic.Bool
	lastChunk       string
	lastChunkIsErr  bool
}

func (r *liveReporter) OnThinking(_ context.Context)                {}
func (r *liveReporter) OnToolCall(_ context.Context, _, _ string)   {}
func (r *liveReporter) OnMessage(_ context.Context, _ string)       {}
func (r *liveReporter) OnToolResult(_ context.Context, name, result string, isError bool) {
	if name == "read_tool_output" {
		r.readToolUsed.Store(true)
		r.lastChunk = result
		r.lastChunkIsErr = isError
	}
}
func (r *liveReporter) OnCompact(_ context.Context, s compact.CompactStats) {
	r.stats = append(r.stats, s)
	if s.LayerBEngaged {
		r.layerBFired.Store(true)
	}
}

func TestCompact_LiveClaude(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set; skipping live-provider smoke")
	}

	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer sess.Close()

	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(dir))
	registry.Register(tools.NewReadToolOutputTool(sess, 65536))

	llm, err := provider.NewClaudeProvider("claude-opus-4-7")
	if err != nil {
		t.Fatalf("claude: %v", err)
	}

	// Memory ceiling — track residency before and after to detect leak/OOM.
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	cfg := compact.Config{MaxToolBytes: 65536, RecentTurnsVerbatim: 4}
	c := compact.NewCompactor(cfg)
	cal := compact.NewCalibrator(0.3)

	eng := engine.NewAgentEngine(llm, registry, dir, false)
	eng.SetSession(sess)
	eng.SetCompactor(c)
	eng.SetCalibrator(cal)
	eng.SetCompactConfig(cfg)
	eng.SetModelID("claude-opus-4-7")
	eng.SetOutputCap(4096)
	eng.SetSafetyFactor(0.75)
	eng.MaxTurns = 6

	rep := &liveReporter{}
	prompt := `run "find / -type f 2>/dev/null | head -50000" and tell me the count, ` +
		`then call read_tool_output on the same call_id to confirm you can retrieve a chunk`

	if err := eng.Run(context.Background(), prompt, rep); err != nil {
		t.Logf("engine returned (may be expected if model stopped early): %v", err)
	}

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	const memCeiling = uint64(512 << 20) // 512 MB
	if memAfter.HeapAlloc > memBefore.HeapAlloc+memCeiling {
		t.Fatalf("heap grew by %d MB (>= 512 MB ceiling)",
			(memAfter.HeapAlloc-memBefore.HeapAlloc)>>20)
	}

	if !rep.layerBFired.Load() {
		t.Fatalf("Layer B never engaged across %d compact events", len(rep.stats))
	}
	if !rep.readToolUsed.Load() {
		t.Fatalf("model did not call read_tool_output during follow-up turn")
	}
	if rep.lastChunkIsErr {
		t.Fatalf("read_tool_output returned error: %q", rep.lastChunk)
	}
	if strings.TrimSpace(rep.lastChunk) == "" {
		t.Fatalf("read_tool_output returned empty chunk")
	}
}
```

- [ ] **Step 2: Verify the file compiles under the live_provider tag**

```bash
go vet -tags=live_provider ./tests/engine
```

Expected: PASS (no compile errors). This step is required even though we are not running the test — a compile-broken Layer-5 file silently lives on `main` forever otherwise.

- [ ] **Step 3: Run the default test suite and confirm Layer 5 is skipped**

```bash
go test ./tests/engine -count=1
```

Expected: PASS. The Layer-5 file is excluded by the build tag.

- [ ] **Step 4: (Optional) Run the live smoke if you have an API key**

```bash
ANTHROPIC_API_KEY=sk-ant-... go test -tags=live_provider ./tests/engine -run TestCompact_LiveClaude -count=1 -v
```

Expected: PASS within a few minutes. If the model refuses to run `find /` (some safety-tuned variants do), the prompt above can be replaced with another huge-output-producing command from the test author's environment.

- [ ] **Step 5: Document the invocation in CLAUDE.md**

Add a section near the end of `CLAUDE.md` (alongside other "Running tests" guidance):

```markdown
## Running the live-provider smoke test

`tests/engine/compact_live_test.go` is gated by the `live_provider` build
tag and is skipped by default. It exercises the full adaptive-compaction
pipeline against a real Claude endpoint and is the canonical "did the
OOM fix really land" gate.

```bash
ANTHROPIC_API_KEY=sk-ant-... \
  go test -tags=live_provider ./tests/engine -run TestCompact_LiveClaude -count=1 -v
```

Even without an API key, run `go vet -tags=live_provider ./tests/engine`
before merging changes that touch `internal/compact/`, `internal/engine/`,
or `internal/session/`. The build-tagged file must still compile.
```

- [ ] **Step 6: Commit**

```bash
git add tests/engine/compact_live_test.go CLAUDE.md
git commit -m "test(compact): Layer 5 opt-in live-provider smoke (build tag live_provider)"
```

---

### Task 20: Final validation, changelog, TODOs

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `TODOS.md` (only if an existing entry was closed by this work)

The closeout. Run the full default test suite, verify the `internal/compact/...` coverage ceiling, update the changelog with a user-facing summary, and remove any TODOS items closed by this feature.

- [ ] **Step 1: Run the full test suite clean**

```bash
go test ./... -count=1
```

Expected: PASS across every package. If a flake surfaces, re-run; if it persists, fix before merging.

- [ ] **Step 2: Verify coverage**

```bash
go test -cover ./internal/compact/...
```

Expected: each package in `internal/compact/...` reports ≥ 85% line coverage. If any package dips below, write the missing unit tests in the appropriate `tests/compact/*_test.go` file and re-run.

- [ ] **Step 3: Verify the Layer-5 build still compiles**

```bash
go vet -tags=live_provider ./tests/engine
```

Expected: PASS.

- [ ] **Step 4: Update CHANGELOG.md**

Add a new section at the top (above the previous entry):

```markdown
## v0.7.0.0 — 2026-05-20

### Added

- **Adaptive semantic compaction.** Replaced the drop-based session
  trimmer with a deterministic, semantics-preserving compactor
  (`internal/compact`). Layer A applies structural shrink to every
  message (tool-result truncation with spill marker, write_file /
  edit_file argument stripping outside the verbatim window). Layer B
  rolls oldest non-verbatim turns into a deterministic digest. Pure
  function — no second model, no LLM round-trip.
- **Tool-output disk spill.** Tool results larger than
  `--compact-max-tool-bytes` (default 64 KB) spill to
  `.claw/sessions/<id>-tool-outputs/<call_id>.txt`. The original
  result is replaced with a head+tail marker that names the call_id.
- **`read_tool_output` built-in tool.** Model-facing chunked retrieval
  of spilled outputs. Supports `start_line`, `line_count` (max 1000),
  and surfaces total line counts so the model knows how much remains.
- **Adaptive token budget.** Per-model context-window registry in
  `internal/compact/modellimits.go` plus optional override at
  `.claw/model-limits.yaml`. Budget derives from the resolved limit
  times `--compact-safety-factor` (default 0.75) minus the requested
  `--max-tokens` output cap.
- **EWMA calibrator.** Self-tunes the chars/4 local estimate toward
  the provider-reported `Usage.InputTokens` after 2-3 turns. Resets
  per session.
- **`OnCompact` reporter callback.** Prints a `[compact] turn N: ...`
  line to stderr when Layer B engaged, when tool outputs spilled, or
  when ≥ 5% was saved. Audited to
  `.claw/sessions/<id>-compact-events.jsonl` under the same per-write
  flock as message persistence.

### Removed

- `--trim-strategy`, `--max-context-turns`, `--max-context-tokens`
  CLI flags. Using any of them prints a hard error pointing at the
  replacement (`--compact-recent-turns`, `--compact-fallback-limit`).
- `internal/session/window.go`, `internal/session/trim.go`,
  `internal/session/tokens.go`. The trim strategy registry is gone.

### Test coverage

- 4-layer pyramid: unit (`tests/compact/`), integration
  (`tests/engine/compact_integration_test.go`), real-case fixtures
  with goldens (`tests/engine/compact_realcase_test.go` +
  `testdata/compact/`), and live-provider smoke
  (`tests/engine/compact_live_test.go`, build tag `live_provider`).
- Default `go test ./...` runs layers 1-4; layer 5 is invoked with
  `go test -tags=live_provider ./tests/engine -run TestCompact_LiveClaude`.

### Migration

Existing on-disk sessions resume unchanged — the JSONL format is the
same; only the read-time view producer is replaced. CLI users on the
removed flags must switch to the `--compact-*` equivalents.
```

- [ ] **Step 5: Update TODOS.md if applicable**

Inspect `TODOS.md`. If there are entries about OOM from large tool outputs, adaptive budgets, or context trimming that this PR closes, remove or strike them with a note pointing at this CHANGELOG entry. If no entries match exactly, leave the file alone.

- [ ] **Step 6: Final commit**

```bash
git add CHANGELOG.md TODOS.md
git commit -m "chore: complete adaptive semantic compaction (final validation)"
```

- [ ] **Step 7: Verify branch state**

```bash
git log --oneline main..HEAD
```

Expected: 20 commits in the order the tasks were authored. Run `go test ./... -count=1` one more time as a final smoke before opening the PR.

---

