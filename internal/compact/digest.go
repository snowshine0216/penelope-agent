package compact

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// digestTextLimit caps per-turn text bodies in the digest. The model
// only needs the gist; a 120-char head + ellipsis preserves enough to
// recall what happened without bloating the digest.
const digestTextLimit = 120

// callIDRe matches the call_id=... marker that Layer A embeds in
// truncated tool results. The digest re-extracts it so the model can
// see the call_id again at-a-glance.
var callIDRe = regexp.MustCompile(`call_id=([A-Za-z0-9_\-]+)`)

// Fold runs Layer B. It folds the oldest non-verbatim turns into a
// single synthetic assistant message inserted at position 1 (after the
// system message, which lives at index 0 when the engine assembles the
// final view; from this function's perspective the slice has no system
// message and the digest is inserted at index 0). Returns the new
// slice and the number of turns folded.
//
// recentTurnsVerbatim turns at the tail are preserved unchanged. The
// digest grows backward — oldest first — until the result fits the
// budget. If even (digest + verbatim tail) exceeds the budget the
// caller (Compactor.View) is responsible for the emergency floor.
//
// Pure: input is never mutated. calibrator may be nil; if provided it
// converts local estimates to predicted provider counts before the
// budget check, giving the fold loop a tighter target.
func Fold(viewA []schema.Message, budget int, recentTurnsVerbatim int, calibrator *Calibrator) ([]schema.Message, int) {
	if len(viewA) == 0 {
		return nil, 0
	}
	if predict(calibrator, EstimateTokens(viewA)) <= budget {
		return CloneMessages(viewA), 0
	}

	// Idempotency: if the view already starts with a digest message,
	// preserve it — never re-fold a digest into a new digest.
	if len(viewA) > 0 && isDigestMessage(viewA[0]) {
		return CloneMessages(viewA), 0
	}

	turns := splitIntoTurns(viewA)
	if len(turns) == 0 {
		return CloneMessages(viewA), 0
	}

	// Determine where the verbatim window begins (by turn index).
	verbatimStart := len(turns) - recentTurnsVerbatim
	if verbatimStart < 0 {
		verbatimStart = 0
	}

	// Grow the fold window backward: fold turns [0..foldEnd).
	for foldEnd := 1; foldEnd <= verbatimStart; foldEnd++ {
		folded := assembleFolded(turns, foldEnd, verbatimStart)
		if predict(calibrator, EstimateTokens(folded)) <= budget {
			return folded, foldEnd
		}
	}
	// Could not fit even after folding every non-verbatim turn.
	// Return the maximally-folded view; emergency floor is the
	// Compactor's job, not the digest's.
	if verbatimStart > 0 {
		return assembleFolded(turns, verbatimStart, verbatimStart), verbatimStart
	}
	return CloneMessages(viewA), 0
}

// isDigestMessage returns true when the message is a synthetic assistant
// digest produced by a prior Fold call.
func isDigestMessage(m schema.Message) bool {
	return m.Role == schema.RoleAssistant && strings.HasPrefix(m.Content, "## Prior session digest")
}

// assembleFolded builds a view = [digest, ...turns[verbatimStart:]].
// turns[0..foldEnd) become the digest body; turns[foldEnd..verbatimStart)
// are dropped (they would have been folded but the caller chose a
// smaller window); turns[verbatimStart:] are preserved verbatim.
func assembleFolded(turns [][]schema.Message, foldEnd, verbatimStart int) []schema.Message {
	digest := buildDigest(turns[:foldEnd])
	out := []schema.Message{{Role: schema.RoleAssistant, Content: digest}}
	for _, t := range turns[verbatimStart:] {
		out = append(out, t...)
	}
	return out
}

// splitIntoTurns groups messages into turn boundaries. A turn starts
// at each user message and includes every following non-user message
// until the next user message. The leading slice before the first
// user message (system-pre-pended views never hit this path, but
// belt-and-suspenders) becomes turn 0.
func splitIntoTurns(msgs []schema.Message) [][]schema.Message {
	var turns [][]schema.Message
	var cur []schema.Message
	for _, m := range msgs {
		if m.Role == schema.RoleUser && len(cur) > 0 {
			turns = append(turns, cur)
			cur = nil
		}
		cur = append(cur, m)
	}
	if len(cur) > 0 {
		turns = append(turns, cur)
	}
	return turns
}

// buildDigest renders the spec §1 Layer B example format.
func buildDigest(turns [][]schema.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Prior session digest (turns 1..%d, compacted)\n\n", len(turns))
	for i, t := range turns {
		turnNum := i + 1
		for _, m := range t {
			switch m.Role {
			case schema.RoleUser:
				fmt.Fprintf(&b, "Turn %d — user: %q\n", turnNum, clipText(m.Content))
			case schema.RoleAssistant:
				if m.Content != "" {
					fmt.Fprintf(&b, "Turn %d — assistant: %s\n", turnNum, clipText(m.Content))
				}
				if len(m.ToolCalls) > 0 {
					fmt.Fprintf(&b, "Turn %d — assistant: tools:\n", turnNum)
					for _, call := range m.ToolCalls {
						fmt.Fprintf(&b, "  • %s\n", formatCall(call, t))
					}
				}
			}
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// formatCall renders one tool call as "name(key=value) → outcome [call_id=...]".
// outcome is derived from the matching tool result in the same turn.
func formatCall(call schema.ToolCall, turn []schema.Message) string {
	key := summariseArgs(call.Name, call.Arguments)
	outcome := lookupOutcome(call.ID, turn)
	if id := callIDFromOutcome(outcome); id != "" {
		return fmt.Sprintf("%s(%s) → %s; call_id=%s", call.Name, key, summariseOutcome(outcome), id)
	}
	return fmt.Sprintf("%s(%s) → %s", call.Name, key, summariseOutcome(outcome))
}

// summariseArgs picks the most-informative single field from the
// arguments JSON: path / command / pattern. Returns "" if none apply.
func summariseArgs(name string, args json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	for _, key := range []string{"path", "command", "pattern", "url"} {
		if v, ok := m[key]; ok {
			s := fmt.Sprintf("%v", v)
			return clipText(s)
		}
	}
	return ""
}

// lookupOutcome returns the tool-result Content for the given call ID
// within the same turn, or "" if not present.
func lookupOutcome(id string, turn []schema.Message) string {
	for _, m := range turn {
		if m.Role == schema.RoleTool && m.ToolCallID == id {
			return m.Content
		}
	}
	return ""
}

// summariseOutcome produces a short one-line summary of the tool result.
// For typical small outputs we return the first line clipped; for
// truncated outputs Layer A's marker provides "lines spilled" detail.
func summariseOutcome(outcome string) string {
	if outcome == "" {
		return "(no result)"
	}
	if strings.Contains(outcome, "bytes elided") {
		return "(elided; see call_id)"
	}
	line := outcome
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	return clipText(line)
}

// callIDFromOutcome extracts call_id=... from a truncated outcome
// marker if Layer A left one. Returns "" when absent.
func callIDFromOutcome(outcome string) string {
	matches := callIDRe.FindStringSubmatch(outcome)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// clipText caps a body line to digestTextLimit runes with an ellipsis
// suffix when truncated.
func clipText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= digestTextLimit {
		return s
	}
	return s[:digestTextLimit] + "..."
}

// predict applies the calibrator's ratio to a local estimate. nil
// calibrator means ratio=1.0 (no calibration data yet).
func predict(c *Calibrator, localEst int) int {
	if c == nil {
		return localEst
	}
	return c.Predict(localEst)
}
