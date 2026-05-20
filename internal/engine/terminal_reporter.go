package engine

import (
	"context"
	"fmt"
	"os"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
)

// TerminalReporter writes agent events to stdout in a human-readable format.
type TerminalReporter struct {
	sess *agentsession.Session
}

// NewTerminalReporter returns a Reporter that prints events to stdout.
func NewTerminalReporter() *TerminalReporter { return &TerminalReporter{} }

// AttachSession lets the engine give the reporter a session handle so
// OnCompact can persist to the audit log. Optional — the reporter
// degrades to stderr-only when nil.
func (r *TerminalReporter) AttachSession(s *agentsession.Session) { r.sess = s }

func (r *TerminalReporter) OnThinking(_ context.Context) {
	fmt.Println("[thinking]")
}

func (r *TerminalReporter) OnToolCall(_ context.Context, toolName string, args string) {
	fmt.Printf("[tool] %s args=%s\n", toolName, args)
}

func (r *TerminalReporter) OnToolResult(_ context.Context, toolName string, result string, isError bool) {
	if isError {
		fmt.Printf("[tool:error] %s result=%s\n", toolName, result)
		return
	}
	fmt.Printf("[tool:ok] %s result=%s\n", toolName, result)
}

func (r *TerminalReporter) OnMessage(_ context.Context, content string) {
	fmt.Println(content)
}

// OnCompact prints a [compact] line to stderr and appends to the audit log.
func (r *TerminalReporter) OnCompact(_ context.Context, s compact.CompactStats) {
	line := formatCompactLine(s)
	fmt.Fprintln(os.Stderr, line)
	if r.sess != nil {
		_ = r.sess.AppendCompactEvent(s) // audit is best-effort
	}
}

// formatCompactLine implements spec §4. Three shapes:
//   - Short: turn N: B → A tokens (saved X) | <extras>
//   - Layer B: turn N: B → A tokens (saved X, P%) | budget Y | folded turns 1..F into digest | <spill>
//   - Calibrator warming: turn N: A → A tokens (saved 0) | calibrator ratio R (warming)
func formatCompactLine(s compact.CompactStats) string {
	if s.LayerBEngaged {
		pct := 0.0
		if s.Before > 0 {
			pct = float64(s.Saved) / float64(s.Before) * 100
		}
		extras := ""
		if s.ToolOutputsSpilled > 0 {
			extras = fmt.Sprintf(" | %d tool output%s spilled", s.ToolOutputsSpilled, plural(s.ToolOutputsSpilled))
		}
		return fmt.Sprintf("[compact] turn %d: %d → %d tokens (saved %d, %.1f%%) | budget %d | folded turns 1..%d into digest%s",
			s.Turn, s.Before, s.AfterLayerB, s.Saved, pct, s.Budget, s.TurnsFolded, extras)
	}
	extras := ""
	if s.ToolOutputsSpilled > 0 {
		extras = fmt.Sprintf(" | %d tool output%s spilled", s.ToolOutputsSpilled, plural(s.ToolOutputsSpilled))
	}
	return fmt.Sprintf("[compact] turn %d: %d → %d tokens (saved %d)%s",
		s.Turn, s.Before, s.AfterLayerB, s.Saved, extras)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
