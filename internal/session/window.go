package session

import (
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// WindowTrimmer is the default Trimmer: it keeps the last N user turns,
// applies a token-budget ceiling that can shrink the slice further,
// and runs a defensive cleanup pass so the surviving slice is always
// provider-valid even if concurrent writers (D12) interleaved messages.
type WindowTrimmer struct {
	cfg TrimConfig
}

// NewWindowTrimmer constructs a WindowTrimmer. It is registered in init().
func NewWindowTrimmer(cfg TrimConfig) Trimmer {
	return WindowTrimmer{cfg: cfg}
}

func init() {
	Register("window", func(cfg TrimConfig) Trimmer { return NewWindowTrimmer(cfg) })
}

func (w WindowTrimmer) Name() string { return "window" }

// Trim runs three sequential pure passes: window by user turns, apply
// token cap, defensive cleanup. Each pass returns a fresh slice; the
// input is never mutated.
func (w WindowTrimmer) Trim(messages []schema.Message) []schema.Message {
	windowed := windowByUserTurns(messages, w.cfg.MaxUserTurns)
	capped := applyTokenCap(windowed, w.cfg.MaxTokens)
	cleaned := defensiveCleanup(capped)
	return cleaned
}

func windowByUserTurns(messages []schema.Message, maxTurns int) []schema.Message {
	if maxTurns <= 0 || len(messages) == 0 {
		return cloneMessages(messages)
	}
	userIndices := []int{}
	for i, m := range messages {
		if m.Role == schema.RoleUser {
			userIndices = append(userIndices, i)
		}
	}
	if len(userIndices) <= maxTurns {
		return cloneMessages(messages)
	}
	start := userIndices[len(userIndices)-maxTurns]
	return cloneMessages(messages[start:])
}

func applyTokenCap(messages []schema.Message, maxTokens int) []schema.Message {
	if maxTokens <= 0 || len(messages) == 0 {
		return cloneMessages(messages)
	}
	current := cloneMessages(messages)
	for EstimateTokens(current) > maxTokens {
		next := dropOldestUserTurn(current)
		if len(next) == len(current) {
			return current
		}
		current = next
	}
	return current
}

func dropOldestUserTurn(messages []schema.Message) []schema.Message {
	if len(messages) == 0 {
		return messages
	}
	firstUser := -1
	for i, m := range messages {
		if m.Role == schema.RoleUser {
			firstUser = i
			break
		}
	}
	if firstUser < 0 {
		return messages
	}
	nextUser := -1
	for i := firstUser + 1; i < len(messages); i++ {
		if messages[i].Role == schema.RoleUser {
			nextUser = i
			break
		}
	}
	if nextUser < 0 {
		// Only one user turn remains; do not drop it.
		return messages
	}
	out := make([]schema.Message, 0, len(messages)-(nextUser-firstUser))
	out = append(out, messages[:firstUser]...)
	out = append(out, messages[nextUser:]...)
	return out
}

func defensiveCleanup(messages []schema.Message) []schema.Message {
	// Pass 1: drop orphan tool messages whose preceding assistant does
	// not contain a matching tool_call_id.
	pass1 := make([]schema.Message, 0, len(messages))
	for _, m := range messages {
		if m.Role == schema.RoleTool {
			if !matchingCallExists(pass1, m.ToolCallID) {
				continue
			}
		}
		pass1 = append(pass1, m)
	}

	// Pass 2: drop assistants whose tool_calls do not all have matching
	// tool results immediately following (only tool messages permitted
	// between the assistant and the next non-tool message).
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

	// Pass 3: drop any leading tool messages exposed by the previous passes.
	start := 0
	for start < len(pass2) && pass2[start].Role == schema.RoleTool {
		start++
	}
	return pass2[start:]
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

func cloneMessages(messages []schema.Message) []schema.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]schema.Message, len(messages))
	copy(out, messages)
	return out
}
