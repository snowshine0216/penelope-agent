//go:build live_provider

package engine_test

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	"github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// liveReporter captures stats for assertion and forwards messages.
type liveReporter struct {
	stats          []compact.CompactStats
	layerBFired    atomic.Bool
	readToolUsed   atomic.Bool
	lastChunk      string
	lastChunkIsErr bool
}

func (r *liveReporter) OnThinking(_ context.Context)              {}
func (r *liveReporter) OnToolCall(_ context.Context, _, _ string) {}
func (r *liveReporter) OnMessage(_ context.Context, _ string)     {}
func (r *liveReporter) OnToolResult(_ context.Context, name, result string, isError bool) {
	if name == "read_tool_output" {
		r.readToolUsed.Store(true)
		r.lastChunk = result
		r.lastChunkIsErr = isError
	}
}
func (r *liveReporter) OnCompact(_ context.Context, s compact.CompactStats) {
	r.stats = append(r.stats, s)
	if s.LayerBEngaged {
		r.layerBFired.Store(true)
	}
}

func TestCompact_LiveClaude(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set; skipping live-provider smoke")
	}

	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer sess.Close()

	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(dir))
	registry.Register(tools.NewReadToolOutputTool(sess, 65536))

	llm, err := provider.NewClaudeProvider("claude-opus-4-7")
	if err != nil {
		t.Fatalf("claude: %v", err)
	}

	// Memory ceiling — track residency before and after to detect leak/OOM.
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	cfg := compact.Config{MaxToolBytes: 65536, RecentTurnsVerbatim: 4}
	c := compact.NewCompactor(cfg)
	cal := compact.NewCalibrator(0.3)

	eng := engine.NewAgentEngine(llm, registry, dir, false)
	eng.SetSession(sess)
	eng.SetCompactor(c)
	eng.SetCalibrator(cal)
	eng.SetCompactConfig(cfg)
	eng.SetModelID("claude-opus-4-7")
	eng.SetOutputCap(4096)
	eng.SetSafetyFactor(0.75)
	eng.MaxTurns = 6

	rep := &liveReporter{}
	prompt := `run "find / -type f 2>/dev/null | head -50000" and tell me the count, ` +
		`then call read_tool_output on the same call_id to confirm you can retrieve a chunk`

	if err := eng.Run(context.Background(), prompt, rep); err != nil {
		t.Logf("engine returned (may be expected if model stopped early): %v", err)
	}

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	const memCeiling = uint64(512 << 20) // 512 MB
	if memAfter.HeapAlloc > memBefore.HeapAlloc+memCeiling {
		t.Fatalf("heap grew by %d MB (>= 512 MB ceiling)",
			(memAfter.HeapAlloc-memBefore.HeapAlloc)>>20)
	}

	if !rep.layerBFired.Load() {
		t.Fatalf("Layer B never engaged across %d compact events", len(rep.stats))
	}
	if !rep.readToolUsed.Load() {
		t.Fatalf("model did not call read_tool_output during follow-up turn")
	}
	if rep.lastChunkIsErr {
		t.Fatalf("read_tool_output returned error: %q", rep.lastChunk)
	}
	if strings.TrimSpace(rep.lastChunk) == "" {
		t.Fatalf("read_tool_output returned empty chunk")
	}
}
