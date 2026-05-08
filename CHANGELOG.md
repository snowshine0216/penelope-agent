# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

### Added
- `edit_file` tool: multi-edit string replacement with L1→L4 fuzzy match
  chain (exact → CRLF normalization → TrimSpace → line-by-line
  TrimSpace + sliding window with base-indent realignment), atomic
  rollback across an edits array, and atomic file write via temp +
  rename. Refuses non-existent files (use `write_file`) and no-op
  edits. Mounted in `cmd/claw/main.go`.

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
