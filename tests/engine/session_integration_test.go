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
