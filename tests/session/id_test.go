package session_test

import (
	"regexp"
	"testing"
	"time"

	"github.com/snowshine0216/penelope-agent/internal/session"
)

var idRegex = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{6}$`)

func TestNewIDMatchesExpectedFormat(t *testing.T) {
	id := session.NewID(time.Date(2026, 5, 18, 9, 30, 45, 0, time.UTC))
	if !idRegex.MatchString(id) {
		t.Fatalf("id = %q, want match %s", id, idRegex)
	}
	if id[:15] != "20260518-093045" {
		t.Fatalf("id prefix = %q, want 20260518-093045", id[:15])
	}
}

func TestNewIDProducesDistinctSuffixes(t *testing.T) {
	now := time.Date(2026, 5, 18, 9, 30, 45, 0, time.UTC)
	a := session.NewID(now)
	b := session.NewID(now)
	if a == b {
		t.Fatalf("two consecutive ids identical: %q", a)
	}
}

func TestIsValidIDAcceptsCanonicalFormat(t *testing.T) {
	if !session.IsValidID("20260518-093045-a1b2c3") {
		t.Fatal("canonical id was rejected")
	}
}

func TestIsValidIDRejectsBadInput(t *testing.T) {
	cases := []string{
		"",
		"not-a-session-id",
		"20260518_093045_a1b2c3",       // wrong separator
		"20260518-093045-ABCDEF",       // uppercase hex
		"20260518-093045-a1b2c3-extra", // trailing
		"20260518-093045-a1b2c",        // short suffix
		"../etc/passwd",                // path traversal
		"20260518-093045-a1b2c3/extra",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			if session.IsValidID(input) {
				t.Fatalf("invalid id %q was accepted", input)
			}
		})
	}
}
