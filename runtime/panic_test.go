package runtime_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

func TestRuntime_Run_NodePanicReturnsError(t *testing.T) {
	g := graph.NewGraph("panic-seq")
	g.AddNode(core.NewFuncNode("boom", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		panic("kaboom")
	}))
	g.SetEntry("boom")

	rt := runtime.NewRuntime()

	_, err := rt.Run(context.Background(), g, core.NewEnvelope(), runtime.DefaultRunOptions())
	if err == nil {
		t.Fatal("expected an error from a panicking node, got nil")
	}
	if !errors.Is(err, runtime.ErrNodePanic) {
		t.Errorf("expected error to wrap ErrNodePanic, got %v", err)
	}
}

func TestRuntime_Run_NodePanicContinueOnErrorRecordsAndContinues(t *testing.T) {
	g := graph.NewGraph("panic-continue")
	g.AddNode(core.NewFuncNode("boom", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		panic("kaboom")
	}))
	g.AddNode(core.NewFuncNode("after", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.SetVar("after_ran", true)
		return env, nil
	}))
	g.AddEdge("boom", "after")
	g.SetEntry("boom")

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.ContinueOnError = true

	result, err := rt.Run(context.Background(), g, core.NewEnvelope(), opts)
	if err != nil {
		t.Fatalf("expected nil error with ContinueOnError, got %v", err)
	}
	if !result.HasErrors() {
		t.Fatal("expected a recorded node error after a panic")
	}
	if !strings.Contains(result.Errors[0].Message, "panicked") {
		t.Errorf("recorded error = %q, want it to mention the panic", result.Errors[0].Message)
	}
	if ran, _ := result.GetVar("after_ran"); ran != true {
		t.Error("expected execution to continue to the next node after a recovered panic")
	}
}

func TestRuntime_Run_NodePanicParallelReturnsError(t *testing.T) {
	g := graph.NewGraph("panic-parallel")
	g.AddNode(core.NewFuncNode("boom", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		panic("kaboom")
	}))
	g.SetEntry("boom")

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.Concurrency = 2

	_, err := rt.Run(context.Background(), g, core.NewEnvelope(), opts)
	if err == nil {
		t.Fatal("expected an error from a panicking node in parallel mode, got nil")
	}
	if !errors.Is(err, runtime.ErrNodePanic) {
		t.Errorf("expected error to wrap ErrNodePanic, got %v", err)
	}
}
