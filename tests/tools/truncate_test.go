package tools_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestTruncateShorterThanLimitReturnsAsIs(t *testing.T) {
	got := tools.TruncateForLLM("hello", 100)
	if got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestTruncateExactlyAtLimitReturnsAsIs(t *testing.T) {
	s := strings.Repeat("a", 100)
	got := tools.TruncateForLLM(s, 100)
	if got != s {
		t.Fatalf("expected unchanged at exact limit, got len %d", len(got))
	}
}

func TestTruncateLongInputProducesHeadAndTail(t *testing.T) {
	s := strings.Repeat("a", 1000) + strings.Repeat("z", 1000)
	got := tools.TruncateForLLM(s, 200)
	if len(got) > 400 {
		t.Fatalf("output too long: %d", len(got))
	}
	if !strings.Contains(got, "elided") {
		t.Fatalf("expected elision marker, got: %q", got)
	}
	if !strings.HasPrefix(got, "a") {
		t.Fatalf("expected head to be all 'a's, got prefix %q", got[:5])
	}
	if !strings.HasSuffix(got, "z") {
		t.Fatalf("expected tail to be all 'z's, got suffix %q", got[len(got)-5:])
	}
}

func TestTruncateProducesValidUTF8AtBoundary(t *testing.T) {
	s := strings.Repeat("中", 1000)
	got := tools.TruncateForLLM(s, 100)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated output is not valid UTF-8: %q", got)
	}
}

func TestTruncateZeroLimitReturnsMarkerOnly(t *testing.T) {
	got := tools.TruncateForLLM("abc", 0)
	if !strings.Contains(got, "elided") {
		t.Fatalf("expected elision marker on zero-budget input, got: %q", got)
	}
}
