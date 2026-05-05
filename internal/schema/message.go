package schema

import "encoding/json"

// Role represents the sender of a message.
type Role string

const (
	RoleSystem    Role = "system"    // system prompt
	RoleUser      Role = "user"      // user input
	RoleAssistant Role = "assistant" // model output: text reasoning or tool calls
	RoleTool      Role = "tool"      // tool execution result, correlated by ToolCallID
)

// Message represents a single message in the conversation context.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`

	// ToolCalls is populated when the model requests one or more tool invocations.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ToolCallID links a tool result back to the originating tool call.
	ToolCallID string `json:"tool_call_id,omitempty"`

	// IsError marks a tool result as a failure. Surfaced to providers that
	// support a structured error flag (Anthropic). Ignored by providers that
	// don't (OpenAI relies on text content).
	IsError bool `json:"is_error,omitempty"`
}

// ToolCall represents a model-requested invocation of a specific tool.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Arguments holds raw JSON; parsing is delegated to the concrete tool.
	Arguments json.RawMessage `json:"arguments"`
}

// ToolResult is the outcome of a local tool execution.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Output     string `json:"output"`
	IsError    bool   `json:"is_error"`
}

// ToolDefinition describes a tool the model can invoke.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}
