package compact_test

import (
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

func TestCompactorViewUnderBudgetIsNoOp(t *testing.T) {
	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        65536,
		RecentTurnsVerbatim: 4,
	})
	in := []schema.Message{user("hi"), asst("hello")}
	view, stats := c.View(in, 100_000, 1 /* turn */, compact.NewCalibrator(0.3))
	if len(view) != len(in) {
		t.Fatalf("view changed under budget: %d vs %d", len(view), len(in))
	}
	if stats.LayerBEngaged {
		t.Fatalf("Layer B engaged under budget")
	}
	if stats.Before == 0 {
		t.Fatalf("stats.Before unset")
	}
}

func TestCompactorViewLayerASufficient(t *testing.T) {
	// A huge tool result fits the budget after Layer A truncates it.
	huge := strings.Repeat("x", 200_000)
	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        1000,
		RecentTurnsVerbatim: 4,
	})
	in := []schema.Message{
		user("u"),
		asst("", tc("a", "bash")),
		toolMsg("a", huge),
	}
	view, stats := c.View(in, 10_000, 1, compact.NewCalibrator(0.3))
	if stats.LayerBEngaged {
		t.Fatalf("Layer B engaged when A was sufficient: %+v", stats)
	}
	if stats.AfterLayerA >= stats.Before {
		t.Fatalf("Layer A did not shrink: before=%d after=%d", stats.Before, stats.AfterLayerA)
	}
	if len(view[2].Content) >= len(huge) {
		t.Fatalf("tool result not truncated")
	}
}

func TestCompactorViewLayerBEngaged(t *testing.T) {
	// Many turns, none too large individually, but the sum exceeds budget.
	in := []schema.Message{}
	for i := range 20 {
		in = append(in, user("u"+string(rune('0'+i))), asst("a"+string(rune('0'+i))))
	}
	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        65536,
		RecentTurnsVerbatim: 2,
	})
	view, stats := c.View(in, 50 /* very tight */, 5, compact.NewCalibrator(0.3))
	if !stats.LayerBEngaged {
		t.Fatalf("Layer B not engaged: %+v", stats)
	}
	if stats.TurnsFolded < 1 {
		t.Fatalf("no turns folded: %+v", stats)
	}
	// View[0] should now be the digest (after engine adds system at 0;
	// here we get view without system).
	if view[0].Role != schema.RoleAssistant || !strings.Contains(view[0].Content, "Prior session digest") {
		t.Fatalf("digest missing from view: %+v", view[0])
	}
}

func TestCompactorViewEmergencyFloorOverBudget(t *testing.T) {
	// Tight budget that even digest+verbatim cannot meet.
	huge := strings.Repeat("x", 1_000_000)
	in := []schema.Message{
		user("u1"), asst("a1"),
		user("u2"),
		asst("", tc("a", "bash")),
		toolMsg("a", huge),
	}
	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        100_000,
		RecentTurnsVerbatim: 1,
	})
	view, stats := c.View(in, 100 /* impossible */, 1, compact.NewCalibrator(0.3))
	// Emergency floor: at minimum the last user message is present.
	found := false
	for _, m := range view {
		if m.Role == schema.RoleUser && m.Content == "u2" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("emergency floor lost last user message: %+v", view)
	}
	// Stats still populated.
	if stats.Before == 0 {
		t.Fatalf("stats.Before unset: %+v", stats)
	}
}

func TestCompactorViewIsPure(t *testing.T) {
	in := []schema.Message{user("u"), asst("a")}
	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        65536,
		RecentTurnsVerbatim: 4,
	})
	_, _ = c.View(in, 100_000, 1, compact.NewCalibrator(0.3))
	if in[0].Content != "u" || in[1].Content != "a" {
		t.Fatalf("View mutated input: %+v", in)
	}
}
