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
