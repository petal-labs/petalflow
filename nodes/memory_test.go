package nodes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/memory"
	"github.com/petal-labs/petalflow/runtime"
)

var testScope = memory.Scope{TenantID: "acme", Namespace: "support", SessionID: "sess-1"}

// failingProvider simulates an outage or a programming error.
type failingProvider struct {
	err error
}

func (f failingProvider) Name() string { return "failing" }
func (f failingProvider) Recall(context.Context, memory.RecallRequest) (memory.RecallResult, error) {
	return memory.RecallResult{}, f.err
}
func (f failingProvider) Remember(context.Context, memory.RememberRequest) (memory.RememberResult, error) {
	return memory.RememberResult{}, f.err
}
func (f failingProvider) Search(context.Context, memory.SearchRequest) (memory.SearchResult, error) {
	return memory.SearchResult{}, f.err
}

// eventRecorder captures emitted events and returns a context carrying the
// scope and the emitter, as the runtime would.
type eventRecorder struct {
	events []runtime.Event
}

func (r *eventRecorder) ctx(scope memory.Scope) context.Context {
	ctx := runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) { r.events = append(r.events, e) })
	if !scope.IsZero() {
		ctx = memory.ContextWithScope(ctx, scope)
	}
	return ctx
}

func (r *eventRecorder) find(kind runtime.EventKind) (runtime.Event, bool) {
	for _, e := range r.events {
		if e.Kind == kind {
			return e, true
		}
	}
	return runtime.Event{}, false
}

func seededProvider(t *testing.T) *memory.InMemoryProvider {
	t.Helper()
	p := memory.NewInMemoryProvider()
	ctx := context.Background()
	if _, err := p.Remember(ctx, memory.RememberRequest{Scope: testScope, Messages: []core.Message{
		{Role: "user", Content: "My order is late"},
		{Role: "assistant", Content: "Sorry to hear that, what is the order number?"},
		{Role: "user", Content: "It is 42"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := p.AddDocument(testScope, "kb", core.Artifact{ID: "refund", Text: "Refunds for late orders are processed within five business days", Meta: map[string]any{"title": "Refund policy"}}); err != nil {
		t.Fatal(err)
	}
	if err := p.AddDocument(testScope, "kb", core.Artifact{ID: "picnic", Text: "The company picnic is in July"}); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMemoryRecallNode_Defaults(t *testing.T) {
	node := NewMemoryRecallNode("recall", MemoryRecallNodeConfig{})
	if node.Kind() != core.NodeKindMemory {
		t.Fatalf("kind = %q", node.Kind())
	}
	cfg := node.Config()
	if cfg.OutputVar != "recall_context" || cfg.Timeout != memory.DefaultOperationTimeout || cfg.OnUnavailable != memory.FailurePolicyFail {
		t.Fatalf("defaults = %+v", cfg)
	}
	if _, err := node.Run(context.Background(), core.NewEnvelope()); err == nil {
		t.Fatal("node without providers should fail")
	}
}

func TestMemoryRecallNode_RequiresScope(t *testing.T) {
	node := NewMemoryRecallNode("recall", MemoryRecallNodeConfig{Memory: memory.NewInMemoryProvider()})
	_, err := node.Run(context.Background(), core.NewEnvelope())
	if !errors.Is(err, memory.ErrScopeRequired) {
		t.Fatalf("err = %v, want ErrScopeRequired", err)
	}

	// A knowledge-only node needs a namespace but no session.
	rec := &eventRecorder{}
	kOnly := NewMemoryRecallNode("recall", MemoryRecallNodeConfig{Knowledge: memory.NewInMemoryProvider(), QueryVar: "q"})
	if _, err := kOnly.Run(rec.ctx(memory.Scope{Namespace: "docs"}), core.NewEnvelope().WithVar("q", "x")); err != nil {
		t.Fatalf("knowledge-only node with namespace scope: %v", err)
	}
	mOnly := NewMemoryRecallNode("recall", MemoryRecallNodeConfig{Memory: memory.NewInMemoryProvider()})
	if _, err := mOnly.Run(rec.ctx(memory.Scope{Namespace: "docs"}), core.NewEnvelope()); !errors.Is(err, memory.ErrInvalidScope) {
		t.Fatalf("memory node without session = %v, want ErrInvalidScope", err)
	}
}

func TestMemoryRecallNode_RecallsHistoryAndKnowledge(t *testing.T) {
	p := seededProvider(t)
	rec := &eventRecorder{}
	node := NewMemoryRecallNode("recall", MemoryRecallNodeConfig{
		Memory:         p,
		Knowledge:      p,
		QueryVar:       "question",
		TopK:           1,
		RecordMessages: true,
	})
	env := core.NewEnvelope().WithVar("question", "When will I get a refund for my late order?")
	env.Trace.RunID = "run-1"

	out, err := node.Run(rec.ctx(testScope), env)
	if err != nil {
		t.Fatal(err)
	}
	ctxText := out.GetVarString("recall_context")
	if !strings.Contains(ctxText, "[1] Refund policy") || !strings.Contains(ctxText, "five business days") {
		t.Fatalf("context = %q", ctxText)
	}
	if len(out.Messages) != 3 || out.Messages[0].Content != "My order is late" {
		t.Fatalf("messages = %+v", out.Messages)
	}
	for _, m := range out.Messages {
		if m.Meta["source"] != "memory" {
			t.Fatalf("recalled message should be tagged: %+v", m)
		}
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].ID != "refund" || out.Artifacts[0].Meta["retrieved_by"] != "recall" {
		t.Fatalf("artifacts = %+v", out.Artifacts)
	}
	if _, ok := out.Artifacts[0].Meta["score"].(float64); !ok {
		t.Fatal("artifact should carry its relevance score")
	}
	stats, ok := out.GetVar("recall_context_stats")
	if !ok || stats.(map[string]any)["history_included"] != 3 {
		t.Fatalf("stats = %v", stats)
	}
	if out.HasErrors() {
		t.Fatalf("unexpected errors: %+v", out.Errors)
	}

	e, ok := rec.find(runtime.EventMemoryRecall)
	if !ok {
		t.Fatal("memory.recall event not emitted")
	}
	if e.RunID != "run-1" || e.NodeID != "recall" || e.NodeKind != core.NodeKindMemory {
		t.Fatalf("event identity = %+v", e)
	}
	if e.Payload["status"] != "ok" || e.Payload["history_count"] != 3 || e.Payload["retrieved_count"] != 1 || e.Payload["provider"] != "inmemory" {
		t.Fatalf("payload = %v", e.Payload)
	}
	if e.Payload["namespace"] != "support" || e.Payload["session_id"] != "sess-1" {
		t.Fatalf("scope attributes missing: %v", e.Payload)
	}
	if _, has := e.Payload["query"]; has {
		t.Fatal("query content must not be recorded by default")
	}
	if _, has := e.Payload["tenant_id"]; has {
		t.Fatal("tenant_id must not be recorded in events")
	}
	if _, ok := rec.find(runtime.EventContextAssembled); !ok {
		t.Fatal("context.assembled event not emitted")
	}
	if _, ok := rec.find(runtime.EventContextCompacted); ok {
		t.Fatal("context.compacted should not be emitted when nothing was dropped")
	}
}

func TestMemoryRecallNode_RecordContentOptIn(t *testing.T) {
	p := seededProvider(t)
	rec := &eventRecorder{}
	node := NewMemoryRecallNode("recall", MemoryRecallNodeConfig{Knowledge: p, QueryVar: "q", RecordContent: true})
	if _, err := node.Run(rec.ctx(testScope), core.NewEnvelope().WithVar("q", "refund")); err != nil {
		t.Fatal(err)
	}
	e, _ := rec.find(runtime.EventMemoryRecall)
	if e.Payload["query"] != "refund" {
		t.Fatalf("query should be recorded when opted in: %v", e.Payload)
	}
}

func TestMemoryRecallNode_BudgetCompactsHistory(t *testing.T) {
	p := seededProvider(t)
	rec := &eventRecorder{}
	node := NewMemoryRecallNode("recall", MemoryRecallNodeConfig{
		Memory:         p,
		Budget:         memory.Budget{MaxMessages: 1},
		RecordMessages: true,
	})
	out, err := node.Run(rec.ctx(testScope), core.NewEnvelope())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Messages) != 1 || out.Messages[0].Content != "It is 42" {
		t.Fatalf("messages = %+v", out.Messages)
	}
	e, ok := rec.find(runtime.EventContextCompacted)
	if !ok || e.Payload["history_dropped"] != 2 {
		t.Fatalf("context.compacted = %+v ok=%v", e.Payload, ok)
	}
}

func TestMemoryRecallNode_NamespaceOverrides(t *testing.T) {
	p := memory.NewInMemoryProvider()
	ctx := context.Background()
	chat := memory.Scope{TenantID: "acme", Namespace: "chat", SessionID: "s"}
	if _, err := p.Remember(ctx, memory.RememberRequest{Scope: chat, Messages: []core.Message{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	if err := p.AddDocument(memory.Scope{TenantID: "acme", Namespace: "shared-kb"}, "", core.Artifact{ID: "d", Text: "shared knowledge"}); err != nil {
		t.Fatal(err)
	}
	rec := &eventRecorder{}
	node := NewMemoryRecallNode("recall", MemoryRecallNodeConfig{
		Memory:             p,
		Knowledge:          p,
		Namespace:          "chat",
		KnowledgeNamespace: "shared-kb",
		QueryVar:           "q",
		RecordMessages:     true,
	})
	runScope := memory.Scope{TenantID: "acme", Namespace: "default", SessionID: "s"}
	out, err := node.Run(rec.ctx(runScope), core.NewEnvelope().WithVar("q", "shared knowledge"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Messages) != 1 || len(out.Artifacts) != 1 {
		t.Fatalf("messages=%d artifacts=%d", len(out.Messages), len(out.Artifacts))
	}
	e, _ := rec.find(runtime.EventMemoryRecall)
	if e.Payload["namespace"] != "chat" {
		t.Fatalf("event namespace = %v", e.Payload["namespace"])
	}
}

func TestMemoryRecallNode_SkipsSearchWithoutQuery(t *testing.T) {
	p := seededProvider(t)
	rec := &eventRecorder{}
	node := NewMemoryRecallNode("recall", MemoryRecallNodeConfig{Knowledge: p, QueryVar: "missing"})
	out, err := node.Run(rec.ctx(testScope), core.NewEnvelope())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Artifacts) != 0 || out.GetVarString("recall_context") != "" {
		t.Fatalf("expected no retrieval: %+v", out.Artifacts)
	}
}

func TestMemoryRecallNode_UnavailableFailsByDefault(t *testing.T) {
	rec := &eventRecorder{}
	outage := failingProvider{err: memory.Unavailable(errors.New("connection refused"))}
	node := NewMemoryRecallNode("recall", MemoryRecallNodeConfig{Memory: outage})
	_, err := node.Run(rec.ctx(testScope), core.NewEnvelope())
	if !memory.IsUnavailable(err) {
		t.Fatalf("err = %v, want unavailable", err)
	}
	e, ok := rec.find(runtime.EventMemoryRecall)
	if !ok || e.Payload["status"] != "error" || e.Payload["error_class"] != "unavailable" {
		t.Fatalf("event = %+v ok=%v", e.Payload, ok)
	}
}

func TestMemoryRecallNode_UnavailableContinuePolicy(t *testing.T) {
	rec := &eventRecorder{}
	outage := failingProvider{err: memory.Unavailable(errors.New("connection refused"))}
	node := NewMemoryRecallNode("recall", MemoryRecallNodeConfig{
		Memory:        outage,
		Knowledge:     outage,
		QueryVar:      "q",
		OnUnavailable: memory.FailurePolicyContinue,
	})
	out, err := node.Run(rec.ctx(testScope), core.NewEnvelope().WithVar("q", "x"))
	if err != nil {
		t.Fatalf("continue policy should not fail: %v", err)
	}
	if got := out.GetVarString("recall_context"); got != "" {
		t.Fatalf("context should be empty when unavailable: %q", got)
	}
	if len(out.Errors) != 1 || out.Errors[0].NodeID != "recall" || out.Errors[0].Details["status"] != "unavailable" {
		t.Fatalf("degradation must be recorded: %+v", out.Errors)
	}
	if !memory.IsUnavailable(out.Errors[0].Cause) {
		t.Fatal("recorded error should keep its cause")
	}
	e, _ := rec.find(runtime.EventMemoryRecall)
	if e.Payload["status"] != "unavailable" || e.Payload["error_class"] != "unavailable" {
		t.Fatalf("event = %v", e.Payload)
	}
}

func TestMemoryRecallNode_ContinuePolicyDoesNotMaskProgrammingErrors(t *testing.T) {
	rec := &eventRecorder{}
	bad := failingProvider{err: errors.New("bad request")}
	node := NewMemoryRecallNode("recall", MemoryRecallNodeConfig{Memory: bad, OnUnavailable: memory.FailurePolicyContinue})
	_, err := node.Run(rec.ctx(testScope), core.NewEnvelope())
	if err == nil {
		t.Fatal("non-outage errors must fail even with continue policy")
	}
}

func TestMemoryStoreNode_Defaults(t *testing.T) {
	node := NewMemoryStoreNode("store", MemoryStoreNodeConfig{})
	cfg := node.Config()
	if cfg.OutputVar != "store_stored" || cfg.OnUnavailable != memory.FailurePolicyFail || cfg.Timeout != memory.DefaultOperationTimeout {
		t.Fatalf("defaults = %+v", cfg)
	}
	if _, err := node.Run(context.Background(), core.NewEnvelope()); err == nil {
		t.Fatal("node without provider should fail")
	}
	withProvider := NewMemoryStoreNode("store", MemoryStoreNodeConfig{Memory: memory.NewInMemoryProvider()})
	if _, err := withProvider.Run(context.Background(), core.NewEnvelope()); !errors.Is(err, memory.ErrScopeRequired) {
		t.Fatalf("err = %v, want ErrScopeRequired", err)
	}
}

func TestMemoryStoreNode_StoresEntriesAndNewMessages(t *testing.T) {
	p := memory.NewInMemoryProvider()
	rec := &eventRecorder{}
	node := NewMemoryStoreNode("store", MemoryStoreNodeConfig{
		Memory: p,
		Entries: []MemoryStoreEntry{
			{Role: "user", Var: "question"},
			{Role: "assistant", Var: "answer", Name: "bot"},
			{Role: "user", Var: "missing"},
		},
		IncludeNewMessages: true,
		Metadata:           map[string]string{"workflow": "support"},
	})
	env := core.NewEnvelope().WithVar("question", "hello?").WithVar("answer", "hi!")
	env.AppendMessage(core.Message{Role: "user", Content: "recalled", Meta: map[string]any{"source": "memory"}})
	env.AppendMessage(core.Message{Role: "assistant", Content: "fresh"})

	out, err := node.Run(rec.ctx(testScope), env)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := out.GetVar("store_stored"); v != 3 {
		t.Fatalf("stored = %v, want 3", v)
	}
	res, err := p.Recall(context.Background(), memory.RecallRequest{Scope: testScope})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 3 || res.Messages[0].Content != "hello?" || res.Messages[1].Name != "bot" || res.Messages[2].Content != "fresh" {
		t.Fatalf("persisted = %+v", res.Messages)
	}
	e, ok := rec.find(runtime.EventMemoryStore)
	if !ok || e.Payload["status"] != "ok" || e.Payload["message_count"] != 3 || e.Payload["stored_count"] != 3 {
		t.Fatalf("event = %+v ok=%v", e.Payload, ok)
	}
	for _, key := range []string{"content", "question", "answer"} {
		if _, has := e.Payload[key]; has {
			t.Fatalf("event must not contain content key %q", key)
		}
	}
}

func TestMemoryStoreNode_NothingToStore(t *testing.T) {
	p := memory.NewInMemoryProvider()
	rec := &eventRecorder{}
	node := NewMemoryStoreNode("store", MemoryStoreNodeConfig{Memory: p, Entries: []MemoryStoreEntry{{Var: "nope"}}})
	out, err := node.Run(rec.ctx(testScope), core.NewEnvelope())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := out.GetVar("store_stored"); v != 0 {
		t.Fatalf("stored = %v", v)
	}
}

func TestMemoryStoreNode_UnavailablePolicies(t *testing.T) {
	outage := failingProvider{err: memory.Unavailable(errors.New("down"))}
	env := func() *core.Envelope { return core.NewEnvelope().WithVar("q", "hello") }
	entries := []MemoryStoreEntry{{Role: "user", Var: "q"}}

	rec := &eventRecorder{}
	strict := NewMemoryStoreNode("store", MemoryStoreNodeConfig{Memory: outage, Entries: entries})
	if _, err := strict.Run(rec.ctx(testScope), env()); !memory.IsUnavailable(err) {
		t.Fatalf("default policy err = %v", err)
	}

	rec = &eventRecorder{}
	lenient := NewMemoryStoreNode("store", MemoryStoreNodeConfig{Memory: outage, Entries: entries, OnUnavailable: memory.FailurePolicyContinue})
	out, err := lenient.Run(rec.ctx(testScope), env())
	if err != nil {
		t.Fatalf("continue policy err = %v", err)
	}
	if len(out.Errors) != 1 || out.Errors[0].Details["message_count"] != 1 {
		t.Fatalf("errors = %+v", out.Errors)
	}
	e, _ := rec.find(runtime.EventMemoryStore)
	if e.Payload["status"] != "unavailable" {
		t.Fatalf("event = %v", e.Payload)
	}

	bad := NewMemoryStoreNode("store", MemoryStoreNodeConfig{Memory: failingProvider{err: errors.New("schema")}, Entries: entries, OnUnavailable: memory.FailurePolicyContinue})
	if _, err := bad.Run(rec.ctx(testScope), env()); err == nil {
		t.Fatal("non-outage error must fail")
	}
}

func TestLLMNode_IncludeMessagesAssemblesContext(t *testing.T) {
	client := &mockLLMClient{response: core.LLMResponse{Text: "answer"}}
	node := NewLLMNode("llm", client, LLMNodeConfig{
		System:          "sys",
		PromptTemplate:  "{{.question}}",
		IncludeMessages: true,
		ContextBudget:   &memory.Budget{MaxMessages: 2},
	})
	rec := &eventRecorder{}
	env := core.NewEnvelope().WithVar("question", "and now?")
	env.AppendMessage(core.Message{Role: "user", Content: "one"})
	env.AppendMessage(core.Message{Role: "assistant", Content: "two", Name: "bot"})
	env.AppendMessage(core.Message{Role: "user", Content: "three"})

	if _, err := node.Run(rec.ctx(memory.Scope{}), env); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d", len(client.requests))
	}
	req := client.requests[0]
	if req.InputText != "and now?" || req.System != "sys" {
		t.Fatalf("request = %+v", req)
	}
	if len(req.Messages) != 2 || req.Messages[0].Content != "two" || req.Messages[0].Name != "bot" || req.Messages[1].Content != "three" {
		t.Fatalf("history = %+v", req.Messages)
	}
	key, _ := req.Meta["prompt_cache_key"].(string)
	if len(key) != 16 {
		t.Fatalf("prompt_cache_key = %q", key)
	}
	e, ok := rec.find(runtime.EventContextAssembled)
	if !ok || e.NodeID != "llm" || e.Payload["history_included"] != 2 || e.Payload["history_dropped"] != 1 || e.Payload["prompt_cache_key"] != key {
		t.Fatalf("context.assembled = %+v ok=%v", e.Payload, ok)
	}
	if _, ok := rec.find(runtime.EventContextCompacted); !ok {
		t.Fatal("context.compacted expected")
	}
}

func TestLLMNode_IncludeMessagesBudgetExceeded(t *testing.T) {
	client := &mockLLMClient{response: core.LLMResponse{Text: "answer"}}
	node := NewLLMNode("llm", client, LLMNodeConfig{
		System:          strings.Repeat("x", 400),
		PromptTemplate:  "{{.q}}",
		IncludeMessages: true,
		ContextBudget:   &memory.Budget{MaxTokens: 10},
	})
	_, err := node.Run(context.Background(), core.NewEnvelope().WithVar("q", "hi"))
	if !errors.Is(err, memory.ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if len(client.requests) != 0 {
		t.Fatal("no request should be sent when the budget cannot be met")
	}
}

func TestLLMNode_WithoutIncludeMessagesSendsNoHistory(t *testing.T) {
	client := &mockLLMClient{response: core.LLMResponse{Text: "answer"}}
	node := NewLLMNode("llm", client, LLMNodeConfig{PromptTemplate: "{{.q}}"})
	env := core.NewEnvelope().WithVar("q", "hi")
	env.AppendMessage(core.Message{Role: "user", Content: "old"})
	if _, err := node.Run(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if len(client.requests[0].Messages) != 0 || client.requests[0].Meta != nil {
		t.Fatalf("request = %+v", client.requests[0])
	}
}

// End-to-end: recall -> llm -> store through the runtime with a scoped run.
func TestMemoryNodes_EndToEndThroughRuntime(t *testing.T) {
	p := seededProvider(t)
	client := &mockLLMClient{response: core.LLMResponse{Text: "Your refund arrives in five business days."}}

	g := graph.NewGraph("memory-e2e")
	for _, n := range []core.Node{
		NewMemoryRecallNode("recall", MemoryRecallNodeConfig{
			Memory: p, Knowledge: p, QueryVar: "question", RecordMessages: true, OutputVar: "context",
		}),
		NewLLMNode("answer", client, LLMNodeConfig{
			System:          "Use the context.",
			PromptTemplate:  "Context:\n{{.context}}\n\nQuestion: {{.question}}",
			OutputKey:       "answer",
			IncludeMessages: true,
		}),
		NewMemoryStoreNode("store", MemoryStoreNodeConfig{
			Memory:  p,
			Entries: []MemoryStoreEntry{{Role: "user", Var: "question"}, {Role: "assistant", Var: "answer"}},
		}),
	} {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddEdge("recall", "answer"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("answer", "store"); err != nil {
		t.Fatal(err)
	}
	if err := g.SetEntry("recall"); err != nil {
		t.Fatal(err)
	}

	var kinds []runtime.EventKind
	opts := runtime.DefaultRunOptions()
	opts.TenantID = "acme"
	opts.Scope = memory.Scope{Namespace: "support", SessionID: "sess-1"}
	opts.EventHandler = func(e runtime.Event) { kinds = append(kinds, e.Kind) }

	env := core.NewEnvelope().WithVar("question", "When is my refund for the late order?")
	out, err := runtime.NewRuntime().Run(context.Background(), g, env, opts)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetVarString("answer") == "" {
		t.Fatal("answer missing")
	}
	req := client.requests[0]
	if len(req.Messages) != 3 || !strings.Contains(req.InputText, "Refund policy") {
		t.Fatalf("model request did not include history and context: %+v", req)
	}
	res, err := p.Recall(context.Background(), memory.RecallRequest{Scope: memory.Scope{TenantID: "acme", Namespace: "support", SessionID: "sess-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 5 || res.Messages[4].Role != "assistant" {
		t.Fatalf("session should now hold 5 turns: %+v", res.Messages)
	}
	seen := map[runtime.EventKind]int{}
	for _, k := range kinds {
		seen[k]++
	}
	if seen[runtime.EventMemoryRecall] != 1 || seen[runtime.EventMemoryStore] != 1 || seen[runtime.EventContextAssembled] != 2 {
		t.Fatalf("event counts = %v", seen)
	}
}
