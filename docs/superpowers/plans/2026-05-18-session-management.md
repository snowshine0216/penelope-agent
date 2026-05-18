# Per-Session Context History And Pluggable Trimming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist conversation history per session as JSONL under `.claw/sessions/<id>.jsonl`, allow resume via `--session <id>`, and bound what the model sees on every provider call through a pluggable `Trimmer` interface whose default (`WindowTrimmer`) keeps the last 6 user turns under a chars/4 token cap.

**Architecture:** Add `internal/session` as the owner of persistence, locking, ID generation, and trimming. Engine gains a `*session.Session` field and reads its canonical history from it; trimming is applied before every `provider.Generate` call. Concurrent writers are permitted at file-integrity level via per-append `syscall.Flock(LOCK_EX)`; coherence under contention is the trimmer's defensive responsibility (drops orphan tool results and dangling tool_calls).

**Tech Stack:** Go 1.26.2, existing `schema.Message` / `tools.Tool` / `provider.LLMProvider` interfaces, `crypto/rand`, `syscall.Flock` (POSIX-only), `go test ./...`.

**Spec deviation noted up front:** Spec D18 proposed `Run(ctx, reporter)` (dropping `userPrompt`). During planning we kept the parameter — main.go and tests still call `Run(ctx, userPrompt, reporter)` and the engine appends the prompt to its session as its first action. The session remains the single source of truth; this avoids churning ~25 existing engine test callsites for zero behavioral benefit.

---

## File Structure

**New files:**

- `internal/session/id.go` — pure `NewID()` returning `YYYYMMDD-HHMMSS-XXXXXX`; `IsValidID(string)`.
- `internal/session/tokens.go` — pure `EstimateTokens([]schema.Message) int` and `EstimateOne(schema.Message) int`.
- `internal/session/trim.go` — `Trimmer` interface, `TrimConfig` struct, `Register`/`Get` for the strategy registry.
- `internal/session/window.go` — `WindowTrimmer` (default strategy), exposes pure passes for testing.
- `internal/session/lock_posix.go` — `//go:build !windows`: `lockExclusive(fd uintptr) error`.
- `internal/session/lock_windows.go` — `//go:build windows`: no-op stub returning nil.
- `internal/session/session.go` — `Session` type, `Append`, `Messages`, `Close`, `ID`.
- `internal/session/store.go` — `NewSession(dir)`, `OpenSession(id, dir)`, JSONL parse, dir creation.
- `tests/session/id_test.go` — format, validation, uniqueness.
- `tests/session/tokens_test.go` — empty, ascii, mixed content, overhead.
- `tests/session/trim_window_test.go` — comprehensive table-driven tests for the three passes plus emergency-floor signaling.
- `tests/session/store_test.go` — filesystem round-trip, corrupt line, missing file, concurrent append, dir creation.
- `tests/engine/session_integration_test.go` — engine + session end-to-end with the existing `fakeProvider`.

**Modified files:**

- `internal/engine/loop.go` — add `session *session.Session` field, add `SetSession`, change `Run` to seed from session and trim before every `Generate`, persist act-phase assistant messages and tool results.
- `cmd/claw/main.go` — add `--session`, `--max-context-turns`, `--max-context-tokens`, `--trim-strategy`, `--sessions-dir` flags; open/create session; print ID to stderr.
- `README.md` — document session flag, sessions directory, trim flags, JSONL format, concurrent-writer caveat.
- `TODOS.md` — remove the "Cap context history" P2 entry (closed by this work).

**Package name:** the directory is `internal/session` and the Go package is `session`. The engine imports it with the alias `agentsession` to avoid any local-variable name collision: `agentsession "github.com/snowshine0216/penelope-agent/internal/session"`.

---

### Task 1: Generate Session IDs

**Files:**
- Create: `internal/session/id.go`
- Test: `tests/session/id_test.go`

- [ ] **Step 1: Write failing tests for the ID format and validator**

Create `tests/session/id_test.go`:

```go
package session_test

import (
	"regexp"
	"testing"
	"time"

	"github.com/snowshine0216/penelope-agent/internal/session"
)

var idRegex = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{6}$`)

func TestNewIDMatchesExpectedFormat(t *testing.T) {
	id := session.NewID(time.Date(2026, 5, 18, 9, 30, 45, 0, time.UTC))
	if !idRegex.MatchString(id) {
		t.Fatalf("id = %q, want match %s", id, idRegex)
	}
	if id[:15] != "20260518-093045" {
		t.Fatalf("id prefix = %q, want 20260518-093045", id[:15])
	}
}

func TestNewIDProducesDistinctSuffixes(t *testing.T) {
	now := time.Date(2026, 5, 18, 9, 30, 45, 0, time.UTC)
	a := session.NewID(now)
	b := session.NewID(now)
	if a == b {
		t.Fatalf("two consecutive ids identical: %q", a)
	}
}

func TestIsValidIDAcceptsCanonicalFormat(t *testing.T) {
	if !session.IsValidID("20260518-093045-a1b2c3") {
		t.Fatal("canonical id was rejected")
	}
}

func TestIsValidIDRejectsBadInput(t *testing.T) {
	cases := []string{
		"",
		"not-a-session-id",
		"20260518_093045_a1b2c3",        // wrong separator
		"20260518-093045-ABCDEF",        // uppercase hex
		"20260518-093045-a1b2c3-extra",  // trailing
		"20260518-093045-a1b2c",         // short suffix
		"../etc/passwd",                  // path traversal
		"20260518-093045-a1b2c3/extra",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			if session.IsValidID(input) {
				t.Fatalf("invalid id %q was accepted", input)
			}
		})
	}
}
```

- [ ] **Step 2: Run id tests and verify they fail**

Run:

```bash
go test ./tests/session -run TestNewID -count=1
go test ./tests/session -run TestIsValidID -count=1
```

Expected: FAIL because the `session` package does not exist.

- [ ] **Step 3: Implement the ID generator**

Create `internal/session/id.go`:

```go
package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

var idPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{6}$`)

// NewID returns a session id of the form YYYYMMDD-HHMMSS-XXXXXX, where
// XXXXXX is six lowercase hex chars from crypto/rand. The supplied time
// is formatted in UTC so two processes started at the same wall clock
// see the same prefix regardless of local timezone.
func NewID(now time.Time) string {
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		// rand.Read uses /dev/urandom on POSIX and never errors in
		// practice; if it ever does, the deterministic zero suffix
		// is still a valid id and will collide loudly with itself.
		for i := range suffix {
			suffix[i] = 0
		}
	}
	return fmt.Sprintf("%s-%s", now.UTC().Format("20060102-150405"), hex.EncodeToString(suffix))
}

// IsValidID returns true iff s matches the canonical id format.
// The regex also blocks path traversal because / and . are excluded.
func IsValidID(s string) bool {
	return idPattern.MatchString(s)
}
```

- [ ] **Step 4: Run id tests and verify they pass**

Run:

```bash
go test ./tests/session -run TestNewID -count=1
go test ./tests/session -run TestIsValidID -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/session/id.go tests/session/id_test.go
git commit -m "feat(session): generate and validate session ids"
```

---

### Task 2: Estimate Tokens

**Files:**
- Create: `internal/session/tokens.go`
- Test: `tests/session/tokens_test.go`

- [ ] **Step 1: Write failing tests for the token estimator**

Create `tests/session/tokens_test.go`:

```go
package session_test

import (
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/session"
)

func TestEstimateOneEmptyMessage(t *testing.T) {
	got := session.EstimateOne(schema.Message{Role: schema.RoleUser})
	if got != session.MessageOverhead {
		t.Fatalf("empty message tokens = %d, want overhead %d", got, session.MessageOverhead)
	}
}

func TestEstimateOneAscii(t *testing.T) {
	msg := schema.Message{Role: schema.RoleUser, Content: "hello world"} // 11 chars
	got := session.EstimateOne(msg)
	want := session.MessageOverhead + (11+3)/4 // ceil division
	if got != want {
		t.Fatalf("ascii tokens = %d, want %d", got, want)
	}
}

func TestEstimateOneToolResultIncludesToolCallID(t *testing.T) {
	msg := schema.Message{Role: schema.RoleTool, Content: "ok", ToolCallID: "call_12345"}
	got := session.EstimateOne(msg)
	want := session.MessageOverhead + (2+3)/4 + (10+3)/4
	if got != want {
		t.Fatalf("tool tokens = %d, want %d (overhead + content + tool_call_id)", got, want)
	}
}

func TestEstimateTokensSumsAcrossMessages(t *testing.T) {
	msgs := []schema.Message{
		{Role: schema.RoleUser, Content: "a"},
		{Role: schema.RoleAssistant, Content: "bb"},
	}
	got := session.EstimateTokens(msgs)
	want := session.EstimateOne(msgs[0]) + session.EstimateOne(msgs[1])
	if got != want {
		t.Fatalf("sum = %d, want %d", got, want)
	}
}
```

- [ ] **Step 2: Run estimator tests and verify they fail**

Run:

```bash
go test ./tests/session -run TestEstimate -count=1
```

Expected: FAIL because `tokens.go` does not exist.

- [ ] **Step 3: Implement the estimator**

Create `internal/session/tokens.go`:

```go
package session

import (
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// MessageOverhead approximates the per-message envelope cost a provider
// incurs beyond the literal content (role marker, separator tokens).
// 8 is a small constant chosen to roughly match OpenAI's documented
// per-message overhead so the total estimate is conservative.
const MessageOverhead = 8

// EstimateOne returns a chars/4 estimate of one message's token cost,
// rounding up so a 1-char message still counts as 1 content token.
func EstimateOne(msg schema.Message) int {
	tokens := MessageOverhead + ceilDiv4(len(msg.Content))
	if msg.ToolCallID != "" {
		tokens += ceilDiv4(len(msg.ToolCallID))
	}
	for _, call := range msg.ToolCalls {
		tokens += ceilDiv4(len(call.ID))
		tokens += ceilDiv4(len(call.Name))
		tokens += ceilDiv4(len(call.Arguments))
	}
	return tokens
}

// EstimateTokens sums EstimateOne across every message in the slice.
func EstimateTokens(msgs []schema.Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateOne(m)
	}
	return total
}

func ceilDiv4(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + 3) / 4
}
```

- [ ] **Step 4: Run estimator tests and verify they pass**

Run:

```bash
go test ./tests/session -run TestEstimate -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/session/tokens.go tests/session/tokens_test.go
git commit -m "feat(session): chars/4 token estimator"
```

---

### Task 3: Trimmer Interface And Strategy Registry

**Files:**
- Create: `internal/session/trim.go`

- [ ] **Step 1: Write the failing interface contract test**

Create `tests/session/trim_test.go`:

```go
package session_test

import (
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/session"
)

// noopTrimmer is the minimum implementation of the Trimmer interface
// used to confirm the registry plumbing works end to end.
type noopTrimmer struct{}

func (noopTrimmer) Trim(msgs []schema.Message) []schema.Message { return msgs }
func (noopTrimmer) Name() string                                { return "noop" }

func TestRegisterAndGetTrimmer(t *testing.T) {
	session.Register("noop-test", func(session.TrimConfig) session.Trimmer { return noopTrimmer{} })
	tr, err := session.Get("noop-test", session.TrimConfig{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if tr.Name() != "noop" {
		t.Fatalf("name = %q", tr.Name())
	}
}

func TestGetUnknownStrategyListsRegisteredNames(t *testing.T) {
	_, err := session.Get("does-not-exist", session.TrimConfig{})
	if err == nil {
		t.Fatal("expected error for unknown strategy")
	}
}
```

- [ ] **Step 2: Run trim registry tests and verify they fail**

Run:

```bash
go test ./tests/session -run 'TestRegisterAndGetTrimmer|TestGetUnknownStrategy' -count=1
```

Expected: FAIL because `Trimmer`, `TrimConfig`, `Register`, `Get` are missing.

- [ ] **Step 3: Implement the interface and registry**

Create `internal/session/trim.go`:

```go
package session

import (
	"fmt"
	"sort"
	"sync"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// Trimmer reduces a non-system message slice to a provider-safe view.
// Implementations must be pure: same input slice -> same output slice,
// no I/O, no allocation of shared state. The engine relies on this so
// that calling Trim before every provider.Generate is deterministic.
type Trimmer interface {
	Trim(messages []schema.Message) []schema.Message
	Name() string
}

// TrimConfig captures user-facing bounds for the default strategy.
// Custom strategies may ignore fields that do not apply to them.
type TrimConfig struct {
	MaxUserTurns int
	MaxTokens    int
}

type constructor func(TrimConfig) Trimmer

var (
	registryMu sync.RWMutex
	registry   = map[string]constructor{}
)

// Register makes a trimmer constructor available under the given name.
// Re-registering the same name overwrites the previous entry; init()
// callers in this package register the built-in "window" strategy.
func Register(name string, ctor constructor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = ctor
}

// Get instantiates the trimmer registered under name with the supplied
// config. An unknown name returns an error listing every known strategy
// so the CLI can surface a useful message.
func Get(name string, cfg TrimConfig) (Trimmer, error) {
	registryMu.RLock()
	ctor, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown trim strategy %q (known: %s)", name, knownNames())
	}
	return ctor(cfg), nil
}

func knownNames() string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
```

- [ ] **Step 4: Run trim registry tests and verify they pass**

Run:

```bash
go test ./tests/session -run 'TestRegisterAndGetTrimmer|TestGetUnknownStrategy' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/session/trim.go tests/session/trim_test.go
git commit -m "feat(session): trimmer interface and strategy registry"
```

---

### Task 4: Default `WindowTrimmer`

**Files:**
- Create: `internal/session/window.go`
- Test: `tests/session/trim_window_test.go`

- [ ] **Step 1: Write failing table-driven tests for the three passes**

Create `tests/session/trim_window_test.go`:

```go
package session_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/session"
)

func user(content string) schema.Message {
	return schema.Message{Role: schema.RoleUser, Content: content}
}

func asst(content string, calls ...schema.ToolCall) schema.Message {
	return schema.Message{Role: schema.RoleAssistant, Content: content, ToolCalls: calls}
}

func toolMsg(id string, content string) schema.Message {
	return schema.Message{Role: schema.RoleTool, Content: content, ToolCallID: id}
}

func toolCall(id, name string) schema.ToolCall {
	return schema.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(`{}`)}
}

func contents(msgs []schema.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = string(m.Role) + ":" + m.Content
	}
	return out
}

func TestWindowTrimmerKeepsAllWhenUnderLimits(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	in := []schema.Message{
		user("first"),
		asst("ack"),
		user("second"),
		asst("ack"),
	}
	got := tr.Trim(in)
	if !reflect.DeepEqual(contents(got), contents(in)) {
		t.Fatalf("got %v, want %v", contents(got), contents(in))
	}
}

func TestWindowTrimmerDropsOldestUserTurns(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 2, MaxTokens: 100000})
	in := []schema.Message{
		user("u1"), asst("a1"),
		user("u2"), asst("a2"),
		user("u3"), asst("a3"),
	}
	got := tr.Trim(in)
	if !reflect.DeepEqual(contents(got), []string{"user:u2", "assistant:a2", "user:u3", "assistant:a3"}) {
		t.Fatalf("got %v", contents(got))
	}
}

func TestWindowTrimmerPreservesToolCallResultPairs(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 2, MaxTokens: 100000})
	in := []schema.Message{
		user("u1"),
		asst("", toolCall("tc1", "bash")),
		toolMsg("tc1", "result1"),
		user("u2"),
		asst("", toolCall("tc2", "bash")),
		toolMsg("tc2", "result2"),
	}
	got := tr.Trim(in)
	if len(got) != 6 {
		t.Fatalf("len = %d, want 6 (pairs preserved)", len(got))
	}
	if got[2].Role != schema.RoleTool || got[2].ToolCallID != "tc1" {
		t.Fatalf("first tool pair broken: %+v", got)
	}
}

func TestWindowTrimmerTokenCapShrinksBelowTurnLimit(t *testing.T) {
	huge := strings.Repeat("x", 1000) // ~250 tokens per content
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 600})
	in := []schema.Message{
		user(huge), asst(huge),
		user(huge), asst(huge),
		user("recent"), asst("done"),
	}
	got := tr.Trim(in)
	// All three turns together exceed 600 tokens; the trimmer should
	// drop the oldest turn(s) until the remainder fits.
	if len(got) >= len(in) {
		t.Fatalf("token cap had no effect: got len %d, in len %d", len(got), len(in))
	}
	if got[len(got)-2].Content != "recent" {
		t.Fatalf("most recent user turn dropped: %v", contents(got))
	}
}

func TestWindowTrimmerDropsOrphanToolResult(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	in := []schema.Message{
		user("u1"),
		toolMsg("orphan", "shouldnt be here"),
		asst("hi"),
	}
	got := tr.Trim(in)
	for _, m := range got {
		if m.Role == schema.RoleTool {
			t.Fatalf("orphan tool result kept: %+v", got)
		}
	}
}

func TestWindowTrimmerDropsAssistantWithDanglingToolCalls(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	in := []schema.Message{
		user("u1"),
		asst("", toolCall("tc1", "bash"), toolCall("tc2", "bash")),
		toolMsg("tc1", "ok"),
		// tc2 result missing entirely
		user("u2"),
	}
	got := tr.Trim(in)
	for _, m := range got {
		if m.Role == schema.RoleAssistant && len(m.ToolCalls) > 0 {
			t.Fatalf("assistant with dangling tool_calls retained: %+v", got)
		}
		if m.Role == schema.RoleTool {
			t.Fatalf("tool message from dropped assistant retained: %+v", got)
		}
	}
}

func TestWindowTrimmerDropsLeadingToolMessages(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	in := []schema.Message{
		toolMsg("stale", "left over from a dropped assistant"),
		user("u1"),
	}
	got := tr.Trim(in)
	if len(got) == 0 || got[0].Role == schema.RoleTool {
		t.Fatalf("leading tool message not dropped: %v", contents(got))
	}
}

func TestWindowTrimmerEmptyInput(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	if got := tr.Trim(nil); len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestWindowTrimmerSingleUserMessageUnchanged(t *testing.T) {
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	in := []schema.Message{user("only")}
	got := tr.Trim(in)
	if len(got) != 1 || got[0].Content != "only" {
		t.Fatalf("got %v", contents(got))
	}
}

func TestWindowTrimmerConsecutiveUserMessagesRetained(t *testing.T) {
	// Models D12: concurrent writers can append two user messages back to back.
	tr := session.NewWindowTrimmer(session.TrimConfig{MaxUserTurns: 6, MaxTokens: 100000})
	in := []schema.Message{user("a"), user("b"), asst("ack")}
	got := tr.Trim(in)
	if len(got) != 3 {
		t.Fatalf("got %v, want both user messages retained", contents(got))
	}
}

func TestWindowTrimmerRegisteredAsWindowStrategy(t *testing.T) {
	tr, err := session.Get("window", session.TrimConfig{MaxUserTurns: 6, MaxTokens: 32000})
	if err != nil {
		t.Fatalf("Get window: %v", err)
	}
	if tr.Name() != "window" {
		t.Fatalf("Name = %q, want window", tr.Name())
	}
}
```

- [ ] **Step 2: Run window trimmer tests and verify they fail**

Run:

```bash
go test ./tests/session -run TestWindowTrimmer -count=1
```

Expected: FAIL because `WindowTrimmer` and `NewWindowTrimmer` do not exist.

- [ ] **Step 3: Implement the window trimmer**

Create `internal/session/window.go`:

```go
package session

import (
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// WindowTrimmer is the default Trimmer: it keeps the last N user turns,
// applies a token-budget ceiling that can shrink the slice further,
// and runs a defensive cleanup pass so the surviving slice is always
// provider-valid even if concurrent writers (D12) interleaved messages.
type WindowTrimmer struct {
	cfg TrimConfig
}

// NewWindowTrimmer constructs a WindowTrimmer. It is registered in init().
func NewWindowTrimmer(cfg TrimConfig) Trimmer {
	return WindowTrimmer{cfg: cfg}
}

func init() {
	Register("window", func(cfg TrimConfig) Trimmer { return NewWindowTrimmer(cfg) })
}

func (w WindowTrimmer) Name() string { return "window" }

// Trim runs three sequential pure passes: window by user turns, apply
// token cap, defensive cleanup. Each pass returns a fresh slice; the
// input is never mutated.
func (w WindowTrimmer) Trim(messages []schema.Message) []schema.Message {
	windowed := windowByUserTurns(messages, w.cfg.MaxUserTurns)
	capped := applyTokenCap(windowed, w.cfg.MaxTokens)
	cleaned := defensiveCleanup(capped)
	return cleaned
}

func windowByUserTurns(messages []schema.Message, maxTurns int) []schema.Message {
	if maxTurns <= 0 || len(messages) == 0 {
		return cloneMessages(messages)
	}
	userIndices := []int{}
	for i, m := range messages {
		if m.Role == schema.RoleUser {
			userIndices = append(userIndices, i)
		}
	}
	if len(userIndices) <= maxTurns {
		return cloneMessages(messages)
	}
	start := userIndices[len(userIndices)-maxTurns]
	return cloneMessages(messages[start:])
}

func applyTokenCap(messages []schema.Message, maxTokens int) []schema.Message {
	if maxTokens <= 0 || len(messages) == 0 {
		return cloneMessages(messages)
	}
	current := cloneMessages(messages)
	for EstimateTokens(current) > maxTokens {
		next := dropOldestUserTurn(current)
		if len(next) == len(current) {
			return current
		}
		current = next
	}
	return current
}

func dropOldestUserTurn(messages []schema.Message) []schema.Message {
	if len(messages) == 0 {
		return messages
	}
	firstUser := -1
	for i, m := range messages {
		if m.Role == schema.RoleUser {
			firstUser = i
			break
		}
	}
	if firstUser < 0 {
		return messages
	}
	nextUser := -1
	for i := firstUser + 1; i < len(messages); i++ {
		if messages[i].Role == schema.RoleUser {
			nextUser = i
			break
		}
	}
	if nextUser < 0 {
		// Only one user turn remains; do not drop it.
		return messages
	}
	out := make([]schema.Message, 0, len(messages)-(nextUser-firstUser))
	out = append(out, messages[:firstUser]...)
	out = append(out, messages[nextUser:]...)
	return out
}

func defensiveCleanup(messages []schema.Message) []schema.Message {
	// Pass 1: drop orphan tool messages whose preceding assistant does
	// not contain a matching tool_call_id.
	pass1 := make([]schema.Message, 0, len(messages))
	for _, m := range messages {
		if m.Role == schema.RoleTool {
			if !matchingCallExists(pass1, m.ToolCallID) {
				continue
			}
		}
		pass1 = append(pass1, m)
	}

	// Pass 2: drop assistants whose tool_calls do not all have matching
	// tool results immediately following (only tool messages permitted
	// between the assistant and the next non-tool message).
	keep := make([]bool, len(pass1))
	for i := range keep {
		keep[i] = true
	}
	for i, m := range pass1 {
		if m.Role != schema.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		expected := map[string]bool{}
		for _, c := range m.ToolCalls {
			expected[c.ID] = false
		}
		j := i + 1
		for j < len(pass1) && pass1[j].Role == schema.RoleTool {
			if _, ok := expected[pass1[j].ToolCallID]; ok {
				expected[pass1[j].ToolCallID] = true
			}
			j++
		}
		allSatisfied := true
		for _, seen := range expected {
			if !seen {
				allSatisfied = false
				break
			}
		}
		if allSatisfied {
			continue
		}
		keep[i] = false
		for k := i + 1; k < j; k++ {
			keep[k] = false
		}
	}
	pass2 := make([]schema.Message, 0, len(pass1))
	for i, m := range pass1 {
		if keep[i] {
			pass2 = append(pass2, m)
		}
	}

	// Pass 3: drop any leading tool messages exposed by the previous passes.
	start := 0
	for start < len(pass2) && pass2[start].Role == schema.RoleTool {
		start++
	}
	return pass2[start:]
}

func matchingCallExists(prefix []schema.Message, toolCallID string) bool {
	for i := len(prefix) - 1; i >= 0; i-- {
		m := prefix[i]
		if m.Role == schema.RoleAssistant {
			for _, c := range m.ToolCalls {
				if c.ID == toolCallID {
					return true
				}
			}
			return false
		}
		if m.Role == schema.RoleUser {
			return false
		}
	}
	return false
}

func cloneMessages(messages []schema.Message) []schema.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]schema.Message, len(messages))
	copy(out, messages)
	return out
}
```

- [ ] **Step 4: Run window trimmer tests and verify they pass**

Run:

```bash
go test ./tests/session -run TestWindowTrimmer -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/session/window.go tests/session/trim_window_test.go
git commit -m "feat(session): window trimmer with defensive cleanup"
```

---

### Task 5: POSIX `flock` Helper

**Files:**
- Create: `internal/session/lock_posix.go`
- Create: `internal/session/lock_windows.go`

This task adds no tests of its own — the locking is exercised by the concurrent-append test in Task 6. The two files exist so the package compiles on either platform.

- [ ] **Step 1: Implement the POSIX exclusive lock**

Create `internal/session/lock_posix.go`:

```go
//go:build !windows

package session

import (
	"fmt"
	"syscall"
)

// lockExclusive acquires a process-wide POSIX advisory exclusive lock on
// the given file descriptor. The lock is released when the descriptor is
// closed; the kernel also releases it on process death so there is no
// stale-lock cleanup to do. Called once per Append; serializes concurrent
// writers at the kernel level so each appended JSONL line is intact.
func lockExclusive(fd uintptr) error {
	if err := syscall.Flock(int(fd), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock LOCK_EX: %w", err)
	}
	return nil
}

func unlock(fd uintptr) error {
	if err := syscall.Flock(int(fd), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("flock LOCK_UN: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: Implement the Windows no-op stub**

Create `internal/session/lock_windows.go`:

```go
//go:build windows

package session

// lockExclusive is a no-op on Windows. Per spec D16 the project is POSIX
// focused; running on Windows means concurrent writers are not protected
// at the file-integrity layer. This is documented in the README.
func lockExclusive(_ uintptr) error { return nil }

func unlock(_ uintptr) error { return nil }
```

- [ ] **Step 3: Verify the package still compiles on both build tags**

Run:

```bash
GOOS=linux  go build ./internal/session
GOOS=darwin go build ./internal/session
GOOS=windows go build ./internal/session
```

Expected: each command succeeds with no output.

- [ ] **Step 4: Commit**

Run:

```bash
git add internal/session/lock_posix.go internal/session/lock_windows.go
git commit -m "feat(session): per-append flock with windows no-op"
```

---

### Task 6: Session Type And JSONL Store

**Files:**
- Create: `internal/session/session.go`
- Create: `internal/session/store.go`
- Test: `tests/session/store_test.go`

- [ ] **Step 1: Write failing tests for the JSONL round-trip and edge cases**

Create `tests/session/store_test.go`:

```go
package session_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/session"
)

func TestNewSessionCreatesFileAndDirectoryWith0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh", "sessions")
	s, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", dirInfo.Mode().Perm())
	}
	if !session.IsValidID(s.ID()) {
		t.Fatalf("session id = %q, not a valid id", s.ID())
	}
	if _, err := os.Stat(filepath.Join(dir, s.ID()+".jsonl")); err != nil {
		t.Fatalf("session file not created: %v", err)
	}
}

func TestAppendAndOpenSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	first, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	want := []schema.Message{
		{Role: schema.RoleUser, Content: "hello"},
		{Role: schema.RoleAssistant, Content: "hi back"},
	}
	for _, m := range want {
		if err := first.Append(m); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := session.OpenSession(first.ID(), dir)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer second.Close()

	got := second.Messages()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Fatalf("msg[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestOpenSessionMissingFileErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := session.OpenSession("20260518-093045-a1b2c3", dir)
	if err == nil {
		t.Fatal("expected error for missing session file")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q, want 'not found'", err)
	}
}

func TestOpenSessionRejectsCorruptLine(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	id := s.ID()
	s.Close()

	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"ok\"}\nnot valid json\n"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	_, err = session.OpenSession(id, dir)
	if err == nil {
		t.Fatal("expected corruption error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error = %q, want mention of line 2", err)
	}
}

func TestOpenSessionInvalidIDRejected(t *testing.T) {
	_, err := session.OpenSession("../etc/passwd", t.TempDir())
	if err == nil {
		t.Fatal("expected invalid-id error")
	}
}

func TestAppendsAreLineDelimited(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := s.Append(schema.Message{Role: schema.RoleUser, Content: "a"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Append(schema.Message{Role: schema.RoleAssistant, Content: "b"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	id := s.ID()
	s.Close()

	bytes, err := os.ReadFile(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(bytes), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for i, line := range lines {
		var m schema.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %d invalid JSON: %v", i+1, err)
		}
	}
}

func TestConcurrentAppendKeepsEveryLineParseable(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	id := s.ID()

	const writers = 2
	const perWriter = 50

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			peer, err := session.OpenSession(id, dir)
			if err != nil {
				t.Errorf("OpenSession: %v", err)
				return
			}
			defer peer.Close()
			for i := 0; i < perWriter; i++ {
				err := peer.Append(schema.Message{
					Role:    schema.RoleUser,
					Content: strings.Repeat("x", 100),
				})
				if err != nil {
					t.Errorf("Append: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	s.Close()

	bytes, err := os.ReadFile(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(bytes), "\n"), "\n")
	if len(lines) != writers*perWriter {
		t.Fatalf("got %d lines, want %d", len(lines), writers*perWriter)
	}
	for i, line := range lines {
		var m schema.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %d invalid JSON: %v\nline=%q", i+1, err, line)
		}
	}
}

func TestNewInMemorySessionDoesNotPersist(t *testing.T) {
	s := session.NewInMemory()
	defer s.Close()
	if err := s.Append(schema.Message{Role: schema.RoleUser, Content: "transient"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if len(s.Messages()) != 1 {
		t.Fatalf("len = %d, want 1", len(s.Messages()))
	}
	if s.ID() == "" {
		t.Fatal("in-memory session id should be non-empty for log lines")
	}
}
```

- [ ] **Step 2: Run store tests and verify they fail**

Run:

```bash
go test ./tests/session -run 'TestNewSession|TestAppend|TestOpenSession|TestConcurrentAppend|TestNewInMemorySession' -count=1
```

Expected: FAIL because `Session`, `NewSession`, `OpenSession`, `NewInMemory` do not exist.

- [ ] **Step 3: Implement the Session type**

Create `internal/session/session.go`:

```go
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// Session owns the canonical conversation history for one conversation.
// On disk it is one JSONL file, append-only. Each Append acquires a
// per-call exclusive flock so concurrent processes serialize at the
// kernel level (see spec D12).
type Session struct {
	id       string
	file     *os.File // nil for in-memory sessions (tests)
	mu       sync.Mutex
	messages []schema.Message
}

// ID returns the session identifier. For persisted sessions this is the
// YYYYMMDD-HHMMSS-XXXXXX string used as the JSONL filename.
func (s *Session) ID() string { return s.id }

// Messages returns a snapshot of the in-memory history (system messages
// are not stored here; they are recomposed by the engine on every run).
func (s *Session) Messages() []schema.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneMessages(s.messages)
}

// Append persists one message and updates the in-memory slice. The
// on-disk write happens under flock; the in-memory update happens under
// the per-session mutex.
func (s *Session) Append(msg schema.Message) error {
	if s.file != nil {
		if err := s.persist(msg); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	s.mu.Unlock()
	return nil
}

// Close releases the file handle (and the kernel-held flock with it).
// Safe to call multiple times.
func (s *Session) Close() error {
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

func (s *Session) persist(msg schema.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal session message: %w", err)
	}
	data = append(data, '\n')
	if err := lockExclusive(s.file.Fd()); err != nil {
		return err
	}
	defer func() { _ = unlock(s.file.Fd()) }()
	if _, err := s.file.Write(data); err != nil {
		return fmt.Errorf("write session line: %w", err)
	}
	return nil
}

// NewInMemory returns a Session that does not persist to disk. Intended
// for tests; production code uses NewSession or OpenSession.
func NewInMemory() *Session {
	return &Session{id: "in-memory"}
}
```

- [ ] **Step 4: Implement the store**

Create `internal/session/store.go`:

```go
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// NewSession creates a fresh session inside dir. The directory is created
// with mode 0700 if missing. The returned Session owns an open file
// handle in O_APPEND mode; close it when the run finishes.
func NewSession(dir string) (*Session, error) {
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	id := NewID(time.Now())
	path := filepath.Join(dir, id+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create session %q: %w", id, err)
	}
	return &Session{id: id, file: file}, nil
}

// OpenSession resumes an existing session by id. The id is validated
// against the canonical format to prevent path traversal. The full
// JSONL is parsed into memory; a malformed line aborts with the line
// number to make manual repair possible.
func OpenSession(id string, dir string) (*Session, error) {
	if !IsValidID(id) {
		return nil, fmt.Errorf("invalid session id %q (must match YYYYMMDD-HHMMSS-XXXXXX)", id)
	}
	path := filepath.Join(dir, id+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("session %s not found in %s", id, dir)
		}
		return nil, fmt.Errorf("open session %q: %w", id, err)
	}

	messages, err := readAllMessages(path, id)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Session{id: id, file: file, messages: messages}, nil
}

// MissingError returns true if err is a "session not found" error from
// OpenSession; callers can branch on it for nicer messages.
func MissingError(err error) bool {
	return err != nil && errors.Is(err, os.ErrNotExist)
}

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create sessions dir %q: %w", dir, err)
	}
	return nil
}

func readAllMessages(path string, id string) ([]schema.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open session %q for read: %w", id, err)
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	out := []schema.Message{}
	lineNumber := 0
	for {
		lineNumber++
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := line
			if trimmed[len(trimmed)-1] == '\n' {
				trimmed = trimmed[:len(trimmed)-1]
			}
			if trimmed == "" {
				if err == io.EOF {
					break
				}
				continue
			}
			var msg schema.Message
			if jsonErr := json.Unmarshal([]byte(trimmed), &msg); jsonErr != nil {
				return nil, fmt.Errorf("session %s: corrupt at line %d: %w", id, lineNumber, jsonErr)
			}
			out = append(out, msg)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read session %q: %w", id, err)
		}
	}
	return out, nil
}
```

- [ ] **Step 5: Run store tests and verify they pass**

Run:

```bash
go test ./tests/session -count=1
```

Expected: PASS for every test in `tests/session/`.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/session/session.go internal/session/store.go tests/session/store_test.go
git commit -m "feat(session): jsonl store with per-append flock"
```

---

### Task 7: Engine Integration — Seed From Session And Trim Every Turn

**Files:**
- Modify: `internal/engine/loop.go`
- Modify: `tests/engine/loop_test.go`
- Create: `tests/engine/session_integration_test.go`

- [ ] **Step 1: Write failing engine integration tests**

Create `tests/engine/session_integration_test.go`:

```go
package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestEngineAppendsUserPromptAndActMessagesToSession(t *testing.T) {
	dir := t.TempDir()
	s, err := agentsession.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	provider := &fakeProvider{responses: []schema.Message{
		{Role: schema.RoleAssistant, Content: "done"},
	}}
	registry := tools.NewRegistry()
	trimmer := agentsession.NewWindowTrimmer(agentsession.TrimConfig{MaxUserTurns: 6, MaxTokens: 32000})
	eng := engine.NewAgentEngine(provider, registry, dir, false)
	eng.SetSession(s)
	eng.SetTrimmer(trimmer)

	if err := eng.Run(context.Background(), "hello", noOpReporter{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := s.Messages()
	if len(got) != 2 {
		t.Fatalf("session messages = %d, want 2 (user + assistant)", len(got))
	}
	if got[0].Role != schema.RoleUser || got[0].Content != "hello" {
		t.Fatalf("first persisted = %+v, want user/hello", got[0])
	}
	if got[1].Role != schema.RoleAssistant || got[1].Content != "done" {
		t.Fatalf("second persisted = %+v, want assistant/done", got[1])
	}
}

func TestEngineResumeSeesPriorHistory(t *testing.T) {
	dir := t.TempDir()
	first, err := agentsession.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	provider1 := &fakeProvider{responses: []schema.Message{
		{Role: schema.RoleAssistant, Content: "first reply"},
	}}
	eng1 := engine.NewAgentEngine(provider1, tools.NewRegistry(), dir, false)
	eng1.SetSession(first)
	eng1.SetTrimmer(agentsession.NewWindowTrimmer(agentsession.TrimConfig{MaxUserTurns: 6, MaxTokens: 32000}))
	if err := eng1.Run(context.Background(), "round one", noOpReporter{}); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	id := first.ID()
	first.Close()

	resumed, err := agentsession.OpenSession(id, dir)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() { _ = resumed.Close() })

	provider2 := &fakeProvider{responses: []schema.Message{
		{Role: schema.RoleAssistant, Content: "second reply"},
	}}
	eng2 := engine.NewAgentEngine(provider2, tools.NewRegistry(), dir, false)
	eng2.SetSession(resumed)
	eng2.SetTrimmer(agentsession.NewWindowTrimmer(agentsession.TrimConfig{MaxUserTurns: 6, MaxTokens: 32000}))
	if err := eng2.Run(context.Background(), "round two", noOpReporter{}); err != nil {
		t.Fatalf("Run 2: %v", err)
	}

	seen := provider2.receivedMsgs[0]
	if len(seen) < 4 {
		t.Fatalf("second run saw %d messages, want at least 4 (sys, u1, a1, u2)", len(seen))
	}
	if seen[1].Content != "round one" {
		t.Fatalf("seen[1] = %+v, want user/round one", seen[1])
	}
	if seen[2].Content != "first reply" {
		t.Fatalf("seen[2] = %+v, want assistant/first reply", seen[2])
	}
	if seen[3].Content != "round two" {
		t.Fatalf("seen[3] = %+v, want user/round two", seen[3])
	}
}

func TestEngineThinkPhaseNotPersisted(t *testing.T) {
	dir := t.TempDir()
	s, err := agentsession.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	provider := &fakeProvider{responses: []schema.Message{
		{Role: schema.RoleAssistant, Content: "thinking out loud"}, // think
		{Role: schema.RoleAssistant, Content: "final answer"},      // act
	}}
	eng := engine.NewAgentEngine(provider, tools.NewRegistry(), dir, true)
	eng.SetSession(s)
	eng.SetTrimmer(agentsession.NewWindowTrimmer(agentsession.TrimConfig{MaxUserTurns: 6, MaxTokens: 32000}))

	if err := eng.Run(context.Background(), "go", noOpReporter{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, m := range s.Messages() {
		if m.Content == "thinking out loud" {
			t.Fatalf("think-phase response was persisted: %+v", s.Messages())
		}
	}
}

func TestEngineEmergencyFloorWhenTokenCapIsHostile(t *testing.T) {
	dir := t.TempDir()
	s, err := agentsession.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	provider := &fakeProvider{responses: []schema.Message{
		{Role: schema.RoleAssistant, Content: "ok"},
	}}
	hostile := agentsession.NewWindowTrimmer(agentsession.TrimConfig{MaxUserTurns: 1, MaxTokens: 1})
	eng := engine.NewAgentEngine(provider, tools.NewRegistry(), dir, false)
	eng.SetSession(s)
	eng.SetTrimmer(hostile)

	prompt := strings.Repeat("y", 1000)
	if err := eng.Run(context.Background(), prompt, noOpReporter{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	first := provider.receivedMsgs[0]
	if len(first) < 2 {
		t.Fatalf("seen %d messages, want at least 2 (system + emergency floor)", len(first))
	}
	if first[1].Role != schema.RoleUser || first[1].Content != prompt {
		t.Fatalf("emergency floor = %+v, want the latest user message", first[1])
	}
}

func TestEnginePersistsToolResultsForResume(t *testing.T) {
	dir := t.TempDir()
	s, err := agentsession.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	tool := &recordingTool{name: "echo", output: "tool-out"}
	registry := tools.NewRegistry()
	registry.Register(tool)

	provider := &fakeProvider{responses: []schema.Message{
		{Role: schema.RoleAssistant, ToolCalls: []schema.ToolCall{
			{ID: "abc", Name: "echo", Arguments: json.RawMessage(`{}`)},
		}},
		{Role: schema.RoleAssistant, Content: "done"},
	}}
	eng := engine.NewAgentEngine(provider, registry, dir, false)
	eng.SetSession(s)
	eng.SetTrimmer(agentsession.NewWindowTrimmer(agentsession.TrimConfig{MaxUserTurns: 6, MaxTokens: 32000}))
	if err := eng.Run(context.Background(), "go", noOpReporter{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	roles := []schema.Role{}
	for _, m := range s.Messages() {
		roles = append(roles, m.Role)
	}
	want := []schema.Role{schema.RoleUser, schema.RoleAssistant, schema.RoleTool, schema.RoleAssistant}
	if len(roles) != len(want) {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles[%d] = %q, want %q", i, roles[i], want[i])
		}
	}
}
```

- [ ] **Step 2: Run the new integration tests and verify they fail**

Run:

```bash
go test ./tests/engine -run 'TestEngineAppendsUserPromptAndActMessagesToSession|TestEngineResumeSeesPriorHistory|TestEngineThinkPhaseNotPersisted|TestEngineEmergencyFloorWhenTokenCapIsHostile|TestEnginePersistsToolResultsForResume' -count=1
```

Expected: FAIL because `SetSession`, `SetTrimmer`, and the trim/persist behavior do not exist.

- [ ] **Step 3: Add session + trimmer fields and setters to the engine**

In `internal/engine/loop.go`, add this import after the existing `agentcontext` import:

```go
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
```

Add these fields to the `AgentEngine` struct (place them after `contextManager`):

```go
	session *agentsession.Session
	trimmer agentsession.Trimmer
```

Add these methods near `SetContextManager`:

```go
// SetSession attaches the canonical history store. The engine appends
// the user prompt, every act-phase assistant message, and every tool
// result to the session; think-phase responses are intentionally not
// persisted (spec D17).
func (e *AgentEngine) SetSession(s *agentsession.Session) {
	e.session = s
}

// SetTrimmer attaches a strategy used to bound what the provider sees.
// If nil, the engine uses an identity trimmer that returns the full
// session history (matches today's unbounded behavior for tests that
// don't care).
func (e *AgentEngine) SetTrimmer(t agentsession.Trimmer) {
	e.trimmer = t
}
```

- [ ] **Step 4: Rewrite `Run` to seed from the session and trim each turn**

In `internal/engine/loop.go`, replace the entire body of `Run` with:

```go
func (e *AgentEngine) Run(ctx context.Context, userPrompt string, report Reporter) error {
	log.Printf("[engine] starting, workdir=%s thinking=%v", e.WorkDir, e.EnableThinking)

	maxTurns := e.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}

	sess := e.session
	if sess == nil {
		sess = agentsession.NewInMemory()
	}
	if userPrompt != "" {
		if err := sess.Append(schema.Message{Role: schema.RoleUser, Content: userPrompt}); err != nil {
			return fmt.Errorf("append user prompt: %w", err)
		}
	}

	systemMsg := schema.Message{Role: schema.RoleSystem, Content: e.systemPrompt()}
	availableTools := e.registry.GetAvailableTools()
	turnCount := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		turnCount++
		if turnCount > maxTurns {
			return ErrMaxTurnsExceeded
		}
		log.Printf("[engine] turn %d", turnCount)

		view := e.providerView(systemMsg, sess.Messages())

		if e.EnableThinking {
			log.Println("[engine] phase=think tools=disabled")
			thinkResp, err := e.provider.Generate(ctx, view, nil)
			if err != nil {
				return fmt.Errorf("think phase: %w", err)
			}
			if thinkResp.Content != "" {
				report.OnThinking(ctx)
				// Spec D17: think-phase responses are NOT persisted to
				// the session. Carry it forward only inside this turn
				// so the act-phase call can see the reasoning.
				view = append(view, *thinkResp)
			}
		}

		log.Println("[engine] phase=act tools=enabled")
		actionResp, err := e.provider.Generate(ctx, view, availableTools)
		if err != nil {
			return fmt.Errorf("act phase: %w", err)
		}

		if err := sess.Append(*actionResp); err != nil {
			return fmt.Errorf("persist act response: %w", err)
		}

		if actionResp.Content != "" {
			report.OnMessage(ctx, actionResp.Content)
		}

		if len(actionResp.ToolCalls) == 0 {
			log.Println("[engine] no tool calls, task complete")
			break
		}

		log.Printf("[engine] tool calls requested: %d", len(actionResp.ToolCalls))

		if hasLoadSkillCall(actionResp.ToolCalls) {
			results, err := e.executeLoadSkillBarrier(ctx, actionResp.ToolCalls, report)
			if err != nil {
				return err
			}
			for _, result := range results {
				if err := sess.Append(toolResultMessage(result)); err != nil {
					return fmt.Errorf("persist tool result: %w", err)
				}
			}
			systemMsg.Content = e.systemPrompt()
			continue
		}

		groups := PlanToolCallGroups(actionResp.ToolCalls, e.registry.ExecutionPolicyFor)
		for _, group := range groups {
			if err := ctx.Err(); err != nil {
				return err
			}

			for _, call := range group {
				report.OnToolCall(ctx, call.Name, string(call.Arguments))
			}

			results, err := executeToolCallGroup(ctx, e.registry, group, e.toolGroupLimit(group))
			if err != nil {
				return err
			}

			for i, result := range results {
				report.OnToolResult(ctx, group[i].Name, result.Output, result.IsError)
				if err := sess.Append(toolResultMessage(result)); err != nil {
					return fmt.Errorf("persist tool result: %w", err)
				}
			}
		}
	}

	return nil
}

// providerView composes the slice handed to provider.Generate: the
// system message at index 0 followed by the trimmed session tail. If
// the trimmer returns an empty slice (token cap so low that even the
// latest user turn does not fit) we substitute the last user message
// so the model still receives a valid prompt.
func (e *AgentEngine) providerView(systemMsg schema.Message, tail []schema.Message) []schema.Message {
	var trimmed []schema.Message
	if e.trimmer != nil {
		trimmed = e.trimmer.Trim(tail)
	} else {
		trimmed = tail
	}
	if len(trimmed) == 0 && len(tail) > 0 {
		log.Printf("[engine] warning: trimmer returned empty slice; applying emergency floor")
		trimmed = []schema.Message{lastUserMessage(tail)}
	}
	view := make([]schema.Message, 0, 1+len(trimmed))
	view = append(view, systemMsg)
	view = append(view, trimmed...)
	return view
}

func lastUserMessage(tail []schema.Message) schema.Message {
	for i := len(tail) - 1; i >= 0; i-- {
		if tail[i].Role == schema.RoleUser {
			return tail[i]
		}
	}
	return tail[len(tail)-1]
}
```

Remove the now-obsolete `contextHistory` slice from earlier versions of `Run` — the replacement above is self-contained.

Also remove the existing `refreshSystemPrompt` method (the new `Run` mutates `systemMsg.Content` directly after `load_skill`):

```go
func (e *AgentEngine) refreshSystemPrompt(history []schema.Message) []schema.Message {
	if len(history) == 0 || history[0].Role != schema.RoleSystem {
		return history
	}
	out := append([]schema.Message(nil), history...)
	out[0].Content = e.systemPrompt()
	return out
}
```

Delete that method from `internal/engine/loop.go` — it is no longer called.

- [ ] **Step 5: Run the integration tests and verify they pass**

Run:

```bash
go test ./tests/engine -run 'TestEngineAppendsUserPromptAndActMessagesToSession|TestEngineResumeSeesPriorHistory|TestEngineThinkPhaseNotPersisted|TestEngineEmergencyFloorWhenTokenCapIsHostile|TestEnginePersistsToolResultsForResume' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run the full engine test suite to confirm no regression**

Run:

```bash
go test ./tests/engine -count=1
```

Expected: PASS for every engine test. The existing tests work unchanged because (a) the engine creates an in-memory session when `SetSession` was not called, (b) the engine uses an identity trimmer (no bounding) when `SetTrimmer` was not called, and (c) the `userPrompt` parameter is still honored.

- [ ] **Step 7: Commit**

Run:

```bash
git add internal/engine/loop.go tests/engine/session_integration_test.go
git commit -m "feat(engine): seed and persist via session, trim every turn"
```

---

### Task 8: CLI Wiring

**Files:**
- Modify: `cmd/claw/main.go`

- [ ] **Step 1: Add the new flags and session lifecycle to main.go**

In `cmd/claw/main.go`, add this import alongside the existing ones:

```go
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
```

Replace the existing flag block (lines 18-25 in the current `main.go`) with:

```go
	prompt := flag.String("prompt", "", "user prompt; if empty, read from stdin")
	think := flag.Bool("think", false, "enable thinking phase before each action")
	providerName := flag.String("provider", "openai", "provider: openai or claude")
	model := flag.String("model", "", "model id; defaults to LLM_MODEL env or provider default")
	maxTurns := flag.Int("max-turns", 25, "max engine turns per run")
	maxTokens := flag.Int("max-tokens", 4096, "max output tokens (claude only)")
	workDir := flag.String("workdir", "", "workspace root; defaults to cwd")
	sessionID := flag.String("session", "", "resume the named session; empty creates a fresh one")
	sessionsDir := flag.String("sessions-dir", "", "directory for session files; defaults to <workdir>/.claw/sessions")
	maxContextTurns := flag.Int("max-context-turns", 6, "trim window: keep this many recent user turns")
	maxContextTokens := flag.Int("max-context-tokens", 32000, "trim window: hard ceiling on estimated tokens (chars/4)")
	trimStrategy := flag.String("trim-strategy", "window", "trim strategy name; v1 ships only 'window'")
	flag.Parse()
```

Replace the existing tool-registration block, context-manager setup, and engine construction with the following (the existing block runs from "registry := tools.NewRegistry()" through "eng := engine.NewAgentEngine(...)" and the call to `Run`):

```go
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(cwd))
	registry.Register(tools.NewWriteFileTool(cwd))
	registry.Register(tools.NewEditFileTool(cwd))
	registry.Register(tools.NewBashTool(cwd))

	contextManager, err := agentcontext.NewManager(cwd)
	if err != nil {
		log.Fatalf("init context: %v", err)
	}
	if contextManager.HasSkills() {
		registry.Register(agentcontext.NewLoadSkillTool(contextManager))
	}

	resolvedSessionsDir := *sessionsDir
	if resolvedSessionsDir == "" {
		resolvedSessionsDir = filepath.Join(cwd, ".claw", "sessions")
	}

	sess, resumed, err := openOrCreateSession(*sessionID, resolvedSessionsDir)
	if err != nil {
		log.Fatalf("session: %v", err)
	}
	defer sess.Close()
	if resumed {
		fmt.Fprintf(os.Stderr, "session: %s (resumed, %d messages)\n", sess.ID(), len(sess.Messages()))
	} else {
		fmt.Fprintf(os.Stderr, "session: %s\n", sess.ID())
	}

	trimmer, err := agentsession.Get(*trimStrategy, agentsession.TrimConfig{
		MaxUserTurns: *maxContextTurns,
		MaxTokens:    *maxContextTokens,
	})
	if err != nil {
		log.Fatalf("trim strategy: %v", err)
	}

	eng := engine.NewAgentEngine(llm, registry, cwd, *think)
	eng.SetContextManager(contextManager)
	eng.SetSession(sess)
	eng.SetTrimmer(trimmer)
	eng.MaxTurns = *maxTurns
	reporter := engine.NewTerminalReporter()

	if err := eng.Run(context.Background(), userPrompt, reporter); err != nil {
		log.Fatalf("engine: %v", err)
	}
```

Add this helper at the bottom of `cmd/claw/main.go`, after `newProvider`:

```go
// openOrCreateSession resolves the --session flag into a Session.
// Empty id creates a fresh session; non-empty id resumes (hard error
// if the file is missing) and rejects ids that fail format validation
// to block path traversal at the flag boundary.
func openOrCreateSession(id string, dir string) (*agentsession.Session, bool, error) {
	if id == "" {
		s, err := agentsession.NewSession(dir)
		if err != nil {
			return nil, false, err
		}
		return s, false, nil
	}
	if !agentsession.IsValidID(id) {
		return nil, false, fmt.Errorf("invalid session id %q (must match YYYYMMDD-HHMMSS-XXXXXX)", id)
	}
	s, err := agentsession.OpenSession(id, dir)
	if err != nil {
		return nil, false, err
	}
	return s, true, nil
}
```

Add `"path/filepath"` to the import block if it is not already imported (it currently is not). The full top of file imports become:

```go
import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)
```

- [ ] **Step 2: Build the CLI and confirm it links**

Run:

```bash
go build ./cmd/claw
```

Expected: builds with no output.

- [ ] **Step 3: Smoke-test the CLI without making a real LLM call**

Run:

```bash
LLM_API_KEY=fake go run ./cmd/claw --prompt "noop" --workdir "$(pwd)" 2>&1 | head -3
```

Expected: stderr first line matches `session: 20\d{6}-\d{6}-[0-9a-f]{6}` (the new session id), followed by a real provider error from the fake API key. The line confirms session machinery initializes correctly before the network call.

- [ ] **Step 4: Smoke-test resume rejection on unknown id**

Run:

```bash
LLM_API_KEY=fake go run ./cmd/claw --prompt "noop" --session "20000101-000000-000000" --workdir "$(pwd)" 2>&1 | head -1
```

Expected: stderr contains `session 20000101-000000-000000 not found` and the process exits non-zero.

- [ ] **Step 5: Smoke-test invalid id rejection**

Run:

```bash
LLM_API_KEY=fake go run ./cmd/claw --prompt "noop" --session "../etc/passwd" --workdir "$(pwd)" 2>&1 | head -1
```

Expected: stderr contains `invalid session id` and the process exits non-zero.

- [ ] **Step 6: Clean up any session files created by the smoke tests**

Run:

```bash
rm -rf .claw/sessions
```

Expected: removes only directories you just created during smoke testing.

- [ ] **Step 7: Commit**

Run:

```bash
git add cmd/claw/main.go
git commit -m "feat(cli): session flags and trim strategy wiring"
```

---

### Task 9: Documentation And Final Verification

**Files:**
- Modify: `README.md`
- Modify: `TODOS.md`

- [ ] **Step 1: Update README with session documentation**

In `README.md`, add the following section after the existing "Dynamic context" section (which currently ends just before "## Quickstart"):

````markdown
## Sessions

Every `claw` run is recorded as a session in `${workdir}/.claw/sessions/<id>.jsonl`.
The id is printed to stderr on the first line:

```
session: 20260518-093045-a1b2c3
```

Resume a conversation by passing the id back:

```bash
go run ./cmd/claw --prompt "another question" --session 20260518-093045-a1b2c3
```

What the model sees each turn is bounded by a trim strategy. The default
(`window`) keeps the last `--max-context-turns` user turns under a
`--max-context-tokens` chars/4 estimate, dropping the oldest user turn
first when either limit is exceeded. Orphan tool messages and dangling
tool_call/result pairs are removed defensively so the view sent to the
provider is always valid.

| Flag | Default | Notes |
|------|---------|-------|
| `--session` | (empty) | empty creates a fresh session; passed id resumes |
| `--sessions-dir` | `${workdir}/.claw/sessions` | override session storage location |
| `--max-context-turns` | `6` | window depth in user turns |
| `--max-context-tokens` | `32000` | estimated-token ceiling (chars/4) |
| `--trim-strategy` | `window` | currently the only built-in strategy |

Concurrent writers to the same session are permitted at the file
integrity layer via per-append `flock(LOCK_EX)`. Two processes resuming
the same session simultaneously may interleave their turns in ways the
trimmer best-effort cleans up; treat "one process per session" as the
recommended pattern.

Windows note: `flock` is not used on Windows, so concurrent writers on
that platform are not protected against torn lines. The project is
POSIX-focused (see the `bash` tool sandboxing notes).
````

In the existing "Known limitations" section, add the bullet:

```markdown
- Sessions are append-only JSONL. Long sessions accumulate every message
  ever appended even though the model only sees the windowed view.
  A future `claw sessions compact <id>` command could rewrite the file
  to the windowed view; for now, inspection and pruning happen with
  `cat`, `head`, and `rm`.
```

- [ ] **Step 2: Close the TODOS entry**

In `TODOS.md`, remove the "Cap context history to prevent context-window exhaustion" entry under "## Engine" entirely (it is closed by this work).

- [ ] **Step 3: Run the full test suite**

Run:

```bash
go test ./... -count=1
```

Expected: PASS across every package.

- [ ] **Step 4: Confirm clean working tree before final commit**

Run:

```bash
git status --short
```

Expected: only `README.md` and `TODOS.md` are modified (everything else was already committed in earlier tasks).

- [ ] **Step 5: Commit**

Run:

```bash
git add README.md TODOS.md
git commit -m "docs: document session management and close cap-history TODO"
```

---

## Final Verification

After all tasks are complete, run:

```bash
go test ./... -count=1
git status --short
git log --oneline -10
```

Expected:

- `go test ./... -count=1` passes.
- `git status --short` is empty.
- `git log --oneline -10` shows the nine commits added by this plan (id generator, token estimator, trimmer interface, window trimmer, flock helpers, jsonl store, engine integration, CLI wiring, docs).
