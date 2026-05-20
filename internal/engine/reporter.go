// internal/engine/reporter.go
package engine

import (
	"context"

	"github.com/snowshine0216/penelope-agent/internal/compact"
)

// Reporter defines the contract for surfacing agent engine events to the
// presentation layer (CLI, Feishu, web UI, etc.).
type Reporter interface {
	// OnThinking is called when the model enters a slow-reasoning phase.
	OnThinking(ctx context.Context)

	// OnToolCall is called when the model decides to invoke a tool.
	OnToolCall(ctx context.Context, toolName string, args string)

	// OnToolResult is called when a tool finishes execution.
	OnToolResult(ctx context.Context, toolName string, result string, isError bool)

	// OnMessage is called when the model emits a final text response.
	OnMessage(ctx context.Context, content string)

	// OnCompact is fired once per turn when compaction stats merit
	// surfacing. Emission rule lives in the engine (see shouldEmit).
	// Task 15 implements the body; this task only needs the stub.
	OnCompact(ctx context.Context, stats compact.CompactStats)
}
