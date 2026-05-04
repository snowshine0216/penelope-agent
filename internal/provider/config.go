package provider

import (
	"fmt"
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

func readDotEnvUpward(dir string) (map[string]string, error) {
	currentDir := dir
	for {
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
			return map[string]string{}, nil
		}
		currentDir = parentDir
	}
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
