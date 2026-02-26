package hydrate

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
	condnode "github.com/petal-labs/petalflow/nodes/conditional"
	"github.com/petal-labs/petalflow/registry"
)

// mockLLMClient implements core.LLMClient for testing.
type mockLLMClient struct {
	providerName string
}

func (m *mockLLMClient) Complete(context.Context, core.LLMRequest) (core.LLMResponse, error) {
	return core.LLMResponse{Provider: m.providerName}, nil
}

func newMockClientFactory() (ClientFactory, map[string]int) {
	calls := make(map[string]int)
	return func(name string, cfg ProviderConfig) (core.LLMClient, error) {
		calls[name]++
		return &mockLLMClient{providerName: name}, nil
	}, calls
}

func TestNewLiveNodeFactory_LLMPrompt(t *testing.T) {
	providers := ProviderMap{
		"anthropic": {APIKey: "sk-test"},
	}
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(providers, factory)

	nd := graph.NodeDef{
		ID:   "summarizer",
		Type: "llm_prompt",
		Config: map[string]any{
			"provider":        "anthropic",
			"model":           "claude-3-haiku",
			"system_prompt":   "You summarize text.",
			"prompt_template": "Summarize: {{.input}}",
			"output_key":      "summary",
			"temperature":     0.5,
			"max_tokens":      float64(1024),
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	llmNode, ok := node.(*nodes.LLMNode)
	if !ok {
		t.Fatalf("expected *nodes.LLMNode, got %T", node)
	}

	cfg := llmNode.Config()
	if cfg.Model != "claude-3-haiku" {
		t.Errorf("Model = %q, want %q", cfg.Model, "claude-3-haiku")
	}
	if cfg.System != "You summarize text." {
		t.Errorf("System = %q, want %q", cfg.System, "You summarize text.")
	}
	if cfg.PromptTemplate != "Summarize: {{.input}}" {
		t.Errorf("PromptTemplate = %q, want %q", cfg.PromptTemplate, "Summarize: {{.input}}")
	}
	if cfg.OutputKey != "summary" {
		t.Errorf("OutputKey = %q, want %q", cfg.OutputKey, "summary")
	}
	if cfg.Temperature == nil || *cfg.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5", cfg.Temperature)
	}
	if cfg.MaxTokens == nil || *cfg.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %v, want 1024", cfg.MaxTokens)
	}
}

func TestNewLiveNodeFactory_LLMPromptWithFunctionCallTools(t *testing.T) {
	providers := ProviderMap{
		"anthropic": {APIKey: "sk-test"},
	}
	factory, _ := newMockClientFactory()
	toolRegistry := core.NewToolRegistry()
	toolRegistry.Register(core.NewFuncTool(
		"context7.resolve",
		"Resolve docs",
		func(context.Context, map[string]any) (map[string]any, error) {
			return map[string]any{"ok": true}, nil
		},
	))

	nodeFactory := NewLiveNodeFactory(
		providers,
		factory,
		WithToolRegistry(toolRegistry),
	)

	nd := graph.NodeDef{
		ID:   "researcher",
		Type: "llm_prompt",
		Config: map[string]any{
			"provider": "anthropic",
			"model":    "claude-sonnet",
			"tools":    []any{"context7.resolve"},
			"tool_config": map[string]any{
				"context7": map[string]any{
					"workspace": "petalflow",
				},
			},
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	llmNode, ok := node.(*nodes.LLMNode)
	if !ok {
		t.Fatalf("expected *nodes.LLMNode, got %T", node)
	}

	cfg := llmNode.Config()
	if len(cfg.Tools) != 1 || cfg.Tools[0] != "context7.resolve" {
		t.Fatalf("cfg.Tools = %#v, want [context7.resolve]", cfg.Tools)
	}
	if cfg.ToolRegistry == nil {
		t.Fatal("cfg.ToolRegistry should be set when WithToolRegistry is provided")
	}
	if cfg.ToolConfig["context7"]["workspace"] != "petalflow" {
		t.Fatalf("cfg.ToolConfig context7 workspace = %v, want petalflow", cfg.ToolConfig["context7"]["workspace"])
	}
}

func TestNewLiveNodeFactory_LLMRouter(t *testing.T) {
	providers := ProviderMap{
		"openai": {APIKey: "sk-test"},
	}
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(providers, factory)

	nd := graph.NodeDef{
		ID:   "classifier",
		Type: "llm_router",
		Config: map[string]any{
			"provider":      "openai",
			"model":         "gpt-4",
			"system_prompt": "Classify the input.",
			"allowed_targets": map[string]any{
				"positive": "happy_path",
				"negative": "sad_path",
			},
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	router, ok := node.(*nodes.LLMRouter)
	if !ok {
		t.Fatalf("expected *nodes.LLMRouter, got %T", node)
	}

	cfg := router.Config()
	if cfg.Model != "gpt-4" {
		t.Errorf("Model = %q, want %q", cfg.Model, "gpt-4")
	}
	if cfg.System != "Classify the input." {
		t.Errorf("System = %q, want %q", cfg.System, "Classify the input.")
	}
	if len(cfg.AllowedTargets) != 2 {
		t.Errorf("AllowedTargets len = %d, want 2", len(cfg.AllowedTargets))
	}
	if cfg.AllowedTargets["positive"] != "happy_path" {
		t.Errorf("AllowedTargets[positive] = %q, want %q", cfg.AllowedTargets["positive"], "happy_path")
	}
}

func TestNewLiveNodeFactory_UnknownType(t *testing.T) {
	providers := ProviderMap{}
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(providers, factory)

	nd := graph.NodeDef{
		ID:   "mystery",
		Type: "some_unknown_type",
	}

	_, err := nodeFactory(nd)
	if err == nil {
		t.Fatal("expected error for unknown node type, got nil")
	}
}

func TestNewLiveNodeFactory_MissingProvider(t *testing.T) {
	providers := ProviderMap{} // no providers configured
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(providers, factory)

	nd := graph.NodeDef{
		ID:   "node1",
		Type: "llm_prompt",
		Config: map[string]any{
			"provider": "anthropic",
			"model":    "claude-3",
		},
	}

	_, err := nodeFactory(nd)
	if err == nil {
		t.Fatal("expected error for missing provider, got nil")
	}
}

func TestNewLiveNodeFactory_MissingProviderField(t *testing.T) {
	providers := ProviderMap{
		"anthropic": {APIKey: "sk-test"},
	}
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(providers, factory)

	nd := graph.NodeDef{
		ID:   "node1",
		Type: "llm_prompt",
		Config: map[string]any{
			"model": "claude-3",
			// no "provider" key
		},
	}

	_, err := nodeFactory(nd)
	if err == nil {
		t.Fatal("expected error for missing provider field, got nil")
	}
}

func TestNewLiveNodeFactory_ClientCaching(t *testing.T) {
	providers := ProviderMap{
		"anthropic": {APIKey: "sk-test"},
	}
	factory, calls := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(providers, factory)

	// Create two nodes with the same provider
	for _, id := range []string{"node1", "node2"} {
		nd := graph.NodeDef{
			ID:   id,
			Type: "llm_prompt",
			Config: map[string]any{
				"provider": "anthropic",
				"model":    "claude-3",
			},
		}
		if _, err := nodeFactory(nd); err != nil {
			t.Fatalf("node %s: unexpected error: %v", id, err)
		}
	}

	if calls["anthropic"] != 1 {
		t.Errorf("ClientFactory called %d times for anthropic, want 1 (cached)", calls["anthropic"])
	}
}

func TestNewLiveNodeFactory_NumericCoercion(t *testing.T) {
	providers := ProviderMap{
		"anthropic": {APIKey: "sk-test"},
	}
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(providers, factory)

	// JSON deserialisation turns numbers into float64
	nd := graph.NodeDef{
		ID:   "node1",
		Type: "llm_prompt",
		Config: map[string]any{
			"provider":    "anthropic",
			"model":       "claude-3",
			"temperature": float64(0.3),
			"max_tokens":  float64(2048),
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	llmNode := node.(*nodes.LLMNode)
	cfg := llmNode.Config()
	if cfg.Temperature == nil || *cfg.Temperature != 0.3 {
		t.Errorf("Temperature = %v, want 0.3", cfg.Temperature)
	}
	if cfg.MaxTokens == nil || *cfg.MaxTokens != 2048 {
		t.Errorf("MaxTokens = %v, want 2048", cfg.MaxTokens)
	}
}

func TestNewLiveNodeFactory_DefaultOutputKey(t *testing.T) {
	providers := ProviderMap{
		"anthropic": {APIKey: "sk-test"},
	}
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(providers, factory)

	nd := graph.NodeDef{
		ID:   "mynode",
		Type: "llm_prompt",
		Config: map[string]any{
			"provider": "anthropic",
			"model":    "claude-3",
			// no output_key — NewLLMNode defaults to id + "_output"
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	llmNode := node.(*nodes.LLMNode)
	if llmNode.Config().OutputKey != "mynode_output" {
		t.Errorf("OutputKey = %q, want %q", llmNode.Config().OutputKey, "mynode_output")
	}
}

// --- Merge node tests ---

func TestNewLiveNodeFactory_MergeNode(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	nd := graph.NodeDef{
		ID:   "merger",
		Type: "merge",
		Config: map[string]any{
			"strategy":   "concat",
			"var_name":   "text",
			"separator":  "\n---\n",
			"output_key": "merged",
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mn, ok := node.(*nodes.MergeNode)
	if !ok {
		t.Fatalf("expected *nodes.MergeNode, got %T", node)
	}

	if mn.Kind() != "merge" {
		t.Errorf("Kind = %q, want %q", mn.Kind(), "merge")
	}
}

func TestNewLiveNodeFactory_MergeNode_DefaultStrategy(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	nd := graph.NodeDef{
		ID:     "merger",
		Type:   "merge",
		Config: map[string]any{},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := node.(*nodes.MergeNode); !ok {
		t.Fatalf("expected *nodes.MergeNode, got %T", node)
	}
}

// --- Human node tests ---

// mockHumanHandler implements nodes.HumanHandler for testing.
type mockHumanHandler struct{}

func (m *mockHumanHandler) Request(_ context.Context, _ *nodes.HumanRequest) (*nodes.HumanResponse, error) {
	return &nodes.HumanResponse{}, nil
}

func TestNewLiveNodeFactory_HumanNode(t *testing.T) {
	factory, _ := newMockClientFactory()
	handler := &mockHumanHandler{}
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory, WithHumanHandler(handler))

	nd := graph.NodeDef{
		ID:   "review",
		Type: "human",
		Config: map[string]any{
			"mode":       "approval",
			"prompt":     "Please approve this change.",
			"output_var": "approved",
			"timeout":    "30s",
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hn, ok := node.(*nodes.HumanNode)
	if !ok {
		t.Fatalf("expected *nodes.HumanNode, got %T", node)
	}

	if hn.Kind() != "human" {
		t.Errorf("Kind = %q, want %q", hn.Kind(), "human")
	}
}

func TestNewLiveNodeFactory_HumanNode_MissingHandler(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory) // no WithHumanHandler

	nd := graph.NodeDef{
		ID:   "review",
		Type: "human",
		Config: map[string]any{
			"mode":   "approval",
			"prompt": "Approve?",
		},
	}

	_, err := nodeFactory(nd)
	if err == nil {
		t.Fatal("expected error for missing HumanHandler, got nil")
	}
}

// --- Tool node tests ---

// mockTool implements core.PetalTool for testing.
type mockTool struct {
	name string
}

func (m *mockTool) Name() string { return m.name }
func (m *mockTool) Invoke(_ context.Context, _ map[string]any) (map[string]any, error) {
	return nil, nil
}

func TestNewLiveNodeFactory_ToolNode(t *testing.T) {
	registry := core.NewToolRegistry()
	registry.Register(&mockTool{name: "web_search"})

	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory, WithToolRegistry(registry))

	nd := graph.NodeDef{
		ID:   "search",
		Type: "web_search",
		Config: map[string]any{
			"output_key": "results",
			"timeout":    "10s",
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tn, ok := node.(*nodes.ToolNode)
	if !ok {
		t.Fatalf("expected *nodes.ToolNode, got %T", node)
	}

	if tn.Kind() != "tool" {
		t.Errorf("Kind = %q, want %q", tn.Kind(), "tool")
	}
}

func TestNewLiveNodeFactory_ToolNode_NotInRegistry(t *testing.T) {
	registry := core.NewToolRegistry()
	// Registry is empty — "web_search" not registered.

	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory, WithToolRegistry(registry))

	nd := graph.NodeDef{
		ID:   "search",
		Type: "web_search",
	}

	_, err := nodeFactory(nd)
	if err == nil {
		t.Fatal("expected error when tool type is not registered, got nil")
	}
}

func TestNewLiveNodeFactory_FuncNode(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	nd := graph.NodeDef{
		ID:   "fn",
		Type: "func",
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := node.(*core.FuncNode); !ok {
		t.Fatalf("expected *core.FuncNode, got %T", node)
	}
}

func TestNewLiveNodeFactory_NoopNode(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	nd := graph.NodeDef{
		ID:   "noop",
		Type: "noop",
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := node.(*core.NoopNode); !ok {
		t.Fatalf("expected *core.NoopNode, got %T", node)
	}
}

func TestNewLiveNodeFactory_ConditionalNode(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	nd := graph.NodeDef{
		ID:   "route",
		Type: "conditional",
		Config: map[string]any{
			"default":          "_skip",
			"evaluation_order": "all",
			"pass_through":     false,
			"output_key":       "decision",
			"conditions": []any{
				map[string]any{
					"name":        "approve",
					"expression":  "input.score >= 0.7",
					"description": "high confidence",
				},
				"invalid-condition-shape",
			},
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cn, ok := node.(*condnode.ConditionalNode)
	if !ok {
		t.Fatalf("expected *conditional.ConditionalNode, got %T", node)
	}

	cfg := cn.Config()
	if cfg.Default != "_skip" {
		t.Fatalf("Default = %q, want %q", cfg.Default, "_skip")
	}
	if cfg.EvaluationOrder != "all" {
		t.Fatalf("EvaluationOrder = %q, want %q", cfg.EvaluationOrder, "all")
	}
	if cfg.PassThrough {
		t.Fatalf("PassThrough = true, want false")
	}
	if cfg.OutputKey != "decision" {
		t.Fatalf("OutputKey = %q, want %q", cfg.OutputKey, "decision")
	}
	if len(cfg.Conditions) != 1 {
		t.Fatalf("len(Conditions) = %d, want 1", len(cfg.Conditions))
	}
	if cfg.Conditions[0].Name != "approve" {
		t.Fatalf("condition name = %q, want %q", cfg.Conditions[0].Name, "approve")
	}
}

func TestNewLiveNodeFactory_ToolNodeExplicitType(t *testing.T) {
	registry := core.NewToolRegistry()
	registry.Register(&mockTool{name: "web_search"})
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory, WithToolRegistry(registry))

	nd := graph.NodeDef{
		ID:   "search",
		Type: "tool",
		Config: map[string]any{
			"tool_name": "web_search",
			"args_template": map[string]any{
				"query": "input.query",
			},
			"static_args": map[string]any{
				"limit": float64(5),
			},
			"output_key": "results",
			"timeout":    float64(2),
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tn, ok := node.(*nodes.ToolNode)
	if !ok {
		t.Fatalf("expected *nodes.ToolNode, got %T", node)
	}
	cfg := tn.Config()
	if cfg.ToolName != "web_search" {
		t.Fatalf("ToolName = %q, want %q", cfg.ToolName, "web_search")
	}
	if cfg.OutputKey != "results" {
		t.Fatalf("OutputKey = %q, want %q", cfg.OutputKey, "results")
	}
	if cfg.ArgsTemplate["query"] != "input.query" {
		t.Fatalf("ArgsTemplate[query] = %q, want %q", cfg.ArgsTemplate["query"], "input.query")
	}
	if limit, ok := cfg.StaticArgs["limit"].(float64); !ok || limit != 5 {
		t.Fatalf("StaticArgs[limit] = %v, want 5", cfg.StaticArgs["limit"])
	}
	if cfg.Timeout != 2*time.Second {
		t.Fatalf("Timeout = %s, want 2s", cfg.Timeout)
	}
}

func TestNewLiveNodeFactory_ToolTypeErrors(t *testing.T) {
	factory, _ := newMockClientFactory()

	t.Run("requires registry", func(t *testing.T) {
		nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)
		_, err := nodeFactory(graph.NodeDef{ID: "tool1", Type: "tool", Config: map[string]any{"tool_name": "x"}})
		if err == nil {
			t.Fatal("expected error for missing registry")
		}
	})

	t.Run("requires tool_name", func(t *testing.T) {
		nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory, WithToolRegistry(core.NewToolRegistry()))
		_, err := nodeFactory(graph.NodeDef{ID: "tool1", Type: "tool", Config: map[string]any{}})
		if err == nil {
			t.Fatal("expected error for missing tool_name")
		}
	})

	t.Run("tool not found", func(t *testing.T) {
		nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory, WithToolRegistry(core.NewToolRegistry()))
		_, err := nodeFactory(graph.NodeDef{ID: "tool1", Type: "tool", Config: map[string]any{"tool_name": "missing"}})
		if err == nil {
			t.Fatal("expected error for missing tool in registry")
		}
	})
}

func TestNewLiveNodeFactory_MapAndCacheBindings(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	mapNode, err := nodeFactory(graph.NodeDef{
		ID:   "m1",
		Type: "map",
		Config: map[string]any{
			"input_var":  "items",
			"output_var": "mapped",
			"mapper_binding": map[string]any{
				"type": "transform",
				"config": map[string]any{
					"transform":  "template",
					"template":   "{{.item.name}}",
					"output_var": "label",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("map node: unexpected error: %v", err)
	}
	mn, ok := mapNode.(*nodes.MapNode)
	if !ok {
		t.Fatalf("expected *nodes.MapNode, got %T", mapNode)
	}
	mapCfg := mn.Config()
	if mapCfg.MapperNode == nil {
		t.Fatal("map config MapperNode should be set")
	}
	if mapCfg.InputVar != "items" || mapCfg.OutputVar != "mapped" {
		t.Fatalf("unexpected map config input/output: %q/%q", mapCfg.InputVar, mapCfg.OutputVar)
	}

	cacheNode, err := nodeFactory(graph.NodeDef{
		ID:   "c1",
		Type: "cache",
		Config: map[string]any{
			"ttl":        "15s",
			"output_var": "cache_meta",
			"wrapped_node": map[string]any{
				"type": "transform",
				"config": map[string]any{
					"transform":  "template",
					"template":   "ok",
					"output_var": "result",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("cache node: unexpected error: %v", err)
	}
	cn, ok := cacheNode.(*nodes.CacheNode)
	if !ok {
		t.Fatalf("expected *nodes.CacheNode, got %T", cacheNode)
	}
	cacheCfg := cn.Config()
	if cacheCfg.WrappedNode == nil {
		t.Fatal("cache config WrappedNode should be set")
	}
	if cacheCfg.OutputVar != "cache_meta" {
		t.Fatalf("cache output var = %q, want %q", cacheCfg.OutputVar, "cache_meta")
	}
	if cacheCfg.TTL != 15*time.Second {
		t.Fatalf("cache TTL = %s, want 15s", cacheCfg.TTL)
	}
}

func TestNewLiveNodeFactory_MapAndCacheBindingErrors(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	_, err := nodeFactory(graph.NodeDef{ID: "m1", Type: "map"})
	if err == nil || !strings.Contains(err.Error(), "missing binding config") {
		t.Fatalf("map missing binding error = %v, want missing binding config", err)
	}

	_, err = nodeFactory(graph.NodeDef{
		ID:   "m2",
		Type: "map",
		Config: map[string]any{
			"mapper_binding": "transform",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "config.mapper_binding must be an object") {
		t.Fatalf("map malformed binding error = %v", err)
	}

	_, err = nodeFactory(graph.NodeDef{
		ID:   "c1",
		Type: "cache",
		Config: map[string]any{
			"wrapped_binding": map[string]any{},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "config.wrapped_binding.type is required") {
		t.Fatalf("cache missing type error = %v", err)
	}
}

func TestNewLiveNodeFactory_RuleRouterNode(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	nd := graph.NodeDef{
		ID:   "router",
		Type: "rule_router",
		Config: map[string]any{
			"default_target": "fallback",
			"decision_key":   "router_decision",
			"allow_multiple": true,
			"rules": []any{
				map[string]any{
					"target": "approve",
					"reason": "high score",
					"conditions": []any{
						map[string]any{
							"var_path": "input.score",
							"op":       "gt",
							"value":    float64(0.7),
							"values":   []any{float64(0.7), float64(0.8)},
						},
						"invalid-condition",
					},
				},
				"invalid-rule",
			},
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rr, ok := node.(*nodes.RuleRouter)
	if !ok {
		t.Fatalf("expected *nodes.RuleRouter, got %T", node)
	}
	cfg := rr.Config()
	if cfg.DefaultTarget != "fallback" {
		t.Fatalf("DefaultTarget = %q, want %q", cfg.DefaultTarget, "fallback")
	}
	if cfg.DecisionKey != "router_decision" {
		t.Fatalf("DecisionKey = %q, want %q", cfg.DecisionKey, "router_decision")
	}
	if !cfg.AllowMultiple {
		t.Fatal("AllowMultiple = false, want true")
	}
	if len(cfg.Rules) != 1 || len(cfg.Rules[0].Conditions) != 1 {
		t.Fatalf("expected 1 parsed rule with 1 condition, got %d rules and %d conditions", len(cfg.Rules), len(cfg.Rules[0].Conditions))
	}
}

func TestNewLiveNodeFactory_FilterNode(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	nd := graph.NodeDef{
		ID:   "filter",
		Type: "filter",
		Config: map[string]any{
			"target":     "var",
			"input_var":  "items",
			"output_var": "filtered",
			"stats_var":  "stats",
			"filters": []any{
				map[string]any{
					"type":          "top_n",
					"score_field":   "meta.score",
					"order":         "desc",
					"field":         "kind",
					"value":         "report",
					"pattern":       "rep.*",
					"keep":          "highest_score",
					"n":             float64(3),
					"min":           float64(0.2),
					"max":           float64(0.9),
					"include_types": []any{"chunk", "note"},
					"exclude_types": []any{"metadata"},
				},
				"invalid-filter",
			},
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fn, ok := node.(*nodes.FilterNode)
	if !ok {
		t.Fatalf("expected *nodes.FilterNode, got %T", node)
	}
	cfg := fn.Config()
	if cfg.Target != nodes.FilterTargetVar {
		t.Fatalf("Target = %q, want %q", cfg.Target, nodes.FilterTargetVar)
	}
	if len(cfg.Filters) != 1 {
		t.Fatalf("len(Filters) = %d, want 1", len(cfg.Filters))
	}
	op := cfg.Filters[0]
	if op.N != 3 {
		t.Fatalf("N = %d, want 3", op.N)
	}
	if op.Min == nil || op.Max == nil {
		t.Fatal("expected min/max to be set")
	}
	if len(op.IncludeTypes) != 2 || len(op.ExcludeTypes) != 1 {
		t.Fatalf("unexpected type filters: include=%v exclude=%v", op.IncludeTypes, op.ExcludeTypes)
	}
}

func TestNewLiveNodeFactory_TransformNode(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	nd := graph.NodeDef{
		ID:   "transform",
		Type: "transform",
		Config: map[string]any{
			"transform":      "map",
			"input_var":      "items",
			"output_var":     "mapped",
			"template":       "{{.input}}",
			"format":         "json",
			"separator":      "/",
			"merge_strategy": "deep",
			"input_vars":     []any{"a", "b"},
			"fields":         []any{"name", "meta.score"},
			"max_depth":      float64(3),
			"mapping": map[string]any{
				"old": "new",
				"bad": float64(1),
			},
			"item_transform": map[string]any{
				"transform":  "pick",
				"input_var":  "item",
				"output_var": "picked",
				"fields":     []any{"name"},
			},
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tn, ok := node.(*nodes.TransformNode)
	if !ok {
		t.Fatalf("expected *nodes.TransformNode, got %T", node)
	}
	cfg := tn.Config()
	if cfg.Transform != nodes.TransformMap {
		t.Fatalf("Transform = %q, want %q", cfg.Transform, nodes.TransformMap)
	}
	if cfg.MaxDepth != 3 {
		t.Fatalf("MaxDepth = %d, want 3", cfg.MaxDepth)
	}
	if got := cfg.Mapping["old"]; got != "new" {
		t.Fatalf("Mapping[old] = %q, want %q", got, "new")
	}
	if _, ok := cfg.Mapping["bad"]; ok {
		t.Fatal("expected non-string mapping values to be ignored")
	}
	if cfg.ItemTransform == nil || cfg.ItemTransform.Transform != nodes.TransformPick {
		t.Fatalf("unexpected item transform: %#v", cfg.ItemTransform)
	}
}

func TestNewLiveNodeFactory_GateNode(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	nd := graph.NodeDef{
		ID:   "gate",
		Type: "gate",
		Config: map[string]any{
			"condition_var":    "is_allowed",
			"on_fail":          "redirect",
			"fail_message":     "not allowed",
			"redirect_node_id": "fallback",
			"result_var":       "gate_result",
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gn, ok := node.(*nodes.GateNode)
	if !ok {
		t.Fatalf("expected *nodes.GateNode, got %T", node)
	}
	cfg := gn.Config()
	if cfg.ConditionVar != "is_allowed" || cfg.OnFail != nodes.GateActionRedirect {
		t.Fatalf("unexpected gate config: %#v", cfg)
	}
}

func TestNewLiveNodeFactory_GuardianNode(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	nd := graph.NodeDef{
		ID:   "guardian",
		Type: "guardian",
		Config: map[string]any{
			"input_var":             "payload",
			"on_fail":               "redirect",
			"fail_message":          "validation failed",
			"redirect_node_id":      "fallback",
			"result_var":            "guardian_result",
			"stop_on_first_failure": true,
			"checks": []any{
				map[string]any{
					"name":            "required",
					"type":            "required",
					"field":           "input",
					"pattern":         ".*",
					"expected_type":   "object",
					"schema":          map[string]any{"type": "object"},
					"message":         "missing fields",
					"required_fields": []any{"a", "b"},
					"max_length":      float64(12),
					"min_length":      float64(1),
					"min":             float64(0.1),
					"max":             float64(0.9),
					"allowed_values":  []any{"x", "y"},
					"block_pii":       true,
					"pii_types":       []any{"email", "phone"},
				},
				"invalid-check",
			},
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gn, ok := node.(*nodes.GuardianNode)
	if !ok {
		t.Fatalf("expected *nodes.GuardianNode, got %T", node)
	}
	cfg := gn.Config()
	if !cfg.StopOnFirstFailure {
		t.Fatal("StopOnFirstFailure = false, want true")
	}
	if len(cfg.Checks) != 1 {
		t.Fatalf("len(Checks) = %d, want 1", len(cfg.Checks))
	}
	check := cfg.Checks[0]
	if len(check.RequiredFields) != 2 || check.Min == nil || check.Max == nil {
		t.Fatalf("unexpected parsed guardian check: %#v", check)
	}
	if len(check.PIITypes) != 2 {
		t.Fatalf("len(PIITypes) = %d, want 2", len(check.PIITypes))
	}
}

func TestNewLiveNodeFactory_WebhookCallNode(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	nd := graph.NodeDef{
		ID:   "webhook_call",
		Type: "webhook_call",
		Config: map[string]any{
			"url":               "https://example.com/webhook",
			"method":            "POST",
			"headers":           map[string]any{"X-Test": "1"},
			"template":          "{{ json .vars }}",
			"error_policy":      "record",
			"result_var":        "webhook_result",
			"input_vars":        []any{"summary", "score"},
			"include_artifacts": true,
			"include_messages":  true,
			"include_trace":     true,
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	webhookNode, ok := node.(*nodes.WebhookCallNode)
	if !ok {
		t.Fatalf("expected *nodes.WebhookCallNode, got %T", node)
	}
	cfg := webhookNode.Config()
	if cfg.ErrorPolicy != nodes.WebhookCallErrorPolicyRecord {
		t.Fatalf("ErrorPolicy = %q, want %q", cfg.ErrorPolicy, nodes.WebhookCallErrorPolicyRecord)
	}
	if cfg.URL != "https://example.com/webhook" {
		t.Fatalf("URL = %q, want https://example.com/webhook", cfg.URL)
	}
	if len(cfg.InputVars) != 2 {
		t.Fatalf("unexpected webhook_call config input_vars=%v", cfg.InputVars)
	}
}

func TestNewLiveNodeFactory_WebhookTriggerNode(t *testing.T) {
	factory, _ := newMockClientFactory()
	nodeFactory := NewLiveNodeFactory(ProviderMap{}, factory)

	nd := graph.NodeDef{
		ID:   "webhook_trigger",
		Type: "webhook_trigger",
		Config: map[string]any{
			"methods": []any{"POST", "PUT"},
			"auth": map[string]any{
				"type":   "header_token",
				"header": "X-Test-Webhook",
				"token":  "secret",
			},
			"request_var":  "request",
			"body_var":     "body",
			"headers_var":  "headers",
			"query_var":    "query",
			"metadata_var": "meta",
			"timeout":      "30s",
		},
	}

	node, err := nodeFactory(nd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	triggerNode, ok := node.(*nodes.WebhookTriggerNode)
	if !ok {
		t.Fatalf("expected *nodes.WebhookTriggerNode, got %T", node)
	}
	cfg := triggerNode.Config()
	if len(cfg.Methods) != 2 || cfg.Methods[0] != "POST" || cfg.Methods[1] != "PUT" {
		t.Fatalf("Methods = %v, want [POST PUT]", cfg.Methods)
	}
	if cfg.Auth.Type != nodes.WebhookAuthTypeHeaderToken {
		t.Fatalf("Auth.Type = %q, want header_token", cfg.Auth.Type)
	}
	if cfg.RequestVar != "request" || cfg.MetadataVar != "meta" {
		t.Fatalf("unexpected trigger config: request_var=%q metadata_var=%q", cfg.RequestVar, cfg.MetadataVar)
	}
}

func TestNewLiveNodeFactory_BuiltinTypeConformance(t *testing.T) {
	providers := ProviderMap{
		"anthropic": {APIKey: "sk-test"},
	}
	factory, _ := newMockClientFactory()
	toolRegistry := core.NewToolRegistry()
	toolRegistry.Register(&mockTool{name: "web_search"})
	handler := &mockHumanHandler{}

	nodeFactory := NewLiveNodeFactory(
		providers,
		factory,
		WithToolRegistry(toolRegistry),
		WithHumanHandler(handler),
	)

	type caseDef struct {
		node            graph.NodeDef
		expectErrSubstr string
	}

	cases := map[string]caseDef{
		"llm_prompt": {
			node: graph.NodeDef{
				ID:   "n-llm-prompt",
				Type: "llm_prompt",
				Config: map[string]any{
					"provider": "anthropic",
					"model":    "claude-sonnet",
				},
			},
		},
		"llm_router": {
			node: graph.NodeDef{
				ID:   "n-llm-router",
				Type: "llm_router",
				Config: map[string]any{
					"provider": "anthropic",
					"model":    "claude-sonnet",
				},
			},
		},
		"rule_router": {
			node: graph.NodeDef{
				ID:   "n-rule-router",
				Type: "rule_router",
			},
		},
		"filter": {
			node: graph.NodeDef{
				ID:   "n-filter",
				Type: "filter",
			},
		},
		"transform": {
			node: graph.NodeDef{
				ID:   "n-transform",
				Type: "transform",
			},
		},
		"merge": {
			node: graph.NodeDef{
				ID:   "n-merge",
				Type: "merge",
			},
		},
		"tool": {
			node: graph.NodeDef{
				ID:   "n-tool",
				Type: "tool",
				Config: map[string]any{
					"tool_name": "web_search",
				},
			},
		},
		"gate": {
			node: graph.NodeDef{
				ID:   "n-gate",
				Type: "gate",
			},
		},
		"guardian": {
			node: graph.NodeDef{
				ID:   "n-guardian",
				Type: "guardian",
			},
		},
		"human": {
			node: graph.NodeDef{
				ID:   "n-human",
				Type: "human",
				Config: map[string]any{
					"mode": "approval",
				},
			},
		},
		"map": {
			node: graph.NodeDef{
				ID:   "n-map",
				Type: "map",
				Config: map[string]any{
					"input_var": "items",
					"mapper_binding": map[string]any{
						"type": "transform",
						"config": map[string]any{
							"transform":  "template",
							"template":   "{{.item}}",
							"output_var": "mapped",
						},
					},
				},
			},
		},
		"cache": {
			node: graph.NodeDef{
				ID:   "n-cache",
				Type: "cache",
				Config: map[string]any{
					"wrapped_binding": map[string]any{
						"type": "transform",
						"config": map[string]any{
							"transform":  "template",
							"template":   "cached",
							"output_var": "cache_out",
						},
					},
				},
			},
		},
		"webhook_trigger": {
			node: graph.NodeDef{
				ID:   "n-webhook-trigger",
				Type: "webhook_trigger",
				Config: map[string]any{
					"methods": []any{"POST"},
				},
			},
		},
		"webhook_call": {
			node: graph.NodeDef{
				ID:   "n-webhook-call",
				Type: "webhook_call",
				Config: map[string]any{
					"url": "https://example.com/webhook",
				},
			},
		},
		"noop": {
			node: graph.NodeDef{
				ID:   "n-noop",
				Type: "noop",
			},
		},
		"func": {
			node: graph.NodeDef{
				ID:   "n-func",
				Type: "func",
			},
		},
		"conditional": {
			node: graph.NodeDef{
				ID:   "n-conditional",
				Type: "conditional",
				Config: map[string]any{
					"default": "_skip",
					"conditions": []any{
						map[string]any{
							"name":       "ok",
							"expression": "input.score > 0.5",
						},
					},
				},
			},
		},
	}

	expected := make(map[string]struct{}, len(cases))
	for nodeType := range cases {
		expected[nodeType] = struct{}{}
	}

	seen := make(map[string]struct{}, len(cases))
	for _, def := range registry.Global().All() {
		// Ignore dynamic tool action entries, conformance here is for built-ins.
		if strings.Contains(def.Type, ".") {
			continue
		}
		c, ok := cases[def.Type]
		if !ok {
			t.Fatalf("missing conformance case for built-in type %q", def.Type)
		}
		seen[def.Type] = struct{}{}

		node, err := nodeFactory(c.node)
		if c.expectErrSubstr != "" {
			if err == nil {
				t.Fatalf("type %q: expected error containing %q, got nil", def.Type, c.expectErrSubstr)
			}
			if !strings.Contains(err.Error(), c.expectErrSubstr) {
				t.Fatalf("type %q: error = %q, want substring %q", def.Type, err.Error(), c.expectErrSubstr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("type %q: unexpected hydration error: %v", def.Type, err)
		}
		if node == nil {
			t.Fatalf("type %q: expected non-nil node", def.Type)
		}
	}

	for nodeType := range expected {
		if _, ok := seen[nodeType]; !ok {
			t.Fatalf("conformance case for %q is not registered in global built-ins", nodeType)
		}
	}
}

func TestConfigHelpers_EdgeCases(t *testing.T) {
	if _, ok := configMapInt(map[string]any{"n": math.NaN()}, "n"); ok {
		t.Fatal("expected NaN to fail int conversion")
	}
	if _, ok := configMapInt(map[string]any{"n": math.Inf(1)}, "n"); ok {
		t.Fatal("expected Inf to fail int conversion")
	}
	if _, ok := configMapStringSlice(map[string]any{"v": "not-a-slice"}, "v"); ok {
		t.Fatal("expected non-slice string list parse to fail")
	}
	if got := configMapAnyMap(map[string]any{"m": map[string]any{"k": "v"}}, "m"); got["k"] != "v" {
		t.Fatalf("configMapAnyMap returned %v, want key k=v", got)
	}
}
