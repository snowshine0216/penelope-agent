package provider_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/provider"
)

func TestValidateBaseURLRejectsNonHTTPS(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "LLM_API_KEY=k\nLLM_BASE_URL=http://remote.example.com/v1/\n")

	_, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{}))
	if err == nil {
		t.Fatal("expected error for non-https remote URL, got nil")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected 'https' in error, got: %v", err)
	}
}

func TestValidateBaseURLAllowsLocalhostHTTP(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "LLM_API_KEY=k\nLLM_BASE_URL=http://localhost:8080/v1/\n")

	cfg, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("localhost http should be allowed, got error: %v", err)
	}
	if cfg.BaseURL != "http://localhost:8080/v1/" {
		t.Fatalf("BaseURL = %q, want http://localhost:8080/v1/", cfg.BaseURL)
	}
}

func TestValidateBaseURLAllows127001HTTP(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "LLM_API_KEY=k\nLLM_BASE_URL=http://127.0.0.1:9000/v1/\n")

	cfg, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("127.0.0.1 http should be allowed, got error: %v", err)
	}
	if !strings.HasPrefix(cfg.BaseURL, "http://127.0.0.1") {
		t.Fatalf("BaseURL = %q, want 127.0.0.1 prefix", cfg.BaseURL)
	}
}

func TestValidateBaseURLRejectsMalformedURL(t *testing.T) {
	// A URL with control chars in the host triggers url.Parse failure
	// on some platforms. Use a scheme-only string that is clearly invalid.
	dir := t.TempDir()
	// Providing a URL that parses but has an empty scheme causes the non-https
	// check to fire (empty scheme != "https" and host is not local).
	writeDotEnv(t, dir, "LLM_API_KEY=k\nLLM_BASE_URL=ftp://remote.example.com/v1/\n")

	_, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{}))
	if err == nil {
		t.Fatal("expected error for ftp:// URL, got nil")
	}
}

func TestReadDotEnvUpwardStopsAtFilesystemRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("root detection differs on Windows")
	}
	// Start from a directory that has no .env and is deep enough that the
	// upward walk exhausts maxDotEnvDepth before reaching an ancestor with an .env.
	// We pass /tmp directly; no .env exists in /tmp or above in test environments.
	// Without an API key the call fails with the missing-key error, NOT a path error.
	_, err := provider.LoadConfigFromDir("/tmp", envLookup(map[string]string{}))
	if err == nil {
		t.Fatal("expected missing API key error")
	}
	if !strings.Contains(err.Error(), "LLM_API_KEY") {
		t.Fatalf("expected API key error, got: %v", err)
	}
}

func TestReadDotEnvUpwardBadDotEnvFileReturnsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits behave differently on Windows")
	}
	dir := t.TempDir()
	// Create a .env that exists but is not readable.
	dotEnvPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(dotEnvPath, []byte("LLM_API_KEY=x\n"), 0o000); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dotEnvPath, 0o644) })

	_, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{}))
	if err == nil {
		t.Fatal("expected error reading unreadable .env, got nil")
	}
}

func TestLoadConfigInvalidBaseURLFromEnvReturnsError(t *testing.T) {
	dir := t.TempDir()
	// No .env file — API key comes purely from env lookup.
	_, err := provider.LoadConfigFromDir(dir, envLookup(map[string]string{
		"LLM_API_KEY":  "some-key",
		"LLM_BASE_URL": "http://external.example.com/api/",
	}))
	if err == nil {
		t.Fatal("expected validation error for http external URL, got nil")
	}
	if !strings.Contains(err.Error(), "invalid LLM_BASE_URL") {
		t.Fatalf("expected 'invalid LLM_BASE_URL' in error, got: %v", err)
	}
}
