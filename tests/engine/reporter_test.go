package engine_test

import (
	"context"

	"github.com/snowshine0216/penelope-agent/internal/compact"
)

type noOpReporter struct{}

func (noOpReporter) OnThinking(context.Context)                         {}
func (noOpReporter) OnToolCall(context.Context, string, string)         {}
func (noOpReporter) OnToolResult(context.Context, string, string, bool) {}
func (noOpReporter) OnMessage(context.Context, string)                  {}
func (noOpReporter) OnCompact(context.Context, compact.CompactStats)    {}
