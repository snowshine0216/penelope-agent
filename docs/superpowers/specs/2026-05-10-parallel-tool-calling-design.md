# Parallel Tool Calling — Design

**Date:** 2026-05-10
**Status:** Draft (awaiting review)

## Context

`penelope-agent` currently executes tool calls from a single assistant
message one at a time in `internal/engine/loop.go`. That preserves
determinism, but it leaves obvious performance on the table when the
model asks for independent read operations, such as multiple file reads.

The engine cannot safely run every tool call in parallel. Current tools
include mutating operations (`write_file`, `edit_file`) and an
unsandboxed command runner (`bash`). Running those concurrently can
cause file races, nondeterministic command behavior, and confusing
observations for the next model turn.

The goal is to improve performance for safe independent calls while
keeping tool effects ordered and predictable.

## Scope

**In:**
- Parallel execution for tool calls that explicitly opt into parallel
  safety.
- Conservative serial execution for mutating, effectful, or unknown
  tools.
- Deterministic observation ordering, independent of tool completion
  order.
- Engine-wide and per-tool concurrency caps so future API-backed tools
  can avoid local overload or external rate-limit pressure.
- Unit tests for grouping, ordering, serial behavior, mixed batches,
  and cancellation.

**Out:**
- Provider protocol changes.
- Streaming partial tool results back to the model.
- Automatic dependency analysis between tool arguments.
- Parallel execution for `bash`, `write_file`, or `edit_file`.
- Retry/backoff logic for API-backed tools. The design leaves room for
  rate-limit caps but does not implement remote retry policy.

## Decisions

| ID | Decision | Choice | Rationale |
|----|----------|--------|-----------|
| D1 | Safety source | Registry/tool execution policy | The engine should not hard-code tool names. Tool safety belongs at the tool boundary. |
| D2 | Default behavior | Unknown tools are serial | Conservative by default; new tools must opt into concurrency. |
| D3 | Observation order | Append by original request order | Provider behavior remains deterministic even when goroutines finish in a different order. |
| D4 | Parallel unit | Consecutive safe calls form a group | Preserves ordering around effectful calls without requiring dependency inference. |
| D5 | Rate-limit control | Engine cap plus optional per-tool cap | Prevents unbounded fan-out and gives future API tools lower concurrency without changing the loop again. |
| D6 | Cancellation | Wait for launched workers, then return context error | Avoids goroutine leaks and prevents partial observations from advancing the model after cancellation. |

## Architecture

Tool execution remains an engine responsibility. Tool safety becomes a
registry/tool responsibility.

Each tool exposes execution policy metadata separately from the
model-facing JSON schema. The initial policy classification is:

| Tool | Policy |
|------|--------|
| `read_file` | Parallel safe |
| `bash` | Serial |
| `write_file` | Serial |
| `edit_file` | Serial |
| Unknown future tools | Serial |

The engine converts `actionResp.ToolCalls` into ordered execution
groups. Consecutive parallel-safe calls are grouped together. Every
serial call becomes a one-call group. Groups run in original order.
Calls inside a parallel group can run concurrently.

Example:

```text
read_file A
read_file B
edit_file C
read_file D
bash E
read_file F
read_file G
```

Planned groups:

```text
[read_file A, read_file B]  // parallel
[edit_file C]               // serial
[read_file D]               // single call
[bash E]                    // serial
[read_file F, read_file G]  // parallel
```

This gives speedups for discovery-heavy turns while preserving ordered
effects around writes and commands.

## Components

### Execution Policy

Add a small engine-facing policy type near the tool registry:

```go
type ExecutionPolicy struct {
    ParallelSafe   bool
    MaxConcurrency int
}
```

`MaxConcurrency == 0` means use the engine default. Serial tools ignore
the value.

The registry should expose a way to resolve policy for a tool call. The
engine should treat missing policy as serial. The model-facing
`schema.ToolDefinition` remains unchanged, so provider translation code
does not need to change.

### Pure Group Planner

Add a small pure planner function in `internal/engine`:

```go
func PlanToolCallGroups(
    calls []schema.ToolCall,
    policyFor func(schema.ToolCall) tools.ExecutionPolicy,
) [][]schema.ToolCall
```

The planner returns new slices and does not mutate the input. It is
deterministic and unit-testable without invoking tools.

Planning rule:

1. Start an empty current parallel group.
2. Append parallel-safe calls to the current group.
3. When a serial call appears, flush the current group, then append the
   serial call as its own group.
4. Flush any remaining parallel group at the end.

### Group Executor

Add an engine helper that executes one group:

```go
func executeToolCallGroup(
    ctx context.Context,
    registry tools.Registry,
    group []schema.ToolCall,
    limit int,
) ([]schema.ToolResult, error)
```

For a one-call group, execute directly, then return `ctx.Err()` if the
context was canceled while the tool was running.

For a multi-call group:

1. Create `results := make([]schema.ToolResult, len(group))`.
2. Launch workers up to the resolved concurrency limit.
3. Each worker sends `{index, result}` to a fan-in channel. The parent
   goroutine is the only writer to the ordered `results` slice.
4. Wait for all launched workers to finish.
5. If `ctx.Err()` is non-nil after the group joins, return that error.
6. Return results in the same index order as `group`.

This keeps the engine’s observable behavior deterministic.

## Observation Ordering

Tool observations must not be appended as goroutines finish. No worker
goroutine mutates `contextHistory`. The group executor collects results
by original call index. The loop appends observations only after the
whole group completes, from the parent goroutine:

```go
results := make([]schema.ToolResult, len(group))
resultCh := make(chan indexedToolResult, len(group))
var wg sync.WaitGroup

for i, call := range group {
    wg.Add(1)
    go func(i int, call schema.ToolCall) {
        defer wg.Done()
        resultCh <- indexedToolResult{
            index:  i,
            result: registry.Execute(ctx, call),
        }
    }(i, call)
}

wg.Wait()
close(resultCh)

for item := range resultCh {
    results[item.index] = item.result
}
```

Then the parent goroutine appends in index order:

```go
for _, result := range results {
    contextHistory = append(contextHistory, schema.Message{
        Role:       schema.RoleTool,
        Content:    result.Output,
        ToolCallID: result.ToolCallID,
        IsError:    result.IsError,
    })
}
```

If `call_3` finishes before `call_1`, the next provider request still
sees observations in model request order:

```text
tool result for call_1
tool result for call_2
tool result for call_3
```

That matches the current sequential semantics and avoids completion
timing becoming part of model-visible state.

## Concurrency Limits And Rate Limits

The first implementation only marks local read-only file access as
parallel-safe. Future tools may be read-only but backed by external
services, such as GitHub, web search, databases, or SaaS APIs. Those
tools need rate-limit-aware concurrency.

The engine should have a small default cap for parallel groups. A safe
initial default is `4`, with a future CLI flag possible if needed.

Per-tool policy can lower the cap:

```go
ExecutionPolicy{ParallelSafe: true, MaxConcurrency: 2}
```

For a mixed parallel group, the effective limit is the minimum positive
per-tool cap across the group, bounded by the engine default. This is
conservative and avoids overloading the most constrained tool.

This design does not add retries or backoff. If a future API tool hits a
remote rate limit, that tool should return a normal tool error through
the registry so the model can observe it. Retry policy can be designed
inside that tool later.

## Error Handling And Cancellation

Tool execution errors stay non-fatal. The registry already converts
tool failures into `schema.ToolResult{IsError: true}`. The engine should
append those observations and let the model self-correct.

Context cancellation is fatal to the engine run:

- Check `ctx.Err()` before each group starts.
- Pass the same `ctx` to every worker.
- Wait for launched workers to finish before returning.
- After a group joins, return `ctx.Err()` if canceled.
- Do not call the provider again after cancellation.

If cancellation happens during a parallel group, the engine may have
some completed local results in memory, but it should not append partial
observations and advance the conversation.

## Testing Plan

All implementation should follow TDD.

Add failing tests before implementation for these behaviors:

- Parallel-safe delayed tools complete faster than serial execution.
- Observations are appended in original request order even when tools
  finish out of order.
- Serial tools execute one at a time and in request order.
- Mixed calls are grouped safely, such as `read_file, read_file,
  edit_file, read_file`.
- Unknown tools default to serial policy.
- Context cancellation during a parallel group returns `context.Canceled`
  and does not make another provider call.

Existing tests that assert tool-result propagation and max-turn behavior
should continue to pass unchanged.

## Success Criteria

- Multiple safe read-only tool calls in one assistant message run
  concurrently.
- Mutating and unknown tools remain ordered.
- Provider-visible history is deterministic and compatible with current
  OpenAI and Anthropic translators.
- No goroutines are leaked on cancellation.
- Tests prove both the performance behavior and the ordering semantics.
