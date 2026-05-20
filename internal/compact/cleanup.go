package compact

import (
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// DefensiveCleanup removes orphan tool results, assistants whose
// tool_calls do not all have matching tool results immediately
// following, and any leading tool messages exposed by those drops.
// The result is always a provider-valid slice even if concurrent
// writers interleaved a JSONL file. Pure function: input is not
// mutated.
func DefensiveCleanup(messages []schema.Message) []schema.Message {
	pass1 := make([]schema.Message, 0, len(messages))
	for _, m := range messages {
		if m.Role == schema.RoleTool {
			if !matchingCallExists(pass1, m.ToolCallID) {
				continue
			}
		}
		pass1 = append(pass1, m)
	}

	keep := make([]bool, len(pass1))
	for i := range keep {
		keep[i] = true
	}
	for i, m := range pass1 {
		if m.Role != schema.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		expected := map[string]bool{}
		for _, c := range m.ToolCalls {
			expected[c.ID] = false
		}
		j := i + 1
		for j < len(pass1) && pass1[j].Role == schema.RoleTool {
			if _, ok := expected[pass1[j].ToolCallID]; ok {
				expected[pass1[j].ToolCallID] = true
			}
			j++
		}
		allSatisfied := true
		for _, seen := range expected {
			if !seen {
				allSatisfied = false
				break
			}
		}
		if allSatisfied {
			continue
		}
		keep[i] = false
		for k := i + 1; k < j; k++ {
			keep[k] = false
		}
	}
	pass2 := make([]schema.Message, 0, len(pass1))
	for i, m := range pass1 {
		if keep[i] {
			pass2 = append(pass2, m)
		}
	}

	start := 0
	for start < len(pass2) && pass2[start].Role == schema.RoleTool {
		start++
	}
	return pass2[start:]
}

// CloneMessages returns a fresh slice with the same elements. Returns
// nil for empty input so downstream `len(x) == 0` checks behave the
// same as on the original.
func CloneMessages(messages []schema.Message) []schema.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]schema.Message, len(messages))
	copy(out, messages)
	return out
}

func matchingCallExists(prefix []schema.Message, toolCallID string) bool {
	for i := len(prefix) - 1; i >= 0; i-- {
		m := prefix[i]
		if m.Role == schema.RoleAssistant {
			for _, c := range m.ToolCalls {
				if c.ID == toolCallID {
					return true
				}
			}
			return false
		}
		if m.Role == schema.RoleUser {
			return false
		}
	}
	return false
}
