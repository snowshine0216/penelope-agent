# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

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
