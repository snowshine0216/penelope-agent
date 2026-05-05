package provider

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

const defaultProviderBaseURL = "https://open.bigmodel.cn/api/paas/v4/"
const defaultProviderModel = "glm-4.5-air"

// Config carries the resolved provider settings (api key, base URL, model).
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
}

// LookupEnvFunc matches os.LookupEnv. Injected so callers (and tests) can
// substitute a deterministic environment.
type LookupEnvFunc func(string) (string, bool)

func loadProviderConfig() (Config, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return Config{}, fmt.Errorf("get working directory: %w", err)
	}

	return LoadConfigFromDir(workingDir, os.LookupEnv)
}

// LoadConfigFromDir reads provider settings from environment variables and
// the nearest .env walking up from dir. Environment values win over .env
// values; LLM_* keys win over MINIMAX_* / ZHIPU_* legacy keys.
func LoadConfigFromDir(dir string, lookup LookupEnvFunc) (Config, error) {
	dotEnvValues, err := readDotEnvUpward(dir)
	if err != nil {
		return Config{}, err
	}

	apiKey, ok := firstConfiguredValue(lookup, dotEnvValues, "LLM_API_KEY", "MINIMAX_API_KEY", "ZHIPU_API_KEY")
	if !ok {
		return Config{}, fmt.Errorf("set LLM_API_KEY in environment or .env (compatible: MINIMAX_API_KEY, ZHIPU_API_KEY)")
	}

	baseURL, ok := firstConfiguredValue(lookup, dotEnvValues, "LLM_BASE_URL")
	if !ok {
		baseURL = defaultProviderBaseURL
	} else if err := validateBaseURL(baseURL); err != nil {
		return Config{}, fmt.Errorf("invalid LLM_BASE_URL: %w", err)
	}

	model, ok := firstConfiguredValue(lookup, dotEnvValues, "LLM_MODEL")
	if !ok {
		model = defaultProviderModel
	}

	return Config{APIKey: apiKey, BaseURL: baseURL, Model: model}, nil
}

// DefaultBaseURL exposes the fallback base URL used when no override is set.
// Test-only helper; production code reads it through LoadConfigFromDir.
func DefaultBaseURL() string { return defaultProviderBaseURL }

// DefaultModel exposes the fallback model identifier.
func DefaultModel() string { return defaultProviderModel }

// maxDotEnvDepth limits upward directory traversal when searching for .env
// files to prevent loading credentials from arbitrary ancestor directories.
const maxDotEnvDepth = 4

func readDotEnvUpward(dir string) (map[string]string, error) {
	currentDir := dir
	for depth := 0; depth < maxDotEnvDepth; depth++ {
		dotEnvPath := filepath.Join(currentDir, ".env")
		info, err := os.Stat(dotEnvPath)
		switch {
		case err == nil && !info.IsDir():
			values, readErr := godotenv.Read(dotEnvPath)
			if readErr != nil {
				return nil, fmt.Errorf("read %s: %w", dotEnvPath, readErr)
			}
			return values, nil
		case err != nil && !os.IsNotExist(err):
			return nil, fmt.Errorf("stat %s: %w", dotEnvPath, err)
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			break
		}
		currentDir = parentDir
	}
	return map[string]string{}, nil
}

// validateBaseURL rejects non-HTTPS base URLs to prevent accidental credential
// exposure over plaintext connections or redirection to attacker-controlled endpoints.
// Localhost addresses are allowed with either scheme for development use.
func validateBaseURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("malformed URL: %w", err)
	}
	host := u.Hostname()
	isLocal := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if !isLocal && u.Scheme != "https" {
		return fmt.Errorf("must use https (got %q); set LLM_BASE_URL to an https:// endpoint", u.Scheme)
	}
	return nil
}

func firstConfiguredValue(lookup LookupEnvFunc, dotEnvValues map[string]string, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := lookup(key); ok {
			trimmedValue := strings.TrimSpace(value)
			if trimmedValue != "" {
				return trimmedValue, true
			}
		}

		if value, ok := dotEnvValues[key]; ok {
			trimmedValue := strings.TrimSpace(value)
			if trimmedValue != "" {
				return trimmedValue, true
			}
		}
	}

	return "", false
}
