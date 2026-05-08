package tools_test

import (
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// Tests for Name() and Definition() on each concrete tool.
// These methods are trivial one-liners but they count as statements in coverage.

func TestBashToolNameAndDefinition(t *testing.T) {
	tool := tools.NewBashTool(t.TempDir())

	if tool.Name() != "bash" {
		t.Fatalf("Name() = %q, want bash", tool.Name())
	}

	def := tool.Definition()
	if def.Name != "bash" {
		t.Fatalf("Definition().Name = %q, want bash", def.Name)
	}
	if def.Description == "" {
		t.Fatal("Definition().Description must not be empty")
	}
	if def.InputSchema == nil {
		t.Fatal("Definition().InputSchema must not be nil")
	}
}

func TestReadFileToolNameAndDefinition(t *testing.T) {
	tool := tools.NewReadFileTool(t.TempDir())

	if tool.Name() != "read_file" {
		t.Fatalf("Name() = %q, want read_file", tool.Name())
	}

	def := tool.Definition()
	if def.Name != "read_file" {
		t.Fatalf("Definition().Name = %q, want read_file", def.Name)
	}
	if def.Description == "" {
		t.Fatal("Definition().Description must not be empty")
	}
	if def.InputSchema == nil {
		t.Fatal("Definition().InputSchema must not be nil")
	}
}

func TestWriteFileToolNameAndDefinition(t *testing.T) {
	tool := tools.NewWriteFileTool(t.TempDir())

	if tool.Name() != "write_file" {
		t.Fatalf("Name() = %q, want write_file", tool.Name())
	}

	def := tool.Definition()
	if def.Name != "write_file" {
		t.Fatalf("Definition().Name = %q, want write_file", def.Name)
	}
	if def.Description == "" {
		t.Fatal("Definition().Description must not be empty")
	}
	if def.InputSchema == nil {
		t.Fatal("Definition().InputSchema must not be nil")
	}
}

func TestEditFileToolNameAndDefinition(t *testing.T) {
	tool := tools.NewEditFileTool(t.TempDir())

	if tool.Name() != "edit_file" {
		t.Fatalf("Name() = %q, want edit_file", tool.Name())
	}

	def := tool.Definition()
	if def.Name != "edit_file" {
		t.Fatalf("Definition().Name = %q, want edit_file", def.Name)
	}
	if def.Description == "" {
		t.Fatal("Definition().Description must not be empty")
	}
	if def.InputSchema == nil {
		t.Fatal("Definition().InputSchema must not be nil")
	}
}
