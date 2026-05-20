package provider

import (
	"context"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// Usage captures per-request token counts surfaced by the provider.
// Zero values mean the provider did not return usage; callers must
// treat that as "unknown" rather than "zero".
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Response is the full Generate return: the next assistant message
// plus the provider-reported token usage.
type Response struct {
	Message *schema.Message
	Usage   Usage
}

// LLMProvider is the provider-neutral contract. Implementations must
// always return a non-nil Response on success (Message may be empty
// if the model produced no content).
type LLMProvider interface {
	Generate(ctx context.Context, messages []schema.Message, availableTools []schema.ToolDefinition) (*Response, error)
}
