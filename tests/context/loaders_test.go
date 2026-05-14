package context_test

import (
	"os"
	"path/filepath"
	"testing"

	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
)

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func TestLoadRootAgentsReadsOnlyWorkdirRoot(t *testing.T) {
	parent := t.TempDir()
	work := filepath.Join(parent, "repo")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	writeFile(t, filepath.Join(parent, "AGENTS.md"), "parent instructions")
	writeFile(t, filepath.Join(work, "AGENTS.md"), "root instructions")
	writeFile(t, filepath.Join(work, "nested", "AGENTS.md"), "nested instructions")

	got, err := agentcontext.LoadRootAgents(work)
	if err != nil {
		t.Fatalf("LoadRootAgents: %v", err)
	}
	if got != "root instructions" {
		t.Fatalf("AGENTS content = %q, want root instructions", got)
	}
}

func TestLoadRootAgentsMissingReturnsEmpty(t *testing.T) {
	got, err := agentcontext.LoadRootAgents(t.TempDir())
	if err != nil {
		t.Fatalf("LoadRootAgents: %v", err)
	}
	if got != "" {
		t.Fatalf("missing AGENTS = %q, want empty", got)
	}
}

func TestLoadSkillCatalogDiscoversDirectChildrenInLexicalOrder(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, ".claw", "skills", "zeta", "SKILL.md"), "---\nname: zeta\ndescription: Z skill.\n---\n# Z\n")
	writeFile(t, filepath.Join(work, ".claw", "skills", "alpha", "SKILL.md"), "---\nname: alpha\ndescription: A skill.\n---\n# A\n")
	writeFile(t, filepath.Join(work, ".claw", "skills", "alpha", "nested", "SKILL.md"), "---\nname: nested\ndescription: Nested skill.\n---\n# Nested\n")

	catalog, err := agentcontext.LoadSkillCatalog(work)
	if err != nil {
		t.Fatalf("LoadSkillCatalog: %v", err)
	}
	if len(catalog.Skills) != 2 {
		t.Fatalf("skills = %#v, want 2 direct skills", catalog.Skills)
	}
	if catalog.Skills[0].Name != "alpha" || catalog.Skills[1].Name != "zeta" {
		t.Fatalf("skill order = %#v, want alpha then zeta", catalog.Skills)
	}
}

func TestLoadSkillCatalogSkipsInvalidAndDuplicateSkills(t *testing.T) {
	work := t.TempDir()
	writeFile(t, filepath.Join(work, ".claw", "skills", "first", "SKILL.md"), "---\nname: dup\ndescription: First.\n---\n# First\n")
	writeFile(t, filepath.Join(work, ".claw", "skills", "invalid", "SKILL.md"), "# invalid\n")
	writeFile(t, filepath.Join(work, ".claw", "skills", "second", "SKILL.md"), "---\nname: dup\ndescription: Second.\n---\n# Second\n")

	catalog, err := agentcontext.LoadSkillCatalog(work)
	if err != nil {
		t.Fatalf("LoadSkillCatalog: %v", err)
	}
	if len(catalog.Skills) != 1 || catalog.Skills[0].RelPath != ".claw/skills/first/SKILL.md" {
		t.Fatalf("skills = %#v, want first duplicate only", catalog.Skills)
	}
	if len(catalog.Skipped) != 2 {
		t.Fatalf("skipped = %#v, want invalid and duplicate", catalog.Skipped)
	}
}

func TestLoadSkillBodyReturnsBodyAfterFrontmatter(t *testing.T) {
	work := t.TempDir()
	path := filepath.Join(work, ".claw", "skills", "investigate", "SKILL.md")
	writeFile(t, path, "---\nname: investigate\ndescription: Debug.\n---\n# Body\n\nUse care.\n")

	catalog, err := agentcontext.LoadSkillCatalog(work)
	if err != nil {
		t.Fatalf("LoadSkillCatalog: %v", err)
	}
	loaded, err := agentcontext.LoadSkillBody(work, catalog.Skills[0])
	if err != nil {
		t.Fatalf("LoadSkillBody: %v", err)
	}
	if loaded.Body != "# Body\n\nUse care.\n" {
		t.Fatalf("body = %q", loaded.Body)
	}
}

func TestLoadSkillBodyRejectsSymlinkEscape(t *testing.T) {
	work := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "SKILL.md"), "---\nname: escape\ndescription: Escape.\n---\n# Outside\n")
	if err := os.MkdirAll(filepath.Join(work, ".claw", "skills"), 0o755); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(work, ".claw", "skills", "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	catalog, err := agentcontext.LoadSkillCatalog(work)
	if err != nil {
		t.Fatalf("LoadSkillCatalog: %v", err)
	}
	if len(catalog.Skills) != 0 {
		t.Fatalf("escaped skill was cataloged: %#v", catalog.Skills)
	}
}
