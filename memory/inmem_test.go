package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/petal-labs/petalflow/core"
)

func TestInMemoryRememberRecallIsolation(t *testing.T) {
	p := NewInMemoryProvider()
	ctx := context.Background()
	a := Scope{TenantID: "t1", Namespace: "ns", SessionID: "s1"}
	b := Scope{TenantID: "t2", Namespace: "ns", SessionID: "s1"} // same session, other tenant

	if _, err := p.Remember(ctx, RememberRequest{Scope: a, Messages: []core.Message{msg("user", "for a")}}); err != nil {
		t.Fatal(err)
	}
	res, err := p.Recall(ctx, RecallRequest{Scope: b})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 0 {
		t.Fatalf("tenant b must not see tenant a's messages: %+v", res.Messages)
	}
	res, err = p.Recall(ctx, RecallRequest{Scope: a})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Content != "for a" {
		t.Fatalf("recall = %+v", res.Messages)
	}
}

func TestInMemoryRecallLimitRolesAndThread(t *testing.T) {
	p := NewInMemoryProvider()
	ctx := context.Background()
	scope := Scope{Namespace: "ns", SessionID: "s1"}
	msgs := []core.Message{msg("user", "1"), msg("assistant", "2"), msg("user", "3"), msg("assistant", "4")}
	if _, err := p.Remember(ctx, RememberRequest{Scope: scope, Messages: msgs}); err != nil {
		t.Fatal(err)
	}

	res, err := p.Recall(ctx, RecallRequest{Scope: scope, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 2 || res.Messages[0].Content != "3" || !res.Truncated {
		t.Fatalf("limit recall = %+v truncated=%v", res.Messages, res.Truncated)
	}

	res, err = p.Recall(ctx, RecallRequest{Scope: scope, Roles: []string{"assistant"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 2 || res.Messages[0].Content != "2" {
		t.Fatalf("role recall = %+v", res.Messages)
	}

	// A thread inside the session is a separate conversation.
	thread := scope
	thread.ThreadID = "branch"
	res, err = p.Recall(ctx, RecallRequest{Scope: thread})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 0 {
		t.Fatalf("thread should be empty: %+v", res.Messages)
	}
}

func TestInMemoryScopeValidation(t *testing.T) {
	p := NewInMemoryProvider()
	ctx := context.Background()
	if _, err := p.Recall(ctx, RecallRequest{Scope: Scope{Namespace: "ns"}}); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("recall without session = %v", err)
	}
	if _, err := p.Remember(ctx, RememberRequest{Scope: Scope{SessionID: "s"}}); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("remember without namespace = %v", err)
	}
	if _, err := p.Search(ctx, SearchRequest{Query: "x"}); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("search without namespace = %v", err)
	}
	if err := p.AddDocument(Scope{}, "", core.Artifact{Text: "x"}); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("add without namespace = %v", err)
	}
}

func TestInMemoryCanceledContextIsUnavailable(t *testing.T) {
	p := NewInMemoryProvider()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scope := Scope{Namespace: "ns", SessionID: "s"}
	if _, err := p.Recall(ctx, RecallRequest{Scope: scope}); !IsUnavailable(err) {
		t.Fatalf("recall = %v", err)
	}
	if _, err := p.Remember(ctx, RememberRequest{Scope: scope}); !IsUnavailable(err) {
		t.Fatalf("remember = %v", err)
	}
	if _, err := p.Search(ctx, SearchRequest{Scope: scope, Query: "q"}); !IsUnavailable(err) {
		t.Fatalf("search = %v", err)
	}
}

func TestInMemorySearch(t *testing.T) {
	p := NewInMemoryProvider()
	ctx := context.Background()
	scope := Scope{TenantID: "t", Namespace: "docs"}
	seed := func(collection, id, text string, meta map[string]any) {
		t.Helper()
		if err := p.AddDocument(scope, collection, core.Artifact{ID: id, Text: text, Meta: meta}); err != nil {
			t.Fatal(err)
		}
	}
	seed("kb", "refund", "Refunds are processed within five business days", map[string]any{"lang": "en"})
	seed("kb", "shipping", "Shipping takes three business days", map[string]any{"lang": "en"})
	seed("faq", "refund-faq", "How do refunds work", map[string]any{"lang": "de"})
	seed("kb", "unrelated", "Company picnic schedule", nil)

	res, err := p.Search(ctx, SearchRequest{Scope: scope, Query: "refunds business days", TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 2 || res.Hits[0].Artifact.ID != "refund" {
		t.Fatalf("hits = %+v", res.Hits)
	}
	if res.TotalFound != 3 {
		t.Fatalf("total found = %d", res.TotalFound)
	}
	if res.Hits[0].Score <= res.Hits[1].Score {
		t.Fatal("hits must be ordered by descending score")
	}

	res, err = p.Search(ctx, SearchRequest{Scope: scope, Query: "refunds", Collections: []string{"faq"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Artifact.ID != "refund-faq" {
		t.Fatalf("collection filter hits = %+v", res.Hits)
	}

	res, err = p.Search(ctx, SearchRequest{Scope: scope, Query: "refunds", Filters: map[string]string{"lang": "en"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Artifact.ID != "refund" {
		t.Fatalf("metadata filter hits = %+v", res.Hits)
	}

	res, err = p.Search(ctx, SearchRequest{Scope: scope, Query: "refunds business days", MinScore: 0.9})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("min score hits = %+v", res.Hits)
	}

	// Empty queries and other namespaces return nothing.
	if res, _ := p.Search(ctx, SearchRequest{Scope: scope, Query: "   "}); len(res.Hits) != 0 {
		t.Fatal("blank query should return no hits")
	}
	other := Scope{TenantID: "t", Namespace: "other"}
	if res, _ := p.Search(ctx, SearchRequest{Scope: other, Query: "refunds"}); len(res.Hits) != 0 {
		t.Fatal("other namespace should be empty")
	}

	// Results are copies.
	res, _ = p.Search(ctx, SearchRequest{Scope: scope, Query: "refunds"})
	res.Hits[0].Artifact.Meta["lang"] = "xx"
	res, _ = p.Search(ctx, SearchRequest{Scope: scope, Query: "refunds", Filters: map[string]string{"lang": "en"}})
	if len(res.Hits) != 1 {
		t.Fatal("search results must not alias stored artifacts")
	}

	p.Reset()
	if res, _ := p.Search(ctx, SearchRequest{Scope: scope, Query: "refunds"}); len(res.Hits) != 0 {
		t.Fatal("reset should clear documents")
	}
}
