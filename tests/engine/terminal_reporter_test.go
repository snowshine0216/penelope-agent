package engine_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"

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
