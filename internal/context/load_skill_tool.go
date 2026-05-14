package agentcontext

import (
	stdctx "context"
	"encoding/json"
	"fmt"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

const LoadSkillToolName = "load_skill"

type LoadSkillTool struct {
	manager *Manager
}

type loadSkillInput struct {
	Name string `json:"name"`
}

func NewLoadSkillTool(manager *Manager) *LoadSkillTool {
	return &LoadSkillTool{manager: manager}
}

func (t *LoadSkillTool) Name() string {
	return LoadSkillToolName
}

func (t *LoadSkillTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        LoadSkillToolName,
		Description: "Load full instructions for a relevant local skill from .claw/skills by exact skill name.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"required":   []string{"name"},
			"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string", "enum": t.manager.AvailableSkillNames()}},
		},
	}
}

// ExecutionPolicy is declared serial for documentation purposes. In practice the
// engine intercepts load_skill via hasLoadSkillCall before PlanToolCallGroups and
// never consults this policy.
func (t *LoadSkillTool) ExecutionPolicy() tools.ExecutionPolicy {
	return tools.ExecutionPolicy{}
}

func (t *LoadSkillTool) Execute(_ stdctx.Context, args json.RawMessage) (string, error) {
	var input loadSkillInput
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("parse load_skill args: %w", err)
	}
	return t.manager.LoadSkill(input.Name)
}
