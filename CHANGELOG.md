# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

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

## [0.5.0.0] - 2026-05-14

### Added
- **Dynamic context loading**: the engine reads `AGENTS.md` from the working
  directory at startup and composes it into the system prompt as project-level
  instructions.
- **Local skill catalog**: skills placed in `.claw/skills/<name>/SKILL.md` are
  discovered at startup and advertised to the model in the system prompt via a
  `## Local Skills` section with name, description, and optional aliases.
- **`load_skill` tool**: a new serial-only tool the model can call to promote a
  local skill body into the system prompt of the next turn. The engine enforces a
  loader barrier so `load_skill` is executed alone before any parallel tool group.
- `internal/context` package (`agentcontext`): pure functions for frontmatter
  parsing, filesystem loading, immutable prompt composition, and a `Manager`
  that owns context state for a run.
- `Manager` is goroutine-safe: `LoadSkill` and `SystemPrompt` are protected by
  a `sync.Mutex`.

### Fixed
- `AGENTS.md` symlinks are now rejected via `os.Lstat` to prevent arbitrary file
  injection into the LLM context window.
- `SKILL.md` file symlinks inside skill directories are now detected and skipped
  before reading.
- Skill `name`, `description`, and `aliases` fields are validated to reject
  embedded newlines, preventing markdown injection into the system prompt.
- In-workdir symlinks under `.claw/skills/` are now recorded in `SkillCatalog.Skipped`
  rather than silently dropped.
- `server.go`: removed undefined `user` variable from placeholder stub.

## [0.4.0.0] - 2026-05-13

### Added
- `Reporter` interface (`OnThinking`, `OnToolCall`, `OnToolResult`, `OnMessage`)
  wired into `AgentEngine.Run` so callers receive structured event callbacks
  for every agent lifecycle event.
- `TerminalReporter` concrete implementation that prints formatted events to
  stdout; exposed via `engine.NewTerminalReporter()`.
- `OnToolCall` is now emitted for every tool in a group before execution, and
  `OnToolResult` is emitted per-tool after results are collected.
- CLI (`cmd/claw`) uses `TerminalReporter` to display tool and message events.
- Tests: `noOpReporter` shared helper and full output-capture tests for every
  `TerminalReporter` method.

## [0.3.0.0] - 2026-05-10

### Added
- Parallel-safe tool execution: consecutive safe tool calls in the same assistant
  turn run concurrently with bounded worker fan-out and deterministic observation
  ordering. Unknown and mutating tools remain serial.
- `ExecutionPolicy` type and `ExecutionPolicyProvider` interface in the tools
  registry, letting any tool opt into parallel scheduling with an optional per-tool
  concurrency cap (`MaxConcurrency`).
- `PlanToolCallGroups` pure planner: splits a flat list of tool calls into ordered
  execution groups — consecutive parallel-safe calls share a group; serial calls
  stand alone.
- `AgentEngine.MaxParallelToolCalls` field to cap engine-wide parallel concurrency
  (default 4).
- `read_file` is now the first built-in parallel-safe tool.

## [0.2.0.0] - 2026-05-08

### Added
- `edit_file` tool: multi-edit string replacement with L1→L4 fuzzy match
  chain (exact → CRLF normalization → TrimSpace on multi-line snippets →
  line-by-line TrimSpace + sliding window with base-indent realignment),
  atomic rollback across an edits array, and atomic file write via temp +
  rename. Refuses non-existent files (use `write_file`) and no-op edits.
  Mounted in `cmd/claw/main.go`.
- `AtomicWriteFile` helper: temp file → `fsync` → `os.Rename` with mode
  preservation; exported `AtomicWriteFileWith` for test injection.
- `FuzzyReplace` exported sentinel `ErrNotFound` for typed error matching.
- `CLAUDE.md` gstack skill routing rules.

### Fixed
- `FuzzyReplace` L3 now requires `normOld` to contain a newline before
  applying TrimSpace, preventing silent mid-token substring corruption on
  single-line snippets with surrounding whitespace.
- `edit_file` returns a clear error when `old_text` is empty, preventing
  confusing "matched N places" errors from Go's `strings.Count` behavior
  on empty-string needles.
- `formatEditError` uses `errors.Is(ErrNotFound)` sentinel instead of
  `strings.Contains` string coupling.
- Pre-compute `trimmedOldLines` in `lineByLineReplace` to eliminate O(N×K)
  redundant `strings.TrimSpace` allocations in inner loop.
- `AtomicWriteFileWith` parameter injection replaces the global `AtomicRenameFunc`
  variable (addressed code review feedback).

## [Unreleased]

### Added
- Parallel-safe tool execution in the engine: consecutive safe tool
  calls can run concurrently with deterministic observation ordering,
  an engine-wide concurrency cap, and conservative serial fallback for
  mutating or unknown tools. `read_file` is the first parallel-safe
  built-in tool.

## [0.1.0.0] - 2026-05-05

### Added
- CLI flag interface: `--prompt`, `--provider`, `--model`, `--max-turns`, `--max-tokens`, `--workdir`, `--think` with stdin fallback when not a terminal
- Turn limit: `AgentEngine.MaxTurns` (default 25) with `ErrMaxTurnsExceeded` sentinel so the agent cannot run forever
- `--think` flag enables a two-phase think-then-act loop per turn (thinking phase strips tools to force planning trace)
- File path sandboxing: `ResolveInWorkDir` + `ErrPathEscape` in new `internal/tools/safepath.go` rejects absolute paths, traversal sequences, and symlink escapes
- UTF-8-safe output truncation: `TruncateForLLM` in new `internal/tools/truncate.go` with head+tail elision, rune-boundary guards, and 8 000-byte cap applied to bash and read\_file outputs
- `read_file` pagination via `offset`/`limit` parameters; hard 10 MB file size cap to prevent OOM
- Bash tool: configurable `timeout_s` parameter (default 30 s, max 600 s) with integer-overflow-safe clamping; every command logged before execution
- `RoleTool = "tool"` message role; both providers handle it as a distinct case rather than dropping tool results
- `IsError` field on `schema.Message`; surfaced to Anthropic via `NewToolResultBlock` error flag; OpenAI falls back to text content
- Provider constructors return `(*T, error)` instead of panicking on missing config
- `ClaudeProvider.MaxTokens int64` field; defaults to 4 096 when zero
- `ExtractRequiredStrings` helper handles both `[]string` and `[]interface{}` required-fields schemas
- Deterministic tool listing: `GetAvailableTools` sorts results by name
- Unknown OpenAI tool call type returns an error instead of being silently dropped
- README with quickstart, environment variable table, flag reference, tool listing, project layout, and known limitations
- Comprehensive test suite across `tests/{engine,provider,schema,tools}` with 400+ test cases covering translation, engine loop, tool execution, safepath, truncation, and configuration

### Changed
- Default base URL updated to `https://open.bigmodel.cn/api/paas/v4/` to match the default `glm-4.5-air` model
- `BaseTool` interface renamed to `Tool` for clarity
- All internal log and error messages translated from Chinese to English
- `validateBaseURL` enforces HTTPS for non-localhost hosts; `.env` traversal depth capped at 4 levels
- Empty assistant message gets a sentinel `NewTextBlock("")` to keep conversation history contiguous

### Removed
- `hello.txt` and `helloworld.go` (scaffolding placeholders)
- Hardcoded prompt from `main.go`
