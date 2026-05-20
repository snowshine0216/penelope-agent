package compact

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// FallbackContextLimit is used when a model is in neither the built-in
// registry nor the user-supplied override map. 32k is conservative
// enough that even the smallest legacy models won't trip a 4xx.
const FallbackContextLimit = 32_000

// defaultModelLimits maps model id -> total context-window size in
// tokens. Add new entries here as providers ship new models. Keep keys
// matching the exact strings the CLI passes via --model.
var defaultModelLimits = map[string]int{
	"claude-opus-4-7":           200_000,
	"claude-opus-4-7[1m]":       1_000_000,
	"claude-sonnet-4-6":         200_000,
	"claude-haiku-4-5-20251001": 200_000,
	"gpt-4o":                    128_000,
	"gpt-4o-mini":               128_000,
}

// LookupModelLimit resolves the total context-window for the given
// model id. Overrides win over the built-in registry. The second
// return is false iff the model is in neither map; callers fall back
// to FallbackContextLimit.
func LookupModelLimit(model string, overrides map[string]int) (int, bool) {
	if v, ok := overrides[model]; ok {
		return v, true
	}
	v, ok := defaultModelLimits[model]
	return v, ok
}

// LoadOverridesYAML parses a YAML file shaped as `{model_id: limit}`.
// A missing file is NOT an error (returns empty map) so the override
// file is genuinely optional. Malformed YAML IS an error, surfaced at
// NewCompactor time so silent ignore-and-continue cannot drop user
// intent.
func LoadOverridesYAML(path string) (map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]int{}, nil
		}
		return nil, fmt.Errorf("read model-limits override %q: %w", path, err)
	}
	out := map[string]int{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse model-limits override %q: %w", path, err)
	}
	return out, nil
}
