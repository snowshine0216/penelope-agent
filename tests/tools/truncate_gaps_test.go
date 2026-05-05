package tools_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestTruncateNegativeLimit(t *testing.T) {
	// Negative maxBytes triggers the "maxBytes <= 0" branch.
	got := tools.TruncateForLLM("abc", -1)
	if !strings.Contains(got, "elided") {
		t.Fatalf("expected elision marker for negative maxBytes, got: %q", got)
	}
}

func TestTruncateSingleByteString(t *testing.T) {
	// maxBytes == 1 forces head and tail to each be at most 0/1 byte,
	// exercising the tailStart <= headEnd guard.
	got := tools.TruncateForLLM("abcdefghij", 1)
	if !strings.Contains(got, "elided") {
		t.Fatalf("expected elision marker, got: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("result is not valid UTF-8: %q", got)
	}
}

func TestTruncateWithOddMaxBytes(t *testing.T) {
	// Odd maxBytes (e.g. 5): half = 2; exercises rounding in head/tail split.
	s := strings.Repeat("x", 100)
	got := tools.TruncateForLLM(s, 5)
	if !strings.Contains(got, "elided") {
		t.Fatalf("expected elision marker for odd maxBytes, got: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("result is not valid UTF-8: %q", got)
	}
}

func TestSafeRuneBoundaryAtStringEnd(t *testing.T) {
	// When the input equals maxBytes TruncateForLLM returns it unchanged;
	// this indirectly exercises the "max >= len(s)" guard in safeRuneBoundaryDown.
	s := "hello"
	got := tools.TruncateForLLM(s, len(s))
	if got != s {
		t.Fatalf("expected unchanged string at exact limit, got: %q", got)
	}
}

func TestSafeRuneBoundaryUpAtZero(t *testing.T) {
	// A string that needs truncating with half == 0 forces safeRuneBoundaryUp(s, 0)
	// returning 0, and safeRuneBoundaryDown similarly to 0, so tailStart==headEnd.
	s := strings.Repeat("a", 10)
	got := tools.TruncateForLLM(s, 0)
	if !strings.Contains(got, "elided") {
		t.Fatalf("expected elision marker for maxBytes=0, got: %q", got)
	}
}
