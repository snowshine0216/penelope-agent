package engine

import (
	"encoding/json"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// PlanToolCallGroups splits tool calls into ordered execution groups.
// Consecutive parallel-safe calls share a group; serial calls stand alone.
func PlanToolCallGroups(
	calls []schema.ToolCall,
	policyFor func(schema.ToolCall) tools.ExecutionPolicy,
) [][]schema.ToolCall {
	groups := make([][]schema.ToolCall, 0, len(calls))
	currentParallel := []schema.ToolCall{}

	flushParallel := func() {
		if len(currentParallel) == 0 {
			return
		}
		groups = append(groups, cloneToolCalls(currentParallel))
		currentParallel = []schema.ToolCall{}
	}

	for _, call := range calls {
		if isParallelSafe(call, policyFor) {
			currentParallel = append(currentParallel, call)
			continue
		}
		flushParallel()
		groups = append(groups, []schema.ToolCall{call})
	}

	flushParallel()
	return groups
}

func isParallelSafe(call schema.ToolCall, policyFor func(schema.ToolCall) tools.ExecutionPolicy) bool {
	if policyFor == nil {
		return false
	}
	return policyFor(call).ParallelSafe
}

func cloneToolCalls(calls []schema.ToolCall) []schema.ToolCall {
	out := make([]schema.ToolCall, len(calls))
	copy(out, calls)
	for i := range out {
		if calls[i].Arguments != nil {
			out[i].Arguments = append(json.RawMessage(nil), calls[i].Arguments...)
		}
	}
	return out
}
