# `edit_file` Tool — Design

**Date:** 2026-05-07
**Branch:** `claude/beautiful-boyd-e71a90`
**Status:** Draft (awaiting review)

## Context

`penelope-agent` currently mounts three tools: `read_file`, `write_file`,
`bash`. To modify code, the agent has only two options today, and both
are wrong for local edits:

- `write_file` rewrites the entire file. For a 2 000-line file with a
  one-line bug, this burns tokens and exposes the model to long-form
  generation truncation and drift.
- `bash sed/awk` requires the model to author multi-line regular
  expressions with shell-escape semantics. Empirically the failure rate
  on this is high enough that it can corrupt the file outright.

The right primitive is `edit_file` — a focused string-replace tool that
the model can call cheaply without rewriting the file.

The non-trivial design problem is **format-fidelity hallucination**.
LLMs preserve the *semantics* of `old_text` reliably but lose surface
formatting — leading whitespace, tab/space mix, trailing blank lines,
CRLF vs LF — frequently enough that an exact-match-only tool ends up in
a death loop where the model resubmits the same wrong indentation
across turns. The harness must absorb this format drift; stricter error
messages alone do not break the loop because the model's internal
representation does not change between retries.

This design follows the multi-level fuzzy match pattern documented in
the `go-tiny-claw` training material (Chapter 07: 容错艺术), augmented
with multi-edit batching, atomic file write, and base-indent
realignment on fuzzy hits.

## Scope

**In:**
- New tool `edit_file` mounted in `cmd/claw/main.go`.
- L1→L4 fuzzy match chain with uniqueness check at every level.
- Base-indentation prefix realignment on L4 hits (resolves the chapter's
  acknowledged open problem).
- Multi-edit array with all-or-nothing rollback.
- Atomic file write via temp file + `fsync` + rename.
- Refusal of non-existent files and no-op edits.
- Concrete, self-correction-friendly error messages.
- Tests across `tests/tools/`.
- README + CHANGELOG updates.

**Out:**
- Read-before-edit enforcement (would require engine session state;
  orthogonal hardening, deferred).
- AST-level / syntax-aware editing.
- Diff or unified-patch input format.
- Streaming or partial-progress reporting (atomicity precludes this).
- Cross-file edits in a single tool call.

## Decisions

| ID | Decision | Choice | Rationale |
|----|----------|--------|-----------|
| D1 | Edit semantics | Exact string replace with L1→L4 fuzzy fallback | LLMs produce subtly broken diffs and patches; line numbers drift between calls. Search-replace blocks dominate empirically (Aider, Claude Code, Cursor, Cline all converged here). Fuzzy fallback absorbs format drift. |
| D2 | Tool surface | Single tool `edit_file` with always-array `edits` field | One schema, provider-portable, uniform rollback semantics. Avoids `oneOf` (poor provider support). Tiny ergonomic tax (wrap single edits in array) is invisible in practice. |
| D3 | Argument naming | `old_text` / `new_text` | Matches `go-tiny-claw` lineage and the chapter the design derives from. Internal consistency over Claude Code mimicry. |
| D4 | Multi-edit atomicity | All-or-nothing rollback via in-memory accumulation, single atomic write at end | Avoids half-edited file state on partial failure. Simpler than per-edit on-disk writes with undo. |
| D5 | Disk write | Temp file in same dir + `fsync` + `os.Rename` | Crash-safe; preserves original on process kill mid-write. Same-directory temp ensures rename is atomic on POSIX. |
| D6 | Uniqueness override | Explicit `replace_all` boolean per edit | Default is strict (refuse on multi-match). Override is opt-in and per-edit so the model commits consciously. |
| D7 | Success output | `edited PATH (N edits applied: L1=a L2=b L3=c L4=d)` | Per-level counts give the model passive feedback when its `old_text` was loose. Useful within a session, harmless when ignored. |
| D8 | File organization | Split: `edit_file.go` (Tool impl + I/O), `fuzzy.go` (pure match algorithms), `atomic_write.go` (pure-ish write helper) | Keeps each file under the 200-line guideline. Fuzzy algorithms become independently testable pure functions. |

## Design

### Unit 1 — Tool surface and schema

**File:** `internal/tools/edit_file.go`

```go
type EditFileTool struct {
    workDir string
}

func NewEditFileTool(workDir string) *EditFileTool { ... }

func (t *EditFileTool) Name() string { return "edit_file" }

func (t *EditFileTool) Definition() schema.ToolDefinition {
    // JSON Schema:
    // {
    //   "type": "object",
    //   "properties": {
    //     "path": {"type": "string"},
    //     "edits": {
    //       "type": "array",
    //       "items": {
    //         "type": "object",
    //         "properties": {
    //           "old_text":    {"type": "string"},
    //           "new_text":    {"type": "string"},
    //           "replace_all": {"type": "boolean"}
    //         },
    //         "required": ["old_text", "new_text"]
    //       },
    //       "minItems": 1
    //     }
    //   },
    //   "required": ["path", "edits"]
    // }
}

type editFileArgs struct {
    Path  string     `json:"path"`
    Edits []editSpec `json:"edits"`
}

type editSpec struct {
    OldText    string `json:"old_text"`
    NewText    string `json:"new_text"`
    ReplaceAll bool   `json:"replace_all,omitempty"`
}
```

`Definition()` description text (concrete, model-facing):

> Apply one or more string replacements to an existing file. Each edit
> finds `old_text` in the file and replaces it with `new_text`. Edits
> apply sequentially against the in-memory result; if any edit fails,
> none are applied and the file is unchanged. Set `replace_all=true` to
> replace every occurrence; otherwise `old_text` must match exactly one
> location after fuzzy normalization. Use `read_file` first to obtain
> exact contents. Use `write_file` to create new files.

### Unit 2 — Fuzzy match chain

**File:** `internal/tools/fuzzy.go`

All functions are pure: take strings, return strings, no I/O, no
mutation.

```go
// FuzzyReplace runs the L1→L4 chain against content. Returns the new
// content, the level (1-4) that hit, and an error on miss or
// ambiguity. When replaceAll is true, the uniqueness check is relaxed
// at every level: multiple matches at any level result in multiple
// replacements, and the chain still terminates at the first level
// that produces ≥1 match.
func FuzzyReplace(content, oldText, newText string, replaceAll bool) (string, int, error)

// Internal helpers (lowercase, package-private; tests reach them
// through FuzzyReplace's behavior):
func normalizeLineEndings(s string) string
func trimSpaceMatch(content, oldText, newText string, replaceAll bool) (string, bool, error)
func lineByLineReplace(content, oldText, newText string, replaceAll bool) (string, bool, error)
func extractBasePrefix(line string) string
func reindent(text, basePrefix string) string
```

**Level semantics:**

- **L1 — Exact:** `count := strings.Count(content, oldText)`. If
  `count == 1`, replace once. If `count > 1` and `!replaceAll`, return
  multi-match error. If `replaceAll`, replace all.
- **L2 — CRLF normalization:** normalize both `content` and `oldText`
  to LF (`strings.ReplaceAll(s, "\r\n", "\n")`), then retry L1 against
  the normalized strings. If hit, the post-edit content is the
  normalized content with the replacement applied. **Side effect:** the
  whole file is rewritten with LF line endings. This is the simplest
  predictable behavior and matches `git`'s long-standing default. Files
  that need CRLF preservation should be inspected via `read_file`
  before editing.
- **L3 — TrimSpace on `oldText` only:** apply `strings.TrimSpace` to
  `oldText`. Search the (post-L2) content for the trimmed string.
  Replacement uses the trimmed match's byte range — i.e., we replace
  exactly what the model meant (without the leading/trailing whitespace
  it accidentally included), and `new_text` goes in as-given. This
  handles the "model wrapped the snippet in extra blank lines" case
  without disturbing surrounding whitespace.
- **L4 — Line-by-line TrimSpace + sliding window:** split content and
  `oldText` by `\n`. For each window of `len(oldTextLines)` consecutive
  content lines, compare lines pairwise after `strings.TrimSpace`. If
  exactly one window matches, capture `basePrefix` from the first
  matched line of the original content (`extractBasePrefix`), reindent
  `new_text` against `basePrefix`, and splice. If multiple windows
  match and `!replaceAll`, return ambiguity error. If multiple windows
  match and `replaceAll`, replace each matched window independently —
  each gets its own `basePrefix` extracted from its own first line, so
  blocks at different indentation depths still come out correctly
  aligned to their own context.

**Base-indent realignment** (D8 detail):

```go
// extractBasePrefix returns the leading whitespace run of line.
// Whitespace = spaces and tabs only.
func extractBasePrefix(line string) string

// reindent strips any common leading whitespace from non-empty lines
// of text, then prepends basePrefix to every non-empty line.
// Empty lines are preserved as empty (no trailing whitespace).
func reindent(text, basePrefix string) string
```

This solves the chapter's open problem: even when the model submits
`new_text` with no indentation, L4 repositions it to match the matched
block's depth.

### Unit 3 — Atomic file write

**File:** `internal/tools/atomic_write.go`

```go
// AtomicWriteFile writes data to path atomically: writes to a temp file
// in the same directory, fsyncs, then renames over path. On any error
// the temp file is removed and the original path is untouched.
// Preserves the original file's mode (the tool refuses non-existent
// files, so the original is always present at call time).
func AtomicWriteFile(path string, data []byte) error
```

Implementation outline:

1. `dir := filepath.Dir(path)`.
2. `origInfo, _ := os.Stat(path)` (caller has already verified path exists).
3. `tmp, err := os.CreateTemp(dir, ".edit-*.tmp")`.
4. Defer `os.Remove(tmp.Name())` — no-op once rename succeeds.
5. `tmp.Write(data)`, `tmp.Sync()`, `tmp.Close()`.
6. `os.Chmod(tmp.Name(), origInfo.Mode().Perm())`.
7. `os.Rename(tmp.Name(), path)`.

Same-directory temp guarantees rename is atomic on a single filesystem.

### Unit 4 — Execute orchestration

**File:** `internal/tools/edit_file.go` (continued)

```go
func (t *EditFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
    // 1. Unmarshal args.
    // 2. Validate: edits non-empty.
    // 3. ResolveInWorkDir(t.workDir, input.Path) — sandboxing.
    // 4. os.Stat → reject if missing with "use write_file" message.
    // 5. os.ReadFile.
    // 6. Apply edits in memory:
    //    levelCounts := [4]int{}
    //    current := string(originalBytes)
    //    for i, e := range input.Edits {
    //        if e.OldText == e.NewText { return error }
    //        next, level, err := FuzzyReplace(current, e.OldText, e.NewText, e.ReplaceAll)
    //        if err != nil { return wrapped error including edit index i }
    //        current = next
    //        levelCounts[level-1]++
    //    }
    // 7. AtomicWriteFile(fullPath, []byte(current)).
    // 8. Return success message with counts.
}
```

`Execute` is the only function in this design that touches disk.
Everything else is pure.

### Unit 5 — Error messages

Returned to the model verbatim through the existing `ToolResult.Output`
+ `IsError=true` path. Each is concrete enough to support
self-correction:

| Failure | Message |
|---------|---------|
| `edits` empty | `edit_file: edits array is empty` |
| File missing | `edit_file: %q does not exist; use write_file to create it` |
| Path escape | (existing `ErrPathEscape` message via `ResolveInWorkDir`) |
| No-op edit | `edit_file: edit[%d] old_text equals new_text` |
| Match miss (all levels) | `edit_file: edit[%d] old_text not found in %q; re-read the file and check exact contents (incl. whitespace and line endings)` |
| Multiple matches | `edit_file: edit[%d] old_text matched %d places in %q; provide more surrounding context to disambiguate, or set replace_all=true` |

Success: `edited %q (%d edits applied: L1=%d L2=%d L3=%d L4=%d)`.

### Unit 6 — `main.go` integration

**File:** `cmd/claw/main.go`

Single line addition after the existing tool registrations:

```go
registry.Register(tools.NewReadFileTool(cwd))
registry.Register(tools.NewWriteFileTool(cwd))
registry.Register(tools.NewEditFileTool(cwd))   // new
registry.Register(tools.NewBashTool(cwd))
```

Tool ordering in registry storage doesn't matter; `GetAvailableTools`
already sorts by name before returning.

### Unit 7 — README and CHANGELOG

**File:** `README.md`

Add row to the Tools table:

| Tool | Description | Sandbox |
|------|-------------|---------|
| `edit_file` | Apply string replacements to an existing file. Multi-edit atomic; fuzzy match (CRLF, whitespace, indentation) absorbs format drift; uniqueness enforced. | Path traversal blocked. Refuses non-existent files. |

**File:** `CHANGELOG.md`

Add new top section above the existing `[0.1.0.0]` entry:

```
## [Unreleased]

### Added
- `edit_file` tool: multi-edit string replacement with L1→L4 fuzzy match
  chain (exact → CRLF normalization → TrimSpace → line-by-line
  TrimSpace + sliding window with base-indent realignment), atomic
  rollback across an edits array, and atomic file write via temp +
  rename. Refuses non-existent files (use `write_file`) and no-op
  edits. Mounted in `cmd/claw/main.go`.
```

## Test Strategy

TDD per global guidelines: each test is written before its
implementation. All file I/O tests use `t.TempDir()`.

### Unit tests — `tests/tools/fuzzy_test.go`

Pure-function tests of `FuzzyReplace` and helpers. No mocks.

| Case | Expectation |
|------|-------------|
| L1 unique exact match | `level=1`, content updated |
| L1 multi-match, `replaceAll=false` | error: "matched N places" |
| L1 multi-match, `replaceAll=true` | all replaced, `level=1` |
| L1 zero match → L2 hit (CRLF in file, LF in oldText) | `level=2`, content updated |
| L1+L2 zero → L3 hit (extra blank lines around block) | `level=3`, content updated |
| L1..L3 zero → L4 hit (model omitted leading whitespace) | `level=4`, content updated, replacement carries original indent |
| L4 ambiguous (two same-content blocks at different indents) | error: "matched N places" |
| L4 ambiguous, `replaceAll=true` | all matching windows replaced, each reindented to its own basePrefix |
| All four levels miss | error: "not found" |
| `extractBasePrefix` on `"        if x"` | `"        "` |
| `extractBasePrefix` on `"\t\tfoo"` | `"\t\t"` |
| `extractBasePrefix` on `"foo"` | `""` |
| `reindent` strips common indent then prepends basePrefix | non-empty lines reindented; empty lines stay empty |

### Unit tests — `tests/tools/atomic_write_test.go`

| Case | Expectation |
|------|-------------|
| Write to new path | file exists with payload, perm `0644` |
| Overwrite existing path | content replaced, original perm preserved |
| Mid-write fault (inject failure between write and rename) | original file unchanged, no `.edit-*.tmp` left in dir |

The fault injection uses a small wrapper that returns an error after
the temp write; the test asserts the original file matches its
pre-call snapshot byte-for-byte.

### Integration tests — `tests/tools/edit_file_test.go`

Through `Execute()` only.

| Case | Expectation |
|------|-------------|
| Single edit, exact match | file updated, success message includes `L1=1` |
| Multi-edit applied sequentially | edit 2's `old_text` matches edit 1's `new_text` output |
| Multi-edit rollback | edit 1 succeeds, edit 2 misses → file on disk unchanged byte-for-byte |
| File missing | error names `write_file`; no file created |
| Path traversal | propagates `ErrPathEscape` |
| No-op edit (`old_text == new_text`) | error mentions edit index |
| Empty edits array | error |
| Indentation hallucination scenario | file has 8-space-indented block; `old_text` has no indent; edit succeeds via L4; resulting file has correct 8-space-indented `new_text` |
| Malformed JSON args | parse error |

### Tool definition test

Add a row to the existing `tests/tools/tool_definition_test.go` table to
assert `edit_file` exposes the documented schema fields.

## Risks and Mitigations

| Risk | Mitigation |
|------|-----------|
| L4 base-indent reindent corrupts code that *intentionally* mixes indentation in `new_text` (e.g., a here-doc with significant leading spaces) | `reindent` strips only the *common* leading whitespace across non-empty lines. Lines with extra indent retain their relative depth. Documented in tests. |
| L4 sliding window O(n·m) cost on large files | The 10 MB `read_file` cap already bounds n. m (edit size) is model-controlled and small in practice. Acceptable. Optimisation deferred. |
| Atomic rename across filesystems is not atomic | Same-directory temp file guarantees same fs. Documented. |
| Two edits in one call where edit 2's `old_text` overlaps edit 1's region | Sequential application against in-memory result already handles this correctly. Tested. |
| Model sets `replace_all=true` carelessly | Per-edit flag (not file-wide), default false. Error messages on multi-match nudge toward adding context first. |

## Open Questions

None at draft time. The chapter's open problem (base-indent realignment
on L4 hits) is solved in Unit 2.

## Implementation Order

For the implementation plan (next phase), the natural sequence is:

1. `atomic_write.go` + tests (smallest, independent).
2. `fuzzy.go` L1 + L2 + tests.
3. `fuzzy.go` L3 + tests.
4. `fuzzy.go` L4 + base-indent helpers + tests.
5. `edit_file.go` Execute orchestration + tests.
6. `main.go` registration.
7. README + CHANGELOG.
8. Tool definition test row.

Each step ships green tests before moving on.
