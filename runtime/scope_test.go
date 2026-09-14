package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/memory"
)

// scopeCaptureGraph returns a single-node graph whose node records the scope
// it observed in its context.
func scopeCaptureGraph(t *testing.T, seen *atomic.Value) *graph.BasicGraph {
	t.Helper()
	g := graph.NewGraph("scope")
	if err := g.AddNode(core.NewFuncNode("capture", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		scope, ok := memory.ScopeFromContext(ctx)
		seen.Store(struct {
			scope memory.Scope
			ok    bool
		}{scope, ok})
		return env.Clone(), nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := g.SetEntry("capture"); err != nil {
		t.Fatal(err)
	}
	return g
}

func loadSeenScope(t *testing.T, seen *atomic.Value) (memory.Scope, bool) {
	t.Helper()
	v, ok := seen.Load().(struct {
		scope memory.Scope
		ok    bool
	})
	if !ok {
		t.Fatal("node did not run")
	}
	return v.scope, v.ok
}

func TestRun_PropagatesScopeToNodesAndEvents(t *testing.T) {
	var seen atomic.Value
	g := scopeCaptureGraph(t, &seen)

	var started Event
	opts := DefaultRunOptions()
	opts.TenantID = "acme"
	opts.Scope = memory.Scope{Namespace: " support ", SessionID: "sess-1", ThreadID: "t-1"}
	opts.EventHandler = func(e Event) {
		if e.Kind == EventRunStarted {
			started = e
		}
	}
	result, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts)
	if err != nil {
		t.Fatal(err)
	}

	scope, ok := loadSeenScope(t, &seen)
	if !ok {
		t.Fatal("node context should carry the scope")
	}
	want := memory.Scope{TenantID: "acme", Namespace: "support", SessionID: "sess-1", ThreadID: "t-1", RunID: result.Trace.RunID}
	if scope != want {
		t.Fatalf("scope = %+v, want %+v", scope, want)
	}
	if started.Payload["namespace"] != "support" || started.Payload["session_id"] != "sess-1" || started.Payload["thread_id"] != "t-1" {
		t.Fatalf("run.started payload = %v", started.Payload)
	}
	if _, present := started.Payload["tenant_id"]; present {
		t.Fatal("tenant_id should not be recorded in event payloads")
	}
}

func TestRun_NoScopeLeavesContextEmpty(t *testing.T) {
	var seen atomic.Value
	g := scopeCaptureGraph(t, &seen)
	if _, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), DefaultRunOptions()); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadSeenScope(t, &seen); ok {
		t.Fatal("runs without a scope must not attach one")
	}
}

func TestRun_TenantOnlyScopeIsAttached(t *testing.T) {
	var seen atomic.Value
	g := scopeCaptureGraph(t, &seen)
	opts := DefaultRunOptions()
	opts.TenantID = "acme"
	result, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts)
	if err != nil {
		t.Fatal(err)
	}
	scope, ok := loadSeenScope(t, &seen)
	if !ok || scope.TenantID != "acme" || scope.RunID != result.Trace.RunID || scope.Namespace != "" {
		t.Fatalf("scope = %+v, ok=%v", scope, ok)
	}
}

func TestRun_RejectsInvalidScopeBeforeExecution(t *testing.T) {
	var seen atomic.Value
	g := scopeCaptureGraph(t, &seen)
	opts := DefaultRunOptions()
	opts.Scope = memory.Scope{SessionID: "sess-1"} // namespace missing
	_, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts)
	if !errors.Is(err, memory.ErrInvalidScope) {
		t.Fatalf("err = %v, want ErrInvalidScope", err)
	}
	if seen.Load() != nil {
		t.Fatal("node must not run when the scope is invalid")
	}
}

func TestRun_RejectsScopeTenantMismatch(t *testing.T) {
	var seen atomic.Value
	g := scopeCaptureGraph(t, &seen)
	opts := DefaultRunOptions()
	opts.TenantID = "acme"
	opts.Scope = memory.Scope{TenantID: "other", Namespace: "ns", SessionID: "s"}
	_, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts)
	if !errors.Is(err, memory.ErrInvalidScope) {
		t.Fatalf("err = %v, want ErrInvalidScope", err)
	}
}

func TestRun_ParallelPropagatesScope(t *testing.T) {
	var seen atomic.Value
	g := scopeCaptureGraph(t, &seen)
	opts := DefaultRunOptions()
	opts.Concurrency = 2
	opts.Scope = memory.Scope{Namespace: "ns", SessionID: "s"}
	if _, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts); err != nil {
		t.Fatal(err)
	}
	scope, ok := loadSeenScope(t, &seen)
	if !ok || scope.Namespace != "ns" || scope.RunID == "" {
		t.Fatalf("scope = %+v, ok=%v", scope, ok)
	}
}

func TestDurableRun_PersistsAndRestoresScope(t *testing.T) {
	store := NewMemoryRunStore()
	var seen atomic.Value
	var attempts atomic.Int32
	g := graph.NewGraph("durable-scope")
	if err := g.AddNode(core.NewFuncNode("flaky", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		if attempts.Add(1) == 1 {
			panic("transient")
		}
		scope, _ := memory.ScopeFromContext(ctx)
		seen.Store(scope)
		return env.Clone(), nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := g.SetEntry("flaky"); err != nil {
		t.Fatal(err)
	}

	_, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), RunOptions{
		RunStore: store,
		TenantID: "acme",
		Scope:    memory.Scope{Namespace: "support", SessionID: "sess-1"},
	})
	if !errors.Is(err, ErrNodePanic) {
		t.Fatalf("first run error = %v", err)
	}
	runID := findRunID(t, store)
	record, err := store.Get(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Scope == nil || record.Scope.Namespace != "support" || record.Scope.SessionID != "sess-1" || record.Scope.TenantID != "acme" || record.Scope.RunID != runID {
		t.Fatalf("persisted scope = %+v", record.Scope)
	}

	// Resume without repeating the scope: the persisted one is restored.
	if _, err := NewRuntime().Resume(context.Background(), g, runID, RunOptions{RunStore: store}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	scope, _ := seen.Load().(memory.Scope)
	if scope.Namespace != "support" || scope.SessionID != "sess-1" || scope.TenantID != "acme" || scope.RunID != runID {
		t.Fatalf("restored scope = %+v", scope)
	}
}

func TestDurableResume_RejectsDifferentScope(t *testing.T) {
	store := NewMemoryRunStore()
	g := graph.NewGraph("durable-scope-mismatch")
	if err := g.AddNode(core.NewFuncNode("boom", func(context.Context, *core.Envelope) (*core.Envelope, error) {
		return nil, errors.New("fail once")
	})); err != nil {
		t.Fatal(err)
	}
	if err := g.SetEntry("boom"); err != nil {
		t.Fatal(err)
	}
	_, _ = NewRuntime().Run(context.Background(), g, core.NewEnvelope(), RunOptions{
		RunStore: store,
		Scope:    memory.Scope{Namespace: "support", SessionID: "sess-1"},
	})
	runID := findRunID(t, store)

	_, err := NewRuntime().Resume(context.Background(), g, runID, RunOptions{
		RunStore: store,
		Scope:    memory.Scope{Namespace: "support", SessionID: "someone-else"},
	})
	if !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("err = %v, want ErrScopeMismatch", err)
	}

	// Repeating the same scope is allowed.
	_, err = NewRuntime().Resume(context.Background(), g, runID, RunOptions{
		RunStore: store,
		Scope:    memory.Scope{Namespace: "support", SessionID: "sess-1"},
	})
	if errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("identical scope should be accepted, got %v", err)
	}
}
