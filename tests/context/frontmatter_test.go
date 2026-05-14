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
