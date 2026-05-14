package context_test

import (
	"strings"
	"testing"

	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
)

func TestComposerBaseOnly(t *testing.T) {
	composer := agentcontext.NewComposer(agentcontext.ComposerInput{
		BaseInstructions: "You are penelope-agent.",
	})

	got := composer.SystemPrompt()
	if got != "You are penelope-agent." {
		t.Fatalf("prompt = %q", got)
	}
}

func TestComposerIncludesAgentsCatalogAndLoadedSkillsInOrder(t *testing.T) {
	composer := agentcontext.NewComposer(agentcontext.ComposerInput{
		BaseInstructions: "Base.",
		Agents:           "AGENTS rules.",
		Catalog: agentcontext.SkillCatalog{Skills: []agentcontext.SkillMeta{
			{Name: "investigate", Description: "Debug deeply.", RelPath: ".claw/skills/investigate/SKILL.md"},
		}},
	})
	composer = composer.WithLoadedSkill(agentcontext.LoadedSkill{
		Meta: agentcontext.SkillMeta{Name: "investigate", RelPath: ".claw/skills/investigate/SKILL.md"},
		Body: "# Investigate\n\nInstructions.\n",
	})

	got := composer.SystemPrompt()
	wantOrder := []string{
		"Base.",
		"## Workdir Instructions",
		"AGENTS rules.",
		"## Local Skills",
		"name: investigate",
		"## Skill Loading",
		"## Loaded Skill: investigate",
		"# Investigate",
	}

	last := -1
	for _, marker := range wantOrder {
		index := strings.Index(got, marker)
		if index < 0 {
			t.Fatalf("prompt missing marker %q:\n%s", marker, got)
		}
		if index <= last {
			t.Fatalf("marker %q appeared out of order in:\n%s", marker, got)
		}
		last = index
	}
}

func TestComposerDoesNotDuplicateLoadedSkill(t *testing.T) {
	composer := agentcontext.NewComposer(agentcontext.ComposerInput{
		BaseInstructions: "Base.",
	})
	loaded := agentcontext.LoadedSkill{
		Meta: agentcontext.SkillMeta{Name: "investigate", RelPath: ".claw/skills/investigate/SKILL.md"},
		Body: "# Investigate\n",
	}
	composer = composer.WithLoadedSkill(loaded)
	composer = composer.WithLoadedSkill(loaded)

	got := composer.SystemPrompt()
	if strings.Count(got, "## Loaded Skill: investigate") != 1 {
		t.Fatalf("loaded skill duplicated in:\n%s", got)
	}
}
