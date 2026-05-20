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

