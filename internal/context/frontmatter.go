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
