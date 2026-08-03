package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

// TestRuntime_Run_ParallelResultIsDeterministic fans out to two non-merged leaf
// nodes. The result must be deterministic across runs regardless of which
// branch finishes first. Leaf "a" sleeps so that, under the old "last result
// wins" logic, it would arrive last and be returned; the runtime must instead
// return the designated sink ("b", the last sink in node-insertion order).
func TestRuntime_Run_ParallelResultIsDeterministic(t *testing.T) {
	g := graph.NewGraph("fanout")
	g.AddNode(core.NewFuncNode("start", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.SetVar("started", true)
		return env, nil
	}))
	g.AddNode(core.NewFuncNode("a", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		time.Sleep(5 * time.Millisecond)
		env.SetVar("leaf", "a")
		return env, nil
	}))
	g.AddNode(core.NewFuncNode("b", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.SetVar("leaf", "b")
		return env, nil
	}))
	g.AddEdge("start", "a")
	g.AddEdge("start", "b")
	g.SetEntry("start")

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.Concurrency = 2

	for i := 0; i < 10; i++ {
		result, err := rt.Run(context.Background(), g, core.NewEnvelope(), opts)
		if err != nil {
			t.Fatalf("run %d: unexpected error: %v", i, err)
		}
		if v, _ := result.GetVar("leaf"); v != "b" {
			t.Fatalf("run %d: result leaf = %v, want deterministic %q (last sink)", i, v, "b")
		}
	}
}
