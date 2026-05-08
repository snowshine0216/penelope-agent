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
	if level != 0 {
		t.Fatalf("level = %d, want 0 on miss", level)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q", err)
	}
}
