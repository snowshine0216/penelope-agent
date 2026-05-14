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
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("stat AGENTS.md: %w", err)
	}
	// Reject symlinks to prevent arbitrary file injection into the system prompt.
	if info.Mode()&os.ModeSymlink != 0 {
		return "", nil
	}
	if info.IsDir() {
		return "", nil
	}
	bytes, readErr := os.ReadFile(path)
	if readErr != nil {
		return "", fmt.Errorf("read AGENTS.md: %w", readErr)
	}
	return string(bytes), nil
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
		skillDir := filepath.Join(root, entry.Name())
		relPath := filepath.ToSlash(filepath.Join(".claw", "skills", entry.Name(), "SKILL.md"))
		fullPath := filepath.Join(workDir, filepath.FromSlash(relPath))

		// Skip symlinks entirely; record escaping ones for diagnostics.
		if entry.Type()&os.ModeSymlink != 0 {
			if !isWithinWorkDir(workDir, skillDir) {
				catalog.Skipped = append(catalog.Skipped, SkillSkip{RelPath: relPath, Reason: "path escapes workdir"})
			} else {
				catalog.Skipped = append(catalog.Skipped, SkillSkip{RelPath: relPath, Reason: "symlinks are not followed"})
			}
			continue
		}

		if !entry.IsDir() {
			continue
		}

		// Guard against SKILL.md being a symlink to an external file.
		skillFileInfo, lstatErr := os.Lstat(fullPath)
		if lstatErr == nil && skillFileInfo.Mode()&os.ModeSymlink != 0 {
			catalog.Skipped = append(catalog.Skipped, SkillSkip{RelPath: relPath, Reason: "SKILL.md is a symlink"})
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

	// Reject symlinks to prevent TOCTOU injection: a file valid at catalog time
	// could be swapped for a symlink before LoadSkillBody is called.
	fileInfo, lstatErr := os.Lstat(fullPath)
	if lstatErr != nil {
		return LoadedSkill{}, fmt.Errorf("stat skill %q: %w", meta.Name, lstatErr)
	}
	if fileInfo.Mode()&os.ModeSymlink != 0 {
		return LoadedSkill{}, fmt.Errorf("skill %q SKILL.md is a symlink", meta.Name)
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
