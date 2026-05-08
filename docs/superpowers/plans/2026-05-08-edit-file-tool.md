# `edit_file` Tool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `edit_file` tool to penelope-agent that performs string replacement on existing files via an L1→L4 fuzzy match chain (exact → CRLF normalization → TrimSpace → line-by-line TrimSpace + sliding window with base-indent realignment), with multi-edit atomic rollback and atomic file write.

**Architecture:** Three new files in `internal/tools/` — `atomic_write.go` (pure-ish disk write helper), `fuzzy.go` (pure string-matching algorithms), `edit_file.go` (Tool interface impl + Execute orchestration, the only file that touches I/O). Three corresponding test files in `tests/tools/`. One-line addition to `cmd/claw/main.go`. Updates to `tests/tools/tool_definition_test.go`, `README.md`, `CHANGELOG.md`.

**Tech Stack:** Go 1.21+, standard library only (no new dependencies). Existing internal packages: `internal/tools` (registry, safepath, truncate), `internal/schema` (ToolDefinition).

**Spec:** See [docs/superpowers/specs/2026-05-07-edit-file-tool-design.md](../specs/2026-05-07-edit-file-tool-design.md) for the full design rationale.

---

## File Map

**Create:**
- `internal/tools/atomic_write.go` — `AtomicWriteFile(path, data)` helper (~30 lines)
- `internal/tools/fuzzy.go` — `FuzzyReplace` + L1-L4 helpers (~150 lines, target <200)
- `internal/tools/edit_file.go` — `EditFileTool` struct + `Execute` orchestration (~100 lines)
- `tests/tools/atomic_write_test.go` — happy path, mode preservation, rollback
- `tests/tools/fuzzy_test.go` — all four fuzzy levels, uniqueness, base-indent
- `tests/tools/edit_file_test.go` — public-surface integration through `Execute`

**Modify:**
- `cmd/claw/main.go` — register `EditFileTool` (1 line added)
- `tests/tools/tool_definition_test.go` — add `TestEditFileToolNameAndDefinition`
- `README.md` — add `edit_file` row to Tools table
- `CHANGELOG.md` — add `[Unreleased]` section

---

## Conventions to Follow

- **External test packages.** All test files use `package tools_test` and import `github.com/snowshine0216/penelope-agent/internal/tools`.
- **Functional style** (per global CLAUDE.md): pure functions return new strings; no mutation; no I/O outside `edit_file.go`'s `Execute`.
- **File size target <200 lines, function size <20 lines.**
- **Path safety:** all path resolution goes through `tools.ResolveInWorkDir` (existing).
- **Error wrapping:** use `%w` for wrappable errors; plain `%v` for human messages.
- **Commit style:** `feat(tools): ...`, `test: ...`, `docs: ...` per existing repo style. Use HEREDOC for commit messages.
- **Run tests with:** `go test ./tests/tools/ -v` (full package) or `go test ./tests/tools/ -run TestName -v` (single test).

---

## Task 1: `AtomicWriteFile` helper

**Files:**
- Create: `internal/tools/atomic_write.go`
- Create: `tests/tools/atomic_write_test.go`

### Step 1.1: Write the first failing test (happy path — new file)

- [ ] Create `tests/tools/atomic_write_test.go` with:

```go
package tools_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestAtomicWriteCreatesFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("seed"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := tools.AtomicWriteFile(target, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want hello", got)
	}
}
```

> Note: even the "first" test seeds an existing file, because `AtomicWriteFile`'s contract requires the target to already exist (the tool refuses non-existent files at a higher level, so this helper never sees them).

### Step 1.2: Run the test — verify it fails

- [ ] Run: `go test ./tests/tools/ -run TestAtomicWriteCreatesFile -v`
- [ ] Expected: compile error or `undefined: tools.AtomicWriteFile`.

### Step 1.3: Implement minimal `AtomicWriteFile`

- [ ] Create `internal/tools/atomic_write.go`:

```go
package tools

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes data to path atomically: writes to a temp file
// in the same directory, fsyncs, then renames over path. On any error,
// the temp file is removed and the original path is untouched.
// Preserves the original file's mode. Caller must ensure path exists.
func AtomicWriteFile(path string, data []byte) error {
	origInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat original: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".edit-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if anything fails before rename.
	committed := false
	defer func() {
		if !committed {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	if err := os.Chmod(tmpName, origInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod temp: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	committed = true
	return nil
}
```

### Step 1.4: Run the test — verify it passes

- [ ] Run: `go test ./tests/tools/ -run TestAtomicWriteCreatesFile -v`
- [ ] Expected: `--- PASS: TestAtomicWriteCreatesFile`.

### Step 1.5: Add overwrite test

- [ ] Append to `tests/tools/atomic_write_test.go`:

```go
func TestAtomicWriteOverwritesContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := tools.AtomicWriteFile(target, []byte("new")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want new", got)
	}
}
```

### Step 1.6: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestAtomicWriteOverwritesContent -v`
- [ ] Expected: PASS.

### Step 1.7: Add mode preservation test

- [ ] Append:

```go
func TestAtomicWritePreservesMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := tools.AtomicWriteFile(target, []byte("new")); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != fs.FileMode(0o600) {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}
```

### Step 1.8: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestAtomicWritePreservesMode -v`
- [ ] Expected: PASS.

### Step 1.9: Add cleanup-on-success test

- [ ] Append:

```go
func TestAtomicWriteRemovesTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := tools.AtomicWriteFile(target, []byte("new")); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".edit-") {
			t.Fatalf("temp file %q still present after success", e.Name())
		}
	}
}
```

### Step 1.10: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestAtomicWriteRemovesTempOnSuccess -v`
- [ ] Expected: PASS.

### Step 1.11: Add rollback-on-failure test (skip if root)

- [ ] Append:

```go
func TestAtomicWriteRollsBackOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Make the directory unwritable so CreateTemp fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// Restore so t.TempDir cleanup can remove the directory.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := tools.AtomicWriteFile(target, []byte("new")); err == nil {
		t.Fatal("expected error when temp file cannot be created")
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("re-chmod: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("content = %q, want original (rollback failed)", got)
	}
}
```

### Step 1.12: Run all atomic-write tests — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestAtomicWrite -v`
- [ ] Expected: 5 PASS.

### Step 1.13: Run full test suite — verify nothing else broke

- [ ] Run: `go test ./...`
- [ ] Expected: all PASS.

### Step 1.14: Commit

- [ ] Run:

```bash
git add internal/tools/atomic_write.go tests/tools/atomic_write_test.go
git commit -m "$(cat <<'EOF'
feat(tools): add AtomicWriteFile helper

Writes data to a temp file in the same directory, fsyncs, then renames
over the target. Preserves the original file's mode. Cleans up the
temp file on any error so callers see no half-written state.

Foundation for edit_file's atomic rollback semantics.
EOF
)"
```

---

## Task 2: `fuzzy.go` skeleton + L1 (exact match)

**Files:**
- Create: `internal/tools/fuzzy.go`
- Create: `tests/tools/fuzzy_test.go`

### Step 2.1: Write failing test for L1 unique match

- [ ] Create `tests/tools/fuzzy_test.go`:

```go
package tools_test

import (
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestFuzzyReplaceL1ExactUniqueMatch(t *testing.T) {
	content := "hello world\nfoo bar\n"
	out, level, err := tools.FuzzyReplace(content, "world", "Go", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 1 {
		t.Fatalf("level = %d, want 1", level)
	}
	if out != "hello Go\nfoo bar\n" {
		t.Fatalf("out = %q", out)
	}
}
```

### Step 2.2: Run — verify fail

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceL1ExactUniqueMatch -v`
- [ ] Expected: `undefined: tools.FuzzyReplace`.

### Step 2.3: Implement minimal `FuzzyReplace` (L1 only)

- [ ] Create `internal/tools/fuzzy.go`:

```go
package tools

import (
	"fmt"
	"strings"
)

// FuzzyReplace runs the L1->L4 fuzzy match chain against content. It
// returns the new content, the level (1-4) that matched, and an error
// on miss or ambiguity. When replaceAll is true, the uniqueness check
// is relaxed at every level: multiple matches result in multiple
// replacements, and the chain still terminates at the first level
// that produces >=1 match.
func FuzzyReplace(content, oldText, newText string, replaceAll bool) (string, int, error) {
	if out, ok, err := exactReplace(content, oldText, newText, replaceAll); ok || err != nil {
		return out, 1, err
	}
	return "", 0, fmt.Errorf("old_text not found")
}

// exactReplace handles L1: strings.Count == 1 (or replaceAll for any
// positive count). Returns (newContent, true, nil) on hit,
// ("", false, nil) on miss (so caller can fall through to L2),
// ("", true, err) on ambiguity (multiple matches without replaceAll).
func exactReplace(content, oldText, newText string, replaceAll bool) (string, bool, error) {
	count := strings.Count(content, oldText)
	if count == 0 {
		return "", false, nil
	}
	if count > 1 && !replaceAll {
		return "", true, fmt.Errorf("matched %d places", count)
	}
	return strings.ReplaceAll(content, oldText, newText), true, nil
}
```

### Step 2.4: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceL1ExactUniqueMatch -v`
- [ ] Expected: PASS.

### Step 2.5: Add L1 multi-match test

- [ ] Append to `tests/tools/fuzzy_test.go`:

```go
func TestFuzzyReplaceL1MultiMatchErrors(t *testing.T) {
	content := "foo\nfoo\n"
	_, _, err := tools.FuzzyReplace(content, "foo", "bar", false)
	if err == nil {
		t.Fatal("expected error for multi-match, got nil")
	}
	if !strings.Contains(err.Error(), "matched") {
		t.Fatalf("error = %q, want it to mention match count", err)
	}
}

func TestFuzzyReplaceL1ReplaceAllReplacesAll(t *testing.T) {
	content := "foo\nfoo\n"
	out, level, err := tools.FuzzyReplace(content, "foo", "bar", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 1 {
		t.Fatalf("level = %d, want 1", level)
	}
	if out != "bar\nbar\n" {
		t.Fatalf("out = %q, want bar\\nbar\\n", out)
	}
}

func TestFuzzyReplaceMissReturnsError(t *testing.T) {
	_, _, err := tools.FuzzyReplace("hello", "missing", "x", false)
	if err == nil {
		t.Fatal("expected error for miss, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q, want 'not found'", err)
	}
}
```

### Step 2.6: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplace -v`
- [ ] Expected: 4 PASS.

### Step 2.7: Commit L1

- [ ] Run:

```bash
git add internal/tools/fuzzy.go tests/tools/fuzzy_test.go
git commit -m "$(cat <<'EOF'
feat(tools): add FuzzyReplace skeleton with L1 exact match

L1 of the four-level fuzzy match chain. Handles unique exact match,
multi-match ambiguity, and replace_all override. L2-L4 to follow.
EOF
)"
```

---

## Task 3: `fuzzy.go` L2 (CRLF normalization)

**Files:**
- Modify: `internal/tools/fuzzy.go`
- Modify: `tests/tools/fuzzy_test.go`

### Step 3.1: Write failing test for L2 (CRLF in content, LF in oldText)

- [ ] Append to `tests/tools/fuzzy_test.go`:

```go
func TestFuzzyReplaceL2CRLFNormalization(t *testing.T) {
	// File on disk has CRLF; model produced oldText with LF.
	content := "line1\r\nline2\r\nline3\r\n"
	out, level, err := tools.FuzzyReplace(content, "line2\nline3", "X", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 2 {
		t.Fatalf("level = %d, want 2", level)
	}
	// L2 normalizes the whole file to LF as a documented side effect.
	if out != "line1\nX\n" {
		t.Fatalf("out = %q, want %q", out, "line1\nX\n")
	}
}
```

### Step 3.2: Run — verify fail

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceL2 -v`
- [ ] Expected: FAIL — current impl returns "not found" because exact match misses.

### Step 3.3: Add L2 to `FuzzyReplace`

- [ ] In `internal/tools/fuzzy.go`, replace the body of `FuzzyReplace` with:

```go
func FuzzyReplace(content, oldText, newText string, replaceAll bool) (string, int, error) {
	if out, ok, err := exactReplace(content, oldText, newText, replaceAll); ok || err != nil {
		return out, 1, err
	}

	normContent := normalizeLineEndings(content)
	normOld := normalizeLineEndings(oldText)
	if out, ok, err := exactReplace(normContent, normOld, newText, replaceAll); ok || err != nil {
		return out, 2, err
	}

	return "", 0, fmt.Errorf("old_text not found")
}

// normalizeLineEndings replaces CRLF with LF.
func normalizeLineEndings(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}
```

### Step 3.4: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceL2 -v`
- [ ] Expected: PASS.

### Step 3.5: Re-run all fuzzy tests — verify nothing regressed

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplace -v`
- [ ] Expected: 5 PASS.

### Step 3.6: Commit L2

- [ ] Run:

```bash
git add internal/tools/fuzzy.go tests/tools/fuzzy_test.go
git commit -m "$(cat <<'EOF'
feat(tools): add FuzzyReplace L2 CRLF normalization

When exact match misses, normalize both content and oldText to LF and
retry. On hit, the file's line endings are normalized to LF as a side
effect (matches git's default behavior).
EOF
)"
```

---

## Task 4: `fuzzy.go` L3 (TrimSpace on oldText)

**Files:**
- Modify: `internal/tools/fuzzy.go`
- Modify: `tests/tools/fuzzy_test.go`

### Step 4.1: Write failing test for L3 (extra whitespace around oldText)

- [ ] Append to `tests/tools/fuzzy_test.go`:

```go
func TestFuzzyReplaceL3TrimsOldText(t *testing.T) {
	// Content has the bare snippet; model wrapped oldText in extra
	// blank lines and trailing whitespace.
	content := "before\nfoo bar\nafter\n"
	oldText := "\n\n  foo bar  \n\n"
	out, level, err := tools.FuzzyReplace(content, oldText, "REPLACED", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 3 {
		t.Fatalf("level = %d, want 3", level)
	}
	// L3 replaces only the trimmed-match's byte range; surrounding
	// whitespace in the original file stays intact.
	if out != "before\nREPLACED\nafter\n" {
		t.Fatalf("out = %q", out)
	}
}
```

### Step 4.2: Run — verify fail

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceL3 -v`
- [ ] Expected: FAIL — currently returns "not found" because L1 and L2 both miss.

### Step 4.3: Add L3 to `FuzzyReplace`

- [ ] In `internal/tools/fuzzy.go`, update `FuzzyReplace`:

```go
func FuzzyReplace(content, oldText, newText string, replaceAll bool) (string, int, error) {
	if out, ok, err := exactReplace(content, oldText, newText, replaceAll); ok || err != nil {
		return out, 1, err
	}

	normContent := normalizeLineEndings(content)
	normOld := normalizeLineEndings(oldText)
	if out, ok, err := exactReplace(normContent, normOld, newText, replaceAll); ok || err != nil {
		return out, 2, err
	}

	trimmedOld := strings.TrimSpace(normOld)
	if trimmedOld != "" {
		if out, ok, err := exactReplace(normContent, trimmedOld, newText, replaceAll); ok || err != nil {
			return out, 3, err
		}
	}

	return "", 0, fmt.Errorf("old_text not found")
}
```

> Note: L3 trims only `oldText`. The replacement uses the trimmed match's byte range — so surrounding whitespace in the original content is preserved automatically because `strings.ReplaceAll` only consumes what it matched.

### Step 4.4: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceL3 -v`
- [ ] Expected: PASS.

### Step 4.5: Add L3 ambiguity test

- [ ] Append:

```go
func TestFuzzyReplaceL3AmbiguityErrors(t *testing.T) {
	content := "foo bar\nfoo bar\n"
	oldText := "  foo bar  "
	_, _, err := tools.FuzzyReplace(content, oldText, "X", false)
	if err == nil {
		t.Fatal("expected error for L3 ambiguity")
	}
}
```

### Step 4.6: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceL3 -v`
- [ ] Expected: 2 PASS.

### Step 4.7: Re-run all fuzzy tests — verify no regression

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplace -v`
- [ ] Expected: 7 PASS.

### Step 4.8: Commit L3

- [ ] Run:

```bash
git add internal/tools/fuzzy.go tests/tools/fuzzy_test.go
git commit -m "$(cat <<'EOF'
feat(tools): add FuzzyReplace L3 TrimSpace on oldText

When L1 and L2 miss, trim leading/trailing whitespace from oldText and
retry. Replacement uses the trimmed byte range so surrounding
whitespace in the file is preserved. Handles the case where the model
wrapped a snippet in extra blank lines.
EOF
)"
```

---

## Task 5: `fuzzy.go` L4 (line-by-line + base-indent realignment)

This is the largest task. It implements the core indentation-hallucination absorber and the chapter's open problem (base-indent realignment).

**Files:**
- Modify: `internal/tools/fuzzy.go`
- Modify: `tests/tools/fuzzy_test.go`

### Step 5.1: Write failing test for L4 hit with indent restoration

- [ ] Append to `tests/tools/fuzzy_test.go`:

```go
func TestFuzzyReplaceL4IndentationHallucination(t *testing.T) {
	// File has 8-space indented block; model omitted indentation in oldText.
	content := "func main() {\n        if x {\n                doThing()\n        }\n}\n"
	oldText := "if x {\n        doThing()\n}"
	newText := "if y {\n        otherThing()\n}"

	out, level, err := tools.FuzzyReplace(content, oldText, newText, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 4 {
		t.Fatalf("level = %d, want 4", level)
	}

	// Expected: the matched block was at 8-space base indent, so the
	// replacement must come out at 8-space base indent as well, with
	// the relative deeper indent of "doThing" preserved.
	want := "func main() {\n        if y {\n                otherThing()\n        }\n}\n"
	if out != want {
		t.Fatalf("out = %q\nwant = %q", out, want)
	}
}
```

### Step 5.2: Run — verify fail

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceL4 -v`
- [ ] Expected: FAIL — L1-L3 all miss, L4 not implemented.

### Step 5.3: Implement L4 helpers and integrate

- [ ] In `internal/tools/fuzzy.go`, replace the entire file with the L4-complete version:

```go
package tools

import (
	"fmt"
	"strings"
)

// FuzzyReplace runs the L1->L4 fuzzy match chain against content. It
// returns the new content, the level (1-4) that matched, and an error
// on miss or ambiguity. When replaceAll is true, the uniqueness check
// is relaxed at every level: multiple matches result in multiple
// replacements, and the chain still terminates at the first level
// that produces >=1 match.
func FuzzyReplace(content, oldText, newText string, replaceAll bool) (string, int, error) {
	if out, ok, err := exactReplace(content, oldText, newText, replaceAll); ok || err != nil {
		return out, 1, err
	}

	normContent := normalizeLineEndings(content)
	normOld := normalizeLineEndings(oldText)
	if out, ok, err := exactReplace(normContent, normOld, newText, replaceAll); ok || err != nil {
		return out, 2, err
	}

	trimmedOld := strings.TrimSpace(normOld)
	if trimmedOld != "" {
		if out, ok, err := exactReplace(normContent, trimmedOld, newText, replaceAll); ok || err != nil {
			return out, 3, err
		}
	}

	if out, ok, err := lineByLineReplace(normContent, normOld, newText, replaceAll); ok || err != nil {
		return out, 4, err
	}

	return "", 0, fmt.Errorf("old_text not found")
}

// exactReplace handles L1.
func exactReplace(content, oldText, newText string, replaceAll bool) (string, bool, error) {
	count := strings.Count(content, oldText)
	if count == 0 {
		return "", false, nil
	}
	if count > 1 && !replaceAll {
		return "", true, fmt.Errorf("matched %d places", count)
	}
	return strings.ReplaceAll(content, oldText, newText), true, nil
}

// normalizeLineEndings replaces CRLF with LF.
func normalizeLineEndings(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// lineByLineReplace splits content and oldText by '\n', then slides a
// window of len(oldLines) over the content lines comparing each pair
// after TrimSpace. On a unique match, the replacement is reindented to
// the matched window's base indentation prefix.
func lineByLineReplace(content, oldText, newText string, replaceAll bool) (string, bool, error) {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(oldText, "\n")
	if len(oldLines) == 0 || len(oldLines) > len(contentLines) {
		return "", false, nil
	}

	matches := findLineWindowMatches(contentLines, oldLines)
	if len(matches) == 0 {
		return "", false, nil
	}
	if len(matches) > 1 && !replaceAll {
		return "", true, fmt.Errorf("matched %d places", len(matches))
	}

	// Process matches in reverse order so earlier indices remain valid.
	resultLines := append([]string(nil), contentLines...)
	for i := len(matches) - 1; i >= 0; i-- {
		start := matches[i]
		basePrefix := extractBasePrefix(resultLines[start])
		reindented := reindent(newText, basePrefix)
		newSegmentLines := strings.Split(reindented, "\n")

		head := resultLines[:start]
		tail := resultLines[start+len(oldLines):]
		// Concatenate without aliasing.
		combined := make([]string, 0, len(head)+len(newSegmentLines)+len(tail))
		combined = append(combined, head...)
		combined = append(combined, newSegmentLines...)
		combined = append(combined, tail...)
		resultLines = combined
	}

	return strings.Join(resultLines, "\n"), true, nil
}

// findLineWindowMatches returns the start indices of every contiguous
// content-line window of length len(oldLines) where each pair compares
// equal after TrimSpace. Match windows are non-overlapping (advance i
// by len(oldLines) after a hit).
func findLineWindowMatches(contentLines, oldLines []string) []int {
	var hits []int
	if len(oldLines) == 0 {
		return hits
	}
	i := 0
	for i+len(oldLines) <= len(contentLines) {
		matched := true
		for j := 0; j < len(oldLines); j++ {
			if strings.TrimSpace(contentLines[i+j]) != strings.TrimSpace(oldLines[j]) {
				matched = false
				break
			}
		}
		if matched {
			hits = append(hits, i)
			i += len(oldLines)
		} else {
			i++
		}
	}
	return hits
}

// extractBasePrefix returns the leading whitespace run of line.
// Whitespace is spaces and tabs only.
func extractBasePrefix(line string) string {
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c != ' ' && c != '\t' {
			return line[:i]
		}
	}
	return line
}

// reindent strips the common leading whitespace from non-empty lines
// of text, then prepends basePrefix to every non-empty line. Empty
// lines stay empty (no trailing whitespace is introduced).
func reindent(text, basePrefix string) string {
	lines := strings.Split(text, "\n")
	common := commonLeadingWhitespace(lines)

	out := make([]string, len(lines))
	for i, line := range lines {
		if line == "" {
			out[i] = ""
			continue
		}
		stripped := strings.TrimPrefix(line, common)
		out[i] = basePrefix + stripped
	}
	return strings.Join(out, "\n")
}

// commonLeadingWhitespace returns the longest leading whitespace
// prefix shared by every non-empty line. Returns "" if there are no
// non-empty lines.
func commonLeadingWhitespace(lines []string) string {
	first := true
	prefix := ""
	for _, line := range lines {
		if line == "" {
			continue
		}
		linePrefix := extractBasePrefix(line)
		if first {
			prefix = linePrefix
			first = false
			continue
		}
		prefix = longestCommonPrefix(prefix, linePrefix)
		if prefix == "" {
			return ""
		}
	}
	return prefix
}

// longestCommonPrefix returns the longest string that is a prefix of
// both a and b.
func longestCommonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}
```

### Step 5.4: Run the new test — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceL4IndentationHallucination -v`
- [ ] Expected: PASS.

### Step 5.5: Add L4 ambiguity test

- [ ] Append to `tests/tools/fuzzy_test.go`:

```go
func TestFuzzyReplaceL4AmbiguityErrors(t *testing.T) {
	// Two windows TrimSpace-match identically.
	content := "    foo\n    bar\n        foo\n        bar\n"
	oldText := "foo\nbar"
	_, _, err := tools.FuzzyReplace(content, oldText, "X\nY", false)
	if err == nil {
		t.Fatal("expected error for L4 ambiguity")
	}
	if !strings.Contains(err.Error(), "matched") {
		t.Fatalf("error = %q, want it to mention match count", err)
	}
}
```

### Step 5.6: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceL4Ambiguity -v`
- [ ] Expected: PASS.

### Step 5.7: Add L4 replaceAll-with-different-indents test

- [ ] Append:

```go
func TestFuzzyReplaceL4ReplaceAllPreservesPerWindowIndent(t *testing.T) {
	// Two windows at different base indents. With replaceAll, each
	// must come out reindented to its own base prefix.
	content := "" +
		"    foo\n" +
		"    bar\n" +
		"middle\n" +
		"        foo\n" +
		"        bar\n"
	oldText := "foo\nbar"
	newText := "X\nY"

	out, level, err := tools.FuzzyReplace(content, oldText, newText, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 4 {
		t.Fatalf("level = %d, want 4", level)
	}
	want := "" +
		"    X\n" +
		"    Y\n" +
		"middle\n" +
		"        X\n" +
		"        Y\n"
	if out != want {
		t.Fatalf("out = %q\nwant = %q", out, want)
	}
}
```

### Step 5.8: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceL4ReplaceAll -v`
- [ ] Expected: PASS.

### Step 5.9: Add L4 reindent preserves relative depth test

- [ ] Append:

```go
func TestFuzzyReplaceL4PreservesRelativeIndentInNewText(t *testing.T) {
	// new_text already has internal relative indentation. The model
	// produced it with 4-space base; the matched block in the file is
	// at 8 spaces. After reindent, base shifts to 8 but the relative
	// extra indent of the inner line is preserved.
	content := "" +
		"top\n" +
		"        if x {\n" +
		"                inner()\n" +
		"        }\n" +
		"end\n"
	oldText := "" +
		"if x {\n" +
		"        inner()\n" +
		"}"
	newText := "" +
		"if y {\n" +
		"    inner2()\n" +
		"}"

	out, level, err := tools.FuzzyReplace(content, oldText, newText, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 4 {
		t.Fatalf("level = %d, want 4", level)
	}
	// new_text's common prefix is "" (line 1 has no indent), so reindent
	// strips nothing and prepends "        " (8 spaces) to non-empty lines.
	want := "" +
		"top\n" +
		"        if y {\n" +
		"            inner2()\n" +
		"        }\n" +
		"end\n"
	if out != want {
		t.Fatalf("out = %q\nwant = %q", out, want)
	}
}
```

### Step 5.10: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceL4PreservesRelative -v`
- [ ] Expected: PASS.

### Step 5.11: Add four-levels-miss test

- [ ] Append:

```go
func TestFuzzyReplaceAllLevelsMiss(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	_, level, err := tools.FuzzyReplace(content, "delta", "epsilon", false)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if level != 0 {
		t.Fatalf("level = %d, want 0 on miss", level)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q", err)
	}
}
```

### Step 5.12: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplaceAllLevelsMiss -v`
- [ ] Expected: PASS.

### Step 5.13: Run all fuzzy tests — verify no regression

- [ ] Run: `go test ./tests/tools/ -run TestFuzzyReplace -v`
- [ ] Expected: 11+ PASS.

### Step 5.14: Run full test suite

- [ ] Run: `go test ./...`
- [ ] Expected: all PASS.

### Step 5.15: Commit L4

- [ ] Run:

```bash
git add internal/tools/fuzzy.go tests/tools/fuzzy_test.go
git commit -m "$(cat <<'EOF'
feat(tools): add FuzzyReplace L4 line-by-line match with indent realignment

L4 is the indentation-hallucination absorber. Splits content and
oldText by '\n', slides a window comparing each pair after TrimSpace.
On a hit, captures the matched block's base indent prefix and
realigns new_text so non-empty lines come out at the original block's
depth, with relative deeper indents preserved.

This solves the chapter's open problem (base-indent alignment) and
breaks the agent death-loop where a stricter harness would reject
indent-stripped old_text indefinitely.
EOF
)"
```

---

## Task 6: `edit_file.go` Tool implementation

**Files:**
- Create: `internal/tools/edit_file.go`
- Create: `tests/tools/edit_file_test.go`

### Step 6.1: Write failing test for happy-path single edit

- [ ] Create `tests/tools/edit_file_test.go`:

```go
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func editArgs(t *testing.T, path string, edits []map[string]interface{}) json.RawMessage {
	t.Helper()
	out, err := json.Marshal(map[string]interface{}{
		"path":  path,
		"edits": edits,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func TestEditFileSingleExactMatch(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(target, []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{
		{"old_text": "world", "new_text": "Go"},
	})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "edited") || !strings.Contains(out, "L1=1") {
		t.Fatalf("output = %q, want it to mention edited + L1=1", out)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "hello Go\n" {
		t.Fatalf("file = %q, want hello Go\\n", got)
	}
}
```

### Step 6.2: Run — verify fail

- [ ] Run: `go test ./tests/tools/ -run TestEditFileSingleExactMatch -v`
- [ ] Expected: `undefined: tools.NewEditFileTool`.

### Step 6.3: Create `edit_file.go`

- [ ] Create `internal/tools/edit_file.go`:

```go
// internal/tools/edit_file.go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// EditFileTool applies one or more string replacements to an existing
// file in the workspace, using the L1->L4 fuzzy match chain. Multi-edit
// is atomic: any failure rolls back to the original file content.
type EditFileTool struct {
	workDir string
}

func NewEditFileTool(workDir string) *EditFileTool {
	return &EditFileTool{workDir: workDir}
}

func (t *EditFileTool) Name() string {
	return "edit_file"
}

func (t *EditFileTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name: t.Name(),
		Description: "Apply one or more string replacements to an existing file. " +
			"Each edit finds old_text in the file and replaces it with new_text. " +
			"Edits apply sequentially against the in-memory result; if any edit " +
			"fails, none are applied and the file is unchanged. Set replace_all=true " +
			"to replace every occurrence; otherwise old_text must match exactly one " +
			"location after fuzzy normalization. Use read_file first to obtain exact " +
			"contents. Use write_file to create new files.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "File path relative to the workspace",
				},
				"edits": map[string]interface{}{
					"type":        "array",
					"description": "One or more string replacements to apply atomically",
					"minItems":    1,
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"old_text": map[string]interface{}{
								"type":        "string",
								"description": "Text to find (matched via fuzzy chain)",
							},
							"new_text": map[string]interface{}{
								"type":        "string",
								"description": "Replacement text",
							},
							"replace_all": map[string]interface{}{
								"type":        "boolean",
								"description": "If true, replace every occurrence (default false)",
							},
						},
						"required": []string{"old_text", "new_text"},
					},
				},
			},
			"required": []string{"path", "edits"},
		},
	}
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

func (t *EditFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input editFileArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	if len(input.Edits) == 0 {
		return "", fmt.Errorf("edit_file: edits array is empty")
	}

	fullPath, err := ResolveInWorkDir(t.workDir, input.Path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("edit_file: %q does not exist; use write_file to create it", input.Path)
		}
		return "", fmt.Errorf("stat file: %w", err)
	}

	original, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	current := string(original)
	var levelCounts [4]int
	for i, e := range input.Edits {
		if e.OldText == e.NewText {
			return "", fmt.Errorf("edit_file: edit[%d] old_text equals new_text", i)
		}
		next, level, ferr := FuzzyReplace(current, e.OldText, e.NewText, e.ReplaceAll)
		if ferr != nil {
			return "", formatEditError(i, input.Path, ferr)
		}
		current = next
		levelCounts[level-1]++
	}

	if err := AtomicWriteFile(fullPath, []byte(current)); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf(
		"edited %q (%d edits applied: L1=%d L2=%d L3=%d L4=%d)",
		input.Path, len(input.Edits),
		levelCounts[0], levelCounts[1], levelCounts[2], levelCounts[3],
	), nil
}

// formatEditError translates a FuzzyReplace error into the model-facing
// message format documented in the spec. FuzzyReplace returns one of
// two shapes: "old_text not found" on miss, or "matched N places" on
// ambiguity.
func formatEditError(editIndex int, path string, ferr error) error {
	msg := ferr.Error()
	if strings.Contains(msg, "not found") {
		return fmt.Errorf(
			"edit_file: edit[%d] old_text not found in %q; re-read the file and check exact contents (incl. whitespace and line endings)",
			editIndex, path,
		)
	}
	return fmt.Errorf(
		"edit_file: edit[%d] %s in %q; provide more surrounding context to disambiguate, or set replace_all=true",
		editIndex, msg, path,
	)
}
```

### Step 6.4: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestEditFileSingleExactMatch -v`
- [ ] Expected: PASS.

### Step 6.5: Add multi-edit sequential test

- [ ] Append to `tests/tools/edit_file_test.go`:

```go
func TestEditFileMultiEditSequential(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(target, []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{
		{"old_text": "alpha", "new_text": "beta"},
		// Edit 2 should see edit 1's output ("beta\n").
		{"old_text": "beta", "new_text": "gamma"},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "gamma\n" {
		t.Fatalf("file = %q, want gamma\\n", got)
	}
}
```

### Step 6.6: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestEditFileMultiEditSequential -v`
- [ ] Expected: PASS.

### Step 6.7: Add multi-edit rollback test

- [ ] Append:

```go
func TestEditFileMultiEditRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	originalBytes := []byte("alpha\nbeta\n")
	if err := os.WriteFile(target, originalBytes, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{
		{"old_text": "alpha", "new_text": "ALPHA"}, // succeeds
		{"old_text": "missing", "new_text": "x"},   // misses
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error from missing old_text in edit[1]")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != string(originalBytes) {
		t.Fatalf("file changed despite rollback: got %q, want %q", got, originalBytes)
	}
}
```

### Step 6.8: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestEditFileMultiEditRollback -v`
- [ ] Expected: PASS.

### Step 6.9: Add file-missing test

- [ ] Append:

```go
func TestEditFileFileMissingNamesWriteFile(t *testing.T) {
	dir := t.TempDir()
	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "nope.txt", []map[string]interface{}{
		{"old_text": "a", "new_text": "b"},
	})

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "write_file") {
		t.Fatalf("error = %q, want it to mention write_file", err)
	}
}
```

### Step 6.10: Add path-traversal test

- [ ] Append:

```go
func TestEditFileRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(target, []byte("seed"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(work)
	args := editArgs(t, "../outside.txt", []map[string]interface{}{
		{"old_text": "seed", "new_text": "leaked"},
	})

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected ErrPathEscape")
	}
	if !errors.Is(err, tools.ErrPathEscape) {
		t.Fatalf("expected ErrPathEscape, got %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "seed" {
		t.Fatal("outside file should not have been modified")
	}
}
```

### Step 6.11: Add no-op test

- [ ] Append:

```go
func TestEditFileRejectsNoOpEdit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{
		{"old_text": "hello", "new_text": "hello"},
	})

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for no-op edit")
	}
	if !strings.Contains(err.Error(), "equals new_text") {
		t.Fatalf("error = %q, want it to mention no-op", err)
	}
}
```

### Step 6.12: Add empty-edits and malformed-args tests

- [ ] Append:

```go
func TestEditFileEmptyEditsArrayErrors(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(target, []byte("seed"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	args := editArgs(t, "x.txt", []map[string]interface{}{})

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for empty edits")
	}
}

func TestEditFileMalformedArgsErrors(t *testing.T) {
	tool := tools.NewEditFileTool(t.TempDir())
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":}`))
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}
```

### Step 6.13: Add indentation-hallucination integration test

- [ ] Append:

```go
func TestEditFileIndentationHallucinationIntegration(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "server.go")
	original := "" +
		"func main() {\n" +
		"        if x {\n" +
		"                doThing()\n" +
		"        }\n" +
		"}\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := tools.NewEditFileTool(dir)
	// Model produced oldText with no indentation at all.
	args := editArgs(t, "server.go", []map[string]interface{}{
		{
			"old_text": "if x {\n        doThing()\n}",
			"new_text": "if y {\n        otherThing()\n}",
		},
	})

	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "L4=1") {
		t.Fatalf("output = %q, want L4=1", out)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	want := "" +
		"func main() {\n" +
		"        if y {\n" +
		"                otherThing()\n" +
		"        }\n" +
		"}\n"
	if string(got) != want {
		t.Fatalf("file = %q\nwant = %q", got, want)
	}
}
```

### Step 6.14: Run all edit_file tests — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestEditFile -v`
- [ ] Expected: 8 PASS.

### Step 6.15: Run full test suite — verify no regression

- [ ] Run: `go test ./...`
- [ ] Expected: all PASS.

### Step 6.16: Commit

- [ ] Run:

```bash
git add internal/tools/edit_file.go tests/tools/edit_file_test.go
git commit -m "$(cat <<'EOF'
feat(tools): add edit_file tool with multi-edit atomic rollback

Wraps the FuzzyReplace L1->L4 chain in a Tool implementation that:
- accepts an edits array of {old_text, new_text, replace_all?}
- applies edits sequentially against the in-memory result
- rolls back the file on any failure (atomic: read once, edit in
  memory, AtomicWriteFile once at the end)
- refuses non-existent files (use write_file) and no-op edits
- sandboxes paths via ResolveInWorkDir
- returns concrete, self-correction-friendly error messages

Reports per-level match counts in the success message so the model
gets passive feedback when its old_text was loose.
EOF
)"
```

---

## Task 7: Mount in `main.go` + tool definition test

**Files:**
- Modify: `cmd/claw/main.go`
- Modify: `tests/tools/tool_definition_test.go`

### Step 7.1: Add definition test (TDD: it should pass once registered + implemented; we already have the impl, so this test verifies wiring)

- [ ] Append to `tests/tools/tool_definition_test.go`:

```go
func TestEditFileToolNameAndDefinition(t *testing.T) {
	tool := tools.NewEditFileTool(t.TempDir())

	if tool.Name() != "edit_file" {
		t.Fatalf("Name() = %q, want edit_file", tool.Name())
	}

	def := tool.Definition()
	if def.Name != "edit_file" {
		t.Fatalf("Definition().Name = %q, want edit_file", def.Name)
	}
	if def.Description == "" {
		t.Fatal("Definition().Description must not be empty")
	}
	if def.InputSchema == nil {
		t.Fatal("Definition().InputSchema must not be nil")
	}
}
```

### Step 7.2: Run — verify pass

- [ ] Run: `go test ./tests/tools/ -run TestEditFileToolNameAndDefinition -v`
- [ ] Expected: PASS.

### Step 7.3: Register in `main.go`

- [ ] Edit `cmd/claw/main.go`. Find the existing tool-registration block:

```go
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(cwd))
	registry.Register(tools.NewWriteFileTool(cwd))
	registry.Register(tools.NewBashTool(cwd))
```

- [ ] Replace with:

```go
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(cwd))
	registry.Register(tools.NewWriteFileTool(cwd))
	registry.Register(tools.NewEditFileTool(cwd))
	registry.Register(tools.NewBashTool(cwd))
```

### Step 7.4: Build to verify

- [ ] Run: `go build ./...`
- [ ] Expected: no output (success).

### Step 7.5: Smoke-run the binary

- [ ] Run: `LLM_API_KEY=fake go run ./cmd/claw --prompt "noop" 2>&1 | head -5`
- [ ] Expected: a log line `[registry] mounted tool: edit_file` among the four mounted tools (the run will fail later when it tries to call the LLM; that's fine).

### Step 7.6: Commit

- [ ] Run:

```bash
git add cmd/claw/main.go tests/tools/tool_definition_test.go
git commit -m "$(cat <<'EOF'
feat(cli): mount edit_file tool in claw

Registers the new tool alongside read_file, write_file, and bash. Adds
the standard Name+Definition test row mirroring existing tools.
EOF
)"
```

---

## Task 8: README + CHANGELOG

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`

### Step 8.1: Update README Tools table

- [ ] Edit `README.md`. Find the Tools section table:

```markdown
## Tools

| Tool | Description | Sandbox |
|------|-------------|---------|
| `bash` | Run a shell command in the workdir | **Unsandboxed.** Every command is logged. |
| `read_file` | Read a file in the workdir | Path traversal blocked. Optional `offset`/`limit` for line pagination. |
| `write_file` | Write a file in the workdir | Path traversal blocked. Creates parent dirs. |
```

- [ ] Replace with:

```markdown
## Tools

| Tool | Description | Sandbox |
|------|-------------|---------|
| `bash` | Run a shell command in the workdir | **Unsandboxed.** Every command is logged. |
| `read_file` | Read a file in the workdir | Path traversal blocked. Optional `offset`/`limit` for line pagination. |
| `write_file` | Write a file in the workdir | Path traversal blocked. Creates parent dirs. |
| `edit_file` | Apply string replacements to an existing file via fuzzy match (CRLF, whitespace, indentation). Multi-edit atomic; uniqueness enforced. | Path traversal blocked. Refuses non-existent files. |
```

### Step 8.2: Update CHANGELOG

- [ ] Edit `CHANGELOG.md`. Find the line:

```markdown
## [0.1.0.0] - 2026-05-05
```

- [ ] Insert a new section above it:

```markdown
## [Unreleased]

### Added
- `edit_file` tool: multi-edit string replacement with L1→L4 fuzzy match
  chain (exact → CRLF normalization → TrimSpace → line-by-line
  TrimSpace + sliding window with base-indent realignment), atomic
  rollback across an edits array, and atomic file write via temp +
  rename. Refuses non-existent files (use `write_file`) and no-op
  edits. Mounted in `cmd/claw/main.go`.

## [0.1.0.0] - 2026-05-05
```

### Step 8.3: Commit docs

- [ ] Run:

```bash
git add README.md CHANGELOG.md
git commit -m "$(cat <<'EOF'
docs: document edit_file tool in README and CHANGELOG

Adds the tool row to the README Tools table and an [Unreleased]
section to CHANGELOG describing the L1->L4 fuzzy match chain,
atomic rollback, and atomic write semantics.
EOF
)"
```

---

## Task 9: Final verification

**Files:** none modified.

### Step 9.1: Run the full test suite

- [ ] Run: `go test ./...`
- [ ] Expected: all PASS, no skips other than the root-skip in `TestAtomicWriteRollsBackOnFailure`.

### Step 9.2: Run with race detector

- [ ] Run: `go test -race ./...`
- [ ] Expected: all PASS, no data races reported.

### Step 9.3: Run `go vet`

- [ ] Run: `go vet ./...`
- [ ] Expected: no output.

### Step 9.4: Confirm file size budgets

- [ ] Run: `wc -l internal/tools/atomic_write.go internal/tools/fuzzy.go internal/tools/edit_file.go`
- [ ] Expected: each file under 200 lines (target). If any exceeds, factor into helpers.

### Step 9.5: Confirm git log

- [ ] Run: `git log --oneline | head -10`
- [ ] Expected: the recent commits include the spec, atomic_write, fuzzy L1, L2, L3, L4, edit_file, main.go registration, and docs commits in order.

### Step 9.6: Manual end-to-end smoke test (optional, requires API key)

- [ ] Set `LLM_API_KEY` in a real `.env`.
- [ ] Create a scratch file: `echo 'package x; func Foo() int { return 1 }' > /tmp/scratch.go`.
- [ ] Set workdir: `go run ./cmd/claw --workdir /tmp --prompt "use edit_file to change return 1 to return 2 in scratch.go"`.
- [ ] Verify: `cat /tmp/scratch.go` shows `return 2`. Tool log should report `L1=1` (exact match) for a clean prompt.

---

## Self-Review

After completing this plan, the following spec sections are covered:

- **D1 Edit semantics** → Tasks 2–5 (FuzzyReplace L1–L4)
- **D2 Tool surface** → Task 6 (always-array `edits`)
- **D3 Argument naming** → Task 6 (`old_text`/`new_text`)
- **D4 Multi-edit atomicity** → Task 6 (in-memory accumulation, single write at end, Step 6.7 rollback test)
- **D5 Disk write** → Task 1 (AtomicWriteFile)
- **D6 Uniqueness override** → Tasks 2 (L1 multi-match) and 5 (L4 replaceAll)
- **D7 Success output format** → Step 6.3 (`fmt.Sprintf("edited %q (%d edits applied: L1=%d L2=%d L3=%d L4=%d)", ...)`)
- **D8 File organization** → Tasks 1, 2–5, 6 (three separate files)
- **Spec Unit 5 error messages** → Step 6.3 `formatEditError` + the inline error wrapping in `Execute`
- **Spec Unit 6 main.go** → Task 7
- **Spec Unit 7 README+CHANGELOG** → Task 8
- **Spec Test Strategy** → Tasks 1, 2–5, 6 cover every row

No placeholders remain. All file paths are explicit. Every code step shows the actual code. Every test step shows the expected output. Function names are consistent across tasks (`FuzzyReplace`, `AtomicWriteFile`, `NewEditFileTool`, `extractBasePrefix`, `reindent`).
