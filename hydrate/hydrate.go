package hydrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
)

// ProviderConfig holds configuration for a single LLM provider.
type ProviderConfig struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url,omitempty"`
}

// ProviderMap maps provider names to their configurations.
type ProviderMap map[string]ProviderConfig

// Config represents the ~/.petalflow/config.json file structure.
type Config struct {
	Providers map[string]ProviderConfig `json:"providers"`
	Defaults  map[string]string         `json:"defaults,omitempty"`
}

// ResolveProviders builds a ProviderMap from CLI flags, environment variables,
// and config file. Priority: flags > env vars > config file.
func ResolveProviders(flags map[string]string) (ProviderMap, error) {
	providers := make(ProviderMap)

	// 1. Load from config file (lowest priority)
	cfg, err := loadConfigFile()
	if err != nil {
		// Config file is optional -- only error if it exists but is malformed
		return nil, err
	}
	if cfg != nil {
		for name, pc := range cfg.Providers {
			providers[name] = pc
		}
	}

	// 2. Override with environment variables
	// Pattern: PETALFLOW_PROVIDER_{NAME}_API_KEY, PETALFLOW_PROVIDER_{NAME}_BASE_URL
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := parts[0], parts[1]
		if !strings.HasPrefix(key, "PETALFLOW_PROVIDER_") {
			continue
		}
		rest := strings.TrimPrefix(key, "PETALFLOW_PROVIDER_")
		if strings.HasSuffix(rest, "_API_KEY") {
			name := strings.ToLower(strings.TrimSuffix(rest, "_API_KEY"))
			pc := providers[name]
			pc.APIKey = val
			providers[name] = pc
		} else if strings.HasSuffix(rest, "_BASE_URL") {
			name := strings.ToLower(strings.TrimSuffix(rest, "_BASE_URL"))
			pc := providers[name]
			pc.BaseURL = val
			providers[name] = pc
		}
	}

	// 3. Override with CLI flags (highest priority)
	// Flags are key=value pairs like "anthropic=sk-ant-..."
	for name, apiKey := range flags {
		pc := providers[name]
		pc.APIKey = apiKey
		providers[name] = pc
	}

	return providers, nil
}

// loadConfigFile reads ~/.petalflow/config.json (or PETALFLOW_CONFIG env var).
// Returns nil, nil if the file doesn't exist.
func loadConfigFile() (*Config, error) {
	path := os.Getenv("PETALFLOW_CONFIG")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil // Can't determine home dir, skip config
		}
		path = filepath.Join(home, ".petalflow", "config.json")
	}

	data, err := os.ReadFile(path) // #nosec G304 -- path from well-known config location
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}
	return &cfg, nil
}

// NodeFactory is a function that creates a core.Node from a NodeDef.
// It is used by HydrateGraph to instantiate live nodes.
type NodeFactory func(graph.NodeDef) (core.Node, error)

// HydrateGraph takes a GraphDefinition and a ProviderMap, and returns an
// executable Graph with all nodes instantiated. LLM nodes are wired to
// provider configurations from the ProviderMap.
//
// The nodeFactory parameter creates live Node instances from NodeDef descriptors.
// If nil, a default factory is used that creates FuncNode placeholders.
func HydrateGraph(def *graph.GraphDefinition, providers ProviderMap, nodeFactory NodeFactory) (*graph.BasicGraph, error) {
	if def == nil {
		return nil, fmt.Errorf("graph definition is nil")
	}

	factory := nodeFactory
	if factory == nil {
		factory = defaultNodeFactory(providers)
	}

	return def.ToGraph(graph.WithNodeFactory(factory))
}

// defaultNodeFactory creates a basic NodeFactory that produces FuncNode
// placeholders. This is useful for validation and dry-run modes.
// For real execution, callers should provide a factory that creates
// proper node types (LLMNode, etc.) with live provider connections.
func defaultNodeFactory(providers ProviderMap) NodeFactory {
	return func(nd graph.NodeDef) (core.Node, error) {
		// For LLM nodes, verify the provider exists
		if nd.Type == "llm_prompt" || nd.Type == "llm_router" {
			providerName, _ := nd.Config["provider"].(string)
			if providerName != "" {
				if _, ok := providers[providerName]; !ok {
					return nil, fmt.Errorf("provider %q not configured (needed by node %q)", providerName, nd.ID)
				}
			}
		}
		// Create a placeholder FuncNode
		return core.NewFuncNode(nd.ID, nil), nil
	}
}

// ParseProviderFlags parses --provider-key flag values ("name=key") into a map.
func ParseProviderFlags(flags []string) (map[string]string, error) {
	result := make(map[string]string, len(flags))
	for _, flag := range flags {
		parts := strings.SplitN(flag, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid provider-key format %q: expected name=key", flag)
		}
		result[parts[0]] = parts[1]
	}
	return result, nil
}
