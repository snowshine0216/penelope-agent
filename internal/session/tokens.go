package session

import (
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// MessageOverhead approximates the per-message envelope cost a provider
// incurs beyond the literal content (role marker, separator tokens).
// 8 is a small constant chosen to roughly match OpenAI's documented
// per-message overhead so the total estimate is conservative.
const MessageOverhead = 8

// EstimateOne returns a chars/4 estimate of one message's token cost,
// rounding up so a 1-char message still counts as 1 content token.
func EstimateOne(msg schema.Message) int {
	tokens := MessageOverhead + ceilDiv4(len(msg.Content))
	if msg.ToolCallID != "" {
		tokens += ceilDiv4(len(msg.ToolCallID))
	}
	for _, call := range msg.ToolCalls {
		tokens += ceilDiv4(len(call.ID))
		tokens += ceilDiv4(len(call.Name))
		tokens += ceilDiv4(len(call.Arguments))
	}
	return tokens
}

// EstimateTokens sums EstimateOne across every message in the slice.
func EstimateTokens(msgs []schema.Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateOne(m)
	}
	return total
}

func ceilDiv4(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + 3) / 4
}
