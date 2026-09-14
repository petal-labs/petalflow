// Package memorytest is a conformance suite for memory.MemoryProvider and
// memory.KnowledgeProvider implementations.
//
// A backend adapter (for example one backed by Cortex) runs the suite from its
// own module against its real implementation:
//
//	func TestCortexConformance(t *testing.T) {
//		memorytest.RunMemoryProvider(t, func(t *testing.T) memory.MemoryProvider {
//			return newCortexAdapter(t)
//		})
//	}
//
// The suite exercises only the public contract, so PetalFlow never imports the
// backend and the backend never needs to expose internals to be tested.
package memorytest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/memory"
)

// MemoryFactory returns a fresh, empty MemoryProvider for one subtest. It may
// register cleanup with t.Cleanup.
type MemoryFactory func(t *testing.T) memory.MemoryProvider

// KnowledgeSeeder returns a fresh KnowledgeProvider whose namespace under
// scope contains exactly the given documents. Implementations ingest the
// documents through whatever public path the backend offers.
type KnowledgeSeeder func(t *testing.T, scope memory.Scope, docs []core.Artifact) memory.KnowledgeProvider

// RunMemoryProvider runs the conversation-memory conformance suite.
func RunMemoryProvider(t *testing.T, newProvider MemoryFactory) {
	t.Helper()
	t.Run("RememberThenRecallOldestFirst", func(t *testing.T) {
		p := newProvider(t)
		scope := uniqueScope("recall")
		msgs := []core.Message{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "second"},
			{Role: "user", Content: "third"},
		}
		res, err := p.Remember(ctx(t), memory.RememberRequest{Scope: scope, Messages: msgs})
		if err != nil {
			t.Fatalf("Remember: %v", err)
		}
		if res.Stored != len(msgs) {
			t.Fatalf("Stored = %d, want %d", res.Stored, len(msgs))
		}
		got, err := p.Recall(ctx(t), memory.RecallRequest{Scope: scope, Limit: 10})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		assertContents(t, got.Messages, "first", "second", "third")
		if got.Truncated {
			t.Fatal("Truncated should be false when everything fits")
		}
	})

	t.Run("RecallLimitKeepsMostRecent", func(t *testing.T) {
		p := newProvider(t)
		scope := uniqueScope("limit")
		var msgs []core.Message
		for i := 0; i < 5; i++ {
			msgs = append(msgs, core.Message{Role: "user", Content: fmt.Sprintf("m%d", i)})
		}
		if _, err := p.Remember(ctx(t), memory.RememberRequest{Scope: scope, Messages: msgs}); err != nil {
			t.Fatalf("Remember: %v", err)
		}
		got, err := p.Recall(ctx(t), memory.RecallRequest{Scope: scope, Limit: 2})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		assertContents(t, got.Messages, "m3", "m4")
		if !got.Truncated {
			t.Fatal("Truncated should be true when Limit hides history")
		}
	})

	t.Run("DefaultLimitIsFinite", func(t *testing.T) {
		p := newProvider(t)
		scope := uniqueScope("default-limit")
		msgs := make([]core.Message, memory.DefaultRecallLimit+5)
		for i := range msgs {
			msgs[i] = core.Message{Role: "user", Content: fmt.Sprintf("m%d", i)}
		}
		if _, err := p.Remember(ctx(t), memory.RememberRequest{Scope: scope, Messages: msgs}); err != nil {
			t.Fatalf("Remember: %v", err)
		}
		got, err := p.Recall(ctx(t), memory.RecallRequest{Scope: scope})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		if len(got.Messages) == 0 || len(got.Messages) > memory.DefaultRecallLimit {
			t.Fatalf("Recall with Limit=0 returned %d messages; want 1..%d", len(got.Messages), memory.DefaultRecallLimit)
		}
	})

	t.Run("RoleFilter", func(t *testing.T) {
		p := newProvider(t)
		scope := uniqueScope("roles")
		msgs := []core.Message{
			{Role: "user", Content: "u1"},
			{Role: "assistant", Content: "a1"},
			{Role: "user", Content: "u2"},
		}
		if _, err := p.Remember(ctx(t), memory.RememberRequest{Scope: scope, Messages: msgs}); err != nil {
			t.Fatalf("Remember: %v", err)
		}
		got, err := p.Recall(ctx(t), memory.RecallRequest{Scope: scope, Limit: 10, Roles: []string{"assistant"}})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		assertContents(t, got.Messages, "a1")
	})

	t.Run("SessionsAreIsolated", func(t *testing.T) {
		p := newProvider(t)
		a := uniqueScope("iso-a")
		b := a
		b.SessionID = a.SessionID + "-other"
		if _, err := p.Remember(ctx(t), memory.RememberRequest{Scope: a, Messages: []core.Message{{Role: "user", Content: "only a"}}}); err != nil {
			t.Fatalf("Remember: %v", err)
		}
		got, err := p.Recall(ctx(t), memory.RecallRequest{Scope: b, Limit: 10})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		if len(got.Messages) != 0 {
			t.Fatalf("session b sees %d messages from session a", len(got.Messages))
		}
	})

	t.Run("NamespacesAreIsolated", func(t *testing.T) {
		p := newProvider(t)
		a := uniqueScope("iso-ns")
		b := a
		b.Namespace = a.Namespace + "-other"
		if _, err := p.Remember(ctx(t), memory.RememberRequest{Scope: a, Messages: []core.Message{{Role: "user", Content: "only a"}}}); err != nil {
			t.Fatalf("Remember: %v", err)
		}
		got, err := p.Recall(ctx(t), memory.RecallRequest{Scope: b, Limit: 10})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		if len(got.Messages) != 0 {
			t.Fatalf("namespace b sees %d messages from namespace a", len(got.Messages))
		}
	})

	t.Run("TenantsAreIsolated", func(t *testing.T) {
		p := newProvider(t)
		a := uniqueScope("iso-tenant")
		a.TenantID = "tenant-a"
		b := a
		b.TenantID = "tenant-b"
		if _, err := p.Remember(ctx(t), memory.RememberRequest{Scope: a, Messages: []core.Message{{Role: "user", Content: "only a"}}}); err != nil {
			t.Fatalf("Remember: %v", err)
		}
		got, err := p.Recall(ctx(t), memory.RecallRequest{Scope: b, Limit: 10})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		if len(got.Messages) != 0 {
			t.Fatalf("tenant b sees %d messages from tenant a", len(got.Messages))
		}
	})

	t.Run("ThreadIsSeparateFromSession", func(t *testing.T) {
		p := newProvider(t)
		session := uniqueScope("thread")
		thread := session
		thread.ThreadID = "branch-1"
		if _, err := p.Remember(ctx(t), memory.RememberRequest{Scope: session, Messages: []core.Message{{Role: "user", Content: "session"}}}); err != nil {
			t.Fatalf("Remember: %v", err)
		}
		if _, err := p.Remember(ctx(t), memory.RememberRequest{Scope: thread, Messages: []core.Message{{Role: "user", Content: "thread"}}}); err != nil {
			t.Fatalf("Remember: %v", err)
		}
		got, err := p.Recall(ctx(t), memory.RecallRequest{Scope: thread, Limit: 10})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		assertContents(t, got.Messages, "thread")
		got, err = p.Recall(ctx(t), memory.RecallRequest{Scope: session, Limit: 10})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		assertContents(t, got.Messages, "session")
	})

	t.Run("InvalidScopeIsRejectedNotUnavailable", func(t *testing.T) {
		p := newProvider(t)
		_, err := p.Recall(ctx(t), memory.RecallRequest{Scope: memory.Scope{Namespace: "ns"}, Limit: 1})
		if err == nil {
			t.Fatal("Recall without session_id should fail")
		}
		if memory.IsUnavailable(err) {
			t.Fatalf("validation failure must not be classified unavailable: %v", err)
		}
		_, err = p.Remember(ctx(t), memory.RememberRequest{Scope: memory.Scope{SessionID: "s"}, Messages: []core.Message{{Role: "user", Content: "x"}}})
		if err == nil {
			t.Fatal("Remember without namespace should fail")
		}
		if memory.IsUnavailable(err) {
			t.Fatalf("validation failure must not be classified unavailable: %v", err)
		}
	})

	t.Run("CanceledContextIsUnavailable", func(t *testing.T) {
		p := newProvider(t)
		scope := uniqueScope("cancel")
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := p.Recall(canceled, memory.RecallRequest{Scope: scope, Limit: 1})
		if err == nil {
			t.Fatal("Recall with canceled context should fail")
		}
		if !memory.IsUnavailable(err) && !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled call should be unavailable, got %v", err)
		}
	})
}

// RunKnowledgeProvider runs the knowledge-retrieval conformance suite.
func RunKnowledgeProvider(t *testing.T, seed KnowledgeSeeder) {
	t.Helper()
	docs := []core.Artifact{
		{ID: "refund", Type: "document", Text: "Refunds are processed within five business days.", Meta: map[string]any{"title": "Refund policy"}},
		{ID: "shipping", Type: "document", Text: "Standard shipping takes three business days.", Meta: map[string]any{"title": "Shipping"}},
		{ID: "picnic", Type: "document", Text: "The company picnic is in July.", Meta: map[string]any{"title": "Picnic"}},
	}

	t.Run("SearchReturnsRelevantRankedHits", func(t *testing.T) {
		scope := uniqueScope("search")
		p := seed(t, scope, docs)
		res, err := p.Search(ctx(t), memory.SearchRequest{Scope: scope, Query: "how long do refunds take", TopK: 3})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(res.Hits) == 0 {
			t.Fatal("expected at least one hit")
		}
		if res.Hits[0].Artifact.ID != "refund" {
			t.Fatalf("top hit = %q, want refund", res.Hits[0].Artifact.ID)
		}
		for i := 1; i < len(res.Hits); i++ {
			if res.Hits[i].Score > res.Hits[i-1].Score {
				t.Fatalf("hits not ordered by descending score at %d", i)
			}
		}
		for _, h := range res.Hits {
			if h.Artifact.Text == "" && len(h.Artifact.Bytes) == 0 && h.Artifact.URI == "" {
				t.Fatalf("hit %q carries no content or reference", h.Artifact.ID)
			}
		}
	})

	t.Run("TopKIsRespected", func(t *testing.T) {
		scope := uniqueScope("topk")
		p := seed(t, scope, docs)
		res, err := p.Search(ctx(t), memory.SearchRequest{Scope: scope, Query: "business days", TopK: 1})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(res.Hits) > 1 {
			t.Fatalf("TopK=1 returned %d hits", len(res.Hits))
		}
	})

	t.Run("DefaultTopKIsFinite", func(t *testing.T) {
		scope := uniqueScope("default-topk")
		many := make([]core.Artifact, memory.DefaultTopK+5)
		for i := range many {
			many[i] = core.Artifact{ID: fmt.Sprintf("d%d", i), Text: "business days shipping refunds"}
		}
		p := seed(t, scope, many)
		res, err := p.Search(ctx(t), memory.SearchRequest{Scope: scope, Query: "business days"})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(res.Hits) == 0 || len(res.Hits) > memory.DefaultTopK {
			t.Fatalf("Search with TopK=0 returned %d hits; want 1..%d", len(res.Hits), memory.DefaultTopK)
		}
	})

	t.Run("NamespacesAreIsolated", func(t *testing.T) {
		scope := uniqueScope("search-iso")
		p := seed(t, scope, docs)
		other := scope
		other.Namespace = scope.Namespace + "-other"
		res, err := p.Search(ctx(t), memory.SearchRequest{Scope: other, Query: "refunds", TopK: 3})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(res.Hits) != 0 {
			t.Fatalf("other namespace returned %d hits", len(res.Hits))
		}
	})

	t.Run("CanceledContextIsUnavailable", func(t *testing.T) {
		scope := uniqueScope("search-cancel")
		p := seed(t, scope, docs)
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := p.Search(canceled, memory.SearchRequest{Scope: scope, Query: "refunds", TopK: 1})
		if err == nil {
			t.Fatal("Search with canceled context should fail")
		}
		if !memory.IsUnavailable(err) && !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled call should be unavailable, got %v", err)
		}
	})
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

var scopeSeq int

func uniqueScope(label string) memory.Scope {
	scopeSeq++
	suffix := fmt.Sprintf("%s-%d-%d", label, time.Now().UnixNano(), scopeSeq)
	return memory.Scope{
		TenantID:  "conformance",
		Namespace: "ns-" + suffix,
		SessionID: "session-" + suffix,
	}
}

func assertContents(t *testing.T, got []core.Message, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d messages %v, want %d %v", len(got), contents(got), len(want), want)
	}
	for i := range want {
		if got[i].Content != want[i] {
			t.Fatalf("message %d = %q, want %q (all: %v)", i, got[i].Content, want[i], contents(got))
		}
	}
}

func contents(msgs []core.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}
