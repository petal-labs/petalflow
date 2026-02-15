package hydrate

import (
	"context"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
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
