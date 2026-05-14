package agentcontext

import (
	"fmt"
	"strings"
)

const DefaultBaseInstructions = "You are penelope-agent, an expert coding assistant. You have full access to tools in the workspace."

type ComposerInput struct {
	BaseInstructions string
	Agents           string
	Catalog          SkillCatalog
	Loaded           []LoadedSkill
}

type Composer struct {
	base    string
	agents  string
	catalog SkillCatalog
	loaded  []LoadedSkill
}

func NewComposer(input ComposerInput) Composer {
	base := strings.TrimSpace(input.BaseInstructions)
	if base == "" {
		base = DefaultBaseInstructions
	}
	return Composer{
		base:    base,
		agents:  strings.TrimSpace(input.Agents),
		catalog: input.Catalog,
		loaded:  cloneLoadedSkills(input.Loaded),
	}
}

func (c Composer) WithLoadedSkill(skill LoadedSkill) Composer {
	if c.HasLoadedSkill(skill.Meta.Name) {
		return c
	}
	next := c
	next.loaded = append(cloneLoadedSkills(c.loaded), skill)
	return next
}

func (c Composer) HasLoadedSkill(name string) bool {
	for _, skill := range c.loaded {
		if skill.Meta.Name == name {
			return true
		}
	}
	return false
}

func (c Composer) SystemPrompt() string {
	sections := []string{c.base}
	if c.agents != "" {
		sections = append(sections, "## Workdir Instructions\n\n"+c.agents)
	}
	if len(c.catalog.Skills) > 0 {
		sections = append(sections, c.skillCatalogSection())
		sections = append(sections, skillLoadingSection())
	}
	for _, skill := range c.loaded {
		sections = append(sections, loadedSkillSection(skill))
	}
	return strings.Join(sections, "\n\n")
}

func (c Composer) skillCatalogSection() string {
	lines := []string{
		"## Local Skills",
		"",
		"These skills are available under .claw/skills. Initially only metadata is loaded.",
		"Call load_skill with the exact skill name before following a skill's instructions.",
		"",
	}
	for _, skill := range c.catalog.Skills {
		lines = append(lines, fmt.Sprintf("- name: %s", skill.Name))
		lines = append(lines, fmt.Sprintf("  description: %s", skill.Description))
		if len(skill.Aliases) > 0 {
			lines = append(lines, fmt.Sprintf("  aliases: %s", strings.Join(skill.Aliases, ", ")))
		}
	}
	return strings.Join(lines, "\n")
}

func skillLoadingSection() string {
	return strings.Join([]string{
		"## Skill Loading",
		"",
		"When a local skill is materially relevant to the user's request, call load_skill with the exact skill name before following that skill's instructions.",
		"Do not call load_skill for unrelated skills.",
	}, "\n")
}

func loadedSkillSection(skill LoadedSkill) string {
	return fmt.Sprintf("## Loaded Skill: %s\n\nSource: %s\n\n%s", skill.Meta.Name, skill.Meta.RelPath, strings.TrimSpace(skill.Body))
}

func cloneLoadedSkills(skills []LoadedSkill) []LoadedSkill {
	out := make([]LoadedSkill, len(skills))
	copy(out, skills)
	return out
}
