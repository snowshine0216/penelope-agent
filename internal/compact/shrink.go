package compact

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// ShrinkConfig parameterises Layer A.
type ShrinkConfig struct {
	MaxToolBytes        int // tool result truncation threshold (default 65536)
	RecentTurnsVerbatim int // last N turns skip write_file / edit_file arg stripping
}

// ShrinkStats summarises what Layer A did. The Compactor wraps this
// into the public CompactStats.
type ShrinkStats struct {
	ToolResultsTruncated int
	ToolCallArgsStripped int
}

// ShrinkApply runs the Layer A passes. Pure: input is not mutated.
// Returns a fresh slice.
func ShrinkApply(history []schema.Message, cfg ShrinkConfig) ([]schema.Message, ShrinkStats) {
	if cfg.MaxToolBytes <= 0 {
		cfg.MaxToolBytes = 65536
	}
	out := make([]schema.Message, len(history))
	copy(out, history)

	// Determine which message indices are inside the verbatim tail.
	// A "turn" starts at each user message. We walk backwards and
	// count user messages until we have RecentTurnsVerbatim.
	verbatimStart := verbatimStartIndex(out, cfg.RecentTurnsVerbatim)

	stats := ShrinkStats{}
	for i := range out {
		switch out[i].Role {
		case schema.RoleTool:
			// Skip re-truncation if already truncated (idempotency guard).
			if len(out[i].Content) > cfg.MaxToolBytes && !isAlreadyTruncated(out[i].Content) {
				marker := fmt.Sprintf(
					"\n\n...[%d bytes elided of %d total for call_id=%s; "+
						"use read_tool_output(call_id=%q, start_line=N, line_count=M) to read more]...\n\n",
					len(out[i].Content), len(out[i].Content), out[i].ToolCallID, out[i].ToolCallID,
				)
				out[i].Content = tools.TruncateWithMarker(out[i].Content, cfg.MaxToolBytes, marker)
				stats.ToolResultsTruncated++
			}
		case schema.RoleAssistant:
			if i >= verbatimStart {
				continue
			}
			for j := range out[i].ToolCalls {
				if !isLargeArgTool(out[i].ToolCalls[j].Name) {
					continue
				}
				stripped, changed := stripLargeArgs(out[i].ToolCalls[j].Arguments)
				if changed {
					out[i].ToolCalls[j].Arguments = stripped
					stats.ToolCallArgsStripped++
				}
			}
		}
	}
	return out, stats
}

// isAlreadyTruncated returns true when the content already contains
// the spill marker that ShrinkApply inserts, so we don't re-truncate.
func isAlreadyTruncated(content string) bool {
	return strings.Contains(content, "bytes elided of") && strings.Contains(content, "call_id=")
}

func isLargeArgTool(name string) bool {
	return name == "write_file" || name == "edit_file"
}

// stripLargeArgs reads the raw JSON args, replaces `content`,
// `new_string`, `old_string` with `"<content elided: N bytes>"` if
// they are larger than 256 bytes, and re-marshals. Returns the new
// raw bytes and whether anything changed.
func stripLargeArgs(args json.RawMessage) (json.RawMessage, bool) {
	const threshold = 256
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return args, false
	}
	changed := false
	for _, key := range []string{"content", "new_string", "old_string"} {
		v, ok := m[key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		if len(s) > threshold {
			m[key] = fmt.Sprintf("<content elided: %d bytes>", len(s))
			changed = true
		}
	}
	if !changed {
		return args, false
	}
	out, err := json.Marshal(m)
	if err != nil {
		return args, false
	}
	return out, true
}

// verbatimStartIndex returns the index of the first message inside
// the verbatim tail (recentTurns user-message-bounded turns from the
// end). Returns len(history) if recentTurns <= 0 (no verbatim window).
func verbatimStartIndex(history []schema.Message, recentTurns int) int {
	if recentTurns <= 0 {
		return len(history)
	}
	userCount := 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == schema.RoleUser {
			userCount++
			if userCount == recentTurns {
				return i
			}
		}
	}
	return 0
}
