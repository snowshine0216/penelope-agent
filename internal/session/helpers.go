package session

import "github.com/snowshine0216/penelope-agent/internal/schema"

// cloneMessages returns a copy of the slice so callers cannot mutate
// the session's in-memory history through the returned value.
func cloneMessages(messages []schema.Message) []schema.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]schema.Message, len(messages))
	copy(out, messages)
	return out
}
