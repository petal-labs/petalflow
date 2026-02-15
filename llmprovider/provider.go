package llmprovider

import (
	"fmt"
	"strings"

	"github.com/petal-labs/iris/providers"
	anthropicprovider "github.com/petal-labs/iris/providers/anthropic"
	ollamaprovider "github.com/petal-labs/iris/providers/ollama"
	openaiprovider "github.com/petal-labs/iris/providers/openai"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
)

// NewClient creates a core.LLMClient for the named provider using the given config.
// It delegates to the iris provider registry to instantiate the underlying provider.
func NewClient(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
	provider, err := createProvider(name, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating provider %q: %w", name, err)
	}
	return &irisAdapter{provider: provider}, nil
}

func createProvider(name string, cfg hydrate.ProviderConfig) (providers.Provider, error) {
	normalized := strings.ToLower(name)

	switch normalized {
	case "openai":
		opts := make([]openaiprovider.Option, 0, 1)
		if cfg.BaseURL != "" {
			opts = append(opts, openaiprovider.WithBaseURL(cfg.BaseURL))
		}
		return openaiprovider.New(cfg.APIKey, opts...), nil
	case "anthropic":
		opts := make([]anthropicprovider.Option, 0, 1)
		if cfg.BaseURL != "" {
			opts = append(opts, anthropicprovider.WithBaseURL(cfg.BaseURL))
		}
		return anthropicprovider.New(cfg.APIKey, opts...), nil
	case "ollama":
		opts := make([]ollamaprovider.Option, 0, 2)
		if cfg.APIKey != "" {
			opts = append(opts, ollamaprovider.WithAPIKey(cfg.APIKey))
		}
		if cfg.BaseURL != "" {
			opts = append(opts, ollamaprovider.WithBaseURL(cfg.BaseURL))
		}
		return ollamaprovider.New(opts...), nil
	default:
		return providers.Create(normalized, cfg.APIKey)
	}
}
