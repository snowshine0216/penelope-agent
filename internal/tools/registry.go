package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// Tool is the interface every concrete tool must implement.
type Tool interface {
	// Name returns the globally unique tool name (the model uses this to invoke it).
	Name() string

	// Definition returns the tool metadata and JSON Schema submitted to the model.
	Definition() schema.ToolDefinition

	// Execute receives the raw JSON arguments emitted by the model and runs the tool.
	// Deserialization of args is handled by each concrete tool implementation.
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry defines the tool registration and dispatch interface.
type Registry interface {
	// Register mounts a new tool into the registry.
	Register(tool Tool)

	// GetAvailableTools returns the schemas of all mounted tools for the provider.
	GetAvailableTools() []schema.ToolDefinition

	// Execute routes and runs a model-requested tool call.
	Execute(ctx context.Context, call schema.ToolCall) schema.ToolResult
}

// registryImpl is the default Registry implementation.
type registryImpl struct {
	// tools uses the tool Name as key for O(1) routing.
	tools map[string]Tool
}

func NewRegistry() Registry {
	return &registryImpl{
		tools: make(map[string]Tool),
	}
}

func (r *registryImpl) Register(tool Tool) {
	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		log.Printf("[registry] tool %q already registered, overwriting", name)
	}
	r.tools[name] = tool
	log.Printf("[registry] mounted tool: %s", name)
}

func (r *registryImpl) GetAvailableTools() []schema.ToolDefinition {
	defs := make([]schema.ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		defs = append(defs, tool.Definition())
	}
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})
	return defs
}

func (r *registryImpl) Execute(ctx context.Context, call schema.ToolCall) schema.ToolResult {
	tool, exists := r.tools[call.Name]
	if !exists {
		errMsg := fmt.Sprintf("Error: tool %q not found", call.Name)
		return schema.ToolResult{
			ToolCallID: call.ID,
			Output:     errMsg,
			IsError:    true,
		}
	}

	output, err := tool.Execute(ctx, call.Arguments)

	if err != nil {
		errMsg := fmt.Sprintf("Error executing %s: %v", call.Name, err)
		return schema.ToolResult{
			ToolCallID: call.ID,
			Output:     errMsg,
			IsError:    true,
		}
	}

	return schema.ToolResult{
		ToolCallID: call.ID,
		Output:     output,
		IsError:    false,
	}
}
