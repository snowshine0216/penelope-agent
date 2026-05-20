# Adaptive Semantic Compaction Design

**Date:** 2026-05-20
**Status:** Approved (pending user spec review)
**Scope:** Replace the current `internal/session/{window,trim,tokens}.go` "drop the oldest user turn" trimmer with a deterministic, semantics-preserving compactor; cap large tool outputs at the engine boundary with disk spill + paged retrieval; surface real model token usage; report compaction stats per turn.

## Problem

The current trimmer in [internal/session/window.go](../../../internal/session/window.go) addressed one OOM case (provider request too large) but leaves five problems unsolved:

1. **Single huge tool output still OOMs.** The result is appended to `sess.messages` at full size before the trimmer runs; a single multi-MB result blows the Go process heap as well as the provider's request limit.
2. **Messages outside the window are thrown away.** `dropOldestUserTurn` removes the oldest user turn (and everything between it and the next user turn) from the provider view permanently, sacrificing context that may still matter.
3. **Token threshold is hardcoded** (`--max-context-tokens=32000`) and does not adapt to the model in use, the model's real reported usage, or the local-vs-tokenizer estimator drift.
4. **The trim strategy itself is the wrong shape.** A "drop oldest" policy can never preserve semantics; we want compaction that reduces tokens while keeping the *meaning* of what happened.
5. **No visibility.** The CLI is silent about how much was compacted, when, or why.
6. **No second model.** Any compaction must be deterministic and run in-process; we cannot spend an extra round-trip on a summarizer LLM.

The terminology in the original ask referred to `internal/context/`, but the OOM/window/trim code actually lives in `internal/session/`. `internal/context/` only handles skill catalogs and system-prompt composition and is out of scope here.

## Goals & non-goals

**Goals.**

- Eliminate Go-process OOM from large single tool results.
- Eliminate provider "request too large" errors from cumulative history growth.
- Never lose tool output content — keep it addressable on disk and retrievable in chunks by the model.
- Replace the drop-based trimmer with a deterministic, semantics-preserving compactor that runs in-process with no second model.
- Derive the token budget adaptively from per-model context-window limits plus calibration against the provider's real usage counts.
- Surface per-turn compaction stats to the user and to a session-scoped audit log.

**Non-goals.**

- Persistent cross-session learning. The calibrator resets on resume.
- Mid-session model switching. The CLI fixes the model at start.
- A pluggable strategy registry. There is exactly one compactor implementation.
- A "legacy window" escape hatch. The new compactor degrades cleanly under tight budgets; we are not shipping two code paths.

## Architecture

**Package layout.**

```
internal/
├── compact/                    NEW — read-only provider-view producer
│   ├── compactor.go            Compactor.View(history, budget, calibrator)
│   ├── shrink.go               Layer A: per-message structural shrink
│   ├── digest.go               Layer B: rolling deterministic digest
│   ├── budget.go               Budget(BudgetInput) int
│   ├── modellimits.go          defaultModelLimits + YAML override loader
│   ├── calibration.go          Calibrator (EWMA ratio)
│   ├── cleanup.go              defensiveCleanup (moved from session/window.go)
│   ├── tokens.go               EstimateTokens / EstimateOne (moved from session/tokens.go)
│   └── stats.go                CompactStats
├── session/
│   ├── session.go              UNCHANGED message I/O
│   ├── store.go                UNCHANGED
│   ├── tool_spill.go           NEW — spill + chunked retrieval helpers
│   └── (window.go, trim.go, tokens.go DELETED)
├── provider/
│   ├── interface.go            CHANGED — Generate returns *Response{Message, Usage}
│   ├── claude.go               CHANGED — capture resp.Usage
│   └── openai.go               CHANGED — capture usage.{prompt,completion}_tokens
├── engine/
│   ├── loop.go                 CHANGED — uses compact.Compactor, threads lastUsage
│   ├── tool_execution.go       CHANGED — boundary cap + spill before sess.Append
│   ├── reporter.go             CHANGED — adds OnCompact(ctx, stats)
│   └── terminal_reporter.go    CHANGED — implements OnCompact
└── tools/
    └── read_tool_output.go     NEW — built-in tool, registered in main.go
cmd/claw/
└── main.go                     CHANGED — removes old flags, adds --compact-*
```

**Read-time, not write-time.** The session JSONL stays append-only and complete; the compactor reduces a *view* every turn without mutating history on disk.

**Per-turn flow.**

```
turn N starts
  history := sess.Messages()
  budget  := compact.Budget(BudgetInput{model, lastUsage, outputCap, safetyFactor})
  view, stats := compactor.View(history, budget, calibrator)
  resp, err := provider.Generate(ctx, view, tools)
  calibrator.Observe(stats.LocalEstimate, resp.Usage.InputTokens)
  lastUsage = resp.Usage
  if shouldEmit(stats) { reporter.OnCompact(ctx, stats) }
  // ... tool execution (with boundary cap + spill) ...
```

## Component design

### 1. The compactor

**Turn boundary.** A "turn" is one user message plus the assistant message(s) and tool message(s) following it until the next user message. All Layer A / Layer B operations work on turn boundaries so an assistant `tool_calls` is never separated from its matching `tool` results (which would break providers).

**Layer A — per-message structural shrink (always on, deterministic).**

| Message kind | Rule |
| --- | --- |
| `tool` result ≤ `MaxToolBytes` | unchanged |
| `tool` result > `MaxToolBytes` | `TruncateForLLM` head+tail with elision marker that includes `call_id` and total bytes/lines; the full output was already spilled at the engine boundary |
| `assistant` `tool_calls` for `write_file` / `edit_file` | strip large `content` / `new_string` / `old_string` fields, replace with `"<content elided: N bytes>"`; call ID, tool name, file path preserved |
| `assistant` `tool_calls` for other tools | unchanged (args are small) |
| `assistant` text, `user` text | unchanged |

The last `RecentTurnsVerbatim` turns (default 4) skip the `write_file` / `edit_file` arg stripping — they remain "live" so the model can reason about its recent actions in full. Tool-result head/tail truncation still applies inside the verbatim window because a single huge result can blow the budget on its own.

**Layer B — rolling deterministic digest (engaged only when Layer A is not enough).**

If `EstimateTokens(layerA_view) > budget`, fold the oldest non-verbatim turns into a single synthetic assistant message inserted at position 1 (after system, before the verbatim tail). The digest grows backward — oldest first — until the view fits.

Format (deterministic; no model involved):

```
## Prior session digest (turns 1..N, compacted)

Turn 1 — user: "fix the OOM in the trimmer..."
Turn 2 — assistant: planned 3-step approach, called tools:
  • read_file(internal/session/window.go) → 189 lines
  • read_file(internal/session/trim.go) → 73 lines
Turn 4 — assistant: tools:
  • edit_file(internal/session/window.go) → ok
  • bash("go test ./...") → FAIL (3 failures, 2,431 lines spilled; call_id=toolu_01abc)
```

One line per turn for user/assistant text (truncated to ~120 chars), one indented line per tool call with name, key arg (path / command head), outcome, and (for spilled outputs) `call_id` so the model can fetch the body via `read_tool_output`. Generated by walking the persisted messages — pure function, no I/O, no model.

**Trigger logic.**

```go
budget := compact.Budget(BudgetInput{Model, LastUsage, OutputCap, SafetyFactor})
viewA  := shrink.Apply(history, cfg)
if EstimateTokens(viewA) <= budget {
    return viewA, stats{Before, AfterLayerA: same, LayerBEngaged: false}
}
viewB, foldedTurns := digest.Fold(viewA, budget, cfg.RecentTurnsVerbatim)
return viewB, stats{Before, AfterLayerA, AfterLayerB, LayerBEngaged: true, TurnsFolded: foldedTurns}
```

If even `digest + verbatim tail` exceeds the budget, an emergency floor halves `MaxToolBytes` for the verbatim tail and retries once; if still over budget, send what we have and let the provider error — better a clean upstream 4xx than silent corruption. The latest user turn is never deleted.

### 2. Tool-output boundary cap + spill + paged retrieval

**Cap point.** At the engine tool-execution boundary, *before* `sess.Append`:

```go
result := registry.Execute(ctx, call)
if len(result.Output) > cfg.MaxToolBytes {
    path, lines, err := sess.SpillToolOutput(call.ID, result.Output)
    if err != nil {
        return fmt.Errorf("spill tool output for call %s: %w", call.ID, err)
    }
    marker := fmt.Sprintf(
        "...[%d bytes / %d lines spilled to tool-outputs/%s.txt; " +
        "use read_tool_output(call_id=%q, start_line=N, line_count=M) to read more]...",
        len(result.Output), lines, call.ID, call.ID,
    )
    result.Output = tools.TruncateWithMarker(result.Output, cfg.MaxToolBytes, marker)
}
sess.Append(toolResultMessage(result))
```

[`tools.TruncateForLLM`](../../../internal/tools/truncate.go) today hard-codes its elision marker. We refactor it minimally: introduce `tools.TruncateWithMarker(s, maxBytes, marker)` that performs the same head-cut / tail-cut / UTF-8-safe-boundary logic but accepts a caller-supplied marker string. The existing `TruncateForLLM` becomes a thin wrapper around `TruncateWithMarker` with its current fixed marker, so existing callers and tests are unaffected.

**Spill path.** `.claw/sessions/<session-id>/tool-outputs/<call-id>.txt`. Session-scoped (cleaned up with the session, no `/tmp` races or cross-session collisions). Survives across resumes — a session resumed days later can still read its own old outputs.

**`read_tool_output` tool.**

```
name: read_tool_output
description: Read a chunk of a previously-spilled tool output by its tool_call_id.
             Use this when an earlier tool result was too large and was elided.
             The elision marker in the original result shows the call_id and total lines.
input_schema:
  call_id:    string  (required)            the tool_call_id of the original call
  start_line: int     (default 1, 1-indexed)
  line_count: int     (default 200, max 1000)
returns: the requested line range with a "lines N-M of TOTAL" header and a tail
         marker if more remains. Subject to the same MaxToolBytes cap as any other
         tool result.
```

Implementation: `bufio.Scanner` with `scanner.Buffer` bumped to `MaxToolBytes` so realistic-length lines fit. If a single line exceeds `MaxToolBytes`, fall back to a byte-range read for that chunk and document the behavior. Unknown `call_id` returns a tool error (not an engine crash).

### 3. Adaptive budget + provider Usage plumbing

**Provider interface change.**

```go
// internal/provider/interface.go
type Usage struct {
    InputTokens  int // claude resp.Usage.InputTokens / openai usage.prompt_tokens
    OutputTokens int // claude resp.Usage.OutputTokens / openai usage.completion_tokens
}

type Response struct {
    Message *schema.Message
    Usage   Usage
}

type LLMProvider interface {
    Generate(ctx context.Context, msgs []schema.Message, tools []schema.ToolDefinition) (*Response, error)
}
```

Both providers already receive `Usage` from their SDKs — we just stop discarding it.

**Per-model context-window registry.**

```go
// internal/compact/modellimits.go
var defaultModelLimits = map[string]int{
    "claude-opus-4-7":           200_000,
    "claude-opus-4-7[1m]":       1_000_000,
    "claude-sonnet-4-6":         200_000,
    "claude-haiku-4-5-20251001": 200_000,
    "gpt-4o":                    128_000,
    "gpt-4o-mini":               128_000,
}
// Optional override file: <workdir>/.claw/model-limits.yaml
// Format: { model_id: max_context_tokens }
// Unknown model after registry + override miss => FallbackContextLimit (32_000).
```

**Budget derivation.**

```go
type BudgetInput struct {
    Model        string
    LastUsage    provider.Usage // zero on first turn
    OutputCap    int            // = the existing --max-tokens CLI flag (default 4096 for Claude)
    SafetyFactor float64        // default 0.75
}

func Budget(in BudgetInput) int {
    limit, ok := lookupModelLimit(in.Model)
    if !ok { limit = FallbackContextLimit }
    return int(float64(limit)*in.SafetyFactor) - in.OutputCap
}
```

We subtract `OutputCap` (max output we will request) rather than `LastUsage.OutputTokens` (what we got last time) because the provider hard-fails if `input + output` exceeds the window; we must reserve for the worst case we will allow ourselves to request.

**Calibrator.**

```go
// internal/compact/calibration.go
type Calibrator struct {
    ratio float64 // multiply local estimate by this to predict provider InputTokens
    alpha float64 // EWMA weight, default 0.3
}
func (c *Calibrator) Observe(localEst, providerActual int)
func (c *Calibrator) Predict(localEst int) int // = localEst * ratio
```

The compactor consults `Predict` when measuring against `budget` so chars/4 fudge factor self-corrects toward the real tokenizer after a few turns. Reset to `ratio=1.0` on session open / resume.

### 4. Visibility

**Reporter method.**

```go
type Reporter interface {
    OnThinking(ctx context.Context)
    OnMessage(ctx context.Context, content string)
    OnToolCall(ctx context.Context, name, args string)
    OnToolResult(ctx context.Context, name, output string, isError bool)
    OnCompact(ctx context.Context, stats CompactStats) // NEW
}

type CompactStats struct {
    Turn               int
    Before             int     // EstimateTokens(history) before any shrink
    AfterLayerA        int     // EstimateTokens after structural shrink only
    AfterLayerB        int     // == AfterLayerA if Layer B was skipped
    Budget             int
    Saved              int     // Before - AfterLayerB
    LayerBEngaged      bool
    TurnsFolded        int
    ToolOutputsSpilled int     // summed from engine for this turn
    CalibratorRatio    float64
}
```

**Emission rule.** Emit when `LayerBEngaged || ToolOutputsSpilled > 0 || Saved > Before/20` (≥5% saved). Otherwise stay quiet.

**Terminal output (stderr, same stream as `[engine]` logs):**

Short form:
```
[compact] turn 7: 48,210 → 47,920 tokens (saved 290) | 2 tool outputs spilled
```

Layer B engaged:
```
[compact] turn 12: 192,430 → 11,930 tokens (saved 180,500, 93.8%) | budget 144,000 | folded turns 1–8 into digest | 1 tool output spilled
```

Calibrator warming:
```
[compact] turn 2: 6,420 → 6,420 tokens (saved 0) | calibrator ratio 1.07 (warming)
```

**Audit JSONL.** Each `CompactStats` is appended to `.claw/sessions/<id>/compact-events.jsonl`. One line per emission, same per-session flock pattern as message persistence.

### 5. CLI flags

| Old flag | New flag | Default |
| --- | --- | --- |
| `--trim-strategy` | *(removed; hard error pointing to new flags)* | — |
| `--max-context-turns` | `--compact-recent-turns` | 4 |
| `--max-context-tokens` | `--compact-fallback-limit` (only for unknown model) | 32000 |
| *(new)* | `--compact-safety-factor` | 0.75 |
| *(new)* | `--compact-max-tool-bytes` | 65536 |

## Error handling

The rule: fail when we cannot produce a *correct* view; degrade when we cannot produce an *optimal* view.

| Site | Outcome | Reasoning |
| --- | --- | --- |
| `SpillToolOutput` write fails | Fail turn | `read_tool_output` depends on persistence; truncating-and-pretending-it's-saved is silent data loss |
| `provider.Generate` returns zero `Usage` | Degrade | Skip calibrator update; budget falls back to `localEstimate`. Log once per session |
| Model unknown, no override | Degrade | Use `FallbackContextLimit` (32k). Log once |
| Override file malformed YAML | Fail at NewCompactor | Fail-fast at startup beats silently ignoring user intent |
| `read_tool_output` unknown call_id | Tool error | Model can recover; engine continues |
| `read_tool_output` spill file missing | Tool error | Same; hint at session dir |
| `defensiveCleanup` would empty the view | Emergency floor | Substitute `lastUserMessage(tail)`, matches existing [loop.go:198](../../../internal/engine/loop.go:198) behavior |
| Layer B can't fit even digest + verbatim tail | Emergency floor + warning | Halve `MaxToolBytes` in verbatim tail and retry once; otherwise send what we have |

## Concurrency & determinism

- `Compactor.View` is a **pure function** of (history, budget, calibratorRatio). Same inputs → same output.
- The `Calibrator` is the only per-engine mutable state. It lives on `AgentEngine`, mutated only on the main goroutine after `provider.Generate` returns. No locks.
- `SpillToolOutput` writes to a per-call-ID path; the existing parallel-tool-call worker pool stays safe because each writer targets a distinct file.
- `compact-events.jsonl` uses the same per-session flock pattern as [session.go:71-92](../../../internal/session/session.go:71). Append-only, one line per emission.

## Edge cases

1. **First turn.** `lastUsage` zero; `Budget` returns `modelLimit*0.75 - OutputCap`. Layer A idempotent on small history. No emission.
2. **Tool result exactly at `MaxToolBytes`.** Strict `>` comparison: no spill, no truncation.
3. **Cross-session call_id collision.** Impossible — spill files are session-scoped.
4. **Resumed session.** JSONL reread into memory; spill files already on disk; `read_tool_output` works seamlessly. Calibrator resets to 1.0 and re-converges in 2–3 turns.
5. **Very long single line in tool output.** `bufio.Scanner.Buffer` bumped to `MaxToolBytes`. If a single line exceeds that, fall back to byte-range read; document the behavior.
6. **Model wants to re-read its own past output.** Desired path — digest tells it the `call_id`, it calls `read_tool_output`, gets the chunk. No recursion concerns (a 10KB chunk is well under the 64KB cap).
7. **Mid-session model switch.** Not supported in v1.

## Testing strategy

Four layers, all run as part of `go test ./...` by default. Live-provider smoke is gated by build tag and skipped without API keys.

### Layer 1 — Unit (pure, no I/O)

- `internal/compact/shrink_test.go` — Layer A rules per message kind; edge cases (empty content, missing `tool_call_id`); idempotency.
- `internal/compact/digest_test.go` — digest format goldens for representative session shapes; turn-boundary detection; call_id preservation.
- `internal/compact/budget_test.go` — known model; unknown-model fallback; output-cap subtraction.
- `internal/compact/calibration_test.go` — EWMA convergence; zero-`Usage` cold start.
- `internal/compact/compactor_test.go` — full `View()` pipeline: under-budget no-op, Layer A sufficient, Layer B engaged, emergency floor.
- `internal/session/tool_spill_test.go` — round-trip write/read; chunk boundaries; missing-call-id error.
- `internal/tools/read_tool_output_test.go` — argument validation; line-range bounds; large-line cap behavior.

### Layer 2 — Integration (engine + session + filesystem)

- `tests/engine/compact_integration_test.go` — fake provider returning huge tool calls; verify spill files exist, `read_tool_output` retrieves the right chunk, `lastUsage` threads correctly across turns, `OnCompact` events fire with expected `CompactStats`.

### Layer 3 — Property tests for invariants

- `internal/compact/properties_test.go`:
  - **Tool-call pairing.** For any history, the produced view either contains both an assistant `tool_calls` message AND every matching `tool` result, or contains neither.
  - **Budget invariant.** `EstimateTokens(View) <= budget` unless the emergency floor fired (in which case a warning was logged).

### Layer 4 — Real-case verification (committed fixtures + goldens)

Fixtures captured from real `claw` runs and committed to the repo:

```
testdata/compact/
├── session-huge-bash.jsonl          30+ turns, one bash output = 8 MB
├── session-many-edits.jsonl         50 edit_file calls with large new_string fields
├── session-mixed-tools.jsonl        Realistic Go-debugging session, 80 turns, ~2 MB total
├── session-pathological.jsonl       Single tool output = 200 MB (generated by TestMain helper)
└── golden/
    ├── session-huge-bash.view.txt
    ├── session-huge-bash.digest.txt
    ├── session-huge-bash.stats.json
    └── ...
```

`tests/engine/compact_realcase_test.go` loads each fixture, runs the compactor, and asserts on invariants and goldens:

```go
cases := []struct {
    fixture string
    budget  int
    wantLayerB bool
    wantSpilledAtLeast int
}{
    {"session-huge-bash.jsonl",     budgetFor("claude-opus-4-7"), true, 1},
    {"session-many-edits.jsonl",    budgetFor("claude-opus-4-7"), true, 0},
    {"session-mixed-tools.jsonl",   budgetFor("claude-opus-4-7"), false, 0},
    {"session-pathological.jsonl",  budgetFor("claude-opus-4-7"), true, 1},
}
```

Goldens regenerable under a `-update` flag (idiomatic Go pattern).

### Layer 5 — Opt-in live-provider smoke

```go
//go:build live_provider

func TestCompact_LiveClaude(t *testing.T) {
    if os.Getenv("ANTHROPIC_API_KEY") == "" { t.Skip("ANTHROPIC_API_KEY not set") }
    // Prompt designed to elicit a huge tool output:
    //   "run `find / -type f 2>/dev/null | head -100000`"
    // Assert: no OOM; OnCompact fired with LayerBEngaged=true;
    // read_tool_output successfully retrieves a chunk in a follow-up turn.
}
```

Default `go test ./...` skips it. Run manually or in a paid CI lane with `go test -tags=live_provider ./...`. Documented in [CLAUDE.md](../../../CLAUDE.md) so future contributors know the convention.

### Benchmarks

- `BenchmarkDigestFold` against `session-huge-bash.jsonl`. Confirms digest generation is sub-millisecond on a 200-turn session (pure string ops, minimal allocations).

### Coverage

`internal/compact/...` held to ≥85% line coverage.

## Migration

- Old CLI flags (`--trim-strategy`, `--max-context-turns`, `--max-context-tokens`) removed; using them prints a hard error pointing to the new flags. No backwards-compatibility shims.
- Existing on-disk sessions resume fine — the JSONL format is unchanged; only the read-time view producer is replaced.
- `defensiveCleanup` (cross-process TOCTOU rescue from spec D12/D13) is preserved; it moves to `internal/compact/cleanup.go`.

## Open questions

None remaining as of this draft. All design decisions resolved during brainstorming; implementation-level details (exact YAML schema for `model-limits.yaml`, exact head/tail byte ratio in `TruncateForLLM` updates, whether `compact-events.jsonl` rotates) are left to the implementation plan.
