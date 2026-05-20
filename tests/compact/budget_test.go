package compact_test

import (
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/provider"
)

func TestBudgetKnownModelSubtractsOutputCap(t *testing.T) {
	got := compact.Budget(compact.BudgetInput{
		Model:        "claude-opus-4-7",
		OutputCap:    4096,
		SafetyFactor: 0.75,
	})
	// 200000*0.75 - 4096 = 150000 - 4096 = 145904
	if got != 145_904 {
		t.Fatalf("budget = %d, want 145904", got)
	}
}

func TestBudgetUnknownModelUsesFallback(t *testing.T) {
	got := compact.Budget(compact.BudgetInput{
		Model:        "wat",
		OutputCap:    1000,
		SafetyFactor: 0.75,
	})
	// 32000*0.75 - 1000 = 24000 - 1000 = 23000
	if got != 23_000 {
		t.Fatalf("budget = %d, want 23000", got)
	}
}

func TestBudgetOverrideMapUsed(t *testing.T) {
	got := compact.Budget(compact.BudgetInput{
		Model:        "tiny",
		OutputCap:    0,
		SafetyFactor: 1.0,
		Overrides:    map[string]int{"tiny": 1000},
	})
	if got != 1000 {
		t.Fatalf("budget = %d, want 1000", got)
	}
}

func TestBudgetUsageIgnoredForReservation(t *testing.T) {
	// LastUsage is informational only; the budget reserves OutputCap
	// for the worst case we'll request next turn.
	a := compact.Budget(compact.BudgetInput{
		Model:        "claude-opus-4-7",
		OutputCap:    4096,
		SafetyFactor: 0.75,
		LastUsage:    provider.Usage{InputTokens: 50_000, OutputTokens: 2000},
	})
	b := compact.Budget(compact.BudgetInput{
		Model:        "claude-opus-4-7",
		OutputCap:    4096,
		SafetyFactor: 0.75,
		LastUsage:    provider.Usage{},
	})
	if a != b {
		t.Fatalf("LastUsage changed budget: %d vs %d", a, b)
	}
}

func TestBudgetClampsNegativeToZero(t *testing.T) {
	// Hostile inputs: tiny model, huge output cap -> negative naive result.
	got := compact.Budget(compact.BudgetInput{
		Model:        "wat",
		OutputCap:    1_000_000,
		SafetyFactor: 0.75,
	})
	if got != 0 {
		t.Fatalf("budget = %d, want clamp-to-zero", got)
	}
}
