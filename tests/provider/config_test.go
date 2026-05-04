package provider_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/provider"
)

func envLookup(values map[string]string) provider.LookupEnvFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func writeDotEnv(t *testing.T, dir string, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
}

func TestLoadConfigFromDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "LLM_API_KEY=file-key\nLLM_BASE_URL=https://example.com/v1/\nLLM_MODEL=test-model\n")

	cfg, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadConfigFromDir returned error: %v", err)
	}

	if cfg.APIKey != "file-key" {
		t.Fatalf("APIKey = %q, want %q", cfg.APIKey, "file-key")
	}

	if cfg.BaseURL != "https://example.com/v1/" {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, "https://example.com/v1/")
	}

	if cfg.Model != "test-model" {
		t.Fatalf("Model = %q, want %q", cfg.Model, "test-model")
	}
}

func TestLoadConfigUsesDefaultBaseURL(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "LLM_API_KEY=file-key\n")

	cfg, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadConfigFromDir returned error: %v", err)
	}

	if cfg.BaseURL != provider.DefaultBaseURL() {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, provider.DefaultBaseURL())
	}
	if cfg.Model != provider.DefaultModel() {
		t.Fatalf("Model = %q, want %q", cfg.Model, provider.DefaultModel())
	}
}

func TestLoadConfigPrefersEnvironmentOverDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "LLM_API_KEY=file-key\nLLM_BASE_URL=https://file.example/v1/\n")

	cfg, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{
		"LLM_API_KEY":  "env-key",
		"LLM_BASE_URL": "https://env.example/v1/",
	}))
	if err != nil {
		t.Fatalf("LoadConfigFromDir returned error: %v", err)
	}

	if cfg.APIKey != "env-key" {
		t.Fatalf("APIKey = %q, want %q", cfg.APIKey, "env-key")
	}

	if cfg.BaseURL != "https://env.example/v1/" {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, "https://env.example/v1/")
	}
}

func TestLoadConfigSupportsLegacyZhipuAPIKey(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "ZHIPU_API_KEY=legacy-key\n")

	cfg, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadConfigFromDir returned error: %v", err)
	}

	if cfg.APIKey != "legacy-key" {
		t.Fatalf("APIKey = %q, want %q", cfg.APIKey, "legacy-key")
	}
}

func TestLoadConfigSupportsLegacyMiniMaxAPIKey(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "MINIMAX_API_KEY=mm-key\n")

	cfg, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadConfigFromDir returned error: %v", err)
	}

	if cfg.APIKey != "mm-key" {
		t.Fatalf("APIKey = %q, want %q", cfg.APIKey, "mm-key")
	}
}

func TestLoadConfigPrefersLLMOverLegacyKeys(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "LLM_API_KEY=primary\nMINIMAX_API_KEY=mm\nZHIPU_API_KEY=zp\n")

	cfg, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadConfigFromDir returned error: %v", err)
	}

	if cfg.APIKey != "primary" {
		t.Fatalf("APIKey = %q, want primary (LLM_API_KEY should win)", cfg.APIKey)
	}
}

func TestLoadConfigSkipsWhitespaceOnlyValues(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "LLM_API_KEY=  \nMINIMAX_API_KEY=real-key\n")

	cfg, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadConfigFromDir returned error: %v", err)
	}

	if cfg.APIKey != "real-key" {
		t.Fatalf("APIKey = %q, want real-key (whitespace LLM_API_KEY should be skipped)", cfg.APIKey)
	}
}

func TestLoadConfigErrorsWhenNoAPIKey(t *testing.T) {
	dir := t.TempDir()

	_, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{}))
	if err == nil {
		t.Fatal("LoadConfigFromDir returned nil error, expected an error about missing API key")
	}
}

func TestLoadConfigWalksUpwardForDotEnv(t *testing.T) {
	root := t.TempDir()
	writeDotEnv(t, root, "LLM_API_KEY=root-key\n")

	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	cfg, err := provider.LoadConfigFromDir(nested, envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("LoadConfigFromDir returned error: %v", err)
	}

	if cfg.APIKey != "root-key" {
		t.Fatalf("APIKey = %q, want root-key (should walk upward to find .env)", cfg.APIKey)
	}
}
