package engine_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/engine"
)

// captureStdout replaces os.Stdout for the duration of fn, then returns what
// was written.
func captureStdout(fn func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestTerminalReporterOnThinking(t *testing.T) {
	r := engine.NewTerminalReporter()
	got := captureStdout(func() { r.OnThinking(context.Background()) })
	want := "[thinking]\n"
	if got != want {
		t.Fatalf("OnThinking output = %q, want %q", got, want)
	}
}

func TestTerminalReporterOnToolCall(t *testing.T) {
	r := engine.NewTerminalReporter()
	got := captureStdout(func() {
		r.OnToolCall(context.Background(), "read_file", `{"path":"main.go"}`)
	})
	want := fmt.Sprintf("[tool] read_file args=%s\n", `{"path":"main.go"}`)
	if got != want {
		t.Fatalf("OnToolCall output = %q, want %q", got, want)
	}
}

func TestTerminalReporterOnToolResultOK(t *testing.T) {
	r := engine.NewTerminalReporter()
	got := captureStdout(func() {
		r.OnToolResult(context.Background(), "read_file", "file contents", false)
	})
	want := "[tool:ok] read_file result=file contents\n"
	if got != want {
		t.Fatalf("OnToolResult(ok) output = %q, want %q", got, want)
	}
}

func TestTerminalReporterOnToolResultError(t *testing.T) {
	r := engine.NewTerminalReporter()
	got := captureStdout(func() {
		r.OnToolResult(context.Background(), "bash", "permission denied", true)
	})
	want := "[tool:error] bash result=permission denied\n"
	if got != want {
		t.Fatalf("OnToolResult(error) output = %q, want %q", got, want)
	}
}

func TestTerminalReporterOnMessage(t *testing.T) {
	r := engine.NewTerminalReporter()
	got := captureStdout(func() {
		r.OnMessage(context.Background(), "task complete")
	})
	want := "task complete\n"
	if got != want {
		t.Fatalf("OnMessage output = %q, want %q", got, want)
	}
}

func TestTerminalReporterImplementsReporter(t *testing.T) {
	// Compile-time check: *TerminalReporter must satisfy engine.Reporter.
	var _ engine.Reporter = engine.NewTerminalReporter()
}

func TestTerminalReporterOnCompactPrintsShortForm(t *testing.T) {
	stderr := captureStderr(t, func() {
		rep := engine.NewTerminalReporter()
		stats := compact.CompactStats{
			Turn: 7, Before: 48_210, AfterLayerA: 48_000, AfterLayerB: 47_920,
			Budget: 100_000, Saved: 290, ToolOutputsSpilled: 2,
		}
		rep.OnCompact(context.Background(), stats)
	})
	if !strings.Contains(stderr, "[compact]") {
		t.Fatalf("missing [compact] prefix: %q", stderr)
	}
	if !strings.Contains(stderr, "turn 7") {
		t.Fatalf("missing turn: %q", stderr)
	}
	if !strings.Contains(stderr, "saved 290") {
		t.Fatalf("missing saved: %q", stderr)
	}
	if !strings.Contains(stderr, "2 tool output") {
		t.Fatalf("missing spill count: %q", stderr)
	}
}

func TestTerminalReporterOnCompactLayerBLine(t *testing.T) {
	stderr := captureStderr(t, func() {
		rep := engine.NewTerminalReporter()
		stats := compact.CompactStats{
			Turn: 12, Before: 192_430, AfterLayerB: 11_930, Budget: 144_000,
			Saved: 180_500, LayerBEngaged: true, TurnsFolded: 8, ToolOutputsSpilled: 1,
		}
		rep.OnCompact(context.Background(), stats)
	})
	if !strings.Contains(stderr, "folded turns") {
		t.Fatalf("missing folded marker: %q", stderr)
	}
	if !strings.Contains(stderr, "budget 144000") && !strings.Contains(stderr, "budget 144,000") {
		t.Fatalf("missing budget: %q", stderr)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = buf.ReadFrom(r)
		close(done)
	}()
	fn()
	_ = w.Close()
	<-done
	return buf.String()
}
