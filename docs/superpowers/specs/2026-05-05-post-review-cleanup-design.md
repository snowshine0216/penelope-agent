# Post-Review Cleanup — Design

**Date:** 2026-05-05
**Branch:** `claude/priceless-payne-ed43d1`
**Status:** Approved (decisions D1–D5 confirmed as A)

## Context

A code review of `penelope-agent` (forked from `go-tiny-claw`) surfaced 23
items: 12 bugs and 11 improvements. The test reorganization (item separate)
already shipped — tests now live under `tests/<package>/` using external
test packages. This spec covers the remaining 22 items.

## Scope

**In:**
- 12 correctness bugs in providers (Claude + OpenAI), engine loop, tools,
  and registry.
- 10 improvements covering CLI ergonomics, sandboxing, truncation, default
  config alignment, README, and the `BaseTool` rename.

**Out:**
- New providers (Gemini, local models).
- TUI / interactive REPL.
- Switching SDK versions.
- A configuration system beyond `.env` + flags.
- Test harness changes (already done).

## Decisions

| ID | Decision | Choice | Rationale |
|----|----------|--------|-----------|
| D1 | Bash sandboxing | A — log every command before exec; no allowlist | Allowlists make the agent unusable; write_file already lets the model write anywhere on disk, so a bash allowlist closes one window in a glass house. Auditability is the realistic win. |
| D2 | Default base URL | A — Zhipu `https://open.bigmodel.cn/api/paas/v4/` | Constructor names already say `Zhipu*Provider` and the default model is `glm-4.5-air` (a Zhipu model). Aligning the URL to match is the smallest fix that makes the out-of-box config work. |
| D3 | CLI scope | A — flags + stdin for prompt | Adding REPL or config files now is scope creep. Flags make `claw` testable from a shell script today. |
| D4 | Log language | A — English everywhere | The repo is public-shaped (open-source-style layout, English deps). Internal logs should match. |
| D5 | Commit strategy | A — one commit per work unit (5 commits) | Bisectable, reviewable, easy to revert one piece without the others. |

## Design

### Unit 1 — Provider correctness

**Files:** `internal/schema/message.go`, `internal/provider/claude.go`,
`internal/provider/openai.go`, `cmd/claw/main.go`.

**Changes:**

1. **Add `IsError bool` to `schema.Message`.** The engine populates it from
   `ToolResult.IsError`. `claude.go` reads it and passes to
   `anthropic.NewToolResultBlock(id, content, isError)`. OpenAI side has no
   equivalent wire field, so it's a no-op there but the schema becomes
   honest.

2. **Propagate JSON errors.** Replace every `_ = json.Unmarshal(...)` and
   `_, _ := json.Marshal(...)` with explicit error handling that returns up
   through `Generate(...) (*schema.Message, error)`. Six sites total: two in
   `claude.go`, two in `openai.go` (input schema fallback + tool args).

3. **Fix `required` field assertion.** New helper in `claude.go`:
   ```go
   func extractStringSlice(v interface{}) []string {
       switch s := v.(type) {
       case []string:
           return s
       case []interface{}:
           out := make([]string, 0, len(s))
           for _, item := range s {
               if str, ok := item.(string); ok {
                   out = append(out, str)
               }
           }
           return out
       }
       return nil
   }
   ```
   Used for `m["required"]`. Handles both literal-built and JSON-decoded
   schemas.

4. **Stop dropping empty assistant messages.** Remove the `if len(blocks) > 0`
   guard at `claude.go:73`. If both content and tool calls are empty (rare,
   but happens on hangups), append an empty text block so history stays
   contiguous.

5. **Error on unknown tool-call types in OpenAI path.** Replace
   `if tc.Type == "function"` with a switch + `default` returning an error
   wrapping `tc.Type`. Loud failure is better than silent skip.

6. **Constructors return errors instead of panicking.** New signatures
   (function names unchanged in this unit; rename to drop the `Zhipu`
   prefix lands in Unit 5 alongside the CLI rewrite, so main.go is touched
   once):
   ```go
   func NewZhipuClaudeProvider(model string) (*ClaudeProvider, error)
   func NewZhipuOpenAIProvider(model string) (*OpenAIProvider, error)
   ```
   `cmd/claw/main.go` is updated in this unit to handle the returned
   error (`log.Fatalf` if non-nil).

7. **Configurable `MaxTokens`.** Add `MaxTokens int` field on
   `ClaudeProvider` (default 4096); a setter `WithMaxTokens(n)` returns the
   provider for fluent setup. Plumb through main.go's `--max-tokens` flag
   (defaults to 4096; 0 means use SDK default).

**Verification:** existing tests in `tests/provider/` continue to pass.
Test additions are deferred to the implementation plan; new public surface
gets at least smoke coverage.

### Unit 2 — Engine robustness

**Files:** `internal/engine/loop.go`, `internal/schema/message.go`,
`internal/tools/registry.go`.

**Changes:**

1. **Add `RoleTool`** to schema as `Role = "tool"`. The engine emits tool
   results as `RoleTool` instead of overloading `RoleUser`. Both providers
   keep working: claude.go branches on role=tool, openai.go does the same
   and emits `openai.ToolMessage(...)`.

2. **Turn limit.** `AgentEngine.MaxTurns int` field, default 25 in
   `NewAgentEngine`. When exceeded, return `ErrMaxTurnsExceeded` (a sentinel
   error). main.go exposes `--max-turns`.

3. **Honor context cancellation.** At the top of each turn body and after
   each tool execution: `if err := ctx.Err(); err != nil { return err }`.

4. **Deterministic tool order.** In `registry.go:GetAvailableTools`, sort
   the result slice by `Name` before returning. Improves prompt-cache hit
   rates and reproducibility.

**Verification:** `tests/engine/loop_test.go` already covers single-turn,
multi-turn, thinking-mode, and parallel-tool paths via `fakeProvider`. Add
turn-limit and ctx-cancel tests.

### Unit 3 — Truncation + tool sandboxing

**Files:** `internal/tools/truncate.go` (new), `internal/tools/safepath.go`
(new), `internal/tools/bash.go`, `internal/tools/read_file.go`,
`internal/tools/write_file.go`.

**Changes:**

1. **`truncate.go`** — single helper:
   ```go
   func TruncateForLLM(s string, maxBytes int) string
   ```
   Behavior: if `len(s) <= maxBytes`, return `s`. Otherwise split the budget
   in half between head and tail, back each cut to the nearest UTF-8 rune
   boundary using `utf8.DecodeLastRune` / `utf8.DecodeRuneInString`, and
   join with a marker:
   ```
   <head>
   ...[<elided> bytes elided of <total> total]...
   <tail>
   ```
   Tail-only mode for stack traces is a future optimization; head+tail is a
   safe default.

2. **`safepath.go`** — single helper:
   ```go
   func ResolveInWorkDir(workDir, relPath string) (string, error)
   ```
   - Reject absolute paths.
   - `filepath.Join(workDir, relPath)`, then `filepath.Abs`.
   - Also `filepath.Abs(workDir)`.
   - Assert `strings.HasPrefix(absPath, absWorkDir + string(os.PathSeparator))`
     (or equality).
   - On reject: return `ErrPathEscape` (sentinel).

3. **`read_file` updates:**
   - Use `ResolveInWorkDir`.
   - Use `TruncateForLLM`.
   - Add optional `offset` (line number, 1-indexed) and `limit` (line count)
     args. Default: read whole file then truncate. With offset/limit: read
     only those lines, no truncation needed.

4. **`write_file` updates:**
   - Use `ResolveInWorkDir`.

5. **`bash` updates:**
   - Use `TruncateForLLM`.
   - Optional `timeout_s` int arg (default 30, max 600).
   - Log every command before exec at INFO: `[bash] cmd=<command> dir=<workDir>`.
   - Document in README that bash is unsandboxed by design.

6. **Flip the two known-bug tests** in `tests/tools/read_file_test.go` and
   `tests/tools/write_file_test.go` to assert the path traversal is now
   rejected with `ErrPathEscape`.

**Verification:** existing tests pass. New tests for `TruncateForLLM`
(short, exact, long, mid-rune-boundary, multibyte) and `ResolveInWorkDir`
(simple, traversal, abs, symlink — symlink test skippable on Windows).

### Unit 4 — Config defaults

**Files:** `internal/provider/config.go`.

**Changes:**

1. Change `defaultProviderBaseURL` from
   `"https://api.minimaxi.com/v1/"` to
   `"https://open.bigmodel.cn/api/paas/v4/"`. This matches the default
   model `glm-4.5-air` and the `Zhipu*` constructor names.

2. Update `tests/provider/config_test.go` `DefaultBaseURL()` assertions —
   the value changes but the test logic stays the same.

3. Note the change in README and CHANGELOG.

**Verification:** `tests/provider/...` passes.

### Unit 5 — CLI + UX

**Files:** `cmd/claw/main.go`, `README.md` (new), `internal/tools/registry.go`,
`internal/tools/{bash,read_file,write_file}.go`. Also: delete `hello.txt`
and root `helloworld.go`.

**Changes:**

1. **Provider rename.** `NewZhipuClaudeProvider` → `NewClaudeProvider`,
   `NewZhipuOpenAIProvider` → `NewOpenAIProvider`. Provider names should
   describe the wire protocol, not the upstream service. Only call sites
   are in `cmd/claw/main.go` (covered by the CLI rewrite below).

2. **CLI flags via `flag` stdlib:**
   - `--prompt string` (or read from stdin if empty and stdin is non-tty).
   - `--think bool` (default false).
   - `--provider string` — `claude` or `openai` (default `openai`).
   - `--model string` (default empty → falls back to env).
   - `--max-turns int` (default 25).
   - `--max-tokens int` (default 4096).
   - `--workdir string` (default `os.Getwd()`).
   - Provider construction wrapped: `(provider, err)` returned, propagate
     errors with friendly messages.

3. **Rename `BaseTool` → `Tool`** in `registry.go`. Update three call sites
   in `bash.go`, `read_file.go`, `write_file.go`. Also update
   `tests/tools/registry_test.go` if it references the type.

4. **English logs.** Walk `internal/engine/loop.go`, `internal/tools/*.go`,
   `internal/provider/*.go`, `internal/provider/config.go` and translate
   Chinese log lines and error messages. Comments stay (mixed) — code
   comments are documentation, not user-facing.

5. **README.md.** Sections:
   - What it is (one-paragraph elevator pitch).
   - Quickstart: clone, `.env` template, `go run ./cmd/claw --prompt "..."`.
   - Providers: env var matrix, default URL/model alignment.
   - Thinking mode: what it does, when to enable.
   - Tools: list with one-liner each, security caveats (bash unsandboxed).
   - Layout: `internal/` vs `tests/` rationale.
   - Known limitations (anything explicitly deferred from this round).

6. **Cleanup.** `git rm hello.txt helloworld.go`. They're demo artifacts
   that say "go-tiny-claw" — wrong project name.

7. **Fix main.go comment numbering and remove the duplicated commented-out
   prompt line** (subsumed by the CLI rewrite, which replaces main.go
   wholesale).

**Verification:** `go build ./...`, `go test ./...`. Manually run
`go run ./cmd/claw --help` and confirm flags render.

## Order of work

1. **Unit 1** (provider correctness) — biggest correctness wins. No API
   consumer changes outside of constructor renames, which Unit 5 picks up.
2. **Unit 2** (engine robustness) — depends on Unit 1's `IsError` field
   and `RoleTool` addition.
3. **Unit 3** (truncation + sandboxing) — independent of 1 and 2.
4. **Unit 4** (config defaults) — trivial, can land anytime; sequenced
   here so Unit 5's README references the new default.
5. **Unit 5** (CLI + README + cleanup) — last, references everything above.

After each unit: `go test ./...` clean, then commit.

## Acceptance criteria

- All 22 in-scope review items resolved; any deferred items documented in
  README "Known limitations".
- `go build ./...` and `go test ./...` pass on the worktree branch.
- Path-traversal tests in `tests/tools/` flipped to assert rejection.
- New tests added for: `TruncateForLLM`, `ResolveInWorkDir`, turn-limit,
  ctx-cancel, deterministic tool ordering, error propagation in providers.
- README explains how to run with both `claude` and `openai` paths.
- Five commits on the branch, one per unit, with descriptive messages.

## Risks & mitigations

- **Default base URL change is user-visible.** Anyone relying on the
  MiniMax default will break. Mitigation: README changelog entry; the old
  default was already broken (URL/model mismatch), so the blast radius is
  near-zero.
- **`BaseTool` → `Tool` rename is a breaking API change.** All call sites
  are inside this repo. Mitigation: change them in the same commit; no
  external consumers exist.
- **Constructor rename (`NewZhipuClaudeProvider` → `NewClaudeProvider`).**
  Same as above — only main.go uses it. No external consumers.
- **Bash logging is a behavior change.** Audit log lines may surprise
  someone scripting against the binary. Mitigation: document in README;
  acceptable cost for auditability.
