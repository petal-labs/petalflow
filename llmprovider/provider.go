package llmprovider

import (
	"fmt"
	"strings"

	iriscore "github.com/petal-labs/iris/core"
	"github.com/petal-labs/iris/providers"
	anthropicprovider "github.com/petal-labs/iris/providers/anthropic"
	ollamaprovider "github.com/petal-labs/iris/providers/ollama"
	openaiprovider "github.com/petal-labs/iris/providers/openai"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
)

// NewClient creates a core.LLMClient for the named provider using the given config.
// It delegates to the iris provider registry to instantiate the underlying provider.
//
// The returned client is wrapped with capability preflight: requests whose
// implied capabilities (tools, structured output, reasoning, multimodal
// input) exceed the model's registered capabilities fail before any
// provider request is dispatched. Capability metadata comes from the
// bundled snapshot plus the provider config's per-model overrides.
// Config warnings (malformed capability entries) are dropped; use
// NewClientDetailed to receive them.
func NewClient(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
	client, _, err := NewClientDetailed(name, cfg)
	return client, err
}

// NewClientDetailed is NewClient with the capability-configuration warnings
// returned to the caller.
func NewClientDetailed(name string, cfg hydrate.ProviderConfig) (core.LLMClient, []string, error) {
	provider, err := createProvider(name, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("creating provider %q: %w", name, err)
	}

	registry, warnings := NewCapabilityRegistry(name, cfg)
	var client core.LLMClient = &irisAdapter{provider: provider}
	if streaming, ok := client.(StreamingClient); ok {
		return NewCapabilityAwareStreamingClient(streaming, registry, name), warnings, nil
	}
	return NewCapabilityAwareClient(client, registry, name), warnings, nil
}

func createProvider(name string, cfg hydrate.ProviderConfig) (iriscore.Provider, error) {
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
