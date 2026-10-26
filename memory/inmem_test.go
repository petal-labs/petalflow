package memory

import (
	"context"
	"testing"
)

func TestInMemoryProviderRoundtrip(t *testing.T) {
	ctx := context.Background()
	p := NewInMemoryProvider()
	scope := Scope{Namespace: "support", SessionID: "s1"}

	if err := p.Write(ctx, WriteRequest{Scope: scope, Key: "fact", Text: "the sky is blue"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	docs, err := p.Retrieve(ctx, RetrievalQuery{Scope: scope, Query: "sky"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 1 || docs[0].Text != "the sky is blue" {
		t.Fatalf("Retrieve = %+v, want the written document", docs)
	}
	if docs[0].ID == "" {
		t.Error("document should have an assigned ID")
	}
}

func TestInMemoryProviderScopeIsolation(t *testing.T) {
	ctx := context.Background()
	p := NewInMemoryProvider()
	base := Scope{TenantID: "t1", Namespace: "ns", SessionID: "s1"}

	if err := p.Write(ctx, WriteRequest{Scope: base, Text: "tenant one secret"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	isolated := []Scope{
		{TenantID: "t1", Namespace: "ns", SessionID: "s2"},    // other session
		{TenantID: "t1", Namespace: "other", SessionID: "s1"}, // other namespace
		{TenantID: "t2", Namespace: "ns", SessionID: "s1"},    // other tenant
	}
	for _, scope := range isolated {
		docs, err := p.Retrieve(ctx, RetrievalQuery{Scope: scope, Query: "tenant one secret"})
		if err != nil {
			t.Fatalf("Retrieve(%+v): %v", scope, err)
		}
		if len(docs) != 0 {
			t.Errorf("Retrieve(%+v) leaked %d documents", scope, len(docs))
		}
	}
}

func TestInMemoryProviderLimitAndTokenBudget(t *testing.T) {
	ctx := context.Background()
	p := NewInMemoryProvider()
	scope := Scope{Namespace: "ns", SessionID: "s1"}
	for i := 0; i < 5; i++ {
		if err := p.Write(ctx, WriteRequest{Scope: scope, Text: "probe entry for budget"}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	docs, err := p.Retrieve(ctx, RetrievalQuery{Scope: scope, Query: "probe entry for budget", Limit: 2})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("Limit: got %d documents, want 2", len(docs))
	}

	docs, err = p.Retrieve(ctx, RetrievalQuery{Scope: scope, Query: "probe entry for budget", MaxTokens: EstimateTokens("probe entry for budget")})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 1 {
		t.Errorf("MaxTokens: got %d documents, want 1", len(docs))
	}
}

func TestInMemoryProviderInvalidScope(t *testing.T) {
	ctx := context.Background()
	p := NewInMemoryProvider()

	if err := p.Write(ctx, WriteRequest{Scope: Scope{Namespace: "ns"}}); err == nil {
		t.Error("Write with missing session: expected error")
	}
	if _, err := p.Retrieve(ctx, RetrievalQuery{Scope: Scope{SessionID: "s"}}); err == nil {
		t.Error("Retrieve with missing namespace: expected error")
	}
}

func TestInMemoryProviderRecentWithoutQuery(t *testing.T) {
	ctx := context.Background()
	p := NewInMemoryProvider()
	scope := Scope{Namespace: "ns", SessionID: "s1"}
	if err := p.Write(ctx, WriteRequest{Scope: scope, Text: "recent record"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	docs, err := p.Retrieve(ctx, RetrievalQuery{Scope: scope})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 1 {
		t.Errorf("empty query should return recent records, got %d", len(docs))
	}
}
