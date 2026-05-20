package compact

// Calibrator tracks the EWMA-smoothed ratio between the provider's
// reported InputTokens and our local chars/4 estimate. The ratio
// resets to 1.0 every session (the spec is explicit that cross-session
// learning is a non-goal) and converges in 2-3 turns under steady
// observation. NOT goroutine-safe — call from a single goroutine
// (the engine main loop in practice).
type Calibrator struct {
	ratio float64
	alpha float64
}

// NewCalibrator returns a calibrator with the supplied EWMA weight.
// alpha=0 (or negative) falls back to 0.3, which gives ~95% influence
// after ~10 observations — fast enough to track a real tokenizer,
// slow enough to ignore a single outlier turn.
func NewCalibrator(alpha float64) *Calibrator {
	if alpha <= 0 {
		alpha = 0.3
	}
	return &Calibrator{ratio: 1.0, alpha: alpha}
}

// Ratio returns the current EWMA-smoothed multiplier.
func (c *Calibrator) Ratio() float64 { return c.ratio }

// Alpha returns the EWMA weight; exposed for tests / debugging.
func (c *Calibrator) Alpha() float64 { return c.alpha }

// Observe folds a single (localEstimate, providerActual) sample into
// the running ratio. Zeros on either side are ignored — they mean
// "no signal this turn" rather than "the real ratio is zero".
func (c *Calibrator) Observe(localEst, providerActual int) {
	if localEst <= 0 || providerActual <= 0 {
		return
	}
	sample := float64(providerActual) / float64(localEst)
	c.ratio = c.alpha*sample + (1-c.alpha)*c.ratio
}

// Predict converts a local estimate into a predicted provider count.
// Rounded to nearest int.
func (c *Calibrator) Predict(localEst int) int {
	return int(float64(localEst)*c.ratio + 0.5)
}
