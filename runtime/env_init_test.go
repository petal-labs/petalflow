package runtime_test

import (
	"context"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

// TestRuntime_Run_InitializesNilVarsOnHandBuiltEnvelope ensures a caller passing
// a hand-built &core.Envelope{} (non-nil but with a nil Vars map) is normalized,
// so a node that writes directly to env.Vars does not hit an assignment-to-nil-map.
func TestRuntime_Run_InitializesNilVarsOnHandBuiltEnvelope(t *testing.T) {
	g := graph.NewGraph("x")
	g.AddNode(core.NewFuncNode("a", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.Vars["direct"] = "written" // direct map write panics if Vars is nil
		return env, nil
	}))
	g.SetEntry("a")

	rt := runtime.NewRuntime()

	result, err := rt.Run(context.Background(), g, &core.Envelope{}, runtime.DefaultRunOptions())
	if err != nil {
		t.Fatalf("unexpected error for a hand-built envelope with nil Vars: %v", err)
	}
	if v, _ := result.GetVar("direct"); v != "written" {
		t.Errorf("direct var = %v, want %q", v, "written")
	}
}
