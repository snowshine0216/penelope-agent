package compact

import (
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// Config carries the user-tunable knobs into the compactor. Pure
// data — no I/O handles here.
type Config struct {
	MaxToolBytes        int // tool-result truncation threshold (default 65536)
	RecentTurnsVerbatim int // last N user turns skip Layer A arg stripping (default 4)
	Overrides           map[string]int // optional model -> limit override map
}

// Compactor produces a read-time provider view from the canonical
// session history. View is the only public method.
type Compactor struct {
	cfg Config
}

// NewCompactor returns a Compactor with the given configuration.
// Defaults are filled in for any zero-valued field.
func NewCompactor(cfg Config) *Compactor {
	if cfg.MaxToolBytes <= 0 {
		cfg.MaxToolBytes = 65536
	}
	if cfg.RecentTurnsVerbatim <= 0 {
		cfg.RecentTurnsVerbatim = 4
	}
	return &Compactor{cfg: cfg}
}

// Config returns the active configuration (read-only; safe to share).
func (c *Compactor) Config() Config { return c.cfg }

// View runs the full pipeline:
//   - Layer A structural shrink (always).
//   - Budget check via calibrator.Predict.
//   - Layer B rolling digest if Layer A was not enough.
//   - Emergency floor: drop everything except the verbatim tail (and
//     synthesise a tail from the last user message if even that is gone)
//     when the digest + tail still exceeds budget.
//
// Pure: input history is never mutated; calibrator is consulted via
// Predict only — Observe is the caller's job after provider.Generate.
func (c *Compactor) View(history []schema.Message, budget, turn int, cal *Calibrator) ([]schema.Message, CompactStats) {
	cleaned := DefensiveCleanup(history)
	before := EstimateTokens(cleaned)
	stats := NewCompactStats(turn, before)
	if cal != nil {
		stats.CalibratorRatio = cal.Ratio()
	}

	// Layer A.
	shrunk, _ := ShrinkApply(cleaned, ShrinkConfig{
		MaxToolBytes:        c.cfg.MaxToolBytes,
		RecentTurnsVerbatim: c.cfg.RecentTurnsVerbatim,
	})
	stats.AfterLayerA = EstimateTokens(shrunk)

	if predict(cal, stats.AfterLayerA) <= budget {
		stats.AfterLayerB = stats.AfterLayerA
		stats.Saved = stats.Before - stats.AfterLayerB
		return shrunk, stats
	}

	// Layer B.
	folded, foldedTurns := Fold(shrunk, budget, c.cfg.RecentTurnsVerbatim, cal)
	stats.AfterLayerB = EstimateTokens(folded)
	stats.LayerBEngaged = foldedTurns > 0
	stats.TurnsFolded = foldedTurns
	stats.Saved = stats.Before - stats.AfterLayerB

	if predict(cal, stats.AfterLayerB) <= budget {
		return folded, stats
	}

	// Emergency floor: halve MaxToolBytes for the verbatim tail and
	// retry once with the same fold target. If still over budget, send
	// what we have — the provider will surface a clean 4xx.
	tightened := *c
	tightened.cfg.MaxToolBytes = c.cfg.MaxToolBytes / 2
	if tightened.cfg.MaxToolBytes < 1024 {
		tightened.cfg.MaxToolBytes = 1024
	}
	shrunk2, _ := ShrinkApply(cleaned, ShrinkConfig{
		MaxToolBytes:        tightened.cfg.MaxToolBytes,
		RecentTurnsVerbatim: c.cfg.RecentTurnsVerbatim,
	})
	folded2, foldedTurns2 := Fold(shrunk2, budget, c.cfg.RecentTurnsVerbatim, cal)
	stats.AfterLayerB = EstimateTokens(folded2)
	stats.LayerBEngaged = foldedTurns2 > 0 || stats.LayerBEngaged
	if foldedTurns2 > stats.TurnsFolded {
		stats.TurnsFolded = foldedTurns2
	}
	stats.Saved = stats.Before - stats.AfterLayerB

	if len(folded2) == 0 {
		// Last-ditch fallback: synthesise the last user message so the
		// model still receives a valid prompt (mirrors the
		// engine.providerView emergency floor in the old code path).
		if last, ok := lastUserMessageCompactor(cleaned); ok {
			return []schema.Message{last}, stats
		}
	}
	return folded2, stats
}

func lastUserMessageCompactor(msgs []schema.Message) (schema.Message, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == schema.RoleUser {
			return msgs[i], true
		}
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1], true
	}
	return schema.Message{}, false
}
