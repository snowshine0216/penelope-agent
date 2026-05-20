package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestReadToolOutputBasic(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	body := "line 1\nline 2\nline 3\nline 4\n"
	if _, _, err := sess.SpillToolOutput("c1", body); err != nil {
		t.Fatalf("spill: %v", err)
	}
	tool := tools.NewReadToolOutputTool(sess, 65536)
	args, _ := json.Marshal(map[string]any{"call_id": "c1", "start_line": 2, "line_count": 2})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "lines 2-3 of 4") {
		t.Fatalf("header missing: %q", out)
	}
	if !strings.Contains(out, "line 2") || !strings.Contains(out, "line 3") {
		t.Fatalf("body missing: %q", out)
	}
}

func TestReadToolOutputDefaultArgs(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	if _, _, err := sess.SpillToolOutput("c1", "a\nb\nc\n"); err != nil {
		t.Fatalf("spill: %v", err)
	}
	tool := tools.NewReadToolOutputTool(sess, 65536)
	args, _ := json.Marshal(map[string]any{"call_id": "c1"})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "lines 1-3 of 3") {
		t.Fatalf("default header missing: %q", out)
	}
}

func TestReadToolOutputUnknownCallID(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	tool := tools.NewReadToolOutputTool(sess, 65536)
	args, _ := json.Marshal(map[string]any{"call_id": "ghost"})
	out, err := tool.Execute(context.Background(), args)
	if err == nil && !strings.Contains(out, "not found") && !strings.Contains(out, "no such") {
		t.Fatalf("expected error or not-found marker, got: %q / %v", out, err)
	}
}

func TestReadToolOutputArgValidation(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	tool := tools.NewReadToolOutputTool(sess, 65536)

	// Empty call_id.
	args, _ := json.Marshal(map[string]any{"call_id": ""})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatalf("empty call_id should error")
	}
	// Negative start_line accepted (clamped to 1) — should not panic.
	if _, _, err := sess.SpillToolOutput("c", "x\n"); err != nil {
		t.Fatalf("spill: %v", err)
	}
	args, _ = json.Marshal(map[string]any{"call_id": "c", "start_line": -3})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("negative start_line should clamp, got: %v", err)
	}
	// line_count > 1000 clamped to 1000.
	args, _ = json.Marshal(map[string]any{"call_id": "c", "line_count": 99999})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("huge line_count should clamp, got: %v", err)
	}
}

func TestReadToolOutputTailMarkerWhenMore(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	body := ""
	for i := 1; i <= 50; i++ {
		body += "line\n"
	}
	if _, _, err := sess.SpillToolOutput("c", body); err != nil {
		t.Fatalf("spill: %v", err)
	}
	tool := tools.NewReadToolOutputTool(sess, 65536)
	args, _ := json.Marshal(map[string]any{"call_id": "c", "start_line": 1, "line_count": 10})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "more remaining") && !strings.Contains(out, "40 more") {
		t.Fatalf("tail marker missing: %q", out)
	}
}

func TestReadToolOutputCapByMaxToolBytes(t *testing.T) {
	dir := t.TempDir()
	sess, _ := session.NewSession(dir)
	defer sess.Close()
	huge := strings.Repeat("x", 100_000) + "\n"
	if _, _, err := sess.SpillToolOutput("c", huge); err != nil {
		t.Fatalf("spill: %v", err)
	}
	tool := tools.NewReadToolOutputTool(sess, 4096)
	args, _ := json.Marshal(map[string]any{"call_id": "c"})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(out) > 8192 {
		t.Fatalf("output not capped: len=%d", len(out))
	}
}
