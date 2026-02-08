package hydrate

import (
	"fmt"
	"math"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
)

// ClientFactory creates a core.LLMClient for a named provider.
// The hydrate package defines this type but never imports iris directly —
// the caller supplies an implementation backed by llmprovider.
type ClientFactory func(providerName string, cfg ProviderConfig) (core.LLMClient, error)

// NewLiveNodeFactory returns a NodeFactory that creates real LLMNode and
// LLMRouter instances wired to live LLM providers. Non-LLM node types fall
// back to FuncNode placeholders.
func NewLiveNodeFactory(providers ProviderMap, clientFactory ClientFactory) NodeFactory {
	// Cache one client per provider name so multiple nodes sharing a provider reuse it.
	clients := make(map[string]core.LLMClient)

	getClient := func(providerName string) (core.LLMClient, error) {
		if c, ok := clients[providerName]; ok {
			return c, nil
		}
		cfg, ok := providers[providerName]
		if !ok {
			return nil, fmt.Errorf("provider %q not configured", providerName)
		}
		c, err := clientFactory(providerName, cfg)
		if err != nil {
			return nil, err
		}
		clients[providerName] = c
		return c, nil
	}

	return func(nd graph.NodeDef) (core.Node, error) {
		switch nd.Type {
		case "llm_prompt":
			return buildLLMNode(nd, getClient)
		case "llm_router":
			return buildLLMRouter(nd, getClient)
		default:
			return core.NewFuncNode(nd.ID, nil), nil
		}
	}
}

// buildLLMNode extracts config from a NodeDef and returns an LLMNode.
func buildLLMNode(nd graph.NodeDef, getClient func(string) (core.LLMClient, error)) (core.Node, error) {
	providerName, _ := nd.Config["provider"].(string)
	if providerName == "" {
		return nil, fmt.Errorf("node %q: missing \"provider\" in config", nd.ID)
	}

	client, err := getClient(providerName)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", nd.ID, err)
	}

	cfg := nodes.LLMNodeConfig{
		Model:          configString(nd.Config, "model"),
		System:         configString(nd.Config, "system_prompt"),
		PromptTemplate: configString(nd.Config, "prompt_template"),
		OutputKey:      configString(nd.Config, "output_key"),
	}

	if v, ok := configFloat64(nd.Config, "temperature"); ok {
		cfg.Temperature = &v
	}
	if v, ok := configInt(nd.Config, "max_tokens"); ok {
		cfg.MaxTokens = &v
	}

	return nodes.NewLLMNode(nd.ID, client, cfg), nil
}

// buildLLMRouter extracts config from a NodeDef and returns an LLMRouter.
func buildLLMRouter(nd graph.NodeDef, getClient func(string) (core.LLMClient, error)) (core.Node, error) {
	providerName, _ := nd.Config["provider"].(string)
	if providerName == "" {
		return nil, fmt.Errorf("node %q: missing \"provider\" in config", nd.ID)
	}

	client, err := getClient(providerName)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", nd.ID, err)
	}

	cfg := nodes.LLMRouterConfig{
		Model:       configString(nd.Config, "model"),
		System:      configString(nd.Config, "system_prompt"),
		DecisionKey: configString(nd.Config, "decision_key"),
	}

	if v, ok := configFloat64(nd.Config, "temperature"); ok {
		cfg.Temperature = &v
	}

	// Parse allowed_targets map
	if targets, ok := nd.Config["allowed_targets"].(map[string]any); ok {
		cfg.AllowedTargets = make(map[string]string, len(targets))
		for k, v := range targets {
			if s, ok := v.(string); ok {
				cfg.AllowedTargets[k] = s
			}
		}
	}

	return nodes.NewLLMRouter(nd.ID, client, cfg), nil
}

// --- config helpers ---

func configString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// configFloat64 extracts a float64 from config (JSON numbers are float64).
func configFloat64(m map[string]any, key string) (float64, bool) {
	v, ok := m[key].(float64)
	return v, ok
}

// configInt extracts an int from config, handling JSON float64 → int coercion.
func configInt(m map[string]any, key string) (int, bool) {
	v, ok := m[key].(float64)
	if !ok {
		return 0, false
	}
	// Guard against NaN/Inf and non-integer values
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return int(v), true
}
