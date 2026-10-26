package nodes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/memory"
	"github.com/petal-labs/petalflow/runtime"
)

// stubProvider is a memory.Provider stub capturing calls.
type stubProvider struct {
	docs        []memory.Document
	retrieveErr error
	writeErr    error
	lastQuery   memory.RetrievalQuery
	lastWrite   memory.WriteRequest
	retrieveOps int
	writeOps    int
}

func (p *stubProvider) Retrieve(ctx context.Context, query memory.RetrievalQuery) ([]memory.Document, error) {
	p.retrieveOps++
	p.lastQuery = query
	if p.retrieveErr != nil {
		return nil, p.retrieveErr
	}
	return p.docs, nil
}

func (p *stubProvider) Write(ctx context.Context, req memory.WriteRequest) error {
	p.writeOps++
	p.lastWrite = req
	return p.writeErr
}

func unavailableErr() error {
	return fmt.Errorf("%w: backend down", memory.ErrUnavailable)
}

func newTestEnvelope() *core.Envelope {
	return core.NewEnvelope().
		WithInput("user question about deployments").
		WithTrace(core.TraceInfo{RunID: "run-1"})
}

func collectEvents(t *testing.T, run func(emit runtime.EventEmitter)) []runtime.Event {
	t.Helper()
	var events []runtime.Event
	run(func(e runtime.Event) { events = append(events, e) })
	return events
}

func TestMemoryNodeRetrieve(t *testing.T) {
	provider := &stubProvider{docs: []memory.Document{
		{ID: "d1", Text: "deploys happen on tuesdays", Source: "kb-ops", Score: 0.9},
		{ID: "d2", Text: "rollbacks use the pipeline", Score: 0.5},
	}}
	node := NewMemoryNode("mem", MemoryNodeConfig{
		Provider:  provider,
		Namespace: "ops",
		SessionID: "sess-1",
		Budget:    memory.ContextBudget{MaxDocuments: 5, MaxTokens: 1000},
	})

	var events []runtime.Event
	ctx := runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) { events = append(events, e) })
	env := newTestEnvelope()

	result, err := node.Run(ctx, env)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Assembled context stored on the output var.
	contextText, _ := result.GetVar("mem_memory")
	if s, ok := contextText.(string); !ok || !strings.Contains(s, "deploys happen on tuesdays") {
		t.Errorf("output var = %#v, want rendered context", contextText)
	}

	// Stats var carries provenance and compaction.
	statsVar, _ := result.GetVar("mem_memory_stats")
	stats, ok := statsVar.(MemoryResult)
	if !ok {
		t.Fatalf("stats var = %#v, want MemoryResult", statsVar)
	}
	if stats.Namespace != "ops" || stats.SessionID != "sess-1" {
		t.Errorf("stats scope = %+v", stats)
	}
	if stats.DocumentCount != 2 {
		t.Errorf("DocumentCount = %d, want 2", stats.DocumentCount)
	}

	// Documents injected as artifacts.
	chunks := result.GetArtifactsByType("chunk")
	if len(chunks) != 2 {
		t.Errorf("artifacts = %d, want 2", len(chunks))
	}

	// Provider received the resolved scope and query.
	if provider.lastQuery.Scope.Namespace != "ops" || provider.lastQuery.Scope.SessionID != "sess-1" {
		t.Errorf("provider scope = %+v", provider.lastQuery.Scope)
	}
	if provider.lastQuery.Scope.RunID == "" {
		t.Error("provider scope should carry the run ID from the envelope trace")
	}
	if provider.lastQuery.Limit != 5 || provider.lastQuery.MaxTokens != 1000 {
		t.Errorf("provider budget = %+v", provider.lastQuery)
	}

	// Events: memory.read and context.assembled, content-free by default.
	var sawRead, sawAssembled bool
	for _, e := range events {
		switch e.Kind {
		case runtime.EventMemoryRead:
			sawRead = true
			if _, hasContent := e.Payload["documents"]; hasContent {
				t.Error("memory.read should not include document content by default")
			}
			if e.Payload["namespace"] != "ops" || e.Payload["session_id"] != "sess-1" {
				t.Errorf("memory.read scope payload = %+v", e.Payload)
			}
		case runtime.EventContextAssembled:
			sawAssembled = true
			if e.Payload["documents_in"] != 2 || e.Payload["documents_out"] != 2 {
				t.Errorf("context.assembled counts = %+v", e.Payload)
			}
		}
	}
	if !sawRead || !sawAssembled {
		t.Fatalf("events missing: sawRead=%v sawAssembled=%v", sawRead, sawAssembled)
	}
}

func TestMemoryNodeRetrieveEventRedaction(t *testing.T) {
	provider := &stubProvider{docs: []memory.Document{{ID: "d1", Text: "sensitive content"}}}
	node := NewMemoryNode("mem", MemoryNodeConfig{
		Provider:               provider,
		Namespace:              "ops",
		SessionID:              "sess-1",
		IncludeContentInEvents: true,
	})

	var events []runtime.Event
	ctx := runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) { events = append(events, e) })
	if _, err := node.Run(ctx, newTestEnvelope()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var sawContent bool
	for _, e := range events {
		if e.Kind == runtime.EventMemoryRead {
			if docs, ok := e.Payload["documents"].([]map[string]any); ok && len(docs) > 0 {
				sawContent = true
			}
		}
	}
	if !sawContent {
		t.Error("IncludeContentInEvents should opt in to document content in events")
	}
}

func TestMemoryNodeBudgetTruncates(t *testing.T) {
	provider := &stubProvider{docs: []memory.Document{
		{ID: "d1", Text: strings.Repeat("a", 100)}, // 25 tokens
		{ID: "d2", Text: strings.Repeat("b", 100)},
		{ID: "d3", Text: strings.Repeat("c", 100)},
	}}
	node := NewMemoryNode("mem", MemoryNodeConfig{
		Provider:  provider,
		Namespace: "ops",
		SessionID: "sess-1",
		Budget:    memory.ContextBudget{MaxTokens: 30},
	})

	result, err := node.Run(context.Background(), newTestEnvelope())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	statsVar, _ := result.GetVar("mem_memory_stats")
	stats := statsVar.(MemoryResult)
	if !stats.Truncated {
		t.Error("expected Truncated=true when over budget")
	}
	if stats.EstimatedTokens > 30 {
		t.Errorf("EstimatedTokens = %d, exceeds budget", stats.EstimatedTokens)
	}
	if len(result.GetArtifactsByType("chunk")) > 3 {
		t.Errorf("artifact count exceeded budget")
	}
}

func TestMemoryNodeOmitArtifacts(t *testing.T) {
	provider := &stubProvider{docs: []memory.Document{{ID: "d1", Text: "fact"}}}
	node := NewMemoryNode("mem", MemoryNodeConfig{
		Provider:      provider,
		Namespace:     "ops",
		SessionID:     "sess-1",
		OmitArtifacts: true,
	})

	result, err := node.Run(context.Background(), newTestEnvelope())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.GetArtifactsByType("chunk")) != 0 {
		t.Error("OmitArtifacts should suppress artifact injection")
	}
}

func TestMemoryNodeUnavailableFail(t *testing.T) {
	provider := &stubProvider{retrieveErr: unavailableErr()}
	node := NewMemoryNode("mem", MemoryNodeConfig{
		Provider:  provider,
		Namespace: "ops",
		SessionID: "sess-1",
	})

	_, err := node.Run(context.Background(), newTestEnvelope())
	if err == nil {
		t.Fatal("expected error when unavailable and policy is fail")
	}
	if !errors.Is(err, memory.ErrUnavailable) {
		t.Errorf("error should wrap memory.ErrUnavailable, got %v", err)
	}
}

func TestMemoryNodeUnavailableSkip(t *testing.T) {
	provider := &stubProvider{retrieveErr: unavailableErr()}
	node := NewMemoryNode("mem", MemoryNodeConfig{
		Provider:      provider,
		Namespace:     "ops",
		SessionID:     "sess-1",
		OnUnavailable: MemoryUnavailableSkip,
	})

	var events []runtime.Event
	ctx := runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) { events = append(events, e) })

	result, err := node.Run(ctx, newTestEnvelope())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	contextText, _ := result.GetVar("mem_memory")
	if s, _ := contextText.(string); s != "" {
		t.Errorf("context = %q, want empty on skip", s)
	}

	var sawUnavailable bool
	for _, e := range events {
		if e.Kind == runtime.EventMemoryRead && e.Payload["unavailable"] == true {
			sawUnavailable = true
		}
		if e.Kind == runtime.EventContextAssembled {
			t.Error("context.assembled should not fire when retrieval is unavailable")
		}
	}
	if !sawUnavailable {
		t.Error("memory.read event with unavailable=true missing")
	}
}

func TestMemoryNodeWrite(t *testing.T) {
	provider := &stubProvider{}
	node := NewMemoryNode("mem", MemoryNodeConfig{
		Provider:  provider,
		Mode:      MemoryModeWrite,
		Namespace: "ops",
		SessionID: "sess-1",
		WriteVar:  "summary",
		WriteKey:  "summary-{{.vars.run}}",
	})

	env := newTestEnvelope()
	env.SetVar("summary", "the deploy succeeded")
	env.SetVar("run", "42")

	var events []runtime.Event
	ctx := runtime.ContextWithEmitter(context.Background(), func(e runtime.Event) { events = append(events, e) })

	if _, err := node.Run(ctx, env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if provider.lastWrite.Text != "the deploy succeeded" {
		t.Errorf("written text = %q", provider.lastWrite.Text)
	}
	if provider.lastWrite.Key != "summary-42" {
		t.Errorf("write key = %q, want summary-42", provider.lastWrite.Key)
	}
	if provider.lastWrite.Scope.Namespace != "ops" || provider.lastWrite.Scope.SessionID != "sess-1" {
		t.Errorf("write scope = %+v", provider.lastWrite.Scope)
	}
	if provider.lastWrite.Metadata["source_node"] != "mem" {
		t.Errorf("write metadata = %+v", provider.lastWrite.Metadata)
	}

	var sawWrite, contentFree bool
	for _, e := range events {
		if e.Kind == runtime.EventMemoryWrite {
			sawWrite = true
			_, contentFree = e.Payload["content"]
			contentFree = !contentFree
		}
	}
	if !sawWrite {
		t.Fatal("memory.write event missing")
	}
	if !contentFree {
		t.Error("memory.write should not include content by default")
	}
}

func TestMemoryNodeWriteFallsBackToInput(t *testing.T) {
	provider := &stubProvider{}
	node := NewMemoryNode("mem", MemoryNodeConfig{
		Provider:  provider,
		Mode:      MemoryModeWrite,
		Namespace: "ops",
		SessionID: "sess-1",
	})

	if _, err := node.Run(context.Background(), newTestEnvelope()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if provider.lastWrite.Text != "user question about deployments" {
		t.Errorf("written text = %q, want envelope input", provider.lastWrite.Text)
	}
}

func TestMemoryNodeScopeFromRunOptions(t *testing.T) {
	provider := &stubProvider{docs: []memory.Document{{ID: "d1", Text: "fact"}}}
	node := NewMemoryNode("mem", MemoryNodeConfig{Provider: provider})

	// No per-node scope; the run scope supplies namespace and session.
	ctx := runtime.ContextWithScope(context.Background(), memory.Scope{
		TenantID:  "acme",
		Namespace: "support",
		SessionID: "sess-9",
		ThreadID:  "th-2",
	})

	result, err := node.Run(ctx, newTestEnvelope())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	statsVar, _ := result.GetVar("mem_memory_stats")
	stats := statsVar.(MemoryResult)
	if stats.Namespace != "support" || stats.SessionID != "sess-9" || stats.ThreadID != "th-2" || stats.TenantID != "acme" {
		t.Errorf("stats scope = %+v", stats)
	}
	if provider.lastQuery.Scope != (memory.Scope{TenantID: "acme", Namespace: "support", SessionID: "sess-9", ThreadID: "th-2", RunID: provider.lastQuery.Scope.RunID}) {
		t.Errorf("provider scope = %+v", provider.lastQuery.Scope)
	}
}

func TestMemoryNodeScopeTemplateOverride(t *testing.T) {
	provider := &stubProvider{}
	node := NewMemoryNode("mem", MemoryNodeConfig{
		Provider:  provider,
		Namespace: "{{.vars.ns}}",
		SessionID: "{{.vars.sid}}",
	})

	env := newTestEnvelope()
	env.SetVar("ns", "dyn-ns")
	env.SetVar("sid", "dyn-sess")

	if _, err := node.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if provider.lastQuery.Scope.Namespace != "dyn-ns" || provider.lastQuery.Scope.SessionID != "dyn-sess" {
		t.Errorf("template scope override failed: %+v", provider.lastQuery.Scope)
	}
}

func TestMemoryNodeMissingScope(t *testing.T) {
	node := NewMemoryNode("mem", MemoryNodeConfig{Provider: &stubProvider{}})

	_, err := node.Run(context.Background(), newTestEnvelope())
	if err == nil {
		t.Fatal("expected error when neither config nor run scope resolve a namespace/session")
	}
	if !strings.Contains(err.Error(), "RunOptions.MemoryScope") {
		t.Errorf("error should point at the fix: %v", err)
	}
}

func TestMemoryNodeUnknownMode(t *testing.T) {
	node := NewMemoryNode("mem", MemoryNodeConfig{
		Provider:  &stubProvider{},
		Mode:      MemoryMode("bogus"),
		Namespace: "ns",
		SessionID: "s",
	})
	if _, err := node.Run(context.Background(), newTestEnvelope()); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestMemoryNodeInvalidScopeCharacters(t *testing.T) {
	node := NewMemoryNode("mem", MemoryNodeConfig{
		Provider:  &stubProvider{},
		Namespace: "ns",
		SessionID: "bad sess",
	})
	if _, err := node.Run(context.Background(), newTestEnvelope()); err == nil {
		t.Fatal("expected error for invalid scope characters")
	}
}

func TestMemoryNodeNoProvider(t *testing.T) {
	node := NewMemoryNode("mem", MemoryNodeConfig{Namespace: "ns", SessionID: "s"})
	if _, err := node.Run(context.Background(), newTestEnvelope()); err == nil {
		t.Fatal("expected error when provider is nil")
	}
}
