package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func envLookup(values map[string]string) func(string) (string, bool) {
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

func TestLoadProviderConfigFromDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "LLM_API_KEY=file-key\nLLM_BASE_URL=https://example.com/v1/\nLLM_MODEL=test-model\n")

	cfg, err := loadProviderConfigFromDir(dir, envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("loadProviderConfigFromDir returned error: %v", err)
	}

	if cfg.apiKey != "file-key" {
		t.Fatalf("apiKey = %q, want %q", cfg.apiKey, "file-key")
	}

	if cfg.baseURL != "https://example.com/v1/" {
		t.Fatalf("baseURL = %q, want %q", cfg.baseURL, "https://example.com/v1/")
	}

	if cfg.model != "test-model" {
		t.Fatalf("model = %q, want %q", cfg.model, "test-model")
	}
}

func TestLoadProviderConfigUsesMiniMaxDefaultBaseURL(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "LLM_API_KEY=file-key\n")

	cfg, err := loadProviderConfigFromDir(dir, envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("loadProviderConfigFromDir returned error: %v", err)
	}

	if cfg.baseURL != defaultProviderBaseURL {
		t.Fatalf("baseURL = %q, want %q", cfg.baseURL, defaultProviderBaseURL)
	}
}

func TestLoadProviderConfigPrefersEnvironmentOverDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "LLM_API_KEY=file-key\nLLM_BASE_URL=https://file.example/v1/\n")

	cfg, err := loadProviderConfigFromDir(dir, envLookup(map[string]string{
		"LLM_API_KEY":  "env-key",
		"LLM_BASE_URL": "https://env.example/v1/",
	}))
	if err != nil {
		t.Fatalf("loadProviderConfigFromDir returned error: %v", err)
	}

	if cfg.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want %q", cfg.apiKey, "env-key")
	}

	if cfg.baseURL != "https://env.example/v1/" {
		t.Fatalf("baseURL = %q, want %q", cfg.baseURL, "https://env.example/v1/")
	}
}

func TestLoadProviderConfigSupportsLegacyZhipuAPIKey(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "ZHIPU_API_KEY=legacy-key\n")

	cfg, err := loadProviderConfigFromDir(dir, envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("loadProviderConfigFromDir returned error: %v", err)
	}

	if cfg.apiKey != "legacy-key" {
		t.Fatalf("apiKey = %q, want %q", cfg.apiKey, "legacy-key")
	}
	if cfg.baseURL != defaultProviderBaseURL {
		t.Fatalf("baseURL = %q, want %q", cfg.baseURL, defaultProviderBaseURL)
	}
}
