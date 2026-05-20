package compact

import (
	"github.com/snowshine0216/penelope-agent/internal/provider"
)

// BudgetInput captures the inputs to Budget. LastUsage is informational
// only — the budget reserves OutputCap for the worst case we may
// request next turn rather than the last actual output count.
type BudgetInput struct {
	Model        string
	LastUsage    provider.Usage // zero on first turn
	OutputCap    int            // == --max-tokens (default 4096 for Claude)
	SafetyFactor float64        // default 0.75
	Overrides    map[string]int // optional model -> limit override
}

// Budget returns the input-token ceiling the compactor must keep below.
// The provider hard-fails if `input + output` exceeds the window so we
// subtract OutputCap (worst-case output we will request) rather than
// LastUsage.OutputTokens (what we got back last turn).
//
// Negative results are clamped to zero so a hostile flag combination
// (huge output cap on a tiny model) yields a documented "everything
// gets compacted" rather than a Go-side negative-int landmine.
func Budget(in BudgetInput) int {
	limit, ok := LookupModelLimit(in.Model, in.Overrides)
	if !ok {
		limit = FallbackContextLimit
	}
	safety := in.SafetyFactor
	if safety <= 0 {
		safety = 0.75
	}
	v := int(float64(limit)*safety) - in.OutputCap
	if v < 0 {
		return 0
	}
	return v
}
