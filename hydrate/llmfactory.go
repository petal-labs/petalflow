package hydrate

import (
	"fmt"
	"math"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/nodes/conditional"
	"github.com/petal-labs/petalflow/nodes/conditional/expr"
)

func init() {
	// Register expression validator for graph-level conditional node validation.
	graph.SetExprValidator(expr.ValidateSyntax)
}

// ClientFactory creates a core.LLMClient for a named provider.
// The hydrate package defines this type but never imports iris directly —
// the caller supplies an implementation backed by llmprovider.
type ClientFactory func(providerName string, cfg ProviderConfig) (core.LLMClient, error)

// liveFactoryOptions holds optional dependencies for non-LLM node types.
type liveFactoryOptions struct {
	toolRegistry *core.ToolRegistry
	humanHandler nodes.HumanHandler
}

// LiveNodeOption configures optional dependencies for NewLiveNodeFactory.
type LiveNodeOption func(*liveFactoryOptions)

// WithToolRegistry provides a ToolRegistry so that tool-type nodes resolve to
// real ToolNode instances instead of FuncNode placeholders.
func WithToolRegistry(r *core.ToolRegistry) LiveNodeOption {
	return func(o *liveFactoryOptions) { o.toolRegistry = r }
}

// WithHumanHandler provides a HumanHandler so that human-type nodes resolve to
// real HumanNode instances instead of FuncNode placeholders.
func WithHumanHandler(h nodes.HumanHandler) LiveNodeOption {
	return func(o *liveFactoryOptions) { o.humanHandler = h }
}

// NewLiveNodeFactory returns a NodeFactory that creates real LLMNode,
// LLMRouter, MergeNode, HumanNode, and ToolNode instances. Node types
// without the required dependencies fall back to FuncNode placeholders.
func NewLiveNodeFactory(providers ProviderMap, clientFactory ClientFactory, opts ...LiveNodeOption) NodeFactory {
	var options liveFactoryOptions
	for _, o := range opts {
		o(&options)
	}

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
		case "merge":
			return buildMergeNode(nd)
		case "human":
			return buildHumanNode(nd, options.humanHandler)
		case "conditional":
			return buildConditionalNode(nd)
		default:
			// Check if the type matches a registered tool.
			if options.toolRegistry != nil {
				if tool, ok := options.toolRegistry.Get(nd.Type); ok {
					return buildToolNode(nd, tool), nil
				}
			}
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

// configDuration extracts a time.Duration from config.
// Accepts a string (e.g. "30s", "5m") or a float64 interpreted as seconds.
func configDuration(m map[string]any, key string) time.Duration {
	switch v := m[key].(type) {
	case string:
		d, _ := time.ParseDuration(v)
		return d
	case float64:
		return time.Duration(v * float64(time.Second))
	}
	return 0
}

// --- merge / human / tool builders ---

// buildMergeNode creates a MergeNode from a NodeDef.
func buildMergeNode(nd graph.NodeDef) (core.Node, error) {
	cfg := nodes.MergeNodeConfig{
		OutputKey: configString(nd.Config, "output_key"),
	}

	strategy := configString(nd.Config, "strategy")
	switch strategy {
	case "concat":
		cfg.Strategy = nodes.NewConcatMergeStrategy(nodes.ConcatMergeConfig{
			VarName:   configString(nd.Config, "var_name"),
			Separator: configString(nd.Config, "separator"),
		})
	case "best_score":
		higherIsBetter := true
		if v, ok := nd.Config["higher_is_better"].(bool); ok {
			higherIsBetter = v
		}
		cfg.Strategy = nodes.NewBestScoreMergeStrategy(nodes.BestScoreMergeConfig{
			ScoreVar:       configString(nd.Config, "score_var"),
			HigherIsBetter: higherIsBetter,
		})
	default:
		// "json" or empty → JSON merge (the node default)
		cfg.Strategy = nodes.NewJSONMergeStrategy(nodes.JSONMergeConfig{})
	}

	return nodes.NewMergeNode(nd.ID, cfg), nil
}

// buildHumanNode creates a HumanNode from a NodeDef.
// Returns an error if no HumanHandler was provided.
func buildHumanNode(nd graph.NodeDef, handler nodes.HumanHandler) (core.Node, error) {
	if handler == nil {
		return nil, fmt.Errorf("node %q: human node requires a HumanHandler (use WithHumanHandler)", nd.ID)
	}

	cfg := nodes.HumanNodeConfig{
		RequestType: nodes.HumanRequestType(configString(nd.Config, "mode")),
		Prompt:      configString(nd.Config, "prompt"),
		OutputVar:   configString(nd.Config, "output_var"),
		Timeout:     configDuration(nd.Config, "timeout"),
		Handler:     handler,
	}

	return nodes.NewHumanNode(nd.ID, cfg), nil
}

// buildConditionalNode creates a ConditionalNode from a NodeDef.
func buildConditionalNode(nd graph.NodeDef) (core.Node, error) {
	cfg := conditional.Config{
		Default:     configString(nd.Config, "default"),
		PassThrough: true,
		OutputKey:   configString(nd.Config, "output_key"),
	}

	if order := configString(nd.Config, "evaluation_order"); order != "" {
		cfg.EvaluationOrder = order
	}

	if v, ok := nd.Config["pass_through"].(bool); ok {
		cfg.PassThrough = v
	}

	// Parse conditions array from config
	conditionsRaw, _ := nd.Config["conditions"].([]any)
	for _, raw := range conditionsRaw {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cond := conditional.Condition{
			Name:        configMapString(m, "name"),
			Expression:  configMapString(m, "expression"),
			Description: configMapString(m, "description"),
		}
		cfg.Conditions = append(cfg.Conditions, cond)
	}

	return conditional.NewConditionalNode(nd.ID, cfg)
}

func configMapString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// buildToolNode creates a ToolNode from a NodeDef and a resolved tool.
func buildToolNode(nd graph.NodeDef, tool core.PetalTool) *nodes.ToolNode {
	cfg := nodes.ToolNodeConfig{
		ToolName:  nd.Type,
		OutputKey: configString(nd.Config, "output_key"),
		Timeout:   configDuration(nd.Config, "timeout"),
	}

	return nodes.NewToolNode(nd.ID, tool, cfg)
}
