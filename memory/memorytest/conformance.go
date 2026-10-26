// Package memorytest provides a conformance suite for memory.Provider
// implementations. External backends — such as a Cortex adapter — run the
// suite against their own implementation to verify the PetalFlow contract
// without importing any PetalFlow internals beyond the memory and memorytest
// packages.
package memorytest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/memory"
)

// ConformanceOptions tunes optional suite behavior.
type ConformanceOptions struct {
	// SkipWrite tests providers that only implement retrieval. The suite
	// then skips write-related cases.
	SkipWrite bool
}

// RunConformance verifies that a Provider satisfies the PetalFlow memory
// contract. Use it from an implementation's own tests:
//
//	func TestConformance(t *testing.T) {
//	    memorytest.RunConformance(t, NewCortexProvider(cfg))
//	}
//
// The suite requires a provider backed by isolated state (a fresh namespace
// per run is sufficient); it writes only under the reserved
// "petalflow-conformance" namespace.
func RunConformance(t *testing.T, provider memory.Provider, opts ...ConformanceOptions) {
	t.Helper()

	var o ConformanceOptions
	if len(opts) > 0 {
		o = opts[0]
	}

	ctx := context.Background()
	scope := memory.Scope{
		Namespace: "petalflow-conformance",
		SessionID: fmt.Sprintf("session-%d", timeNowUnixNano()),
	}

	t.Run("scope_isolation", func(t *testing.T) {
		testScopeIsolation(t, ctx, provider, scope)
	})

	t.Run("empty_scope_returns_no_results", func(t *testing.T) {
		query := memory.RetrievalQuery{
			Scope: memory.Scope{Namespace: scope.Namespace, SessionID: scope.SessionID + "-empty"},
		}
		docs, err := provider.Retrieve(ctx, query)
		if err != nil {
			t.Fatalf("Retrieve on empty scope: unexpected error: %v", err)
		}
		if len(docs) != 0 {
			t.Fatalf("Retrieve on empty scope: got %d documents, want 0", len(docs))
		}
	})

	t.Run("invalid_scope_rejected", func(t *testing.T) {
		query := memory.RetrievalQuery{Scope: memory.Scope{Namespace: "ns", SessionID: ""}}
		if _, err := provider.Retrieve(ctx, query); err == nil {
			t.Fatalf("Retrieve with missing session_id: expected error, got nil")
		}
	})

	if !o.SkipWrite {
		t.Run("write_then_retrieve_roundtrip", func(t *testing.T) {
			testWriteRetrieveRoundtrip(t, ctx, provider, scope)
		})

		t.Run("retrieval_budget_respected", func(t *testing.T) {
			testRetrievalBudget(t, ctx, provider, scope)
		})
	}
}

func testScopeIsolation(t *testing.T, ctx context.Context, provider memory.Provider, scope memory.Scope) {
	t.Helper()

	write := memory.WriteRequest{
		Scope: scope,
		Key:   "secret",
		Text:  "the launch code is alpha-7",
	}
	if err := provider.Write(ctx, write); err != nil {
		t.Fatalf("Write: %v", err)
	}

	other := memory.Scope{Namespace: scope.Namespace, SessionID: scope.SessionID + "-other"}
	docs, err := provider.Retrieve(ctx, memory.RetrievalQuery{Scope: other, Query: "launch code"})
	if err != nil {
		t.Fatalf("Retrieve from other session: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("memory leaked across sessions: got %d documents, want 0", len(docs))
	}
}

func testWriteRetrieveRoundtrip(t *testing.T, ctx context.Context, provider memory.Provider, scope memory.Scope) {
	t.Helper()

	text := "the deployment window is tuesday"
	if err := provider.Write(ctx, memory.WriteRequest{Scope: scope, Key: "schedule", Text: text}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	docs, err := provider.Retrieve(ctx, memory.RetrievalQuery{Scope: scope, Query: "deployment window"})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("Retrieve: no documents returned after write")
	}
	found := false
	for _, doc := range docs {
		if strings.Contains(doc.Text, text) {
			found = true
		}
		if doc.ID == "" {
			t.Errorf("Retrieve: document has empty ID")
		}
	}
	if !found {
		t.Errorf("Retrieve: written text not returned")
	}
}

func testRetrievalBudget(t *testing.T, ctx context.Context, provider memory.Provider, scope memory.Scope) {
	t.Helper()

	for i := 0; i < 5; i++ {
		req := memory.WriteRequest{Scope: scope, Text: fmt.Sprintf("budget probe number %d", i)}
		if err := provider.Write(ctx, req); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	docs, err := provider.Retrieve(ctx, memory.RetrievalQuery{
		Scope: scope,
		Query: "budget probe",
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) > 2 {
		t.Errorf("Retrieve: got %d documents, want at most Limit (2)", len(docs))
	}
}

// timeNowUnixNano is a variable so the suite can be deterministic if needed.
var timeNowUnixNano = func() int64 {
	return time.Now().UnixNano()
}

// unavailableConformance optionally verifies ErrUnavailable signaling when an
// implementation opts in via CheckUnavailable.
func CheckUnavailable(t *testing.T, provider memory.Provider, wantUnavailable bool) {
	t.Helper()

	ctx := context.Background()
	scope := memory.Scope{Namespace: "petalflow-conformance", SessionID: "unavailable-probe"}

	_, err := provider.Retrieve(ctx, memory.RetrievalQuery{Scope: scope, Query: "probe"})
	if wantUnavailable {
		if err == nil || !errors.Is(err, memory.ErrUnavailable) {
			t.Fatalf("Retrieve: expected error wrapping memory.ErrUnavailable, got %v", err)
		}
		return
	}
	if err != nil && errors.Is(err, memory.ErrUnavailable) {
		t.Fatalf("Retrieve: unexpected memory.ErrUnavailable: %v", err)
	}
}
