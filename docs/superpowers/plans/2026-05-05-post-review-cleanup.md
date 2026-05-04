# Post-Review Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resolve 22 review findings across penelope-agent via 5 unit commits.

**Architecture:** Five focused commits, one per thematic unit (provider correctness, engine robustness, truncation/sandboxing, config defaults, CLI/UX). TDD inside each unit — tests added or modified before code, then implementation. Tests live in `tests/<package>/` external test packages. After each unit: `go test ./...` must be clean before commit.

**Tech Stack:** Go 1.26 (toolchain), `github.com/anthropics/anthropic-sdk-go`, `github.com/openai/openai-go/v3`, `github.com/joho/godotenv`, stdlib `testing`.

**Spec:** `docs/superpowers/specs/2026-05-05-post-review-cleanup-design.md`

---

## File Structure

**New files:**
- `internal/tools/truncate.go` — UTF-8-safe head+tail truncation helper.
- `internal/tools/safepath.go` — workdir-bounded path resolution helper.
- `tests/tools/truncate_test.go` — tests for `TruncateForLLM`.
- `tests/tools/safepath_test.go` — tests for `ResolveInWorkDir`.
- `tests/provider/translation_test.go` — tests for the new pure translation helpers (added in Unit 1).
- `README.md` — quickstart, env matrix, tool list, known limitations.

**Modified files:**
- `internal/schema/message.go` — adds `IsError`, `RoleTool`.
- `internal/provider/claude.go` — error propagation, `IsError` wiring, `extractRequiredStrings`, no-drop empty messages, `MaxTokens`, constructor returns error.
- `internal/provider/openai.go` — error propagation, unknown-tool-type error, constructor returns error.
- `internal/engine/loop.go` — `MaxTurns`, ctx cancellation, `RoleTool` for tool results, `IsError` propagation.
- `internal/tools/registry.go` — sort `GetAvailableTools`, rename `BaseTool` → `Tool`.
- `internal/tools/bash.go` — use `TruncateForLLM`, optional `timeout_s`, log every command, English logs.
- `internal/tools/read_file.go` — use `ResolveInWorkDir`, use `TruncateForLLM`, optional `offset`/`limit`.
- `internal/tools/write_file.go` — use `ResolveInWorkDir`.
- `internal/provider/config.go` — switch default base URL to Zhipu's `bigmodel.cn`.
- `cmd/claw/main.go` — `flag` package, error handling, English logs.
- `tests/provider/config_test.go` — assert new default URL value.
- `tests/tools/read_file_test.go` — flip path-traversal test to expect error.
- `tests/tools/write_file_test.go` — flip path-traversal test to expect error.
- `tests/tools/registry_test.go` — sort assertion, type rename.

**Deleted files:**
- `hello.txt`, `helloworld.go` (root-level demo artifacts from the original fork).

---

## Task 1: Unit 1 — Provider correctness

**Files:**
- Modify: `internal/schema/message.go`
- Modify: `internal/provider/claude.go`
- Modify: `internal/provider/openai.go`
- Modify: `internal/engine/loop.go`
- Modify: `cmd/claw/main.go`
- Create: `tests/provider/translation_test.go`

**Goal:** Fix B1 (tool error flag), B8 (silent JSON errors), B9 (`required` assertion), B10 (empty assistant message dropped), B12 (non-function tool calls dropped), I14 (constructor panic), I18 (MaxTokens hardcoded). All in one commit.

### Step 1.1: Add `IsError` field to `schema.Message`

- [ ] **Edit `internal/schema/message.go`** — add `IsError` field after `ToolCallID`:

```go
// Message represents a single message in the context.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`

	// Populated when the assistant requests tool calls (supports parallel calls).
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// Populated on tool result messages so the model can correlate them with
	// the originating call.
	ToolCallID string `json:"tool_call_id,omitempty"`

	// IsError marks a tool result as a failure. Surfaced to providers that
	// support a structured error flag (Anthropic). Ignored by providers that
	// don't (OpenAI relies on text content).
	IsError bool `json:"is_error,omitempty"`
}
```

### Step 1.2: Write failing test for `IsError` propagation

- [ ] **Edit `tests/engine/loop_test.go`** — add this test below the existing tests:

```go
func TestEnginePropagatesToolErrorFlagToNextContext(t *testing.T) {
	tool := &recordingTool{name: "boom", err: errors.New("kaboom")}
	registry := tools.NewRegistry()
	registry.Register(tool)

	provider := &fakeProvider{
		responses: []schema.Message{
			{
				Role: schema.RoleAssistant,
				ToolCalls: []schema.ToolCall{
					{ID: "x", Name: "boom", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Role: schema.RoleAssistant, Content: "saw the error"},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	if err := eng.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(provider.receivedMsgs) < 2 {
		t.Fatalf("expected provider called twice, got %d", len(provider.receivedMsgs))
	}
	second := provider.receivedMsgs[1]
	var found *schema.Message
	for i := range second {
		if second[i].ToolCallID == "x" {
			found = &second[i]
			break
		}
	}
	if found == nil {
		t.Fatal("tool result message not found in second turn context")
	}
	if !found.IsError {
		t.Fatalf("expected IsError=true on failed tool result, got false")
	}
}
```

- [ ] **Run the test, expect it to fail:**
```bash
go test ./tests/engine/ -run TestEnginePropagatesToolErrorFlagToNextContext -v
```
Expected: FAIL — engine sets `IsError: false` (actually doesn't set it).

### Step 1.3: Wire `IsError` through the engine

- [ ] **Edit `internal/engine/loop.go`** — modify the observation message at the bottom of the loop to include `IsError`:

```go
// Append the tool's observation to context for the next turn.
observationMsg := schema.Message{
	Role:       schema.RoleTool, // changed from RoleUser; see Unit 2 also
	Content:    result.Output,
	ToolCallID: toolCall.ID,
	IsError:    result.IsError,
}
contextHistory = append(contextHistory, observationMsg)
```

Note: `schema.RoleTool` is added in Unit 2. For Unit 1, use `schema.RoleUser` and only add the `IsError: result.IsError` line. The role rename happens in Unit 2.

Concretely for Unit 1, the change is:

```go
observationMsg := schema.Message{
	Role:       schema.RoleUser,
	Content:    result.Output,
	ToolCallID: toolCall.ID,
	IsError:    result.IsError,
}
```

- [ ] **Run the test, expect it to pass:**
```bash
go test ./tests/engine/ -run TestEnginePropagatesToolErrorFlagToNextContext -v
```
Expected: PASS.

### Step 1.4: Wire `IsError` into Claude provider's tool-result block

- [ ] **Edit `internal/provider/claude.go`** — find the `RoleUser + ToolCallID` branch (around line 46-49) and pass `IsError`:

```go
case schema.RoleUser:
	if msg.ToolCallID != "" {
		anthropicMsgs = append(anthropicMsgs, anthropic.NewUserMessage(
			anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, msg.IsError),
		))
	} else {
		anthropicMsgs = append(anthropicMsgs, anthropic.NewUserMessage(
			anthropic.NewTextBlock(msg.Content),
		))
	}
```

The third arg `msg.IsError` was hardcoded to `false`.

### Step 1.5: Extract `extractRequiredStrings` helper for robust required-field decoding

- [ ] **Edit `internal/provider/claude.go`** — add this helper at the bottom of the file:

```go
// extractRequiredStrings reads a JSON-Schema "required" value from an
// untyped slot. Tools build the schema with a []string literal, but any
// schema that round-trips through JSON arrives as []interface{}. Handle
// both shapes.
func extractRequiredStrings(v interface{}) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []interface{}:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}
```

- [ ] **Edit `internal/provider/claude.go`** — replace the brittle assertion in the tool schema translation block:

Old:
```go
if r, ok := m["required"].([]string); ok {
	required = r
}
```

New:
```go
required = extractRequiredStrings(m["required"])
```

### Step 1.6: Test `extractRequiredStrings`

- [ ] **Create `tests/provider/translation_test.go`:**

```go
package provider_test

import (
	"reflect"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/provider"
)

// TestExtractRequiredStringsAcceptsTypedSlice confirms the helper handles the
// shape produced by literal Go schemas (e.g. []string{"command"}).
func TestExtractRequiredStringsAcceptsTypedSlice(t *testing.T) {
	got := provider.ExtractRequiredStrings([]string{"a", "b"})
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestExtractRequiredStringsAcceptsInterfaceSlice covers schemas that have
// round-tripped through JSON and arrive as []interface{}.
func TestExtractRequiredStringsAcceptsInterfaceSlice(t *testing.T) {
	got := provider.ExtractRequiredStrings([]interface{}{"a", "b"})
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtractRequiredStringsSkipsNonStrings(t *testing.T) {
	got := provider.ExtractRequiredStrings([]interface{}{"a", 42, "b"})
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtractRequiredStringsReturnsNilForOtherTypes(t *testing.T) {
	if got := provider.ExtractRequiredStrings(nil); got != nil {
		t.Fatalf("nil input got %v, want nil", got)
	}
	if got := provider.ExtractRequiredStrings("not-a-slice"); got != nil {
		t.Fatalf("string input got %v, want nil", got)
	}
}
```

- [ ] **Edit `internal/provider/claude.go`** — rename the helper from `extractRequiredStrings` to `ExtractRequiredStrings` (export it) so the test can reach it. Update the call site in `Generate`.

- [ ] **Run translation tests:**
```bash
go test ./tests/provider/ -run TestExtractRequiredStrings -v
```
Expected: 4 PASS.

### Step 1.7: Stop dropping empty assistant messages

- [ ] **Edit `internal/provider/claude.go`** — find the `RoleAssistant` branch and remove the `if len(blocks) > 0` guard:

Old:
```go
if len(blocks) > 0 {
	anthropicMsgs = append(anthropicMsgs, anthropic.NewAssistantMessage(blocks...))
}
```

New:
```go
if len(blocks) == 0 {
	// Anthropic requires at least one content block per assistant message.
	// Insert an empty text block to keep history contiguous.
	blocks = append(blocks, anthropic.NewTextBlock(""))
}
anthropicMsgs = append(anthropicMsgs, anthropic.NewAssistantMessage(blocks...))
```

### Step 1.8: Propagate JSON unmarshal errors in Claude provider

- [ ] **Edit `internal/provider/claude.go`** — find the tool-call translation in the `RoleAssistant` branch and propagate the error:

Old:
```go
for _, tc := range msg.ToolCalls {
	var inputMap map[string]interface{}
	_ = json.Unmarshal(tc.Arguments, &inputMap)
	blocks = append(blocks, anthropic.ContentBlockParamUnion{
		OfToolUse: &anthropic.ToolUseBlockParam{
			ID:    tc.ID,
			Name:  tc.Name,
			Input: inputMap,
		},
	})
}
```

New:
```go
for _, tc := range msg.ToolCalls {
	var inputMap map[string]interface{}
	if err := json.Unmarshal(tc.Arguments, &inputMap); err != nil {
		return nil, fmt.Errorf("decode tool call %s arguments: %w", tc.Name, err)
	}
	blocks = append(blocks, anthropic.ContentBlockParamUnion{
		OfToolUse: &anthropic.ToolUseBlockParam{
			ID:    tc.ID,
			Name:  tc.Name,
			Input: inputMap,
		},
	})
}
```

- [ ] **Edit `internal/provider/claude.go`** — find the response-parsing block at the bottom and propagate the marshal error:

Old:
```go
case "tool_use":
	argsBytes, _ := json.Marshal(block.Input)
	resultMsg.ToolCalls = append(resultMsg.ToolCalls, schema.ToolCall{
		ID:        block.ID,
		Name:      block.Name,
		Arguments: argsBytes,
	})
```

New:
```go
case "tool_use":
	argsBytes, err := json.Marshal(block.Input)
	if err != nil {
		return nil, fmt.Errorf("encode tool call %s input: %w", block.Name, err)
	}
	resultMsg.ToolCalls = append(resultMsg.ToolCalls, schema.ToolCall{
		ID:        block.ID,
		Name:      block.Name,
		Arguments: argsBytes,
	})
```

### Step 1.9: Propagate JSON errors in OpenAI provider

- [ ] **Edit `internal/provider/openai.go`** — find the schema fallback in the tool translation block and propagate errors:

Old:
```go
if m, ok := toolDef.InputSchema.(map[string]interface{}); ok {
	params = shared.FunctionParameters(m)
} else {
	// fallback：JSON 往返序列化
	b, _ := json.Marshal(toolDef.InputSchema)
	_ = json.Unmarshal(b, &params)
}
```

New:
```go
if m, ok := toolDef.InputSchema.(map[string]interface{}); ok {
	params = shared.FunctionParameters(m)
} else {
	b, err := json.Marshal(toolDef.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("encode tool schema for %s: %w", toolDef.Name, err)
	}
	if err := json.Unmarshal(b, &params); err != nil {
		return nil, fmt.Errorf("decode tool schema for %s: %w", toolDef.Name, err)
	}
}
```

### Step 1.10: Error on unknown tool-call types in OpenAI response

- [ ] **Edit `internal/provider/openai.go`** — replace the silent skip:

Old:
```go
for _, tc := range choice.ToolCalls {
	if tc.Type == "function" {
		resultMsg.ToolCalls = append(resultMsg.ToolCalls, schema.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: []byte(tc.Function.Arguments),
		})
	}
}
```

New:
```go
for _, tc := range choice.ToolCalls {
	switch tc.Type {
	case "function":
		resultMsg.ToolCalls = append(resultMsg.ToolCalls, schema.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: []byte(tc.Function.Arguments),
		})
	default:
		return nil, fmt.Errorf("unsupported tool call type from OpenAI: %q", tc.Type)
	}
}
```

### Step 1.11: Constructors return error instead of panicking

- [ ] **Edit `internal/provider/claude.go`** — change signature:

Old:
```go
func NewZhipuClaudeProvider(model string) *ClaudeProvider {
	cfg, err := loadProviderConfig()
	if err != nil {
		panic(err)
	}
	if strings.TrimSpace(model) == "" {
		model = cfg.Model
	}
	return &ClaudeProvider{
		client: anthropic.NewClient(option.WithAPIKey(cfg.APIKey), option.WithBaseURL(cfg.BaseURL)),
		model:  model,
	}
}
```

New:
```go
func NewZhipuClaudeProvider(model string) (*ClaudeProvider, error) {
	cfg, err := loadProviderConfig()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(model) == "" {
		model = cfg.Model
	}
	return &ClaudeProvider{
		client:    anthropic.NewClient(option.WithAPIKey(cfg.APIKey), option.WithBaseURL(cfg.BaseURL)),
		model:     model,
		MaxTokens: 4096,
	}, nil
}
```

- [ ] **Edit `internal/provider/openai.go`** — same shape:

```go
func NewZhipuOpenAIProvider(model string) (*OpenAIProvider, error) {
	cfg, err := loadProviderConfig()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(model) == "" {
		model = cfg.Model
	}
	return &OpenAIProvider{
		client: openai.NewClient(option.WithAPIKey(cfg.APIKey), option.WithBaseURL(cfg.BaseURL)),
		model:  model,
	}, nil
}
```

(Function rename to drop `Zhipu` happens in Unit 5.)

### Step 1.12: Add `MaxTokens` field on `ClaudeProvider`

- [ ] **Edit `internal/provider/claude.go`** — extend the struct and use it:

```go
type ClaudeProvider struct {
	client    anthropic.Client
	model     string
	MaxTokens int64
}
```

In `Generate`, replace the hardcoded value:

Old:
```go
params := anthropic.MessageNewParams{
	Model:     anthropic.Model(p.model),
	MaxTokens: 4096,
	Messages:  anthropicMsgs,
}
```

New:
```go
maxTokens := p.MaxTokens
if maxTokens == 0 {
	maxTokens = 4096
}
params := anthropic.MessageNewParams{
	Model:     anthropic.Model(p.model),
	MaxTokens: maxTokens,
	Messages:  anthropicMsgs,
}
```

### Step 1.13: Update main.go to handle constructor error

- [ ] **Edit `cmd/claw/main.go`** — change the constructor call:

Old:
```go
llmProvider := provider.NewZhipuOpenAIProvider("")
```

New:
```go
llmProvider, err := provider.NewZhipuOpenAIProvider("")
if err != nil {
	log.Fatalf("init provider: %v", err)
}
```

### Step 1.14: Verify Unit 1 — full build and full test suite

- [ ] **Run:**
```bash
go build ./...
```
Expected: no output (success).

- [ ] **Run:**
```bash
go test ./...
```
Expected: all packages pass (provider, tools, schema, engine).

### Step 1.15: Commit Unit 1

- [ ] **Run:**
```bash
git add internal/schema/message.go internal/provider/claude.go internal/provider/openai.go internal/engine/loop.go cmd/claw/main.go tests/engine/loop_test.go tests/provider/translation_test.go
git commit -m "fix(provider): tool error flag, JSON propagation, MaxTokens, constructor errors

- Add IsError to schema.Message; engine sets it from ToolResult.IsError;
  Claude passes it to NewToolResultBlock so the model gets structured
  failure signal instead of seeing every tool as successful.
- Replace silent _ = json.Unmarshal/Marshal with error returns at six
  sites in claude.go and openai.go.
- Extract ExtractRequiredStrings helper to handle both []string (literal
  schemas) and []interface{} (JSON-decoded schemas).
- Stop dropping assistant messages with no content+tools; insert an
  empty text block to keep history contiguous for Anthropic.
- Error loudly when OpenAI returns an unknown tool call type instead of
  silently skipping it.
- Constructors return error instead of panic; main.go handles it.
- MaxTokens is now a field on ClaudeProvider with a 4096 default."
```

---

## Task 2: Unit 2 — Engine robustness

**Files:**
- Modify: `internal/schema/message.go` (add `RoleTool`)
- Modify: `internal/engine/loop.go` (turn limit, ctx cancel, RoleTool)
- Modify: `internal/tools/registry.go` (sort tools)
- Modify: `internal/provider/openai.go` (handle RoleTool in case)
- Modify: `internal/provider/claude.go` (handle RoleTool in case)
- Modify: `tests/engine/loop_test.go` (add new tests)
- Modify: `tests/tools/registry_test.go` (add sort assertion)

**Goal:** Fix B6 (no turn limit), B7 (ctx ignored), B11 (non-deterministic tool order), I17 (wrong role for tool results). All in one commit.

### Step 2.1: Add `RoleTool` to schema

- [ ] **Edit `internal/schema/message.go`** — add a constant:

```go
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool" // tool execution result, correlated by ToolCallID
)
```

### Step 2.2: Engine emits tool results with `RoleTool`

- [ ] **Edit `internal/engine/loop.go`** — change the observation message role:

```go
observationMsg := schema.Message{
	Role:       schema.RoleTool, // was RoleUser
	Content:    result.Output,
	ToolCallID: toolCall.ID,
	IsError:    result.IsError,
}
```

### Step 2.3: Providers handle `RoleTool` branch

- [ ] **Edit `internal/provider/claude.go`** — find the message translation switch and add `RoleTool` as its own case:

```go
case schema.RoleTool:
	anthropicMsgs = append(anthropicMsgs, anthropic.NewUserMessage(
		anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, msg.IsError),
	))
case schema.RoleUser:
	anthropicMsgs = append(anthropicMsgs, anthropic.NewUserMessage(
		anthropic.NewTextBlock(msg.Content),
	))
```

(Drop the `if msg.ToolCallID != ""` branch from `RoleUser` — `RoleTool` covers it now.)

- [ ] **Edit `internal/provider/openai.go`** — same shape:

```go
case schema.RoleTool:
	openaiMsgs = append(openaiMsgs, openai.ToolMessage(msg.Content, msg.ToolCallID))
case schema.RoleUser:
	openaiMsgs = append(openaiMsgs, openai.UserMessage(msg.Content))
```

(Drop the `if msg.ToolCallID != ""` branch from `RoleUser`.)

### Step 2.4: Update existing engine tests for the new role

- [ ] **Edit `tests/engine/loop_test.go`** — in `TestEnginePropagatesToolResultIntoNextContext` and `TestEnginePropagatesToolErrorFlagToNextContext`, the search for the tool result message currently looks for `m.ToolCallID == "..."`. That still works, but to be precise, also assert the role:

In `TestEnginePropagatesToolResultIntoNextContext`, change:
```go
if m.ToolCallID == "abc" && m.Content == "tool-output-marker" {
	found = true
	break
}
```
to:
```go
if m.Role == schema.RoleTool && m.ToolCallID == "abc" && m.Content == "tool-output-marker" {
	found = true
	break
}
```

In `TestEnginePropagatesToolErrorFlagToNextContext`, change the loop to:
```go
for i := range second {
	if second[i].Role == schema.RoleTool && second[i].ToolCallID == "x" {
		found = &second[i]
		break
	}
}
```

### Step 2.5: Write failing test for turn limit

- [ ] **Edit `tests/engine/loop_test.go`** — add this test, plus extend `fakeProvider` so it doesn't run out of responses (loop forever returning the same response):

First, add a helper response-cycler. Then the test:

```go
func TestEngineStopsAtMaxTurns(t *testing.T) {
	// Provider always asks for another tool call; without a turn cap the
	// engine would loop forever.
	tool := &recordingTool{name: "noop", output: "ok"}
	registry := tools.NewRegistry()
	registry.Register(tool)

	provider := &loopingProvider{
		response: schema.Message{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{
				{ID: "x", Name: "noop", Arguments: json.RawMessage(`{}`)},
			},
		},
	}

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	eng.MaxTurns = 3

	err := eng.Run(context.Background(), "loop forever")
	if err == nil {
		t.Fatal("expected MaxTurns error, got nil")
	}
	if provider.calls > 4 { // small slack for thinking-mode-off
		t.Fatalf("expected ~3 calls, got %d", provider.calls)
	}
}

// loopingProvider returns the same canned response indefinitely.
type loopingProvider struct {
	response schema.Message
	calls    int
}

func (l *loopingProvider) Generate(_ context.Context, _ []schema.Message, _ []schema.ToolDefinition) (*schema.Message, error) {
	l.calls++
	r := l.response
	return &r, nil
}
```

- [ ] **Run, expect FAIL:**
```bash
go test ./tests/engine/ -run TestEngineStopsAtMaxTurns -v
```
Expected: FAIL or test timeout (engine has no MaxTurns yet).

### Step 2.6: Implement `MaxTurns`

- [ ] **Edit `internal/engine/loop.go`** — add field and sentinel error:

```go
// ErrMaxTurnsExceeded is returned by Run when the engine hits the per-run
// turn cap before the model stops requesting tool calls.
var ErrMaxTurnsExceeded = errors.New("agent engine exceeded MaxTurns")

type AgentEngine struct {
	provider provider.LLMProvider
	registry tools.Registry

	WorkDir        string
	EnableThinking bool

	// MaxTurns caps the number of model turns per Run. 0 means use the
	// default (25). A turn covers thinking-phase + action-phase + tool exec.
	MaxTurns int
}
```

Add `errors` to imports.

- [ ] **Edit `internal/engine/loop.go`** — in `Run`, enforce the cap at the top of each turn:

```go
const defaultMaxTurns = 25

func (e *AgentEngine) Run(ctx context.Context, userPrompt string) error {
	maxTurns := e.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}
	// ... existing setup ...

	for {
		turnCount++
		if turnCount > maxTurns {
			return ErrMaxTurnsExceeded
		}
		// ... existing turn body ...
	}
}
```

- [ ] **Run the test, expect PASS:**
```bash
go test ./tests/engine/ -run TestEngineStopsAtMaxTurns -v
```

### Step 2.7: Write failing test for context cancellation

- [ ] **Edit `tests/engine/loop_test.go`** — add:

```go
func TestEngineHonorsContextCancellation(t *testing.T) {
	// Provider would loop forever; cancellation should break us out.
	tool := &recordingTool{name: "noop", output: "ok"}
	registry := tools.NewRegistry()
	registry.Register(tool)

	provider := &loopingProvider{
		response: schema.Message{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{
				{ID: "x", Name: "noop", Arguments: json.RawMessage(`{}`)},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before Run starts

	eng := engine.NewAgentEngine(provider, registry, t.TempDir(), false)
	err := eng.Run(ctx, "go")
	if err == nil {
		t.Fatal("expected ctx error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected ctx.Canceled, got %v", err)
	}
}
```

- [ ] **Run, expect FAIL:**
```bash
go test ./tests/engine/ -run TestEngineHonorsContextCancellation -v
```

### Step 2.8: Implement context cancellation

- [ ] **Edit `internal/engine/loop.go`** — at the top of each turn body and after each tool execution, check `ctx.Err()`:

```go
for {
	if err := ctx.Err(); err != nil {
		return err
	}
	turnCount++
	if turnCount > maxTurns {
		return ErrMaxTurnsExceeded
	}
	// ... existing turn body up through tool execution ...

	for _, toolCall := range actionResp.ToolCalls {
		if err := ctx.Err(); err != nil {
			return err
		}
		// ... existing tool execution ...
	}
}
```

- [ ] **Run, expect PASS:**
```bash
go test ./tests/engine/ -run TestEngineHonorsContextCancellation -v
```

### Step 2.9: Write failing test for deterministic tool ordering

- [ ] **Edit `tests/tools/registry_test.go`** — add:

```go
func TestRegistryGetAvailableToolsReturnsSortedOrder(t *testing.T) {
	r := tools.NewRegistry()
	// Register out of alphabetical order.
	r.Register(newFake("zeta", "", okExec("z")))
	r.Register(newFake("alpha", "", okExec("a")))
	r.Register(newFake("mu", "", okExec("m")))

	defs := r.GetAvailableTools()
	if len(defs) != 3 {
		t.Fatalf("len = %d, want 3", len(defs))
	}
	for i, want := range []string{"alpha", "mu", "zeta"} {
		if defs[i].Name != want {
			t.Errorf("defs[%d] = %q, want %q (sorted)", i, defs[i].Name, want)
		}
	}
}
```

- [ ] **Run, expect FAIL** (map iteration is random; will fail nondeterministically — run a few times):
```bash
go test ./tests/tools/ -run TestRegistryGetAvailableToolsReturnsSortedOrder -count=10 -v
```

### Step 2.10: Sort tools in `GetAvailableTools`

- [ ] **Edit `internal/tools/registry.go`** — add `sort` to imports and sort the result:

```go
func (r *registryImpl) GetAvailableTools() []schema.ToolDefinition {
	defs := make([]schema.ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		defs = append(defs, tool.Definition())
	}
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})
	return defs
}
```

- [ ] **Run, expect PASS:**
```bash
go test ./tests/tools/ -run TestRegistryGetAvailableToolsReturnsSortedOrder -count=10 -v
```

### Step 2.11: Verify Unit 2 — full suite

- [ ] **Run:**
```bash
go build ./... && go test ./...
```
Expected: all green.

### Step 2.12: Commit Unit 2

- [ ] **Run:**
```bash
git add internal/schema/message.go internal/engine/loop.go internal/tools/registry.go internal/provider/claude.go internal/provider/openai.go tests/engine/loop_test.go tests/tools/registry_test.go
git commit -m "fix(engine): turn limit, ctx cancellation, deterministic tools, RoleTool

- AgentEngine.MaxTurns caps runs at 25 by default; returns
  ErrMaxTurnsExceeded so callers can distinguish from provider failures.
- Honor ctx.Done() at the top of each turn and after each tool exec so
  Ctrl-C actually stops the loop.
- registry.GetAvailableTools sorts by name. Map iteration randomness
  was breaking prompt-cache hits and reproducibility.
- New schema.RoleTool replaces the RoleUser overload for tool results.
  Both providers handle it explicitly; the wire format is unchanged."
```

---

## Task 3: Unit 3 — Truncation + tool sandboxing

**Files:**
- Create: `internal/tools/truncate.go`
- Create: `internal/tools/safepath.go`
- Create: `tests/tools/truncate_test.go`
- Create: `tests/tools/safepath_test.go`
- Modify: `internal/tools/bash.go`
- Modify: `internal/tools/read_file.go`
- Modify: `internal/tools/write_file.go`
- Modify: `tests/tools/read_file_test.go` (flip path-traversal test)
- Modify: `tests/tools/write_file_test.go` (flip path-traversal test)

**Goal:** Fix B3 (UTF-8 mid-rune cut + head-only), B4 (file path traversal), B5 (bash logging), I15 (bash timeout configurable), I16 (truncation duplication, file pagination). All in one commit.

### Step 3.1: Write failing tests for `TruncateForLLM`

- [ ] **Create `tests/tools/truncate_test.go`:**

```go
package tools_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestTruncateShorterThanLimitReturnsAsIs(t *testing.T) {
	got := tools.TruncateForLLM("hello", 100)
	if got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestTruncateExactlyAtLimitReturnsAsIs(t *testing.T) {
	s := strings.Repeat("a", 100)
	got := tools.TruncateForLLM(s, 100)
	if got != s {
		t.Fatalf("expected unchanged at exact limit, got len %d", len(got))
	}
}

func TestTruncateLongInputProducesHeadAndTail(t *testing.T) {
	s := strings.Repeat("a", 1000) + strings.Repeat("z", 1000)
	got := tools.TruncateForLLM(s, 200)
	if len(got) > 400 { // budget + marker overhead
		t.Fatalf("output too long: %d", len(got))
	}
	if !strings.Contains(got, "elided") {
		t.Fatalf("expected elision marker, got: %q", got)
	}
	if !strings.HasPrefix(got, "a") {
		t.Fatalf("expected head to be all 'a's, got prefix %q", got[:5])
	}
	if !strings.HasSuffix(got, "z") {
		t.Fatalf("expected tail to be all 'z's, got suffix %q", got[len(got)-5:])
	}
}

func TestTruncateProducesValidUTF8AtBoundary(t *testing.T) {
	// 中 is 3 bytes in UTF-8. Build a string whose head budget would
	// land mid-rune if naively sliced on bytes.
	s := strings.Repeat("中", 1000)
	got := tools.TruncateForLLM(s, 100)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated output is not valid UTF-8: %q", got)
	}
}

func TestTruncateZeroLimitReturnsMarkerOnly(t *testing.T) {
	got := tools.TruncateForLLM("abc", 0)
	if !strings.Contains(got, "elided") {
		t.Fatalf("expected elision marker on zero-budget input, got: %q", got)
	}
}
```

- [ ] **Run, expect FAIL** (helper doesn't exist):
```bash
go test ./tests/tools/ -run TestTruncate -v
```

### Step 3.2: Implement `TruncateForLLM`

- [ ] **Create `internal/tools/truncate.go`:**

```go
// Package tools — truncation helper.
package tools

import (
	"fmt"
	"unicode/utf8"
)

// TruncateForLLM returns s unchanged when it fits in maxBytes. Otherwise
// it returns roughly the first half and the last half of the budget joined
// by an elision marker, with both cuts backed up to the nearest valid
// UTF-8 rune boundary so the result is always valid UTF-8.
func TruncateForLLM(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes <= 0 {
		return fmt.Sprintf("...[%d bytes elided]...", len(s))
	}

	half := maxBytes / 2
	headEnd := safeRuneBoundaryDown(s, half)
	tailStart := safeRuneBoundaryUp(s, len(s)-half)

	if tailStart <= headEnd {
		// Budget too small to leave non-overlapping head + tail.
		tailStart = headEnd
	}

	elided := tailStart - headEnd
	return s[:headEnd] +
		fmt.Sprintf("\n\n...[%d bytes elided of %d total]...\n\n", elided, len(s)) +
		s[tailStart:]
}

// safeRuneBoundaryDown returns the largest index <= max that lands on a
// UTF-8 rune boundary in s.
func safeRuneBoundaryDown(s string, max int) int {
	if max >= len(s) {
		return len(s)
	}
	for i := max; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return i
		}
	}
	return 0
}

// safeRuneBoundaryUp returns the smallest index >= min that lands on a
// UTF-8 rune boundary in s.
func safeRuneBoundaryUp(s string, min int) int {
	if min <= 0 {
		return 0
	}
	if min >= len(s) {
		return len(s)
	}
	for i := min; i < len(s); i++ {
		if utf8.RuneStart(s[i]) {
			return i
		}
	}
	return len(s)
}
```

- [ ] **Run, expect PASS:**
```bash
go test ./tests/tools/ -run TestTruncate -v
```

### Step 3.3: Write failing tests for `ResolveInWorkDir`

- [ ] **Create `tests/tools/safepath_test.go`:**

```go
package tools_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestResolveSimpleRelativePath(t *testing.T) {
	dir := t.TempDir()
	got, err := tools.ResolveInWorkDir(dir, "sub/file.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(dir, "sub", "file.txt")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveRejectsTraversalEscape(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := tools.ResolveInWorkDir(dir, "../escaped.txt")
	if err == nil {
		t.Fatal("expected ErrPathEscape, got nil")
	}
	if !errors.Is(err, tools.ErrPathEscape) {
		t.Fatalf("expected ErrPathEscape, got %v", err)
	}
}

func TestResolveRejectsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	_, err := tools.ResolveInWorkDir(dir, "/etc/passwd")
	if err == nil {
		t.Fatal("expected error for absolute path, got nil")
	}
}

func TestResolveAllowsCleanInternalPath(t *testing.T) {
	dir := t.TempDir()
	got, err := tools.ResolveInWorkDir(dir, "a/./b/../c.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, dir) {
		t.Fatalf("resolved path %q escapes workDir %q", got, dir)
	}
}
```

- [ ] **Run, expect FAIL:**
```bash
go test ./tests/tools/ -run TestResolve -v
```

### Step 3.4: Implement `ResolveInWorkDir`

- [ ] **Create `internal/tools/safepath.go`:**

```go
// Package tools — workdir-bounded path resolution.
package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrPathEscape signals that a relative path resolves outside the workdir.
var ErrPathEscape = errors.New("path escapes workdir")

// ResolveInWorkDir joins relPath onto workDir, resolves the result to an
// absolute path, and asserts that the result remains inside workDir.
// Absolute relPaths and paths that traverse outside via "../" are rejected
// with ErrPathEscape.
func ResolveInWorkDir(workDir, relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("%w: absolute path %q not allowed", ErrPathEscape, relPath)
	}

	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve workdir: %w", err)
	}

	joined := filepath.Join(absWorkDir, relPath)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	// Allow exact equality (workDir itself) plus anything underneath.
	sep := string(os.PathSeparator)
	if abs != absWorkDir && !strings.HasPrefix(abs, absWorkDir+sep) {
		return "", fmt.Errorf("%w: %q resolves outside %q", ErrPathEscape, relPath, absWorkDir)
	}

	return abs, nil
}
```

- [ ] **Run, expect PASS:**
```bash
go test ./tests/tools/ -run TestResolve -v
```

### Step 3.5: Wire `ResolveInWorkDir` into `read_file`

- [ ] **Edit `internal/tools/read_file.go`** — replace the `filepath.Join` line with a call to `ResolveInWorkDir`:

Old:
```go
fullPath := filepath.Join(t.workDir, input.Path)

file, err := os.Open(fullPath)
```

New:
```go
fullPath, err := ResolveInWorkDir(t.workDir, input.Path)
if err != nil {
	return "", fmt.Errorf("resolve path: %w", err)
}

file, err := os.Open(fullPath)
```

Also remove the placeholder comment about path traversal that the original author left.

### Step 3.6: Wire `TruncateForLLM` into `read_file`

- [ ] **Edit `internal/tools/read_file.go`** — replace the inline truncation block:

Old:
```go
const maxLen = 8000
if len(content) > maxLen {
	truncatedMsg := fmt.Sprintf("%s\n\n...[由于内容过长，已被系统截断至前 %d 字节]...", string(content[:maxLen]), maxLen)
	return truncatedMsg, nil
}

return string(content), nil
```

New:
```go
return TruncateForLLM(string(content), 8000), nil
```

### Step 3.7: Add optional `offset`/`limit` line params to `read_file`

- [ ] **Edit `internal/tools/read_file.go`** — extend args struct and definition:

```go
type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"` // 1-indexed line to start at
	Limit  int    `json:"limit,omitempty"`  // max lines to return; 0 = no cap
}
```

In `Definition`:

```go
InputSchema: map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"path": map[string]interface{}{
			"type":        "string",
			"description": "File path relative to the workspace, e.g. cmd/claw/main.go",
		},
		"offset": map[string]interface{}{
			"type":        "integer",
			"description": "Optional 1-indexed line number to start reading from",
		},
		"limit": map[string]interface{}{
			"type":        "integer",
			"description": "Optional max number of lines to return",
		},
	},
	"required": []string{"path"},
},
```

In `Execute`, after reading the file content, branch on offset/limit:

```go
text := string(content)
if input.Offset > 0 || input.Limit > 0 {
	text = sliceLines(text, input.Offset, input.Limit)
	return text, nil // line-sliced reads bypass byte truncation
}
return TruncateForLLM(text, 8000), nil
```

Add the helper at the bottom of the file:

```go
// sliceLines returns lines [offset, offset+limit). offset is 1-indexed;
// 0 means start from the first line. limit 0 means "no cap".
func sliceLines(s string, offset, limit int) string {
	lines := strings.Split(s, "\n")
	start := offset - 1
	if start < 0 {
		start = 0
	}
	if start >= len(lines) {
		return ""
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	return strings.Join(lines[start:end], "\n")
}
```

Add `"strings"` to the import block.

### Step 3.8: Wire `ResolveInWorkDir` into `write_file`

- [ ] **Edit `internal/tools/write_file.go`** — replace the join:

Old:
```go
fullPath := filepath.Join(t.workDir, input.Path)

if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
```

New:
```go
fullPath, err := ResolveInWorkDir(t.workDir, input.Path)
if err != nil {
	return "", fmt.Errorf("resolve path: %w", err)
}

if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
```

### Step 3.9: Wire `TruncateForLLM` into `bash`

- [ ] **Edit `internal/tools/bash.go`** — replace the inline truncation block:

Old:
```go
const maxLen = 8000
if len(outputStr) > maxLen {
	return fmt.Sprintf("%s\n\n...[终端输出过长，已截断至前 %d 字节]...", outputStr[:maxLen], maxLen), nil
}

return outputStr, nil
```

New:
```go
return TruncateForLLM(outputStr, 8000), nil
```

### Step 3.10: Add optional `timeout_s` arg to `bash`

- [ ] **Edit `internal/tools/bash.go`** — extend args:

```go
type bashArgs struct {
	Command   string `json:"command"`
	TimeoutS  int    `json:"timeout_s,omitempty"`
}
```

In `Definition`, add the property:

```go
"timeout_s": map[string]interface{}{
	"type":        "integer",
	"description": "Optional command timeout in seconds (default 30, max 600)",
},
```

In `Execute`, replace the hardcoded 30s:

```go
const defaultTimeout = 30 * time.Second
const maxTimeout = 600 * time.Second

timeout := defaultTimeout
if input.TimeoutS > 0 {
	timeout = time.Duration(input.TimeoutS) * time.Second
	if timeout > maxTimeout {
		timeout = maxTimeout
	}
}

timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
defer cancel()
```

### Step 3.11: Log every bash command

- [ ] **Edit `internal/tools/bash.go`** — add a log line right before `cmd.CombinedOutput`:

Add `"log"` to imports if missing.

```go
log.Printf("[bash] dir=%s cmd=%s", t.workDir, input.Command)
out, err := cmd.CombinedOutput()
```

### Step 3.12: Flip the path-traversal tests

- [ ] **Edit `tests/tools/read_file_test.go`** — replace the body of `TestReadFilePathTraversalCurrentlyEscapesWorkDir` and rename it:

```go
func TestReadFileRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tool := tools.NewReadFileTool(work)
	_, err := tool.Execute(context.Background(), readArgs(t, "../outside.txt"))
	if err == nil {
		t.Fatal("expected ErrPathEscape, got nil")
	}
	if !errors.Is(err, tools.ErrPathEscape) {
		t.Fatalf("expected ErrPathEscape, got %v", err)
	}
}
```

Add `"errors"` to imports if not present.

- [ ] **Edit `tests/tools/write_file_test.go`** — same shape:

```go
func TestWriteFileRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tool := tools.NewWriteFileTool(work)
	_, err := tool.Execute(context.Background(), writeArgs(t, "../escaped.txt", "leak"))
	if err == nil {
		t.Fatal("expected ErrPathEscape, got nil")
	}
	if !errors.Is(err, tools.ErrPathEscape) {
		t.Fatalf("expected ErrPathEscape, got %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(root, "escaped.txt")); !os.IsNotExist(statErr) {
		t.Fatal("escaped file should not have been written")
	}
}
```

Add `"errors"` to imports if not present.

### Step 3.13: Verify Unit 3 — full suite

- [ ] **Run:**
```bash
go build ./... && go test ./...
```

### Step 3.14: Commit Unit 3

- [ ] **Run:**
```bash
git add internal/tools/truncate.go internal/tools/safepath.go internal/tools/bash.go internal/tools/read_file.go internal/tools/write_file.go tests/tools/truncate_test.go tests/tools/safepath_test.go tests/tools/read_file_test.go tests/tools/write_file_test.go
git commit -m "fix(tools): UTF-8 truncation, path sandboxing, bash logging, file pagination

- TruncateForLLM: head + tail with elision marker, backed up to UTF-8
  rune boundaries so multibyte content (中文, emoji) never produces
  invalid UTF-8 in LLM context.
- ResolveInWorkDir: shared safepath helper rejects absolute paths and
  ../ traversal. read_file and write_file now reject path-escape with
  ErrPathEscape; the previously-known-bug tests are flipped to expect
  the rejection.
- bash: log every command before exec for auditability; optional
  timeout_s arg (default 30s, max 600s) replaces the hardcoded value.
- read_file: optional offset/limit args for line-based pagination of
  large files instead of always returning the head 8000 bytes."
```

---

## Task 4: Unit 4 — Config defaults

**Files:**
- Modify: `internal/provider/config.go`
- Modify: `tests/provider/config_test.go`

**Goal:** Fix B2 (default base URL ↔ model mismatch). One commit.

### Step 4.1: Update default base URL

- [ ] **Edit `internal/provider/config.go`** — change one line:

Old:
```go
const defaultProviderBaseURL = "https://api.minimaxi.com/v1/"
```

New:
```go
const defaultProviderBaseURL = "https://open.bigmodel.cn/api/paas/v4/"
```

(`defaultProviderModel` stays as `"glm-4.5-air"` — it now matches the URL.)

### Step 4.2: Update tests

- [ ] **Edit `tests/provider/config_test.go`** — `TestLoadConfigUsesDefaultBaseURL` and `TestLoadConfigSupportsLegacyZhipuAPIKey` reference `provider.DefaultBaseURL()`. They should keep passing with the new value because they read the default through the helper. Run them and confirm.

### Step 4.3: Verify Unit 4

- [ ] **Run:**
```bash
go test ./tests/provider/...
```

### Step 4.4: Commit Unit 4

- [ ] **Run:**
```bash
git add internal/provider/config.go
git commit -m "fix(config): align default base URL with default model

Default model is glm-4.5-air (Zhipu); default URL was
api.minimaxi.com (MiniMax). Switch the URL to Zhipu's
open.bigmodel.cn so the out-of-box config doesn't 404."
```

---

## Task 5: Unit 5 — CLI + UX

**Files:**
- Modify: `cmd/claw/main.go` (full rewrite with flags)
- Modify: `internal/tools/registry.go` (rename `BaseTool` → `Tool`)
- Modify: `internal/tools/bash.go`, `read_file.go`, `write_file.go` (interface name)
- Modify: `internal/tools/registry.go`, `internal/tools/bash.go`, `internal/engine/loop.go`, `internal/provider/*.go` (English log messages)
- Modify: `internal/provider/claude.go`, `openai.go` (rename constructors to drop `Zhipu`)
- Modify: `tests/tools/registry_test.go`, `tests/tools/bash_test.go` (Chinese-string assertions become English)
- Create: `README.md`
- Delete: `hello.txt`, `helloworld.go`

**Goal:** Fix I13, I19, I20, I21, I22, P23. One commit.

### Step 5.1: Rename `BaseTool` → `Tool` in registry

- [ ] **Edit `internal/tools/registry.go`** — rename the interface:

```go
// Tool is the interface every concrete tool must implement.
type Tool interface {
	Name() string
	Definition() schema.ToolDefinition
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}
```

Update the `Registry` interface signature:

```go
type Registry interface {
	Register(tool Tool)
	GetAvailableTools() []schema.ToolDefinition
	Execute(ctx context.Context, call schema.ToolCall) schema.ToolResult
}
```

Update `registryImpl`:

```go
type registryImpl struct {
	tools map[string]Tool
}

func NewRegistry() Registry {
	return &registryImpl{
		tools: make(map[string]Tool),
	}
}

func (r *registryImpl) Register(tool Tool) {
	// ...
}
```

The three concrete tools (`BashTool`, `ReadFileTool`, `WriteFileTool`) already satisfy the new `Tool` interface — no changes needed in `bash.go`, `read_file.go`, `write_file.go` for the rename.

### Step 5.2: Translate logs to English

- [ ] **Edit `internal/engine/loop.go`** — translate the user-facing log lines (keep Chinese in code comments):

Replace:
```go
log.Printf("[Engine] 引擎启动，锁定工作区: %s\n", e.WorkDir)
log.Printf("[Engine] 慢思考模式 (Thinking Phase): %v\n", e.EnableThinking)
```
with:
```go
log.Printf("[engine] starting, workdir=%s thinking=%v", e.WorkDir, e.EnableThinking)
```

Replace `========== [Turn N] 开始 ==========` with:
```go
log.Printf("[engine] turn %d", turnCount)
```

Replace the "剥夺工具访问权" / "恢复工具挂载" lines with:
```go
log.Println("[engine] phase=think tools=disabled")
// ...
log.Println("[engine] phase=act tools=enabled")
```

Replace `[内部思考 Trace]` and `[对外回复]` printf lines with English equivalents:
```go
fmt.Printf("[think] %s\n", thinkResp.Content)
// ...
fmt.Printf("[reply] %s\n", actionResp.Content)
```

Replace the rest of Chinese log strings with English.

- [ ] **Edit `internal/tools/registry.go`** — translate:

```go
log.Printf("[registry] tool %q already registered, overwriting", name)
log.Printf("[registry] mounted tool: %s", name)
```

And the error messages:
```go
errMsg := fmt.Sprintf("Error: tool %q not found", call.Name)
```

- [ ] **Edit `internal/tools/bash.go`** — translate (keep `[bash]` log line we added in Unit 3 as English):

```go
return outputStr + "\n[warning: command timed out (30s) and was killed]", nil
// ...
return fmt.Sprintf("execution error: %v\noutput:\n%s", err, outputStr), nil
// ...
return "command finished with no output.", nil
```

- [ ] **Edit `internal/provider/config.go`** — translate the error messages:

```go
return Config{}, fmt.Errorf("get working directory: %w", err)
// ...
return Config{}, fmt.Errorf("read %s: %w", dotEnvPath, readErr)
// ...
return Config{}, fmt.Errorf("stat %s: %w", dotEnvPath, err)
// ...
return Config{}, fmt.Errorf("set LLM_API_KEY in environment or .env (compatible: MINIMAX_API_KEY, ZHIPU_API_KEY)")
```

### Step 5.3: Update tests that asserted Chinese strings

- [ ] **Edit `tests/tools/bash_test.go`** — find tests that assert on Chinese strings:

`TestBashFailingCommandReturnsCombinedErrorAsValue` asserts `执行报错`. Change to `execution error`.

`TestBashEmptyOutputGetsExplicitMessage` asserts `无终端输出`. Change to `no output`.

`TestBashLongOutputIsTruncated` asserts `已截断` — but with the new `TruncateForLLM` (Unit 3) the marker is `elided`. Update the assertion to match `elided`.

(Unit 3 already changed truncate format; that test was updated then. This step covers any other Chinese string left in tests.)

- [ ] **Edit `tests/tools/read_file_test.go`** — `TestReadFileTruncatesLongContent` asserts `已被系统截断` — change to `elided`.

### Step 5.4: Rename provider constructors

- [ ] **Edit `internal/provider/claude.go`** — rename:

```go
func NewClaudeProvider(model string) (*ClaudeProvider, error) {
	// (body unchanged)
}
```

- [ ] **Edit `internal/provider/openai.go`** — rename:

```go
func NewOpenAIProvider(model string) (*OpenAIProvider, error) {
	// (body unchanged)
}
```

(main.go is rewritten in Step 5.5; that's where the new names get used.)

### Step 5.5: Rewrite `cmd/claw/main.go` with `flag`

- [ ] **Edit `cmd/claw/main.go`** — replace the whole file:

```go
// cmd/claw/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func main() {
	prompt := flag.String("prompt", "", "user prompt; if empty, read from stdin")
	think := flag.Bool("think", false, "enable thinking phase before each action")
	providerName := flag.String("provider", "openai", "provider: openai or claude")
	model := flag.String("model", "", "model id; defaults to LLM_MODEL env or provider default")
	maxTurns := flag.Int("max-turns", 25, "max engine turns per run")
	maxTokens := flag.Int("max-tokens", 4096, "max output tokens (claude only)")
	workDir := flag.String("workdir", "", "workspace root; defaults to cwd")
	flag.Parse()

	cwd := *workDir
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			log.Fatalf("get cwd: %v", err)
		}
	}

	userPrompt := *prompt
	if userPrompt == "" {
		stdin, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatalf("read stdin: %v", err)
		}
		userPrompt = string(stdin)
	}
	if userPrompt == "" {
		fmt.Fprintln(os.Stderr, "no prompt provided (use --prompt or pipe to stdin)")
		os.Exit(2)
	}

	llm, err := newProvider(*providerName, *model, *maxTokens)
	if err != nil {
		log.Fatalf("init provider: %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(cwd))
	registry.Register(tools.NewWriteFileTool(cwd))
	registry.Register(tools.NewBashTool(cwd))

	eng := engine.NewAgentEngine(llm, registry, cwd, *think)
	eng.MaxTurns = *maxTurns

	if err := eng.Run(context.Background(), userPrompt); err != nil {
		log.Fatalf("engine: %v", err)
	}
}

func newProvider(name, model string, maxTokens int) (provider.LLMProvider, error) {
	switch name {
	case "openai":
		return provider.NewOpenAIProvider(model)
	case "claude":
		p, err := provider.NewClaudeProvider(model)
		if err != nil {
			return nil, err
		}
		if maxTokens > 0 {
			p.MaxTokens = int64(maxTokens)
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unknown provider %q (supported: openai, claude)", name)
	}
}
```

### Step 5.6: Delete root demo artifacts

- [ ] **Run:**
```bash
git rm hello.txt helloworld.go
```

### Step 5.7: Write README

- [ ] **Create `README.md`** at the repo root:

```markdown
# penelope-agent

A small Go agent loop that drives an LLM through tool use to accomplish
coding tasks in a sandboxed workspace. Forked from `go-tiny-claw`.

## What it does

Given a prompt, the agent:
1. Loads provider config from environment / `.env`.
2. Mounts a tool registry (bash, read_file, write_file).
3. Optionally runs a "thinking" phase (no tools, plan only).
4. Runs an action phase where the model can call tools.
5. Loops on tool results until the model stops asking for tools or
   `--max-turns` is hit.

## Quickstart

```bash
git clone https://github.com/snowshine0216/penelope-agent.git
cd penelope-agent

cat > .env <<'EOF'
LLM_API_KEY=your-zhipu-api-key
LLM_BASE_URL=https://open.bigmodel.cn/api/paas/v4/
LLM_MODEL=glm-4.5-air
EOF

go run ./cmd/claw --prompt "list the files in this repo"
```

## Configuration

Config is read from environment variables, falling back to `.env` walked
upward from the current directory.

| Var | Default | Notes |
|-----|---------|-------|
| `LLM_API_KEY` | (required) | Compatible: `MINIMAX_API_KEY`, `ZHIPU_API_KEY` |
| `LLM_BASE_URL` | `https://open.bigmodel.cn/api/paas/v4/` | OpenAI-compatible endpoint |
| `LLM_MODEL` | `glm-4.5-air` | Zhipu GLM by default |

## Flags

```
--prompt string      user prompt; if empty, read from stdin
--think              enable a planning phase with tools disabled before each action
--provider string    "openai" or "claude" (default "openai")
--model string       overrides LLM_MODEL
--max-turns int      cap on engine turns per run (default 25)
--max-tokens int     max output tokens, claude only (default 4096)
--workdir string     workspace root; defaults to cwd
```

## Tools

| Tool | Description | Sandbox |
|------|-------------|---------|
| `bash` | Run a shell command in the workdir | **Unsandboxed.** Every command is logged. |
| `read_file` | Read a file in the workdir | Path traversal blocked. Optional `offset`/`limit` for line pagination. |
| `write_file` | Write a file in the workdir | Path traversal blocked. Creates parent dirs. |

## Project layout

```
cmd/claw/         CLI entry point
internal/
  engine/         agent loop (thinking + action phases, turn cap, ctx cancel)
  provider/       LLM provider interface, Claude + OpenAI/Zhipu adapters
  schema/         shared message types
  tools/          tool implementations (bash, read_file, write_file) + registry
tests/            external test packages mirroring internal/ structure
docs/             design specs and implementation plans
```

Tests live outside the source tree on purpose — they exercise the public
surface only, which keeps the public API intentional.

## Known limitations

- `bash` is intentionally unsandboxed. The model can run any shell
  command in the workdir. Use a dedicated VM or container for untrusted
  prompts.
- The engine has no automatic retry for provider failures.
- Only OpenAI-compatible (Zhipu / MiniMax) and Anthropic API endpoints
  are supported. No Gemini, no local model adapters yet.

## License

MIT.
```

### Step 5.8: Verify Unit 5 — full build and full test suite

- [ ] **Run:**
```bash
go build ./... && go test ./...
```
Expected: all green.

- [ ] **Smoke check the CLI:**
```bash
go run ./cmd/claw --help
```
Expected: flag descriptions render.

### Step 5.9: Commit Unit 5

- [ ] **Run:**
```bash
git add cmd/claw/main.go internal/tools/registry.go internal/engine/loop.go internal/tools/bash.go internal/provider/claude.go internal/provider/openai.go internal/provider/config.go tests/tools/bash_test.go tests/tools/read_file_test.go README.md
git rm hello.txt helloworld.go
git commit -m "feat(cli): flag-based main.go, README, English logs, Tool rename

- main.go: --prompt / stdin, --think, --provider, --model, --max-turns,
  --max-tokens, --workdir. Errors propagate via log.Fatalf instead of
  panics from constructors.
- Provider constructors renamed to drop Zhipu prefix (NewClaudeProvider,
  NewOpenAIProvider). Names describe wire protocol, not the upstream.
- BaseTool interface renamed to Tool.
- Internal logs translated to English; user-visible error messages in
  config.go also translated.
- README documents quickstart, env matrix, flags, tools, layout, and
  known limitations (bash unsandboxed by design).
- Removed hello.txt and helloworld.go demo artifacts left over from the
  go-tiny-claw fork."
```

---

## Final verification

After all five commits land:

- [ ] **Run:**
```bash
go build ./... && go test -v ./...
git log --oneline main..HEAD
```

Expected: build clean, tests green, six commits on the branch (the test reorg + the five unit commits).

- [ ] **Spot-check end-to-end (optional, requires `.env`):**
```bash
go run ./cmd/claw --prompt "echo hello via bash"
```

---

## Self-review (writer-of-plan, post-write)

**Spec coverage check:**
- B1 IsError flag → Step 1.1, 1.3, 1.4 ✓
- B2 default URL mismatch → Task 4 ✓
- B3 UTF-8 mid-rune cut → Step 3.1, 3.2 ✓
- B4 file path traversal → Step 3.3, 3.4, 3.5, 3.8 ✓
- B5 bash unsandboxed → Step 3.11 (logging) ✓
- B6 no turn limit → Step 2.5, 2.6 ✓
- B7 ctx ignored → Step 2.7, 2.8 ✓
- B8 silent JSON errors → Step 1.8, 1.9 ✓
- B9 required assertion → Step 1.5, 1.6 ✓
- B10 empty assistant message → Step 1.7 ✓
- B11 nondeterministic tool order → Step 2.9, 2.10 ✓
- B12 unknown tool type silently dropped → Step 1.10 ✓
- I13 main.go hardcoded → Step 5.5 ✓
- I14 constructor panic → Step 1.11 ✓
- I15 bash timeout hardcoded → Step 3.10 ✓
- I16 truncation duplication / file pagination → Step 3.2, 3.6, 3.7, 3.9 ✓
- I17 wrong role for tool results → Step 2.1, 2.2, 2.3, 2.4 ✓
- I18 MaxTokens hardcoded → Step 1.12 ✓
- I19 hello.txt + helloworld.go → Step 5.6 ✓
- I20 no README → Step 5.7 ✓
- I21 main.go duplicate prompt / numbering → Step 5.5 (full rewrite) ✓
- I22 mixed Chinese/English logs → Step 5.2 ✓
- P23 BaseTool naming → Step 5.1 ✓

All 23 items covered.

**Type/name consistency check:**
- `Config`, `LookupEnvFunc`, `LoadConfigFromDir` defined in tests already; plan does not redefine.
- `ExtractRequiredStrings` (exported) used in test and source consistently.
- `RoleTool`, `MaxTurns`, `ErrMaxTurnsExceeded`, `ErrPathEscape`, `TruncateForLLM`, `ResolveInWorkDir` — each defined once, referenced consistently.
- `NewClaudeProvider`/`NewOpenAIProvider` rename happens in Unit 5, after Unit 1 already added the error return on the old names. main.go gets one update in Unit 1 (handle error) and another in Unit 5 (new names).

**Placeholder scan:** none found.

**Order dependencies:** Unit 2 depends on Unit 1's `IsError` field. Unit 5 depends on Unit 4's URL change being mentioned in README. Other units are independent.
