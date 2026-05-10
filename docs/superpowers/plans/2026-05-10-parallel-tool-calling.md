# Parallel Tool Calling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Execute safe tool calls concurrently while preserving deterministic observation order and keeping mutating or unknown tools serial.

**Architecture:** Add engine-facing execution policy metadata to the tools registry, then use a pure planner in `internal/engine` to split model-requested tool calls into ordered groups. The engine executes parallel-safe groups with bounded worker fan-out and fan-in by original call index, then appends tool observations from the parent goroutine in deterministic order.

**Tech Stack:** Go 1.26.2, standard library concurrency primitives (`sync`, channels, `time` in tests), existing packages `internal/engine`, `internal/tools`, and `internal/schema`.

**Spec:** `docs/superpowers/specs/2026-05-10-parallel-tool-calling-design.md`

---

## File Structure

**Create:**
- `internal/engine/tool_groups.go` - pure grouping logic for tool calls.
- `internal/engine/tool_execution.go` - bounded group executor and result fan-in.
- `tests/engine/tool_groups_test.go` - pure planner tests.
- `tests/engine/parallel_tool_execution_test.go` - engine integration tests for concurrency, ordering, limits, and cancellation.

**Modify:**
- `internal/tools/registry.go` - add `ExecutionPolicy`, optional policy provider interface, and `Registry.ExecutionPolicyFor`.
- `internal/tools/read_file.go` - mark `read_file` as parallel-safe.
- `internal/engine/loop.go` - replace the sequential tool loop with group planning and execution.
- `tests/tools/registry_test.go` - cover policy lookup defaults and opt-in behavior.
- `README.md` - document ordered parallel-safe tool execution.
- `CHANGELOG.md` - add an Unreleased entry.

**No changes:**
- Provider translation code in `internal/provider/`.
- Model-facing tool schemas in `schema.ToolDefinition`.
- CLI flags.

---

## Task 1: Registry execution policy

**Files:**
- Modify: `internal/tools/registry.go`
- Modify: `internal/tools/read_file.go`
- Modify: `tests/tools/registry_test.go`

- [ ] **Step 1.1: Write failing registry policy tests**

Append to `tests/tools/registry_test.go`:

```go
type policyFakeTool struct {
	*fakeTool
	policy tools.ExecutionPolicy
}

func (p *policyFakeTool) ExecutionPolicy() tools.ExecutionPolicy {
	return p.policy
}

func TestRegistryExecutionPolicyForUnknownToolDefaultsSerial(t *testing.T) {
	r := tools.NewRegistry()

	got := r.ExecutionPolicyFor(schema.ToolCall{Name: "ghost"})

	if got.ParallelSafe {
		t.Fatalf("unknown tool policy ParallelSafe = true, want false")
	}
	if got.MaxConcurrency != 0 {
		t.Fatalf("unknown tool MaxConcurrency = %d, want 0", got.MaxConcurrency)
	}
}

func TestRegistryExecutionPolicyForToolWithoutPolicyDefaultsSerial(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(newFake("plain", "", okExec("ok")))

	got := r.ExecutionPolicyFor(schema.ToolCall{Name: "plain"})

	if got.ParallelSafe {
		t.Fatalf("plain tool policy ParallelSafe = true, want false")
	}
}

func TestRegistryExecutionPolicyUsesToolPolicy(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(&policyFakeTool{
		fakeTool: newFake("api_read", "", okExec("ok")),
		policy:   tools.ExecutionPolicy{ParallelSafe: true, MaxConcurrency: 2},
	})

	got := r.ExecutionPolicyFor(schema.ToolCall{Name: "api_read"})

	if !got.ParallelSafe {
		t.Fatal("policy ParallelSafe = false, want true")
	}
	if got.MaxConcurrency != 2 {
		t.Fatalf("MaxConcurrency = %d, want 2", got.MaxConcurrency)
	}
}

func TestRegistryExecutionPolicyForReadFileAllowsParallel(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(tools.NewReadFileTool(t.TempDir()))

	got := r.ExecutionPolicyFor(schema.ToolCall{Name: "read_file"})

	if !got.ParallelSafe {
		t.Fatal("read_file should be parallel-safe")
	}
	if got.MaxConcurrency != 0 {
		t.Fatalf("read_file MaxConcurrency = %d, want 0 to use engine default", got.MaxConcurrency)
	}
}
```

- [ ] **Step 1.2: Run tests and verify failure**

Run:

```bash
go test ./tests/tools/ -run 'TestRegistryExecutionPolicy' -v
```

Expected: compile failure with `r.ExecutionPolicyFor undefined` and `undefined: tools.ExecutionPolicy`.

- [ ] **Step 1.3: Implement execution policy in the registry**

Edit `internal/tools/registry.go`.

Add below the `Tool` interface:

```go
// ExecutionPolicy describes how safely the engine may schedule a tool.
type ExecutionPolicy struct {
	ParallelSafe   bool
	MaxConcurrency int
}

// ExecutionPolicyProvider can be implemented by tools that opt into
// non-default scheduling. Tools that do not implement it are serial.
type ExecutionPolicyProvider interface {
	ExecutionPolicy() ExecutionPolicy
}
```

Add to the `Registry` interface:

```go
// ExecutionPolicyFor returns engine-facing scheduling metadata for a tool call.
// Unknown tools and tools without explicit policy are treated as serial.
ExecutionPolicyFor(call schema.ToolCall) ExecutionPolicy
```

Add to `registryImpl`:

```go
func (r *registryImpl) ExecutionPolicyFor(call schema.ToolCall) ExecutionPolicy {
	tool, exists := r.tools[call.Name]
	if !exists {
		return ExecutionPolicy{}
	}

	policyProvider, ok := tool.(ExecutionPolicyProvider)
	if !ok {
		return ExecutionPolicy{}
	}

	return normalizeExecutionPolicy(policyProvider.ExecutionPolicy())
}

func normalizeExecutionPolicy(policy ExecutionPolicy) ExecutionPolicy {
	if !policy.ParallelSafe {
		return ExecutionPolicy{}
	}
	if policy.MaxConcurrency < 0 {
		return ExecutionPolicy{ParallelSafe: true}
	}
	return policy
}
```

- [ ] **Step 1.4: Mark `read_file` as parallel-safe**

Add to `internal/tools/read_file.go` after `Definition()`:

```go
func (t *ReadFileTool) ExecutionPolicy() ExecutionPolicy {
	return ExecutionPolicy{ParallelSafe: true}
}
```

- [ ] **Step 1.5: Run registry tests and verify pass**

Run:

```bash
go test ./tests/tools/ -run 'TestRegistryExecutionPolicy|TestRegistry' -v
```

Expected: PASS.

- [ ] **Step 1.6: Commit Task 1**

Run:

```bash
git add internal/tools/registry.go internal/tools/read_file.go tests/tools/registry_test.go
git commit -m "feat(tools): add execution policy metadata"
```

---

## Task 2: Pure tool-call group planner

**Files:**
- Create: `internal/engine/tool_groups.go`
- Create: `tests/engine/tool_groups_test.go`

- [ ] **Step 2.1: Write failing planner tests**

Create `tests/engine/tool_groups_test.go`:

```go
package engine_test

import (
	"reflect"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func toolCall(id, name string) schema.ToolCall {
	return schema.ToolCall{ID: id, Name: name}
}

func groupedIDs(groups [][]schema.ToolCall) [][]string {
	out := make([][]string, 0, len(groups))
	for _, group := range groups {
		ids := make([]string, 0, len(group))
		for _, call := range group {
			ids = append(ids, call.ID)
		}
		out = append(out, ids)
	}
	return out
}

func policyMap(policies map[string]tools.ExecutionPolicy) func(schema.ToolCall) tools.ExecutionPolicy {
	return func(call schema.ToolCall) tools.ExecutionPolicy {
		return policies[call.Name]
	}
}

func TestPlanToolCallGroupsBatchesConsecutiveParallelCalls(t *testing.T) {
	calls := []schema.ToolCall{
		toolCall("1", "read_file"),
		toolCall("2", "read_file"),
		toolCall("3", "edit_file"),
		toolCall("4", "read_file"),
		toolCall("5", "bash"),
		toolCall("6", "read_file"),
		toolCall("7", "read_file"),
	}
	policyFor := policyMap(map[string]tools.ExecutionPolicy{
		"read_file": {ParallelSafe: true},
	})

	got := groupedIDs(engine.PlanToolCallGroups(calls, policyFor))
	want := [][]string{{"1", "2"}, {"3"}, {"4"}, {"5"}, {"6", "7"}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %#v, want %#v", got, want)
	}
}

func TestPlanToolCallGroupsTreatsNilPolicyLookupAsSerial(t *testing.T) {
	calls := []schema.ToolCall{
		toolCall("1", "read_file"),
		toolCall("2", "read_file"),
	}

	got := groupedIDs(engine.PlanToolCallGroups(calls, nil))
	want := [][]string{{"1"}, {"2"}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %#v, want %#v", got, want)
	}
}

func TestPlanToolCallGroupsDoesNotMutateInput(t *testing.T) {
	calls := []schema.ToolCall{
		toolCall("1", "read_file"),
		toolCall("2", "bash"),
	}
	original := append([]schema.ToolCall(nil), calls...)
	policyFor := policyMap(map[string]tools.ExecutionPolicy{
		"read_file": {ParallelSafe: true},
	})

	_ = engine.PlanToolCallGroups(calls, policyFor)

	if !reflect.DeepEqual(calls, original) {
		t.Fatalf("input mutated: got %#v, want %#v", calls, original)
	}
}
```

- [ ] **Step 2.2: Run tests and verify failure**

Run:

```bash
go test ./tests/engine/ -run TestPlanToolCallGroups -v
```

Expected: compile failure with `undefined: engine.PlanToolCallGroups`.

- [ ] **Step 2.3: Implement planner**

Create `internal/engine/tool_groups.go`:

```go
package engine

import (
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
	return out
}
```

- [ ] **Step 2.4: Run planner tests and verify pass**

Run:

```bash
go test ./tests/engine/ -run TestPlanToolCallGroups -v
```

Expected: PASS.

- [ ] **Step 2.5: Commit Task 2**

Run:

```bash
git add internal/engine/tool_groups.go tests/engine/tool_groups_test.go
git commit -m "feat(engine): plan ordered tool execution groups"
```

---

## Task 3: Parallel group executor and loop integration

**Files:**
- Create: `internal/engine/tool_execution.go`
- Modify: `internal/engine/loop.go`
- Create: `tests/engine/parallel_tool_execution_test.go`

- [ ] **Step 3.1: Write failing engine integration tests**

Create `tests/engine/parallel_tool_execution_test.go`:

```go
package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

type activityTracker struct {
	mu        sync.Mutex
	active    int
	maxActive int
	order     []string
}

func (t *activityTracker) enter(name string) func() {
	t.mu.Lock()
	t.active++
	if t.active > t.maxActive {
		t.maxActive = t.active
	}
	t.order = append(t.order, name)
	t.mu.Unlock()

	return func() {
		t.mu.Lock()
		t.active--
		t.mu.Unlock()
	}
}

func (t *activityTracker) snapshot() (int, []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.maxActive, append([]string(nil), t.order...)
}

type delayedPolicyTool struct {
	name    string
	output  string
	delay   time.Duration
	policy  tools.ExecutionPolicy
	tracker *activityTracker
}

func (d *delayedPolicyTool) Name() string { return d.name }

func (d *delayedPolicyTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        d.name,
		Description: "delayed test tool",
		InputSchema: map[string]interface{}{"type": "object"},
	}
}

func (d *delayedPolicyTool) ExecutionPolicy() tools.ExecutionPolicy {
	return d.policy
}

func (d *delayedPolicyTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	if d.tracker != nil {
		leave := d.tracker.enter(d.name)
		defer leave()
	}

	select {
	case <-time.After(d.delay):
		return d.output, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func toolObservationIDs(msgs []schema.Message) []string {
	ids := []string{}
	for _, msg := range msgs {
		if msg.Role == schema.RoleTool {
			ids = append(ids, msg.ToolCallID)
		}
	}
	return ids
}

func TestEngineRunsParallelSafeToolsConcurrently(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&delayedPolicyTool{
		name:   "parallel_a",
		output: "a-output",
		delay:  180 * time.Millisecond,
		policy: tools.ExecutionPolicy{ParallelSafe: true},
	})
	registry.Register(&delayedPolicyTool{
		name:   "parallel_b",
		output: "b-output",
		delay:  180 * time.Millisecond,
		policy: tools.ExecutionPolicy{ParallelSafe: true},
	})

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "a-call", Name: "parallel_a", Arguments: json.RawMessage(`{}`)},
					{ID: "b-call", Name: "parallel_b", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "done"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	start := time.Now()
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed >= 320*time.Millisecond {
		t.Fatalf("elapsed = %s, want parallel execution under 320ms", elapsed)
	}
}

func TestEnginePreservesObservationOrderWhenParallelToolsFinishOutOfOrder(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(&delayedPolicyTool{
		name:   "slow",
		output: "slow-output",
		delay:  180 * time.Millisecond,
		policy: tools.ExecutionPolicy{ParallelSafe: true},
	})
	registry.Register(&delayedPolicyTool{
		name:   "fast",
		output: "fast-output",
		delay:  20 * time.Millisecond,
		policy: tools.ExecutionPolicy{ParallelSafe: true},
	})

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "slow-call", Name: "slow", Arguments: json.RawMessage(`{}`)},
					{ID: "fast-call", Name: "fast", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "done"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := toolObservationIDs(provider.receivedMsgs[1])
	want := []string{"slow-call", "fast-call"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool observation IDs = %#v, want %#v", got, want)
	}
}

func TestEngineRunsSerialToolsOneAtATimeInOrder(t *testing.T) {
	tracker := &activityTracker{}
	registry := tools.NewRegistry()
	registry.Register(&delayedPolicyTool{name: "first", output: "1", delay: 60 * time.Millisecond, tracker: tracker})
	registry.Register(&delayedPolicyTool{name: "second", output: "2", delay: 60 * time.Millisecond, tracker: tracker})

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "1", Name: "first", Arguments: json.RawMessage(`{}`)},
					{ID: "2", Name: "second", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "done"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	maxActive, order := tracker.snapshot()
	if maxActive != 1 {
		t.Fatalf("maxActive = %d, want 1 for serial tools", maxActive)
	}
	if !reflect.DeepEqual(order, []string{"first", "second"}) {
		t.Fatalf("order = %#v, want first then second", order)
	}
}

func TestEngineHonorsPerToolMaxConcurrency(t *testing.T) {
	tracker := &activityTracker{}
	registry := tools.NewRegistry()
	registry.Register(&delayedPolicyTool{
		name:    "limited_a",
		output:  "a",
		delay:   80 * time.Millisecond,
		policy:  tools.ExecutionPolicy{ParallelSafe: true, MaxConcurrency: 1},
		tracker: tracker,
	})
	registry.Register(&delayedPolicyTool{
		name:    "limited_b",
		output:  "b",
		delay:   80 * time.Millisecond,
		policy:  tools.ExecutionPolicy{ParallelSafe: true},
		tracker: tracker,
	})

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "a", Name: "limited_a", Arguments: json.RawMessage(`{}`)},
					{ID: "b", Name: "limited_b", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "done"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	maxActive, _ := tracker.snapshot()
	if maxActive != 1 {
		t.Fatalf("maxActive = %d, want 1 from per-tool cap", maxActive)
	}
}

type cancelOnExecuteTool struct {
	cancel context.CancelFunc
}

func (c *cancelOnExecuteTool) Name() string { return "cancel_now" }
func (c *cancelOnExecuteTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{Name: "cancel_now", Description: "cancels", InputSchema: map[string]interface{}{"type": "object"}}
}
func (c *cancelOnExecuteTool) ExecutionPolicy() tools.ExecutionPolicy {
	return tools.ExecutionPolicy{ParallelSafe: true}
}
func (c *cancelOnExecuteTool) Execute(context.Context, json.RawMessage) (string, error) {
	c.cancel()
	return "cancelled", nil
}

func TestEngineCancellationDuringParallelGroupDoesNotCallProviderAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	registry := tools.NewRegistry()
	registry.Register(&cancelOnExecuteTool{cancel: cancel})
	registry.Register(&delayedPolicyTool{
		name:   "waiter",
		output: "waited",
		delay:  200 * time.Millisecond,
		policy: tools.ExecutionPolicy{ParallelSafe: true},
	})

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "cancel", Name: "cancel_now", Arguments: json.RawMessage(`{}`)},
					{ID: "wait", Name: "waiter", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "should not be requested"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	err := eng.Run(ctx, "go")

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider.calls = %d, want 1", provider.calls)
	}
}
```

- [ ] **Step 3.2: Run tests and verify failure**

Run:

```bash
go test ./tests/engine/ -run 'TestEngineRunsParallelSafe|TestEnginePreservesObservationOrder|TestEngineRunsSerialTools|TestEngineHonorsPerToolMaxConcurrency|TestEngineCancellationDuringParallelGroup' -v
```

Expected: `TestEngineRunsParallelSafeToolsConcurrently` fails under the existing sequential loop because elapsed time is above the threshold.

- [ ] **Step 3.3: Add engine concurrency settings and executor**

Create `internal/engine/tool_execution.go`:

```go
package engine

import (
	"context"
	"log"
	"sync"

	"github.com/snowshine0216/penelope-agent/internal/schema"
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
		if err := ctx.Err(); err != nil {
			close(jobs)
			wg.Wait()
			close(resultCh)
			return nil, err
		}
		jobs <- indexedToolCall{index: i, call: call}
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

func appendToolResultMessages(history []schema.Message, results []schema.ToolResult) []schema.Message {
	out := append([]schema.Message(nil), history...)
	for _, result := range results {
		out = append(out, toolResultMessage(result))
	}
	return out
}
```

- [ ] **Step 3.4: Add engine cap field**

Edit `internal/engine/loop.go`. Add this field to `AgentEngine` after `MaxTurns`:

```go
// MaxParallelToolCalls caps concurrently executing parallel-safe tools.
// 0 means use the default (4).
MaxParallelToolCalls int
```

Add this method at the bottom of `internal/engine/loop.go`:

```go
func (e *AgentEngine) toolGroupLimit(group []schema.ToolCall) int {
	limit := e.MaxParallelToolCalls
	if limit <= 0 {
		limit = defaultParallelToolConcurrency
	}

	for _, call := range group {
		policy := e.registry.ExecutionPolicyFor(call)
		if !policy.ParallelSafe || policy.MaxConcurrency <= 0 {
			continue
		}
		if policy.MaxConcurrency < limit {
			limit = policy.MaxConcurrency
		}
	}

	return boundedWorkerCount(limit, len(group))
}
```

- [ ] **Step 3.5: Integrate grouped execution into the loop**

In `internal/engine/loop.go`, replace the existing sequential loop:

```go
for _, toolCall := range actionResp.ToolCalls {
	if err := ctx.Err(); err != nil {
		return err
	}
	log.Printf("[engine] executing tool=%s args=%s", toolCall.Name, string(toolCall.Arguments))

	result := e.registry.Execute(ctx, toolCall)

	if result.IsError {
		log.Printf("[engine] tool error: %s", result.Output)
	} else {
		log.Printf("[engine] tool ok: %d bytes", len(result.Output))
	}

	// Append the tool result as an observation for the next turn.
	observationMsg := schema.Message{
		Role:       schema.RoleTool,
		Content:    result.Output,
		ToolCallID: toolCall.ID,
		IsError:    result.IsError,
	}
	contextHistory = append(contextHistory, observationMsg)
}
```

with:

```go
groups := PlanToolCallGroups(actionResp.ToolCalls, e.registry.ExecutionPolicyFor)
for _, group := range groups {
	if err := ctx.Err(); err != nil {
		return err
	}

	results, err := executeToolCallGroup(ctx, e.registry, group, e.toolGroupLimit(group))
	if err != nil {
		return err
	}

	contextHistory = appendToolResultMessages(contextHistory, results)
}
```

- [ ] **Step 3.6: Run targeted engine tests**

Run:

```bash
go test ./tests/engine/ -run 'TestEngineRunsParallelSafe|TestEnginePreservesObservationOrder|TestEngineRunsSerialTools|TestEngineHonorsPerToolMaxConcurrency|TestEngineCancellationDuringParallelGroup|TestEngineExecutesAllParallelToolCalls|TestEnginePropagatesToolResultIntoNextContext|TestEnginePropagatesToolErrorFlagToNextContext' -v
```

Expected: PASS.

- [ ] **Step 3.7: Commit Task 3**

Run:

```bash
git add internal/engine/loop.go internal/engine/tool_execution.go tests/engine/parallel_tool_execution_test.go
git commit -m "feat(engine): execute safe tool calls concurrently"
```

---

## Task 4: Documentation and full verification

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 4.1: Update README behavior description**

Edit `README.md`. In the "What it does" numbered list, replace item 4:

```markdown
4. Runs an action phase where the model can call tools.
```

with:

```markdown
4. Runs an action phase where the model can call tools; parallel-safe
   tool calls in the same turn may run concurrently while mutating or
   unknown tools remain serial.
```

Add this paragraph after the Tools table:

```markdown
Tool calls requested in the same assistant message are executed in
ordered groups. `read_file` opts into parallel execution; `bash`,
`write_file`, `edit_file`, and unknown tools stay serial. Tool results
are appended to model history in the original request order, not
completion order.
```

- [ ] **Step 4.2: Update CHANGELOG**

Edit `CHANGELOG.md`. Under `## [Unreleased]`, add:

```markdown
### Added
- Parallel-safe tool execution in the engine: consecutive safe tool
  calls can run concurrently with deterministic observation ordering,
  an engine-wide concurrency cap, and conservative serial fallback for
  mutating or unknown tools. `read_file` is the first parallel-safe
  built-in tool.
```

- [ ] **Step 4.3: Run full test suite**

Run:

```bash
go test ./...
```

Expected: all packages PASS.

- [ ] **Step 4.4: Run race detector on engine and tools tests**

Run:

```bash
go test -race ./tests/engine/ ./tests/tools/
```

Expected: all tests PASS with no race reports.

- [ ] **Step 4.5: Review diff**

Run:

```bash
git diff --stat
git diff -- internal/engine internal/tools tests/engine tests/tools README.md CHANGELOG.md
```

Expected: diff only contains the files listed in this plan, with no provider changes and no unrelated formatting churn.

- [ ] **Step 4.6: Commit Task 4**

Run:

```bash
git add README.md CHANGELOG.md
git commit -m "docs: document parallel tool execution"
```

---

## Final Verification

- [ ] Run:

```bash
go test ./...
go test -race ./tests/engine/ ./tests/tools/
```

Expected: both commands PASS.

- [ ] Run:

```bash
git status --short
```

Expected: only unrelated pre-existing untracked files may remain, currently `AGENTS.md` and `server.go`.

- [ ] Summarize the implementation with:

```text
Implemented parallel-safe tool execution with registry-owned scheduling policy, ordered grouping, bounded concurrent execution, deterministic observation ordering, and tests for serial safety, concurrency caps, and cancellation.
```
