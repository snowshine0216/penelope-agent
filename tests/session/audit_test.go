package session_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/session"
)

func TestAppendCompactEventRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer sess.Close()

	stats := compact.CompactStats{
		Turn:               7,
		Before:             48_210,
		AfterLayerA:        48_000,
		AfterLayerB:        47_920,
		Budget:             100_000,
		Saved:              290,
		LayerBEngaged:      true,
		TurnsFolded:        3,
		ToolOutputsSpilled: 2,
		CalibratorRatio:    1.07,
	}
	if err := sess.AppendCompactEvent(stats); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := sess.AppendCompactEvent(stats); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	path := filepath.Join(dir, sess.ID()+"-compact-events.jsonl")
	if _, err := os.Stat(path); err != nil {
		// alt layout
		path = filepath.Join(dir, sess.ID(), "compact-events.jsonl")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("audit log missing at either location: %v", err)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	lines := 0
	for scanner.Scan() {
		lines++
		var got compact.CompactStats
		if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal audit line: %v", err)
		}
		if got.Turn != 7 {
			t.Fatalf("audit turn = %d, want 7", got.Turn)
		}
	}
	if lines != 2 {
		t.Fatalf("audit lines = %d, want 2", lines)
	}
}
