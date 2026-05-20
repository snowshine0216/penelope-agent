package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/session"
)

// ReadToolOutputTool exposes session.ReadToolOutputChunk to the model.
// The model uses it to retrieve a chunk of a previously-spilled tool
// output after seeing the elision marker that Layer A leaves behind.
type ReadToolOutputTool struct {
	sess         *session.Session
	maxToolBytes int
}

// NewReadToolOutputTool wires the tool to a session and a byte cap.
// The byte cap matches the engine's MaxToolBytes so the chunk
// returned here is governed by the same boundary as any other tool
// result.
func NewReadToolOutputTool(sess *session.Session, maxToolBytes int) *ReadToolOutputTool {
	if maxToolBytes <= 0 {
		maxToolBytes = 65536
	}
	return &ReadToolOutputTool{sess: sess, maxToolBytes: maxToolBytes}
}

func (t *ReadToolOutputTool) Name() string { return "read_tool_output" }

func (t *ReadToolOutputTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name: "read_tool_output",
		Description: "Read a chunk of a previously-spilled tool output by its tool_call_id. " +
			"Use this when an earlier tool result was too large and was elided. " +
			"The elision marker in the original result shows the call_id and total lines.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"call_id": map[string]interface{}{
					"type":        "string",
					"description": "The tool_call_id of the original call.",
				},
				"start_line": map[string]interface{}{
					"type":        "integer",
					"description": "1-indexed line to start at (default 1).",
				},
				"line_count": map[string]interface{}{
					"type":        "integer",
					"description": "Number of lines to read (default 200, max 1000).",
				},
			},
			"required": []string{"call_id"},
		},
	}
}

type readToolOutputArgs struct {
	CallID    string `json:"call_id"`
	StartLine int    `json:"start_line"`
	LineCount int    `json:"line_count"`
}

func (t *ReadToolOutputTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var args readToolOutputArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("read_tool_output: invalid arguments: %w", err)
	}
	if args.CallID == "" {
		return "", fmt.Errorf("read_tool_output: call_id is required")
	}
	if args.StartLine < 1 {
		args.StartLine = 1
	}
	if args.LineCount < 1 {
		args.LineCount = 200
	}
	if args.LineCount > 1000 {
		args.LineCount = 1000
	}

	chunk, total, err := t.sess.ReadToolOutputChunk(args.CallID, args.StartLine, args.LineCount)
	if err != nil {
		return "", fmt.Errorf("read_tool_output: call_id=%q not found in tool-outputs dir: %w", args.CallID, err)
	}

	endLine := args.StartLine + args.LineCount - 1
	if endLine > total {
		endLine = total
	}
	header := fmt.Sprintf("lines %d-%d of %d (call_id=%s)\n", args.StartLine, endLine, total, args.CallID)
	body := chunk
	if endLine < total {
		body += fmt.Sprintf("...[%d more remaining; call read_tool_output with start_line=%d]\n", total-endLine, endLine+1)
	}
	combined := header + body
	if len(combined) > t.maxToolBytes {
		return TruncateWithMarker(combined, t.maxToolBytes,
			fmt.Sprintf("\n...[chunk capped at %d bytes; lower line_count or use a tighter start_line]...\n", t.maxToolBytes)), nil
	}
	return combined, nil
}
