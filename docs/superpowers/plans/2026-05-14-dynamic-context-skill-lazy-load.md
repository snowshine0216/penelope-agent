# Dynamic Context And Skill Lazy Loading Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build dynamic system prompt composition with root `AGENTS.md`, local `.claw/skills` frontmatter cataloging, and a serial `load_skill` tool that promotes selected skill bodies into later system prompts.

**Architecture:** Add `internal/context` as package `agentcontext` for prompt composition, local instruction loading, and skill state. Wire it into the existing engine without changing provider adapters by keeping exactly one system message at index 0. Register a local-only `load_skill` tool when `.claw/skills` contains valid catalog entries, and treat that tool as a turn barrier inside the engine.

**Tech Stack:** Go 1.26.2, existing `schema.Message` and `tools.Tool` interfaces, existing `gopkg.in/yaml.v3`, `go test ./...`.

---

## File Structure

- Create `internal/context/frontmatter.go`: pure YAML-frontmatter parsing for `SKILL.md`.
- Create `internal/context/loaders.go`: filesystem-edge loading for root `AGENTS.md`, direct-child skill catalog discovery, and skill body loading.
- Create `internal/context/composer.go`: pure prompt section composition and idempotent loaded-skill insertion.
- Create `internal/context/manager.go`: explicit mutable boundary that owns current context state for one run.
- Create `internal/context/load_skill_tool.go`: tool adapter for `load_skill`.
- Create `tests/context/frontmatter_test.go`: parser unit tests.
- Create `tests/context/loaders_test.go`: filesystem loading tests.
- Create `tests/context/composer_test.go`: prompt composition tests.
- Create `tests/context/load_skill_tool_test.go`: tool behavior and policy tests.
- Modify `internal/engine/loop.go`: use context manager for system prompt and handle loader barrier.
- Modify `cmd/claw/main.go`: create the context manager and register `load_skill` when available.
- Modify `tests/engine/loop_test.go`: initial dynamic context and post-load skill tests.
- Modify `tests/engine/parallel_tool_execution_test.go`: loader barrier execution test.
- Modify `README.md`: document `AGENTS.md`, `.claw/skills`, and `load_skill`.

Use `agentcontext` as the package name even though the directory is
`internal/context`; this avoids collisions with Go's standard `context`
package in files that need both.

---

### Task 1: Parse Skill Frontmatter

**Files:**
- Create: `internal/context/frontmatter.go`
- Test: `tests/context/frontmatter_test.go`

- [ ] **Step 1: Write failing tests for valid and invalid skill frontmatter**

Create `tests/context/frontmatter_test.go`:

```go
package context_test

import (
	"testing"

	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
)

func TestParseSkillDocumentReturnsMetaAndBody(t *testing.T) {
	doc := "---\nname: investigate\ndescription: Find root causes.\naliases:\n  - debug\n---\n# Investigate\n\nRun a root-cause workflow.\n"

	got, err := agentcontext.ParseSkillDocument(doc)
	if err != nil {
		t.Fatalf("ParseSkillDocument: %v", err)
	}
	if got.Meta.Name != "investigate" {
		t.Fatalf("name = %q, want investigate", got.Meta.Name)
	}
	if got.Meta.Description != "Find root causes." {
		t.Fatalf("description = %q, want Find root causes.", got.Meta.Description)
	}
	if len(got.Meta.Aliases) != 1 || got.Meta.Aliases[0] != "debug" {
		t.Fatalf("aliases = %#v, want [debug]", got.Meta.Aliases)
	}
	if got.Body != "# Investigate\n\nRun a root-cause workflow.\n" {
		t.Fatalf("body = %q", got.Body)
	}
}

func TestParseSkillDocumentRejectsMissingFrontmatter(t *testing.T) {
	_, err := agentcontext.ParseSkillDocument("# No frontmatter\n")
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParseSkillDocumentRejectsMissingRequiredFields(t *testing.T) {
	cases := map[string]string{
		"missing name":        "---\ndescription: Find root causes.\n---\nBody\n",
		"missing description": "---\nname: investigate\n---\nBody\n",
		"blank name":          "---\nname: \"\"\ndescription: Find root causes.\n---\nBody\n",
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := agentcontext.ParseSkillDocument(input)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
```

- [ ] **Step 2: Run parser tests and verify they fail**

Run:

```bash
go test ./tests/context -run TestParseSkillDocument -count=1
```

Expected: FAIL because `internal/context` and `ParseSkillDocument` do not exist.

- [ ] **Step 3: Implement the frontmatter parser**

Create `internal/context/frontmatter.go`:

```go
package agentcontext

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type SkillMeta struct {
	Name        string
	Description string
	Aliases     []string
	RelPath     string
}

type SkillDocument struct {
	Meta SkillMeta
	Body string
}

type skillFrontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Aliases     []string `yaml:"aliases"`
}

func ParseSkillDocument(content string) (SkillDocument, error) {
	frontmatter, body, err := splitFrontmatter(content)
	if err != nil {
		return SkillDocument{}, err
	}

	var decoded skillFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), &decoded); err != nil {
		return SkillDocument{}, fmt.Errorf("parse skill frontmatter: %w", err)
	}

	meta := SkillMeta{
		Name:        strings.TrimSpace(decoded.Name),
		Description: strings.TrimSpace(decoded.Description),
		Aliases:     trimNonEmpty(decoded.Aliases),
	}
	if meta.Name == "" {
		return SkillDocument{}, fmt.Errorf("skill frontmatter missing name")
	}
	if meta.Description == "" {
		return SkillDocument{}, fmt.Errorf("skill frontmatter missing description")
	}

	return SkillDocument{Meta: meta, Body: body}, nil
}

func splitFrontmatter(content string) (string, string, error) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", "", fmt.Errorf("skill document missing opening frontmatter delimiter")
	}

	rest := strings.TrimPrefix(normalized, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", fmt.Errorf("skill document missing closing frontmatter delimiter")
	}

	frontmatter := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")
	return frontmatter, body, nil
}

func trimNonEmpty(values []string) []string {
	out := []string{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
```

- [ ] **Step 4: Run parser tests and verify they pass**

Run:

```bash
go test ./tests/context -run TestParseSkillDocument -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit parser work**

Run:

```bash
git add internal/context/frontmatter.go tests/context/frontmatter_test.go
git commit -m "feat(context): parse local skill frontmatter"
```

---

### Task 2: Load AGENTS And Local Skill Files

**Files:**
- Create: `internal/context/loaders.go`
- Test: `tests/context/loaders_test.go`

- [ ] **Step 1: Write failing filesystem-edge tests**

Create `tests/context/loaders_test.go`:

```go
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
```

- [ ] **Step 2: Run loader tests and verify they fail**

Run:

```bash
go test ./tests/context -run 'TestLoad' -count=1
```

Expected: FAIL because loader functions and types are missing.

- [ ] **Step 3: Implement root AGENTS and skill loaders**

Create `internal/context/loaders.go`:

```go
package agentcontext

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type SkillCatalog struct {
	Skills  []SkillMeta
	Skipped []SkillSkip
}

type SkillSkip struct {
	RelPath string
	Reason  string
}

type LoadedSkill struct {
	Meta SkillMeta
	Body string
}

func LoadRootAgents(workDir string) (string, error) {
	path := filepath.Join(workDir, "AGENTS.md")
	info, err := os.Stat(path)
	switch {
	case err == nil && !info.IsDir():
		bytes, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("read AGENTS.md: %w", readErr)
		}
		return string(bytes), nil
	case err == nil && info.IsDir():
		return "", nil
	case os.IsNotExist(err):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("stat AGENTS.md: %w", err)
	default:
		return "", nil
	}
}

func LoadSkillCatalog(workDir string) (SkillCatalog, error) {
	root := filepath.Join(workDir, ".claw", "skills")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return SkillCatalog{}, nil
	}
	if err != nil {
		return SkillCatalog{}, fmt.Errorf("read skill root: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	seen := map[string]struct{}{}
	catalog := SkillCatalog{}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		relPath := filepath.ToSlash(filepath.Join(".claw", "skills", entry.Name(), "SKILL.md"))
		fullPath := filepath.Join(workDir, filepath.FromSlash(relPath))
		if !isWithinWorkDir(workDir, fullPath) {
			catalog.Skipped = append(catalog.Skipped, SkillSkip{RelPath: relPath, Reason: "path escapes workdir"})
			continue
		}

		content, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			catalog.Skipped = append(catalog.Skipped, SkillSkip{RelPath: relPath, Reason: readErr.Error()})
			continue
		}

		doc, parseErr := ParseSkillDocument(string(content))
		if parseErr != nil {
			catalog.Skipped = append(catalog.Skipped, SkillSkip{RelPath: relPath, Reason: parseErr.Error()})
			continue
		}
		if _, exists := seen[doc.Meta.Name]; exists {
			catalog.Skipped = append(catalog.Skipped, SkillSkip{RelPath: relPath, Reason: fmt.Sprintf("duplicate skill name %q", doc.Meta.Name)})
			continue
		}
		seen[doc.Meta.Name] = struct{}{}
		doc.Meta.RelPath = relPath
		catalog.Skills = append(catalog.Skills, doc.Meta)
	}

	return catalog, nil
}

func LoadSkillBody(workDir string, meta SkillMeta) (LoadedSkill, error) {
	if strings.TrimSpace(meta.RelPath) == "" {
		return LoadedSkill{}, fmt.Errorf("skill %q missing relative path", meta.Name)
	}

	fullPath := filepath.Join(workDir, filepath.FromSlash(meta.RelPath))
	if !isWithinWorkDir(workDir, fullPath) {
		return LoadedSkill{}, fmt.Errorf("skill %q path escapes workdir", meta.Name)
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return LoadedSkill{}, fmt.Errorf("read skill %q: %w", meta.Name, err)
	}

	doc, err := ParseSkillDocument(string(content))
	if err != nil {
		return LoadedSkill{}, fmt.Errorf("parse skill %q: %w", meta.Name, err)
	}
	if doc.Meta.Name != meta.Name {
		return LoadedSkill{}, fmt.Errorf("skill %q changed name to %q", meta.Name, doc.Meta.Name)
	}
	doc.Meta.RelPath = meta.RelPath

	return LoadedSkill{Meta: doc.Meta, Body: doc.Body}, nil
}

func isWithinWorkDir(workDir string, path string) bool {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	realWorkDir := absWorkDir
	if evaluated, evalErr := filepath.EvalSymlinks(absWorkDir); evalErr == nil {
		realWorkDir = evaluated
	}
	realPath := absPath
	if evaluated, evalErr := filepath.EvalSymlinks(absPath); evalErr == nil {
		realPath = evaluated
	}

	sep := string(os.PathSeparator)
	return realPath == realWorkDir || strings.HasPrefix(realPath, realWorkDir+sep)
}
```

- [ ] **Step 4: Run loader tests and verify they pass**

Run:

```bash
go test ./tests/context -run 'TestLoad' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit loader work**

Run:

```bash
git add internal/context/loaders.go tests/context/loaders_test.go
git commit -m "feat(context): load workdir instructions and skill catalog"
```

---

### Task 3: Compose The Dynamic System Prompt

**Files:**
- Create: `internal/context/composer.go`
- Test: `tests/context/composer_test.go`

- [ ] **Step 1: Write failing prompt composition tests**

Create `tests/context/composer_test.go`:

```go
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
```

- [ ] **Step 2: Run composer tests and verify they fail**

Run:

```bash
go test ./tests/context -run TestComposer -count=1
```

Expected: FAIL because `Composer` does not exist.

- [ ] **Step 3: Implement the composer**

Create `internal/context/composer.go`:

```go
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
	base   string
	agents string
	catalog SkillCatalog
	loaded []LoadedSkill
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
```

- [ ] **Step 4: Run composer tests and verify they pass**

Run:

```bash
go test ./tests/context -run TestComposer -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit composer work**

Run:

```bash
git add internal/context/composer.go tests/context/composer_test.go
git commit -m "feat(context): compose dynamic system prompt"
```

---

### Task 4: Add Context Manager And `load_skill` Tool

**Files:**
- Create: `internal/context/manager.go`
- Create: `internal/context/load_skill_tool.go`
- Test: `tests/context/load_skill_tool_test.go`

- [ ] **Step 1: Write failing tests for manager and tool behavior**

Create `tests/context/load_skill_tool_test.go`:

```go
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
```

- [ ] **Step 2: Run manager/tool tests and verify they fail**

Run:

```bash
go test ./tests/context -run 'TestManager|TestLoadSkillTool' -count=1
```

Expected: FAIL because manager and tool are missing.

- [ ] **Step 3: Implement the context manager**

Create `internal/context/manager.go`:

```go
package agentcontext

import (
	"fmt"
	"sort"
	"strings"
)

type Manager struct {
	workDir  string
	catalog  SkillCatalog
	composer Composer
	byName   map[string]SkillMeta
}

func NewManager(workDir string) (*Manager, error) {
	agents, err := LoadRootAgents(workDir)
	if err != nil {
		return nil, err
	}
	catalog, err := LoadSkillCatalog(workDir)
	if err != nil {
		return nil, err
	}
	byName := map[string]SkillMeta{}
	for _, skill := range catalog.Skills {
		byName[skill.Name] = skill
	}

	composer := NewComposer(ComposerInput{
		BaseInstructions: DefaultBaseInstructions,
		Agents:           agents,
		Catalog:          catalog,
	})

	return &Manager{workDir: workDir, catalog: catalog, composer: composer, byName: byName}, nil
}

func (m *Manager) SystemPrompt() string {
	if m == nil {
		return DefaultBaseInstructions
	}
	return m.composer.SystemPrompt()
}

func (m *Manager) HasSkills() bool {
	return m != nil && len(m.catalog.Skills) > 0
}

func (m *Manager) AvailableSkillNames() []string {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.catalog.Skills))
	for _, skill := range m.catalog.Skills {
		names = append(names, skill.Name)
	}
	sort.Strings(names)
	return names
}

func (m *Manager) LoadSkill(name string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("skill manager is not configured")
	}
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return "", fmt.Errorf("skill name is required")
	}
	meta, ok := m.byName[trimmedName]
	if !ok {
		return "", fmt.Errorf("unknown local skill %q (available: %s)", trimmedName, strings.Join(m.AvailableSkillNames(), ", "))
	}
	if m.composer.HasLoadedSkill(trimmedName) {
		return fmt.Sprintf("skill %q already loaded", trimmedName), nil
	}

	loaded, err := LoadSkillBody(m.workDir, meta)
	if err != nil {
		return "", err
	}
	m.composer = m.composer.WithLoadedSkill(loaded)
	return fmt.Sprintf("loaded skill %q", trimmedName), nil
}
```

- [ ] **Step 4: Implement the `load_skill` tool adapter**

Create `internal/context/load_skill_tool.go`:

```go
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
```

- [ ] **Step 5: Run manager/tool tests and verify they pass**

Run:

```bash
go test ./tests/context -run 'TestManager|TestLoadSkillTool' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit manager/tool work**

Run:

```bash
git add internal/context/manager.go internal/context/load_skill_tool.go tests/context/load_skill_tool_test.go
git commit -m "feat(context): add local skill manager and loader tool"
```

---

### Task 5: Wire Initial Dynamic Context Into Engine And CLI

**Files:**
- Modify: `internal/engine/loop.go`
- Modify: `cmd/claw/main.go`
- Modify: `tests/engine/loop_test.go`

- [ ] **Step 1: Write failing engine test for initial dynamic context**

Append to `tests/engine/loop_test.go`:

```go
func TestEngineSeedsContextFromContextManager(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "AGENTS.md"), []byte("Project-specific rules."), 0o644); err != nil {
		t.Fatalf("write AGENTS: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(work, ".claw", "skills", "investigate"), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, ".claw", "skills", "investigate", "SKILL.md"), []byte("---\nname: investigate\ndescription: Debug deeply.\n---\n# Investigate Body\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	manager, err := agentcontext.NewManager(work)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	registry := tools.NewRegistry()
	provider := &fakeProvider{responses: []schema.Message{{Role: schema.RoleAssistant, Content: "ok"}}}
	eng := engine.NewAgentEngine(provider, registry, work, false)
	eng.SetContextManager(manager)

	if err := eng.Run(context.Background(), "hello", noOpReporter{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	system := provider.receivedMsgs[0][0]
	if system.Role != schema.RoleSystem {
		t.Fatalf("first role = %q, want system", system.Role)
	}
	if !strings.Contains(system.Content, "Project-specific rules.") {
		t.Fatalf("system prompt missing AGENTS:\n%s", system.Content)
	}
	if !strings.Contains(system.Content, "name: investigate") {
		t.Fatalf("system prompt missing skill catalog:\n%s", system.Content)
	}
	if strings.Contains(system.Content, "# Investigate Body") {
		t.Fatalf("system prompt loaded body too early:\n%s", system.Content)
	}
}
```

Also add these imports to `tests/engine/loop_test.go`:

```go
	"os"
	"path/filepath"
	"strings"

	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
```

- [ ] **Step 2: Run the new engine context test and verify it fails**

Run:

```bash
go test ./tests/engine -run TestEngineSeedsContextFromContextManager -count=1
```

Expected: FAIL because `SetContextManager` does not exist.

- [ ] **Step 3: Add context manager support to the engine**

In `internal/engine/loop.go`, add this import:

```go
	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
```

Add a field to `AgentEngine`:

```go
	contextManager *agentcontext.Manager
```

Add this method near `NewAgentEngine`:

```go
func (e *AgentEngine) SetContextManager(manager *agentcontext.Manager) {
	e.contextManager = manager
}
```

Replace the hardcoded system message content in `Run` with:

```go
			Content: e.systemPrompt(),
```

Add this helper near `defaultMaxTurns`:

```go
func (e *AgentEngine) systemPrompt() string {
	if e.contextManager == nil {
		return agentcontext.DefaultBaseInstructions
	}
	return e.contextManager.SystemPrompt()
}
```

- [ ] **Step 4: Run the engine context test and verify it passes**

Run:

```bash
go test ./tests/engine -run TestEngineSeedsContextFromContextManager -count=1
```

Expected: PASS.

- [ ] **Step 5: Wire the context manager and loader tool in CLI**

In `cmd/claw/main.go`, add this import:

```go
	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
```

After the tool registry is created and standard tools are registered, add:

```go
	contextManager, err := agentcontext.NewManager(cwd)
	if err != nil {
		log.Fatalf("init context: %v", err)
	}
	if contextManager.HasSkills() {
		registry.Register(agentcontext.NewLoadSkillTool(contextManager))
	}
```

After constructing `eng`, add:

```go
	eng.SetContextManager(contextManager)
```

- [ ] **Step 6: Run affected tests**

Run:

```bash
go test ./tests/engine ./tests/context -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit initial integration**

Run:

```bash
git add internal/engine/loop.go cmd/claw/main.go tests/engine/loop_test.go
git commit -m "feat(engine): seed dynamic workdir context"
```

---

### Task 6: Promote Loaded Skills And Enforce Loader Barrier

**Files:**
- Modify: `internal/engine/loop.go`
- Modify: `tests/engine/loop_test.go`
- Modify: `tests/engine/parallel_tool_execution_test.go`

- [ ] **Step 1: Write failing test for skill body promotion after `load_skill`**

Append to `tests/engine/loop_test.go`:

```go
func TestEnginePromotesLoadedSkillIntoNextSystemPrompt(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, ".claw", "skills", "investigate"), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, ".claw", "skills", "investigate", "SKILL.md"), []byte("---\nname: investigate\ndescription: Debug deeply.\n---\n# Investigate Body\n\nUse root-cause analysis.\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	manager, err := agentcontext.NewManager(work)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	registry := tools.NewRegistry()
	registry.Register(agentcontext.NewLoadSkillTool(manager))
	provider := &fakeProvider{responses: []schema.Message{
		{Role: schema.RoleAssistant, ToolCalls: []schema.ToolCall{{ID: "load-1", Name: agentcontext.LoadSkillToolName, Arguments: json.RawMessage(`{"name":"investigate"}`)}}},
		{Role: schema.RoleAssistant, Content: "done"},
	}}
	eng := engine.NewAgentEngine(provider, registry, work, false)
	eng.SetContextManager(manager)

	if err := eng.Run(context.Background(), "debug this", noOpReporter{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(provider.receivedMsgs[0][0].Content, "# Investigate Body") {
		t.Fatalf("first call loaded body too early:\n%s", provider.receivedMsgs[0][0].Content)
	}
	if !strings.Contains(provider.receivedMsgs[1][0].Content, "# Investigate Body") {
		t.Fatalf("second call missing loaded body:\n%s", provider.receivedMsgs[1][0].Content)
	}
}
```

- [ ] **Step 2: Write failing test for mixed loader barrier**

Append to `tests/engine/parallel_tool_execution_test.go`:

```go
func TestEngineDefersNormalToolsWhenLoadSkillIsRequested(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, ".claw", "skills", "investigate"), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, ".claw", "skills", "investigate", "SKILL.md"), []byte("---\nname: investigate\ndescription: Debug deeply.\n---\n# Investigate Body\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	manager, err := agentcontext.NewManager(work)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	normal := &recordingTool{name: "normal", output: "normal-output"}
	registry := tools.NewRegistry()
	registry.Register(agentcontext.NewLoadSkillTool(manager))
	registry.Register(normal)

	provider := &fakeProvider{responses: []schema.Message{
		{Role: schema.RoleAssistant, ToolCalls: []schema.ToolCall{
			{ID: "load", Name: agentcontext.LoadSkillToolName, Arguments: json.RawMessage(`{"name":"investigate"}`)},
			{ID: "normal", Name: "normal", Arguments: json.RawMessage(`{}`)},
		}},
		{Role: schema.RoleAssistant, Content: "done"},
	}}
	eng := engine.NewAgentEngine(provider, registry, work, false)
	eng.SetContextManager(manager)

	if err := eng.Run(context.Background(), "go", noOpReporter{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if normal.callCount != 0 {
		t.Fatalf("normal tool executed before loaded skill context, callCount=%d", normal.callCount)
	}
	second := provider.receivedMsgs[1]
	var normalObservation string
	for _, msg := range second {
		if msg.Role == schema.RoleTool && msg.ToolCallID == "normal" {
			normalObservation = msg.Content
		}
	}
	if !strings.Contains(normalObservation, "deferred until after skill loading") {
		t.Fatalf("normal deferral observation = %q", normalObservation)
	}
}
```

Add these imports to `tests/engine/parallel_tool_execution_test.go`:

```go
	"os"
	"path/filepath"
	"strings"

	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
```

- [ ] **Step 3: Run the new engine tests and verify they fail**

Run:

```bash
go test ./tests/engine -run 'TestEnginePromotesLoadedSkillIntoNextSystemPrompt|TestEngineDefersNormalToolsWhenLoadSkillIsRequested' -count=1
```

Expected: FAIL because the system prompt is not refreshed and mixed calls are not deferred.

- [ ] **Step 4: Implement loader barrier helpers**

In `internal/engine/loop.go`, add this helper:

```go
func hasLoadSkillCall(calls []schema.ToolCall) bool {
	for _, call := range calls {
		if call.Name == agentcontext.LoadSkillToolName {
			return true
		}
	}
	return false
}
```

Add this helper:

```go
func deferToolResult(call schema.ToolCall) schema.ToolResult {
	return schema.ToolResult{
		ToolCallID: call.ID,
		Output:     fmt.Sprintf("tool %q deferred until after skill loading; request it again if still needed", call.Name),
		IsError:    false,
	}
}
```

Add this method:

```go
func (e *AgentEngine) executeLoadSkillBarrier(ctx context.Context, calls []schema.ToolCall, report Reporter) ([]schema.ToolResult, error) {
	results := make([]schema.ToolResult, len(calls))
	for i, call := range calls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if call.Name != agentcontext.LoadSkillToolName {
			results[i] = deferToolResult(call)
			continue
		}
		report.OnToolCall(ctx, call.Name, string(call.Arguments))
		result := executeToolCall(ctx, e.registry, call)
		report.OnToolResult(ctx, call.Name, result.Output, result.IsError)
		results[i] = result
	}
	return results, nil
}
```

Add this method:

```go
func (e *AgentEngine) refreshSystemPrompt(history []schema.Message) []schema.Message {
	if len(history) == 0 || history[0].Role != schema.RoleSystem {
		return history
	}
	out := append([]schema.Message(nil), history...)
	out[0].Content = e.systemPrompt()
	return out
}
```

- [ ] **Step 5: Use the loader barrier in `Run`**

In `internal/engine/loop.go`, replace the tool-group execution block:

```go
		groups := PlanToolCallGroups(actionResp.ToolCalls, e.registry.ExecutionPolicyFor)
		for _, group := range groups {
			if err := ctx.Err(); err != nil {
				return err
			}

			for _, call := range group {
				report.OnToolCall(ctx, call.Name, string(call.Arguments))
			}

			results, err := executeToolCallGroup(ctx, e.registry, group, e.toolGroupLimit(group))
			if err != nil {
				return err
			}

			for i, result := range results {
				report.OnToolResult(ctx, group[i].Name, result.Output, result.IsError)
			}

			contextHistory = appendToolResultMessages(contextHistory, results)
		}
```

with:

```go
		if hasLoadSkillCall(actionResp.ToolCalls) {
			results, err := e.executeLoadSkillBarrier(ctx, actionResp.ToolCalls, report)
			if err != nil {
				return err
			}
			contextHistory = appendToolResultMessages(contextHistory, results)
			contextHistory = e.refreshSystemPrompt(contextHistory)
			continue
		}

		groups := PlanToolCallGroups(actionResp.ToolCalls, e.registry.ExecutionPolicyFor)
		for _, group := range groups {
			if err := ctx.Err(); err != nil {
				return err
			}

			for _, call := range group {
				report.OnToolCall(ctx, call.Name, string(call.Arguments))
			}

			results, err := executeToolCallGroup(ctx, e.registry, group, e.toolGroupLimit(group))
			if err != nil {
				return err
			}

			for i, result := range results {
				report.OnToolResult(ctx, group[i].Name, result.Output, result.IsError)
			}

			contextHistory = appendToolResultMessages(contextHistory, results)
		}
```

- [ ] **Step 6: Run promotion and barrier tests**

Run:

```bash
go test ./tests/engine -run 'TestEnginePromotesLoadedSkillIntoNextSystemPrompt|TestEngineDefersNormalToolsWhenLoadSkillIsRequested' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit loader barrier integration**

Run:

```bash
git add internal/engine/loop.go tests/engine/loop_test.go tests/engine/parallel_tool_execution_test.go
git commit -m "feat(engine): promote loaded local skills"
```

---

### Task 7: Document And Verify The Complete Feature

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update README documentation**

In `README.md`, after the "What it does" list, add:

```markdown
## Dynamic context

At startup, `claw` composes the system prompt from:

1. The built-in `penelope-agent` operating instructions.
2. `${workdir}/AGENTS.md`, when present.
3. Frontmatter from local skills under `${workdir}/.claw/skills/*/SKILL.md`.
4. Full local skill bodies loaded later through the internal `load_skill` tool.

Only root `${workdir}/AGENTS.md` is loaded. Parent, nested, and global
instruction files are ignored in this version.
```

In the "Tools" table, add this row:

```markdown
| `load_skill` | Load full instructions for a relevant local skill listed in `.claw/skills` | Local skills only. Serial-only; acts as a turn barrier before normal tool work continues. |
```

After the table, add:

````markdown
## Local skills

Local skills live under the workdir:

```text
.claw/skills/my-skill/
  SKILL.md
  scripts/
  references/
  assets/
```

`SKILL.md` must start with YAML frontmatter:

```yaml
---
name: my-skill
description: One sentence explaining when to use it.
---
```

The initial prompt includes only `name`, `description`, and optional aliases.
When the model decides a skill is relevant, it calls `load_skill` with the exact
skill name. The engine then inserts that skill's markdown body into the system
prompt for subsequent model calls.
````

- [ ] **Step 2: Run full tests**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run a CLI smoke test without network**

Run:

```bash
LLM_API_KEY=fake go run ./cmd/claw --prompt "noop" 2>&1 | head -5
```

Expected: command reaches provider initialization and then fails on the fake API request, not on context setup, skill scanning, or tool registration.

- [ ] **Step 4: Inspect git status**

Run:

```bash
git status --short
```

Expected: only `README.md` is modified after Task 6 commits.

- [ ] **Step 5: Commit documentation and final verification**

Run:

```bash
git add README.md
git commit -m "docs: document dynamic context loading"
```

---

## Final Verification

After all tasks are complete, run:

```bash
go test ./... -count=1
git status --short
```

Expected:

- `go test ./... -count=1` passes.
- `git status --short` is clean.
- The commit history contains the parser, loader, composer, manager/tool, engine integration, loader barrier, and documentation commits.
