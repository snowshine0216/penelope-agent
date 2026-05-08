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

func TestFuzzyReplaceL2AmbiguousErrors(t *testing.T) {
	// CRLF content where normalization produces a multi-match for old_text.
	content := "foo\r\nbar\r\nfoo\r\nbar\r\n"
	// oldText with LF only: after CRLF→LF normalization, matches twice.
	_, _, err := tools.FuzzyReplace(content, "foo\nbar", "baz", false)
	if err == nil {
		t.Fatal("expected error for L2 multi-match, got nil")
	}
	if !strings.Contains(err.Error(), "matched") {
		t.Fatalf("error = %q, want it to mention match count", err)
	}
}

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

func TestFuzzyReplaceL3AmbiguityErrors(t *testing.T) {
	content := "foo bar\nfoo bar\n"
	oldText := "  foo bar  "
	_, _, err := tools.FuzzyReplace(content, oldText, "X", false)
	if err == nil {
		t.Fatal("expected error for L3 ambiguity")
	}
}

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

	want := "func main() {\n        if y {\n                otherThing()\n        }\n}\n"
	if out != want {
		t.Fatalf("out = %q\nwant = %q", out, want)
	}
}

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

func TestFuzzyReplaceL4PreservesRelativeIndentInNewText(t *testing.T) {
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

func TestFuzzyReplaceAllLevelsMiss(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	_, level, err := tools.FuzzyReplace(content, "delta", "epsilon", false)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if level != -1 {
		t.Fatalf("level = %d, want -1 on miss", level)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q", err)
	}
}

// Gap coverage: L2 + replaceAll=true
func TestFuzzyReplaceL2ReplaceAll(t *testing.T) {
	// CRLF content with two occurrences; LF-only oldText misses L1.
	// L2 normalises CRLF→LF then replaceAll replaces both.
	content := "foo\r\nbar\r\nfoo\r\nbar\r\n"
	out, level, err := tools.FuzzyReplace(content, "foo\nbar", "X", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 2 {
		t.Fatalf("level = %d, want 2", level)
	}
	if out != "X\nX\n" {
		t.Fatalf("out = %q, want X\\nX\\n", out)
	}
}

// Gap coverage: L3 + replaceAll=true
func TestFuzzyReplaceL3ReplaceAll(t *testing.T) {
	// Two occurrences; oldText is a multi-line snippet with outer blank lines.
	// L3 strips the blank lines and matches. L1 misses because oldText has the
	// extra trailing blank line that the content doesn't have.
	// L3 only activates when normOld contains a newline (to avoid substring
	// matches inside adjacent tokens for single-line snippets).
	content := "foo bar\nfoo bar\n"
	out, level, err := tools.FuzzyReplace(content, "foo bar\n\n", "baz", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 3 {
		t.Fatalf("level = %d, want 3", level)
	}
	if out != "baz\nbaz\n" {
		t.Fatalf("out = %q, want baz\\nbaz\\n", out)
	}
}

// Gap coverage: L3 guard skips all-whitespace old_text (trimmedOld == "")
func TestFuzzyReplaceL3SkipsAllWhitespaceOldText(t *testing.T) {
	// All-whitespace oldText: the L3 guard (trimmedOld != "") prevents L3
	// from running. Content has no blank/empty lines so L4 also misses.
	_, _, err := tools.FuzzyReplace("hello world", "   \t  ", "x", false)
	if err == nil {
		t.Fatal("expected not-found error for all-whitespace old_text")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q, want 'not found'", err)
	}
}

// Gap coverage: lineByLineReplace early-return when oldLines > contentLines
func TestFuzzyReplaceL4OldLongerThanContent(t *testing.T) {
	// A 3-line old_text cannot match a 2-line file at any level.
	content := "one\ntwo\n"
	oldText := "one\ntwo\nthree"
	_, _, err := tools.FuzzyReplace(content, oldText, "x", false)
	if err == nil {
		t.Fatal("expected not-found error when oldText is longer than content")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q, want 'not found'", err)
	}
}

// Gap coverage: extractBasePrefix called on a purely-whitespace first window line
func TestFuzzyReplaceL4FirstWindowLineAllWhitespace(t *testing.T) {
	// The matched window starts at a pure-whitespace line. extractBasePrefix
	// returns the full whitespace string as the base prefix.
	// L1/L2/L3 all miss; L4 matches via TrimSpace comparison.
	content := "   \n   foo\n   bar\n"
	oldText := "  \n  foo\n  bar" // different leading whitespace than content
	newText := "  \n  replaced"
	out, level, err := tools.FuzzyReplace(content, oldText, newText, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 4 {
		t.Fatalf("level = %d, want 4", level)
	}
	// The base prefix comes from the all-whitespace first line "   " → "   ".
	// Non-empty replacement lines get that prefix; empty lines stay empty.
	want := "   \n   replaced\n"
	if out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

// Gap coverage: commonLeadingWhitespace with only empty lines (newText all-empty)
func TestFuzzyReplaceL4NewTextAllEmptyLines(t *testing.T) {
	// newText of "\n" splits to ["", ""] — only empty lines.
	// commonLeadingWhitespace returns "" (no non-empty lines), so reindent
	// leaves them as empty lines.
	content := "    foo\n    bar\nend\n"
	oldText := "foo\nbar" // matches via L4 at the indented window
	newText := "\n"       // two empty lines
	out, level, err := tools.FuzzyReplace(content, oldText, newText, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 4 {
		t.Fatalf("level = %d, want 4", level)
	}
	// The 2-line window is replaced by 2 empty lines; "end" and trailing
	// newline remain.
	want := "\n\nend\n"
	if out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}
