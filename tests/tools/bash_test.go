package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func bashArgs(t *testing.T, command string) json.RawMessage {
	t.Helper()

	out, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatalf("marshal bash args: %v", err)
	}
	return out
}

func TestBashEchoReturnsStdout(t *testing.T) {
	tool := tools.NewBashTool(t.TempDir())

	out, err := tool.Execute(context.Background(), bashArgs(t, "echo hello"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected 'hello' in output, got: %q", out)
	}
}

func TestBashRunsInWorkDir(t *testing.T) {
	dir := t.TempDir()
	tool := tools.NewBashTool(dir)

	out, err := tool.Execute(context.Background(), bashArgs(t, "pwd"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, dir) {
		t.Fatalf("pwd should print workDir %q, got: %q", dir, out)
	}
}

func TestBashFailingCommandReturnsCombinedErrorAsValue(t *testing.T) {
	// Failing commands must NOT return a Go error; the error message has to
	// reach the model so it can self-correct.
	tool := tools.NewBashTool(t.TempDir())

	out, err := tool.Execute(context.Background(), bashArgs(t, "exit 7"))
	if err != nil {
		t.Fatalf("execute returned Go error, expected nil so model can read failure: %v", err)
	}
	if !strings.Contains(out, "execution error") {
		t.Fatalf("expected error preamble in output, got: %q", out)
	}
}

func TestBashEmptyOutputGetsExplicitMessage(t *testing.T) {
	tool := tools.NewBashTool(t.TempDir())

	out, err := tool.Execute(context.Background(), bashArgs(t, "true"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "no output") {
		t.Fatalf("expected explicit success-with-no-output message, got: %q", out)
	}
}

func TestBashLongOutputIsTruncated(t *testing.T) {
	tool := tools.NewBashTool(t.TempDir())

	// Generate ~12000 bytes of output; the 8000-byte cap should kick in.
	out, err := tool.Execute(context.Background(), bashArgs(t, "printf 'a%.0s' {1..12000}"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "elided") {
		t.Fatalf("expected truncation marker in output, got tail: %q", tail(out, 200))
	}
	if len(out) < 8000 {
		t.Fatalf("expected truncated output to be at least 8000 bytes, got %d", len(out))
	}
}

func TestBashRejectsMalformedArgs(t *testing.T) {
	tool := tools.NewBashTool(t.TempDir())

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"command":}`))
	if err == nil {
		t.Fatal("expected JSON parse error for malformed args")
	}
}

func TestBashChainedCommandSucceeds(t *testing.T) {
	tool := tools.NewBashTool(t.TempDir())

	out, err := tool.Execute(context.Background(), bashArgs(t, "echo first && echo second"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Fatalf("chained command output missing expected lines: %q", out)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
