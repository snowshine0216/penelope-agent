package session_test

import (
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/session"
)

// noopTrimmer is the minimum implementation of the Trimmer interface
// used to confirm the registry plumbing works end to end.
type noopTrimmer struct{}

func (noopTrimmer) Trim(msgs []schema.Message) []schema.Message { return msgs }
func (noopTrimmer) Name() string                                { return "noop" }

func TestRegisterAndGetTrimmer(t *testing.T) {
	session.Register("noop-test", func(session.TrimConfig) session.Trimmer { return noopTrimmer{} })
	tr, err := session.Get("noop-test", session.TrimConfig{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if tr.Name() != "noop" {
		t.Fatalf("name = %q", tr.Name())
	}
}

func TestGetUnknownStrategyListsRegisteredNames(t *testing.T) {
	_, err := session.Get("does-not-exist", session.TrimConfig{})
	if err == nil {
		t.Fatal("expected error for unknown strategy")
	}
}
