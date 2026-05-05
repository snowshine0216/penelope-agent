package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func TestBashTimeoutClampsToMaximum(t *testing.T) {
	// Supplying timeout_s > 600 must be clamped and not cause a panic or hang.
	// We verify via a fast command that completes normally.
	tool := tools.NewBashTool(t.TempDir())
	args, err := json.Marshal(map[string]interface{}{"command": "echo clamped", "timeout_s": 9999})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, execErr := tool.Execute(context.Background(), args)
	if execErr != nil {
		t.Fatalf("execute returned Go error: %v", execErr)
	}
	if !strings.Contains(out, "clamped") {
		t.Fatalf("expected 'clamped' in output, got: %q", out)
	}
}
