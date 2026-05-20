package session

import "github.com/snowshine0216/penelope-agent/internal/schema"

// cloneMessages returns a deep copy of the slice so callers cannot
// mutate the session's in-memory history through the returned value.
// Each message's ToolCalls slice is deep-copied to prevent callers
// from writing through the shared backing array (purity invariant).
func cloneMessages(messages []schema.Message) []schema.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]schema.Message, len(messages))
	copy(out, messages)
	for i := range out {
		if len(out[i].ToolCalls) > 0 {
			tc := make([]schema.ToolCall, len(out[i].ToolCalls))
			copy(tc, out[i].ToolCalls)
			out[i].ToolCalls = tc
		}
	}
	return out
}
