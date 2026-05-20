package compact

// CompactStats captures what one compaction did for a single turn.
// Serialised verbatim to .claw/sessions/<id>/compact-events.jsonl when
// emission fires. Field names use JSON-friendly snake_case so the
// audit log is grep-friendly without a tags table.
type CompactStats struct {
	Turn               int     `json:"turn"`
	Before             int     `json:"before"`
	AfterLayerA        int     `json:"after_layer_a"`
	AfterLayerB        int     `json:"after_layer_b"`
	Budget             int     `json:"budget"`
	Saved              int     `json:"saved"`
	LayerBEngaged      bool    `json:"layer_b_engaged"`
	TurnsFolded        int     `json:"turns_folded"`
	ToolOutputsSpilled int     `json:"tool_outputs_spilled"`
	CalibratorRatio    float64 `json:"calibrator_ratio"`
}

// NewCompactStats returns a stats baseline that represents "no-op
// compaction at this token count". Compactor.View populates the rest
// as Layer A and Layer B run.
func NewCompactStats(turn, before int) CompactStats {
	return CompactStats{
		Turn:        turn,
		Before:      before,
		AfterLayerA: before,
		AfterLayerB: before,
	}
}
