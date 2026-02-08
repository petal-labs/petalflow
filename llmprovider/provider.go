package llmprovider

import (
	"fmt"

	"github.com/petal-labs/iris/providers"
	// Auto-register common providers.
	_ "github.com/petal-labs/iris/providers/anthropic"
	_ "github.com/petal-labs/iris/providers/ollama"
	_ "github.com/petal-labs/iris/providers/openai"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
)

// NewClient creates a core.LLMClient for the named provider using the given config.
// It delegates to the iris provider registry to instantiate the underlying provider.
func NewClient(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
	provider, err := providers.Create(name, cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("creating provider %q: %w", name, err)
	}
	return &irisAdapter{provider: provider}, nil
}
