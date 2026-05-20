package compact_test

import (
	"encoding/json"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
)

func TestCompactStatsJSONRoundTrip(t *testing.T) {
	in := compact.CompactStats{
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
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out compact.CompactStats
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round trip mismatch:\nin =%+v\nout=%+v", in, out)
	}
}

func TestNewCompactStatsDefaults(t *testing.T) {
	s := compact.NewCompactStats(3, 1000)
	if s.Turn != 3 {
		t.Fatalf("turn = %d, want 3", s.Turn)
	}
	if s.Before != 1000 {
		t.Fatalf("before = %d, want 1000", s.Before)
	}
	if s.AfterLayerA != 1000 || s.AfterLayerB != 1000 {
		t.Fatalf("after = (%d, %d), want both 1000 (default = no-op)", s.AfterLayerA, s.AfterLayerB)
	}
}
