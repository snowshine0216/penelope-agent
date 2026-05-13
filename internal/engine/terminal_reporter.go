package engine

import (
	"context"
	"fmt"
)

// TerminalReporter writes agent events to stdout in a human-readable format.
type TerminalReporter struct{}

// NewTerminalReporter returns a Reporter that prints events to stdout.
func NewTerminalReporter() *TerminalReporter { return &TerminalReporter{} }

func (r *TerminalReporter) OnThinking(_ context.Context) {
	fmt.Println("[thinking]")
}

func (r *TerminalReporter) OnToolCall(_ context.Context, toolName string, args string) {
	fmt.Printf("[tool] %s args=%s\n", toolName, args)
}

func (r *TerminalReporter) OnToolResult(_ context.Context, toolName string, result string, isError bool) {
	if isError {
		fmt.Printf("[tool:error] %s result=%s\n", toolName, result)
		return
	}
	fmt.Printf("[tool:ok] %s result=%s\n", toolName, result)
}

func (r *TerminalReporter) OnMessage(_ context.Context, content string) {
	fmt.Println(content)
}
