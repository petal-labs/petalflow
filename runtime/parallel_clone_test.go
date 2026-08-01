package runtime_test

import (
	"context"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

// writeNestedVar returns a node func that repeatedly writes to the nested
// "shared" map, widening the window in which a shallow clone (shared backing
// map) would produce a data race under the race detector.
func writeNestedVar(key string) core.NodeFunc {
	return func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		if m, ok := env.Vars["shared"].(map[string]any); ok {
			for i := 0; i < 100; i++ {
				m[key] = i
			}
		}
		return env, nil
	}
}

// TestRuntime_Run_ParallelBranchesIsolateNestedVars fans out to two concurrent
// branches that both mutate a nested map seeded by the entry node. With a deep
// Clone each branch owns a separate copy, so there is no data race; with a
// shallow clone the branches would share the backing map and race. Run under
// `go test -race` (as CI does) this guards against a regression to shallow clone.
func TestRuntime_Run_ParallelBranchesIsolateNestedVars(t *testing.T) {
	g := graph.NewGraph("fanout")
	g.AddNode(core.NewFuncNode("start", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.SetVar("shared", map[string]any{})
		return env, nil
	}))
	g.AddNode(core.NewFuncNode("a", writeNestedVar("a")))
	g.AddNode(core.NewFuncNode("b", writeNestedVar("b")))
	g.AddEdge("start", "a")
	g.AddEdge("start", "b")
	g.SetEntry("start")

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.Concurrency = 2

	if _, err := rt.Run(context.Background(), g, core.NewEnvelope(), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
