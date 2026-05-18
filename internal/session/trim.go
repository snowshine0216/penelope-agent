package session

import (
	"fmt"
	"sort"
	"sync"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// Trimmer reduces a non-system message slice to a provider-safe view.
// Implementations must be pure: same input slice -> same output slice,
// no I/O, no allocation of shared state. The engine relies on this so
// that calling Trim before every provider.Generate is deterministic.
type Trimmer interface {
	Trim(messages []schema.Message) []schema.Message
	Name() string
}

// TrimConfig captures user-facing bounds for the default strategy.
// Custom strategies may ignore fields that do not apply to them.
type TrimConfig struct {
	MaxUserTurns int
	MaxTokens    int
}

type constructor func(TrimConfig) Trimmer

var (
	registryMu sync.RWMutex
	registry   = map[string]constructor{}
)

// Register makes a trimmer constructor available under the given name.
// Re-registering the same name overwrites the previous entry; init()
// callers in this package register the built-in "window" strategy.
func Register(name string, ctor constructor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = ctor
}

// Get instantiates the trimmer registered under name with the supplied
// config. An unknown name returns an error listing every known strategy
// so the CLI can surface a useful message.
func Get(name string, cfg TrimConfig) (Trimmer, error) {
	registryMu.RLock()
	ctor, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown trim strategy %q (known: %s)", name, knownNames())
	}
	return ctor(cfg), nil
}

func knownNames() string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
