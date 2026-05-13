package engine_test

import "context"

type noOpReporter struct{}

func (noOpReporter) OnThinking(context.Context)                          {}
func (noOpReporter) OnToolCall(context.Context, string, string)          {}
func (noOpReporter) OnToolResult(context.Context, string, string, bool)  {}
func (noOpReporter) OnMessage(context.Context, string)                   {}
