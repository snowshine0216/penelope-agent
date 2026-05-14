package context_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
)

func TestManagerBuildsInitialPromptFromWorkdir(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "AGENTS.md"), "Project rules.")
	writeFile(t, filepath.Join(work, ".claw", "skills", "investigate", "SKILL.md"), "---\nname: investigate\ndescription: Debug deeply.\n---\n# Investigate\n")

	manager, err := agentcontext.NewManager(work)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	prompt := manager.SystemPrompt()
	if !strings.Contains(prompt, "Project rules.") {
		t.Fatalf("prompt missing AGENTS:\n%s", prompt)
	}
	if !strings.Contains(prompt, "name: investigate") {
		t.Fatalf("prompt missing catalog:\n%s", prompt)
	}
	if strings.Contains(prompt, "# Investigate") {
		t.Fatalf("prompt included full body before load:\n%s", prompt)
	}
}

func TestLoadSkillToolLoadsBodyIntoManagerPrompt(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, ".claw", "skills", "investigate", "SKILL.md"), "---\nname: investigate\ndescription: Debug deeply.\n---\n# Investigate\n\nUse the workflow.\n")
	manager, err := agentcontext.NewManager(work)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	tool := agentcontext.NewLoadSkillTool(manager)

	out, execErr := tool.Execute(context.Background(), json.RawMessage(`{"name":"investigate"}`))
	if execErr != nil {
		t.Fatalf("Execute: %v", execErr)
	}
	if out != `loaded skill "investigate"` {
		t.Fatalf("output = %q", out)
	}
	if !strings.Contains(manager.SystemPrompt(), "## Loaded Skill: investigate") {
		t.Fatalf("loaded skill not promoted into prompt:\n%s", manager.SystemPrompt())
	}
}

func TestLoadSkillToolRejectsUnknownSkillWithAvailableNames(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, ".claw", "skills", "investigate", "SKILL.md"), "---\nname: investigate\ndescription: Debug deeply.\n---\n# Investigate\n")
	manager, err := agentcontext.NewManager(work)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	tool := agentcontext.NewLoadSkillTool(manager)

	_, execErr := tool.Execute(context.Background(), json.RawMessage(`{"name":"missing"}`))
	if execErr == nil {
		t.Fatal("expected unknown skill error")
	}
	if !strings.Contains(execErr.Error(), "available: investigate") {
		t.Fatalf("error = %v, want available name", execErr)
	}
}

func TestLoadSkillToolIsIdempotent(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, ".claw", "skills", "investigate", "SKILL.md"), "---\nname: investigate\ndescription: Debug deeply.\n---\n# Investigate\n")
	manager, err := agentcontext.NewManager(work)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	tool := agentcontext.NewLoadSkillTool(manager)

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"investigate"}`)); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"investigate"}`))
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if out != `skill "investigate" already loaded` {
		t.Fatalf("second output = %q", out)
	}
	if strings.Count(manager.SystemPrompt(), "## Loaded Skill: investigate") != 1 {
		t.Fatalf("loaded skill duplicated:\n%s", manager.SystemPrompt())
	}
}

func TestLoadSkillToolDefinitionAndPolicy(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, ".claw", "skills"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(work, ".claw", "skills", "investigate", "SKILL.md"), "---\nname: investigate\ndescription: Debug deeply.\n---\n# Investigate\n")
	manager, err := agentcontext.NewManager(work)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	tool := agentcontext.NewLoadSkillTool(manager)

	if tool.Name() != agentcontext.LoadSkillToolName {
		t.Fatalf("name = %q", tool.Name())
	}
	if tool.ExecutionPolicy().ParallelSafe {
		t.Fatal("load_skill must be serial-only")
	}
	def := tool.Definition()
	if def.Name != agentcontext.LoadSkillToolName {
		t.Fatalf("definition name = %q", def.Name)
	}
}

func TestManagerNilReceiverSystemPromptReturnsDefault(t *testing.T) {
	var m *agentcontext.Manager
	got := m.SystemPrompt()
	if got == "" {
		t.Fatal("nil manager SystemPrompt should return DefaultBaseInstructions, got empty")
	}
}

func TestManagerNilReceiverHasSkillsReturnsFalse(t *testing.T) {
	var m *agentcontext.Manager
	if m.HasSkills() {
		t.Fatal("nil manager HasSkills should return false")
	}
}

func TestManagerNilReceiverAvailableSkillNamesReturnsNil(t *testing.T) {
	var m *agentcontext.Manager
	if names := m.AvailableSkillNames(); names != nil {
		t.Fatalf("nil manager AvailableSkillNames should return nil, got %v", names)
	}
}

func TestManagerNilReceiverLoadSkillReturnsError(t *testing.T) {
	var m *agentcontext.Manager
	_, err := m.LoadSkill("investigate")
	if err == nil {
		t.Fatal("nil manager LoadSkill should return error")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %v, want not configured", err)
	}
}

func TestManagerHasSkillsReturnsFalseWhenNoSkills(t *testing.T) {
	work := t.TempDir()
	manager, err := agentcontext.NewManager(work)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if manager.HasSkills() {
		t.Fatal("manager with no .claw/skills should return HasSkills=false")
	}
}

func TestManagerHasSkillsReturnsTrueWhenSkillsPresent(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, ".claw", "skills", "investigate", "SKILL.md"),
		"---\nname: investigate\ndescription: Debug.\n---\n# Investigate\n")
	manager, err := agentcontext.NewManager(work)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if !manager.HasSkills() {
		t.Fatal("manager with skills should return HasSkills=true")
	}
}

func TestManagerLoadSkillRejectsEmptyName(t *testing.T) {
	work := t.TempDir()
	manager, err := agentcontext.NewManager(work)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	_, err = manager.LoadSkill("   ")
	if err == nil {
		t.Fatal("expected error for blank skill name")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Fatalf("error = %v, want required", err)
	}
}

func TestLoadSkillToolExecuteWithMalformedJSONReturnsError(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, ".claw", "skills", "investigate", "SKILL.md"),
		"---\nname: investigate\ndescription: Debug.\n---\n# Investigate\n")
	manager, err := agentcontext.NewManager(work)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	tool := agentcontext.NewLoadSkillTool(manager)

	_, execErr := tool.Execute(context.Background(), json.RawMessage(`{not-valid-json}`))
	if execErr == nil {
		t.Fatal("expected error for malformed JSON args")
	}
}
