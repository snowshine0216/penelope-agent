package compact_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
)

func TestLookupModelLimitKnownClaudeOpus(t *testing.T) {
	got, ok := compact.LookupModelLimit("claude-opus-4-7", nil)
	if !ok || got != 200_000 {
		t.Fatalf("claude-opus-4-7 = (%d, %v), want (200000, true)", got, ok)
	}
}

func TestLookupModelLimitOneMillionVariant(t *testing.T) {
	got, ok := compact.LookupModelLimit("claude-opus-4-7[1m]", nil)
	if !ok || got != 1_000_000 {
		t.Fatalf("claude-opus-4-7[1m] = (%d, %v), want (1000000, true)", got, ok)
	}
}

func TestLookupModelLimitUnknownModelReturnsFalse(t *testing.T) {
	_, ok := compact.LookupModelLimit("nonexistent-model-x", nil)
	if ok {
		t.Fatalf("unknown model should not be found")
	}
}

func TestLookupModelLimitOverrideWins(t *testing.T) {
	overrides := map[string]int{"claude-opus-4-7": 999}
	got, ok := compact.LookupModelLimit("claude-opus-4-7", overrides)
	if !ok || got != 999 {
		t.Fatalf("override should win, got (%d, %v)", got, ok)
	}
}

func TestLookupModelLimitOverrideAddsNewModel(t *testing.T) {
	overrides := map[string]int{"my-custom-model": 50_000}
	got, ok := compact.LookupModelLimit("my-custom-model", overrides)
	if !ok || got != 50_000 {
		t.Fatalf("override-only model not found: (%d, %v)", got, ok)
	}
}

func TestLoadOverridesYAMLMissingFileReturnsEmpty(t *testing.T) {
	overrides, err := compact.LoadOverridesYAML(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if len(overrides) != 0 {
		t.Fatalf("missing file overrides = %v, want empty", overrides)
	}
}

func TestLoadOverridesYAMLValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model-limits.yaml")
	content := "claude-opus-4-7: 999\nmy-model: 1234\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	overrides, err := compact.LoadOverridesYAML(path)
	if err != nil {
		t.Fatalf("LoadOverridesYAML: %v", err)
	}
	if overrides["claude-opus-4-7"] != 999 {
		t.Fatalf("override = %d, want 999", overrides["claude-opus-4-7"])
	}
	if overrides["my-model"] != 1234 {
		t.Fatalf("override my-model = %d, want 1234", overrides["my-model"])
	}
}

func TestLoadOverridesYAMLMalformedFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("this: is: not: yaml::\n"), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	if _, err := compact.LoadOverridesYAML(path); err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

func TestFallbackContextLimitConstant(t *testing.T) {
	if compact.FallbackContextLimit != 32_000 {
		t.Fatalf("FallbackContextLimit = %d, want 32000", compact.FallbackContextLimit)
	}
}
