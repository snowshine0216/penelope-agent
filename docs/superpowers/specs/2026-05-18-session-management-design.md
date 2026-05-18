# Per-Session Context History And Pluggable Trimming — Design

**Date:** 2026-05-18
**Status:** Draft (awaiting written spec review)

## Context

Today every `claw` invocation in `cmd/claw/main.go` builds `contextHistory`
from scratch in `internal/engine/loop.go:63` and discards it when `Run`
returns. There is no way to resume a conversation across CLI invocations, and
there is no bound on how large `contextHistory` can grow within a single run.
The existing `TODOS.md` already lists "Cap context history to prevent
context-window exhaustion" as a P2 item.

This design introduces a `Session` concept that persists conversation history
to disk between invocations, plus a sliding-window trimmer that bounds what
the model sees both on resume and on every turn within a run. The trimmer is
behind an interface so future strategies (compaction, hybrid summarization)
can replace the default without changes to the engine.

## Scope

**In:**

- A new `internal/session` package that owns persisted history, ID generation,
  JSONL append, load, and per-append file locking.
- Auto-generated session IDs printed to stderr on every run.
- Resume via a new `--session <id>` flag.
- A `Trimmer` interface and a default `WindowTrimmer` keyed on user turns
  with a chars/4 token cap as a hard ceiling.
- Defensive cleanup in the default trimmer so that interleaved concurrent
  appends never produce a provider-invalid view.
- Per-append `flock(LOCK_EX)` to keep the JSONL file parseable under
  concurrent writers.
- Trimming applied both at session load and before every `provider.Generate`
  call within the run loop.
- Configurable `--max-context-turns` (default 6), `--max-context-tokens`
  (default 32000), `--trim-strategy` (default `window`), `--sessions-dir`
  (default `${workdir}/.claw/sessions`).
- Unit, filesystem-edge, and engine-integration tests in the external
  `tests/` tree mirroring existing layout.

**Out:**

- A `claw sessions list / show / rm` subcommand surface. Files are inspectable
  with `cat` and `rm` in v1; tooling is a follow-up.
- A REPL or long-running interactive mode.
- A coordinator for true multi-agent shared sessions. Concurrent writers are
  *permitted* but conversation coherence under contention is best-effort.
- LLM-based summarization or compaction strategies. The interface is
  designed to admit them later but v1 ships only `window`.
- Persisting the loaded-skill state across resumes. The model can re-issue
  `load_skill` after resume if a previously loaded body has been trimmed out.
- Persisting the system message. It is recomposed fresh from workdir state
  on every run.
- Windows file locking. `flock` is POSIX-only; on Windows the lock call is
  skipped and the README documents the gap.
- Read-only inspection commands that take `LOCK_SH`. Reserved as future work.

## Decisions

| ID | Decision | Choice | Rationale |
|----|----------|--------|-----------|
| D1 | Session scope | Persisted across CLI runs | Lets the user continue a conversation between invocations. In-memory only would not solve the "resume" use case. |
| D2 | Session ID source | Auto-generated `YYYYMMDD-HHMMSS-XXXXXX` | Sortable, collision-free in practice, ergonomic to tab-complete. Removes naming choices from the user's path. |
| D3 | Default behavior without `--session` | Always create + print ID to stderr | Uniform behavior; every run is auditable; no ephemeral path to maintain. |
| D4 | Storage format | JSONL append log, one file per session | Streaming append, no rewrite cost, human-inspectable, parses incrementally. |
| D5 | Persisted system message | Never | The system prompt is recomposed from workdir state on every run; storing it would freeze stale instructions. |
| D6 | Window unit | User turns | A "user turn" preserves the tool_call → tool_result pairing inside one slice; counting messages would split pairs and break provider APIs. |
| D7 | Default `--max-context-turns` | 6 | Matches the request; covers a couple of back-and-forth exchanges without bloat. |
| D8 | Token estimator | `len(content)/4` + small per-message constant | Zero deps, deterministic, accurate enough for a heuristic. |
| D9 | Default `--max-context-tokens` | 32000 | ~128k chars effective view; leaves room for system prompt and tool definitions under a 128k provider window. |
| D10 | When to trim | On load AND before every `Generate` call | Closes the P2 TODO on within-run context-window exhaustion, not just on cross-run resume. |
| D11 | Trim strategy shape | `Trimmer` interface registered by name | Future `compact` and `hybrid` strategies can be added in one file with no engine changes. |
| D12 | Lock model | Per-append `LOCK_EX` on the JSONL | Permits multiple processes to share a session at file-integrity level. Conversation coherence under contention is the trimmer's defensive responsibility. |
| D13 | Defensive cleanup | Drop orphan tool results and dangling tool_calls inside the trimmer | Compensates for D12 by ensuring the in-memory view sent to the provider is always valid. |
| D14 | `--session <id>` of a missing file | Hard error | Keeps "create new" and "resume existing" semantically distinct so typos do not silently fork a conversation. |
| D15 | Sessions directory | `${workdir}/.claw/sessions`, overridable via `--sessions-dir` | Keeps session data inside the same hidden directory tree that hosts local skills. |
| D16 | Windows locking | Skipped under `//go:build !windows` | Project is POSIX-focused (bash tool, `.claw/skills/`). Avoiding a third-party lock library keeps deps minimal. |
| D17 | Think-phase persistence | Think-phase responses are **not** appended to the session | Planning text would pollute the persisted log and bloat resumes with reasoning that was only ever useful for the immediately-following act phase. Think output stays in per-turn `contextHistory` and dies with the turn. |
| D18 | Engine signature | `Run(ctx, reporter)` — drop the `userPrompt` parameter | The user prompt is appended to the session by `main.go` before `Run` is called, so the engine reads it from `session.Messages()` instead. Single source of truth; no two-path seeding. |
| D19 | Fresh-session contents | The new user prompt is the first persisted message | Files contain only message records. There is no separate "session created" marker; the existence of the JSONL file (even empty) is the marker. A freshly created session always has its first user prompt appended before the engine runs. |

## Architecture

Add `internal/session` as the owner of persistence and trimming. File I/O is
isolated to `store.go` and `lock_posix.go`; trimming and ID generation are
pure.

```
internal/
  session/
    session.go        Session type: ID, in-memory messages, Append, Messages
    store.go          NewSession, OpenSession, JSONL append, load, dir create
    lock_posix.go     //go:build !windows — flock helpers
    lock_windows.go   //go:build windows  — no-op stubs
    id.go             Auto-generated YYYYMMDD-HHMMSS-XXXXXX IDs
    trim.go           Trimmer interface, registry, TrimConfig
    window.go         WindowTrimmer — default strategy
    tokens.go         EstimateTokens helper
tests/session/
  store_test.go
  trim_window_test.go
  tokens_test.go
tests/engine/
  session_integration_test.go
```

Core units:

- `Session`: holds the session ID, an in-memory `[]schema.Message` (excluding
  the system message), and the open file handle. `Append(msg)` updates the
  slice and writes one JSONL line under per-append `LOCK_EX`.
- `Trimmer`: pure function `Trim([]Message) []Message` plus a `Name`. The
  registry maps strategy names to constructors.
- `WindowTrimmer`: default. Three sequential pure passes — window by user
  turns, apply token cap, defensive cleanup.
- `Composer` (existing): unchanged. The session never touches `RoleSystem`.

The engine gains one field — `session *session.Session` — and three small
edits to `Run` in `internal/engine/loop.go`. See Data Flow.

## Data Flow

**Session lifecycle (one CLI invocation):**

1. `cmd/claw/main.go` parses flags.
2. If `--session <id>` is set, `session.OpenSession(id, dir)` reads the JSONL
   into memory, takes `LOCK_EX`, returns the populated `Session`. If the file
   is missing, exits with a hard error.
3. Otherwise, `session.NewSession(dir)` generates an ID, creates the file,
   takes `LOCK_EX`, returns an empty `Session`. The ID is printed to stderr:
   `session: 20260518-093045-a1b2c3`. On resume the message is
   `session: <id> (resumed, N messages)`.
4. `session.Append(userPromptMessage)` writes the new user prompt.
5. `engine.Run(ctx, reporter)` is called. Per D18 the engine reads the
   user prompt (and any prior history) from `session.Messages()` rather
   than from a parameter — uniform between fresh and resumed runs.

**Run loop edits in `loop.go`:**

1. `contextHistory` is seeded as `[systemMsg] + session.Messages()`.
2. Before every `provider.Generate` call (both think and act phases):
   - `trimmedTail := trimmer.Trim(session.Messages())`
   - `view := append([]Message{systemMsg}, trimmedTail...)`
   - Pass `view` to `Generate`.
3. After each **act-phase** assistant message and each tool result message
   is produced:
   - `session.Append(msg)` — persists and updates in-memory.
   Per D17 the think-phase response is never `session.Append`'d; it stays
   in the per-turn `contextHistory` only so the act phase can read it,
   and naturally drops out of the next turn's seed (which re-reads from
   the session).
4. `refreshSystemPrompt` (existing) continues to rewrite `contextHistory[0]`
   after `load_skill`. It does not touch the session.

The session's in-memory slice is the canonical pre-trim history within the
run. The trimmed view is recomputed every turn from the same input, which
makes windowing reproducible.

**Per-append write flow:**

1. `Append` marshals one `schema.Message` to JSON.
2. Takes `flock(fd, LOCK_EX)` on the open JSONL fd.
3. Writes `<json>\n` with `O_APPEND`.
4. Releases the lock.

Concurrent writers therefore serialize at the kernel level. Each line on disk
remains a valid JSON object. Logical conversation interleaving between two
processes is permitted by the design (D12) and handled by the trimmer (D13).

## Trim Strategy

**Interface:**

```go
type Trimmer interface {
    Trim(messages []schema.Message) []schema.Message
    Name() string
}

type TrimConfig struct {
    MaxUserTurns int
    MaxTokens    int
}
```

A package-level registry maps strategy names to constructors. v1 registers
`"window"`. Adding `"compact"` or `"hybrid"` later means writing a new file
and adding one registry entry — no engine changes.

**WindowTrimmer.Trim — three sequential pure passes:**

1. **Window by user turns.** Find user-message indices in input. Keep
   messages from the index of the (N+1)-from-end user message onward. Result:
   the last `MaxUserTurns` user turns plus all assistant/tool messages
   attached to them.

2. **Token cap.** Compute total estimated tokens via `tokens.EstimateTokens`.
   While the total exceeds `MaxTokens`, drop the oldest user turn (the
   leading user message plus all non-user messages until the next user
   message). Stop when under the cap or when only one user turn remains.

3. **Defensive cleanup.**
   - Walk forward. For each `RoleTool` message, verify the most recent
     preceding `RoleAssistant` message contains a matching `ToolCallID` in
     its `ToolCalls`. If not, drop the tool message.
   - Walk forward. For each `RoleAssistant` message with `ToolCalls`, verify
     every `ToolCallID` has a matching `RoleTool` message immediately
     following (only tool messages permitted between). If any ID is missing,
     drop the entire assistant message.
   - If the slice begins with a `RoleTool` message after the above passes,
     drop leading tool messages until a non-tool message is at index 0.

**Emergency floor.** If passes 1–3 produce an empty slice (token cap so low
that even the latest user turn does not fit), the engine substitutes
`[latestUserMessage]` and logs a warning. The model receives a valid prompt
even when configuration is hostile.

## ID Format

`YYYYMMDD-HHMMSS-XXXXXX` where `XXXXXX` is six lowercase hex characters from
`crypto/rand`.

- Sortable lexicographically by creation time.
- 20 characters total.
- Easy to type and tab-complete.
- Collision probability negligible at human scales.

Regex for validation when parsing `--session` flag input:
`^[0-9]{8}-[0-9]{6}-[0-9a-f]{6}$`. Reject anything else at flag parse, which
also blocks path traversal because `/` and `..` cannot appear in valid IDs.

## CLI Surface

| Flag | Default | Notes |
|------|---------|-------|
| `--session` | `""` | Empty → create new and print ID. Set → resume; error if not found. |
| `--max-context-turns` | `6` | `TrimConfig.MaxUserTurns`. |
| `--max-context-tokens` | `32000` | `TrimConfig.MaxTokens`. |
| `--trim-strategy` | `"window"` | Looked up in the trim registry. Unknown → fatal with list of known strategies. |
| `--sessions-dir` | `${workdir}/.claw/sessions` | Override location. |

`stderr` output on session open:

- New session: `session: <id>`
- Resumed session: `session: <id> (resumed, N messages)`

`stdout` is untouched by session machinery so existing piping behavior is
preserved.

## Error Handling

- Malformed JSONL line: `Load` returns `session <id>: corrupt at line N:
  <parse error>`. Hard fail; manual repair by the user.
- Empty JSONL file: valid; treated as a fresh session.
- `--session <id>` with no matching file: hard error
  `session <id> not found in <sessions-dir>`.
- `--session <id>` with invalid characters: hard error at flag parse,
  `invalid session id (must match YYYYMMDD-HHMMSS-XXXXXX)`.
- First persisted message is not `RoleUser`: hard error `session <id>
  corrupt: first message must be user`. Should be unreachable from normal
  operation.
- Trimmer returns empty slice: engine substitutes `[latestUserMessage]` and
  logs a warning.
- `load_skill` mid-resume: `refreshSystemPrompt` updates `contextHistory[0]`
  unchanged. Session is not touched.
- `Run` errors mid-turn: already-persisted messages remain on disk. Resume
  picks up from the last-persisted state; defensive cleanup handles partial
  tool sequences.
- Sessions directory missing: `NewSession` creates it with `0700`.
- `flock(LOCK_EX)` failure for reasons other than `EWOULDBLOCK` (which v1
  cannot encounter with per-append acquire): wrap and return.
- `--trim-strategy <unknown>`: hard error listing registered names.

All errors that abort startup write to stderr and exit non-zero.

## Testing

Follow TDD. Tests are fast, deterministic, and live in the external `tests/`
tree mirroring existing layout.

**Pure unit tests (`tests/session/`):**

- `WindowTrimmer.Trim`:
  - Clean session under all limits — unchanged.
  - Exactly at the user-turn limit — unchanged.
  - Over the user-turn limit — oldest turns dropped.
  - Token cap shrinks below turn limit — further turns dropped.
  - Tool-call and tool-result pair preserved within a kept turn.
  - Orphan tool result (no preceding assistant) dropped.
  - Dangling tool_call (assistant without matching tool results) drops the
    assistant message.
  - Empty input — empty output.
  - Single user message — unchanged.
  - Token cap so low that the latest user turn would be dropped — slice still
    contains the latest user message (caller applies emergency floor).
  - Two consecutive user messages from concurrent writers — both retained.
- `EstimateTokens`: empty, ascii, mixed content, message overhead.
- `id.NewID`: format matches regex; two consecutive IDs differ in the random
  suffix.

**Filesystem-edge tests (`tests/session/`):**

- `NewSession` creates the file and the sessions directory if missing.
- `OpenSession` round-trips messages identical to what was appended.
- `Append` writes one valid JSON line per call, separated by `\n`.
- Corrupt line returns `corrupt at line N` error.
- Missing-file open returns `not found` error.
- Concurrent append from two goroutines writing 100 messages each: all 200
  lines parse, every appended message body is present, no truncation.
- Sessions dir is created with mode `0700`.

**Engine integration tests (`tests/engine/`):**

- Run with a fake provider, no `--session`: session file created, IDs
  printed, history persisted, second `OpenSession` recovers it.
- Run with `--session <id>` resume: prior history visible to the provider via
  the fake provider's recorded inputs.
- Trim invoked before every `Generate` (verified by inspecting the fake
  provider's recorded input length across turns).
- `load_skill` mid-session: the next provider call's system message reflects
  the loaded body; session tail unchanged.
- Emergency floor: with `MaxTokens` set absurdly low, the model receives at
  minimum the latest user message.
- Run that errors mid-turn: a follow-up resume picks up from the last
  persisted state and the trimmer cleans any orphan tool messages.

Existing engine tests remain green; the session integration is additive.

## Risks

- **Concurrent writers can scramble conversation coherence.** The trimmer's
  defensive cleanup masks the worst symptoms (invalid provider input), but a
  pair of agents racing on the same session can still produce nonsensical
  dialog. Documented in README as a known limitation; the safer "one writer
  per session" pattern is recommended.
- **Chars/4 estimator drifts on heavy CJK or unusual tokenization.** The cap
  is a budget, not a guarantee. Users running near a provider's context
  ceiling should set `--max-context-tokens` conservatively.
- **JSONL files grow unboundedly.** A long-lived session accumulates every
  message ever appended even though the model only sees the windowed view.
  Acceptable for v1 — disk is cheap and inspection of the full log is useful.
  A future `claw sessions compact <id>` could rewrite the file to the
  windowed view if needed.
- **Persisting `load_skill` state is deferred.** After resume, previously
  loaded skill bodies are absent until the model re-issues `load_skill`.
  Acceptable because `load_skill` is idempotent and cheap.
- **Per-append lock acquire-release per write** adds two syscalls per
  message. Negligible relative to network I/O of `provider.Generate` but
  measurable in tight unit-test loops; tests use small message counts.
- **Defensive cleanup can be surprising.** Dropping an assistant message with
  dangling tool_calls is the right call for provider validity but may confuse
  someone reading the on-disk JSONL versus the trimmed view. Log at debug
  level when cleanup drops a message so it is observable.
