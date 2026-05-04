package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

const defaultProviderBaseURL = "https://api.minimaxi.com/v1/"
const defaultProviderModel = "glm-4.5-air"

type providerConfig struct {
	apiKey  string
	baseURL string
	model   string
}

type lookupEnvFunc func(string) (string, bool)

func loadProviderConfig() (providerConfig, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return providerConfig{}, fmt.Errorf("获取当前工作目录失败: %w", err)
	}

	return loadProviderConfigFromDir(workingDir, os.LookupEnv)
}

func loadProviderConfigFromDir(dir string, lookup lookupEnvFunc) (providerConfig, error) {
	dotEnvValues, err := readDotEnvUpward(dir)
	if err != nil {
		return providerConfig{}, err
	}

	apiKey, ok := firstConfiguredValue(lookup, dotEnvValues, "LLM_API_KEY", "MINIMAX_API_KEY", "ZHIPU_API_KEY")
	if !ok {
		return providerConfig{}, fmt.Errorf("请在环境变量或 .env 文件中设置 LLM_API_KEY（兼容 MINIMAX_API_KEY / ZHIPU_API_KEY）")
	}

	baseURL, ok := firstConfiguredValue(lookup, dotEnvValues, "LLM_BASE_URL")
	if !ok {
		baseURL = defaultProviderBaseURL
	}

	model, ok := firstConfiguredValue(lookup, dotEnvValues, "LLM_MODEL")
	if !ok {
		model = defaultProviderModel
	}

	return providerConfig{apiKey: apiKey, baseURL: baseURL, model: model}, nil
}

func readDotEnvUpward(dir string) (map[string]string, error) {
	currentDir := dir
	for {
		dotEnvPath := filepath.Join(currentDir, ".env")
		info, err := os.Stat(dotEnvPath)
		switch {
		case err == nil && !info.IsDir():
			values, readErr := godotenv.Read(dotEnvPath)
			if readErr != nil {
				return nil, fmt.Errorf("读取 %s 失败: %w", dotEnvPath, readErr)
			}
			return values, nil
		case err != nil && !os.IsNotExist(err):
			return nil, fmt.Errorf("检查 %s 失败: %w", dotEnvPath, err)
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return map[string]string{}, nil
		}
		currentDir = parentDir
	}
}

func firstConfiguredValue(lookup lookupEnvFunc, dotEnvValues map[string]string, keys ...string) (string, bool) {
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
