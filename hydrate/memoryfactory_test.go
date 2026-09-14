package hydrate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/memory"
	"github.com/petal-labs/petalflow/nodes"
)

type knowledgeOnly struct{}

func (knowledgeOnly) Search(context.Context, memory.SearchRequest) (memory.SearchResult, error) {
	return memory.SearchResult{}, nil
}

func TestBuildMemoryRecallNode_FullConfig(t *testing.T) {
	provider := memory.NewInMemoryProvider()
	counter := memory.HeuristicCounter{CharsPerToken: 3}
	compactor := memory.TruncateOldest{}
	factory := NewLiveNodeFactory(nil, nil,
		WithMemoryProvider(provider),
		WithTokenCounter(counter),
		WithCompactor(compactor),
	)
	node, err := factory(graph.NodeDef{
		ID:   "recall",
		Type: "memory_recall",
		Config: map[string]any{
			"namespace":           "chat",
			"knowledge_namespace": "kb",
			"query_var":           "question",
			"output_var":          "ctx",
			"history_limit":       float64(20),
			"roles":               []any{"user", "assistant"},
			"top_k":               float64(3),
			"collections":         []any{"faq"},
			"min_score":           0.25,
			"budget":              map[string]any{"max_tokens": float64(500), "max_messages": float64(10), "max_artifacts": float64(2)},
			"record_messages":     true,
			"record_content":      true,
			"on_unavailable":      "continue",
			"timeout":             "3s",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	recall, ok := node.(*nodes.MemoryRecallNode)
	if !ok {
		t.Fatalf("node type = %T", node)
	}
	cfg := recall.Config()
	if cfg.Memory != provider || cfg.Knowledge != memory.KnowledgeProvider(provider) {
		t.Fatal("provider should serve both memory and knowledge")
	}
	if cfg.Namespace != "chat" || cfg.KnowledgeNamespace != "kb" || cfg.QueryVar != "question" || cfg.OutputVar != "ctx" {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg.HistoryLimit != 20 || cfg.TopK != 3 || cfg.MinScore != 0.25 || len(cfg.Roles) != 2 || len(cfg.Collections) != 1 {
		t.Fatalf("limits = %+v", cfg)
	}
	if cfg.Budget != (memory.Budget{MaxTokens: 500, MaxMessages: 10, MaxArtifacts: 2}) {
		t.Fatalf("budget = %+v", cfg.Budget)
	}
	if !cfg.RecordMessages || !cfg.RecordContent || cfg.OnUnavailable != memory.FailurePolicyContinue || cfg.Timeout != 3*time.Second {
		t.Fatalf("flags = %+v", cfg)
	}
	if cfg.TokenCounter != memory.TokenCounter(counter) || cfg.Compactor != memory.Compactor(compactor) {
		t.Fatal("token counter and compactor should be wired from factory options")
	}
}

func TestBuildMemoryRecallNode_Errors(t *testing.T) {
	noProvider := NewLiveNodeFactory(nil, nil)
	if _, err := noProvider(graph.NodeDef{ID: "r", Type: "memory_recall"}); err == nil || !strings.Contains(err.Error(), "WithMemoryProvider") {
		t.Fatalf("err = %v", err)
	}
	if _, err := noProvider(graph.NodeDef{ID: "s", Type: "memory_store"}); err == nil || !strings.Contains(err.Error(), "WithMemoryProvider") {
		t.Fatalf("err = %v", err)
	}

	withProvider := NewLiveNodeFactory(nil, nil, WithMemoryProvider(memory.NewInMemoryProvider()))
	if _, err := withProvider(graph.NodeDef{ID: "r", Type: "memory_recall", Config: map[string]any{"on_unavailable": "retry"}}); err == nil || !strings.Contains(err.Error(), "invalid failure policy") {
		t.Fatalf("err = %v", err)
	}
	if _, err := withProvider(graph.NodeDef{ID: "r", Type: "memory_recall", Config: map[string]any{"use_memory": false, "use_knowledge": false}}); err == nil || !strings.Contains(err.Error(), "disables both") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildMemoryRecallNode_KnowledgeOnlyProvider(t *testing.T) {
	factory := NewLiveNodeFactory(nil, nil, WithKnowledgeProvider(knowledgeOnly{}))
	node, err := factory(graph.NodeDef{ID: "r", Type: "memory_recall", Config: map[string]any{"query_var": "q"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := node.(*nodes.MemoryRecallNode).Config()
	if cfg.Memory != nil || cfg.Knowledge == nil {
		t.Fatalf("config = %+v", cfg)
	}
	// Explicit knowledge provider wins over an in-memory provider's own search.
	both := NewLiveNodeFactory(nil, nil, WithMemoryProvider(memory.NewInMemoryProvider()), WithKnowledgeProvider(knowledgeOnly{}))
	node, err = both(graph.NodeDef{ID: "r", Type: "memory_recall", Config: map[string]any{"use_memory": false}})
	if err != nil {
		t.Fatal(err)
	}
	cfg = node.(*nodes.MemoryRecallNode).Config()
	if _, ok := cfg.Knowledge.(knowledgeOnly); !ok || cfg.Memory != nil {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestBuildMemoryStoreNode(t *testing.T) {
	factory := NewLiveNodeFactory(nil, nil, WithMemoryProvider(memory.NewInMemoryProvider()))
	node, err := factory(graph.NodeDef{
		ID:   "store",
		Type: "memory_store",
		Config: map[string]any{
			"namespace":            "chat",
			"output_var":           "n",
			"metadata":             map[string]any{"workflow": "support"},
			"include_new_messages": true,
			"on_unavailable":       "continue",
			"timeout":              float64(2),
			"entries": []any{
				map[string]any{"role": "user", "var": "question"},
				map[string]any{"role": "assistant", "var": "answer", "name": "bot"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := node.(*nodes.MemoryStoreNode).Config()
	if cfg.Namespace != "chat" || cfg.OutputVar != "n" || cfg.Metadata["workflow"] != "support" || !cfg.IncludeNewMessages {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg.OnUnavailable != memory.FailurePolicyContinue || cfg.Timeout != 2*time.Second {
		t.Fatalf("config = %+v", cfg)
	}
	if len(cfg.Entries) != 2 || cfg.Entries[1].Name != "bot" || cfg.Entries[0].Var != "question" {
		t.Fatalf("entries = %+v", cfg.Entries)
	}

	if _, err := factory(graph.NodeDef{ID: "s", Type: "memory_store"}); err == nil || !strings.Contains(err.Error(), "entries or include_new_messages") {
		t.Fatalf("err = %v", err)
	}
	if _, err := factory(graph.NodeDef{ID: "s", Type: "memory_store", Config: map[string]any{"entries": []any{"bad"}}}); err == nil || !strings.Contains(err.Error(), "must be an object") {
		t.Fatalf("err = %v", err)
	}
	if _, err := factory(graph.NodeDef{ID: "s", Type: "memory_store", Config: map[string]any{"entries": []any{map[string]any{"role": "user"}}}}); err == nil || !strings.Contains(err.Error(), "var is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildLLMNode_ContextOptions(t *testing.T) {
	factory, _ := newMockClientFactory()
	counter := memory.HeuristicCounter{CharsPerToken: 3}
	nodeFactory := NewLiveNodeFactory(ProviderMap{"anthropic": {APIKey: "k"}}, factory, WithTokenCounter(counter))
	node, err := nodeFactory(graph.NodeDef{
		ID:   "llm",
		Type: "llm_prompt",
		Config: map[string]any{
			"provider":         "anthropic",
			"include_messages": true,
			"record_messages":  true,
			"context_budget":   map[string]any{"max_tokens": float64(4000), "max_messages": float64(20)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := node.(*nodes.LLMNode).Config()
	if !cfg.IncludeMessages || !cfg.RecordMessages {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg.ContextBudget == nil || cfg.ContextBudget.MaxTokens != 4000 || cfg.ContextBudget.MaxMessages != 20 {
		t.Fatalf("budget = %+v", cfg.ContextBudget)
	}
	if cfg.TokenCounter != memory.TokenCounter(counter) {
		t.Fatal("token counter should be wired")
	}

	plain, err := nodeFactory(graph.NodeDef{ID: "llm2", Type: "llm_prompt", Config: map[string]any{"provider": "anthropic"}})
	if err != nil {
		t.Fatal(err)
	}
	if plain.(*nodes.LLMNode).Config().ContextBudget != nil {
		t.Fatal("no budget should stay nil")
	}
}
