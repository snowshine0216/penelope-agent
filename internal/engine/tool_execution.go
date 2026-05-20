package engine

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

const defaultParallelToolConcurrency = 4

type indexedToolCall struct {
	index int
	call  schema.ToolCall
}

type indexedToolResult struct {
	index  int
	result schema.ToolResult
}

// applyToolBoundaryCap is the engine's tool-output boundary cap from
// spec §2. Called once per tool result before sess.Append. If the
// output exceeds MaxToolBytes, spill the full body to the session's
// tool-outputs dir and replace result.Output with a head+tail
// truncation that carries a spill-aware marker. Returns true iff a
// spill happened (so the caller can increment the per-turn count).
func (e *AgentEngine) applyToolBoundaryCap(sess *agentsession.Session, result *schema.ToolResult) (bool, error) {
	max := e.compactCfg.MaxToolBytes
	if max <= 0 {
		max = 65536
	}
	if len(result.Output) <= max {
		return false, nil
	}
	path, lines, err := sess.SpillToolOutput(result.ToolCallID, result.Output)
	if err != nil {
		return false, fmt.Errorf("spill tool output for call %s: %w", result.ToolCallID, err)
	}
	marker := fmt.Sprintf(
		"\n\n...[%d bytes / %d lines spilled to %s; "+
			"use read_tool_output(call_id=%q, start_line=N, line_count=M) to read more]...\n\n",
		len(result.Output), lines, path, result.ToolCallID,
	)
	result.Output = tools.TruncateWithMarker(result.Output, max, marker)
	return true, nil
}

func executeToolCallGroup(
	ctx context.Context,
	registry tools.Registry,
	group []schema.ToolCall,
	limit int,
) ([]schema.ToolResult, error) {
	if len(group) == 0 {
		return nil, nil
	}

	if len(group) == 1 {
		result := executeToolCall(ctx, registry, group[0])
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return []schema.ToolResult{result}, nil
	}

	workerCount := boundedWorkerCount(limit, len(group))
	jobs := make(chan indexedToolCall)
	resultCh := make(chan indexedToolResult, len(group))
	var wg sync.WaitGroup

	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				resultCh <- indexedToolResult{
					index:  job.index,
					result: executeToolCall(ctx, registry, job.call),
				}
			}
		}()
	}

	for i, call := range group {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(resultCh)
			return nil, ctx.Err()
		case jobs <- indexedToolCall{index: i, call: call}:
		}
	}

	close(jobs)
	wg.Wait()
	close(resultCh)

	results := make([]schema.ToolResult, len(group))
	for item := range resultCh {
		results[item.index] = item.result
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func executeToolCall(ctx context.Context, registry tools.Registry, call schema.ToolCall) schema.ToolResult {
	log.Printf("[engine] executing tool=%s args=%s", call.Name, string(call.Arguments))

	result := registry.Execute(ctx, call)

	if result.IsError {
		log.Printf("[engine] tool error: %s", result.Output)
	} else {
		log.Printf("[engine] tool ok: %d bytes", len(result.Output))
	}

	return result
}

func boundedWorkerCount(limit, groupSize int) int {
	if groupSize <= 0 {
		return 0
	}
	if limit <= 0 || limit > groupSize {
		return groupSize
	}
	return limit
}

func toolResultMessage(result schema.ToolResult) schema.Message {
	return schema.Message{
		Role:       schema.RoleTool,
		Content:    result.Output,
		ToolCallID: result.ToolCallID,
		IsError:    result.IsError,
	}
}
