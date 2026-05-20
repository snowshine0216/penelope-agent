package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// fakeBashTool returns a fixed output; useful to simulate a huge bash result.
type fakeBashTool struct{ output string }

func (t *fakeBashTool) Name() string { return "bash" }
func (t *fakeBashTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{Name: "bash"}
}
func (t *fakeBashTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return t.output, nil
}

type compactCapturingReporter struct {
	statsSeen []compact.CompactStats
	messages  []string
}

func (r *compactCapturingReporter) OnThinking(_ context.Context)                        {}
func (r *compactCapturingReporter) OnToolCall(_ context.Context, _, _ string)           {}
func (r *compactCapturingReporter) OnToolResult(_ context.Context, _, _ string, _ bool) {}
func (r *compactCapturingReporter) OnMessage(_ context.Context, c string) {
	r.messages = append(r.messages, c)
}
func (r *compactCapturingReporter) OnCompact(_ context.Context, s compact.CompactStats) {
	r.statsSeen = append(r.statsSeen, s)
}

func TestCompactIntegrationHugeToolOutputSpills(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer sess.Close()

	huge := strings.Repeat("x", 200_000)
	registry := tools.NewRegistry()
	registry.Register(&fakeBashTool{output: huge})

	args, _ := json.Marshal(map[string]string{"command": "find / -type f"})
	fp := &fakeProvider{
		responses: []schema.Message{
			{Role: schema.RoleAssistant, ToolCalls: []schema.ToolCall{{ID: "tool_huge", Name: "bash", Arguments: args}}},
			{Role: schema.RoleAssistant, Content: "done"},
		},
		usage: provider.Usage{InputTokens: 1234, OutputTokens: 56},
	}

	cfg := compact.Config{MaxToolBytes: 4096, RecentTurnsVerbatim: 4}
	c := compact.NewCompactor(cfg)
	cal := compact.NewCalibrator(0.3)

	eng := engine.NewAgentEngine(fp, registry, dir, false)
	eng.SetSession(sess)
	eng.SetCompactor(c)
	eng.SetCalibrator(cal)
	eng.SetCompactConfig(cfg)
	eng.SetModelID("claude-opus-4-7")
	eng.SetOutputCap(4096)
	eng.SetSafetyFactor(0.75)

	rep := &compactCapturingReporter{}
	if err := eng.Run(context.Background(), "find huge files", rep); err != nil {
		if !errors.Is(err, engine.ErrMaxTurnsExceeded) {
			t.Fatalf("run: %v", err)
		}
	}

	// Spill file exists at spec layout: <sessionsDir>/<sid>/tool-outputs/<call-id>.txt
	spillPath := filepath.Join(dir, sess.ID(), "tool-outputs", "tool_huge.txt")
	if _, err := os.Stat(spillPath); err != nil {
		t.Fatalf("spill file missing at spec path %q: %v", spillPath, err)
	}

	// Marker in the session's tool message refers to call_id.
	found := false
	for _, m := range sess.Messages() {
		if m.Role == schema.RoleTool && m.ToolCallID == "tool_huge" {
			if !strings.Contains(m.Content, "tool_huge") {
				t.Fatalf("marker missing call_id reference: %q", m.Content)
			}
			if len(m.Content) >= len(huge) {
				t.Fatalf("tool output not capped in session: len=%d", len(m.Content))
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("tool result for tool_huge missing from session")
	}

	// read_tool_output retrieves a chunk.
	rt := tools.NewReadToolOutputTool(sess, 65536)
	a, _ := json.Marshal(map[string]any{"call_id": "tool_huge", "start_line": 1, "line_count": 10})
	out, err := rt.Execute(context.Background(), a)
	if err != nil {
		t.Fatalf("read_tool_output: %v", err)
	}
	if !strings.Contains(out, "tool_huge") {
		t.Fatalf("retrieved chunk missing call_id header: %q", out)
	}
}

func TestCompactIntegrationLastUsageThreadsForward(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	registry := tools.NewRegistry()
	fp := &fakeProvider{
		responses: []schema.Message{
			{Role: schema.RoleAssistant, Content: "first"},
		},
		usage: provider.Usage{InputTokens: 9000, OutputTokens: 200},
	}
	cfg := compact.Config{MaxToolBytes: 65536, RecentTurnsVerbatim: 4}
	eng := engine.NewAgentEngine(fp, registry, dir, false)
	eng.SetSession(sess)
	eng.SetCompactor(compact.NewCompactor(cfg))
	eng.SetCalibrator(compact.NewCalibrator(0.3))
	eng.SetCompactConfig(cfg)
	eng.SetModelID("claude-opus-4-7")
	eng.SetOutputCap(4096)
	eng.SetSafetyFactor(0.75)

	rep := &compactCapturingReporter{}
	if err := eng.Run(context.Background(), "hi", rep); err != nil {
		t.Fatalf("run: %v", err)
	}
	if eng.LastUsageForTest().InputTokens != 9000 {
		t.Fatalf("lastUsage not threaded: %+v", eng.LastUsageForTest())
	}
}
