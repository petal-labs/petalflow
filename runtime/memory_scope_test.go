package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/memory"
)

func TestRunPropagatesMemoryScope(t *testing.T) {
	var seen memory.Scope
	node := core.NewFuncNode("probe", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		seen = ScopeFromContext(ctx)
		return env, nil
	})

	g, _ := graph.NewGraphBuilder("scope-test").AddNode(node).Build()

	opts := DefaultRunOptions()
	opts.MemoryScope = memory.Scope{TenantID: "acme", Namespace: "support", SessionID: "sess-1"}

	if _, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if seen.Namespace != "support" || seen.SessionID != "sess-1" || seen.TenantID != "acme" {
		t.Errorf("node saw scope %+v", seen)
	}
}

func TestRunRejectsInvalidMemoryScope(t *testing.T) {
	node := core.NewFuncNode("probe", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		return env, nil
	})
	g, _ := graph.NewGraphBuilder("scope-test").AddNode(node).Build()

	opts := DefaultRunOptions()
	opts.MemoryScope = memory.Scope{Namespace: "ns"} // missing session

	_, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), opts)
	if err == nil {
		t.Fatal("expected error for scope missing session ID")
	}
	if !errors.Is(err, ErrInvalidMemoryScope) {
		t.Errorf("error should wrap ErrInvalidMemoryScope, got %v", err)
	}
}

func TestRunWithoutMemoryScopeLeavesContextUnset(t *testing.T) {
	node := core.NewFuncNode("probe", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		if ScopeFromContext(ctx) != (memory.Scope{}) {
			t.Error("scope should be zero when not requested")
		}
		return env, nil
	})
	g, _ := graph.NewGraphBuilder("scope-test").AddNode(node).Build()

	if _, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), DefaultRunOptions()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
