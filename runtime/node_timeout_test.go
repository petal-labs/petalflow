package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

// blockingNode ignores its context and sleeps, simulating a node that does not
// honor cancellation.
func blockingNode(id string, d time.Duration) core.Node {
	return core.NewFuncNode(id, func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		time.Sleep(d)
		return env, nil
	})
}

func TestRuntime_Run_NodeTimeoutInterruptsBlockingNode(t *testing.T) {
	g := graph.NewGraph("blocking")
	g.AddNode(blockingNode("block", 30*time.Second))
	g.SetEntry("block")

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.NodeTimeout = 50 * time.Millisecond

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := rt.Run(context.Background(), g, core.NewEnvelope(), opts)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a node timeout error, got nil")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected error to wrap context.DeadlineExceeded, got %v", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("run took %v, expected prompt return under NodeTimeout", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return under NodeTimeout with a blocking node")
	}
}

func TestRuntime_Run_NodeTimeoutHonorsParentCancel(t *testing.T) {
	started := make(chan struct{})
	g := graph.NewGraph("cancel")
	g.AddNode(core.NewFuncNode("block", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		close(started)
		time.Sleep(30 * time.Second) // ignores ctx
		return env, nil
	}))
	g.SetEntry("block")

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.NodeTimeout = 30 * time.Second // large; parent cancel should win

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := rt.Run(ctx, g, core.NewEnvelope(), opts)
		done <- err
	}()

	<-started
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error after parent cancel, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after parent context was canceled")
	}
}

func TestRuntime_Run_NodeTimeoutIsolatesAbandonedWrites(t *testing.T) {
	release := make(chan struct{})
	wrote := make(chan struct{})
	g := graph.NewGraph("abandon")
	g.AddNode(core.NewFuncNode("slow", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		<-release // do not write until the test allows it (after the run returns)
		env.SetVar("late", "written-after-timeout")
		close(wrote)
		return env, nil
	}))
	g.SetEntry("slow")

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.NodeTimeout = 30 * time.Millisecond

	inputEnv := core.NewEnvelope()
	_, err := rt.Run(context.Background(), g, inputEnv, opts)
	if err == nil {
		t.Fatal("expected a node timeout error, got nil")
	}

	// Let the abandoned node proceed; it should write to its own clone.
	close(release)
	<-wrote

	if _, ok := inputEnv.GetVar("late"); ok {
		t.Error("abandoned node write leaked into the caller's envelope; clone isolation failed")
	}
}

func TestRuntime_Run_NodeTimeoutFastNodeSucceeds(t *testing.T) {
	g := graph.NewGraph("fast")
	g.AddNode(core.NewFuncNode("quick", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		env.SetVar("done", true)
		return env, nil
	}))
	g.SetEntry("quick")

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.NodeTimeout = 2 * time.Second

	result, err := rt.Run(context.Background(), g, core.NewEnvelope(), opts)
	if err != nil {
		t.Fatalf("unexpected error for a fast node under NodeTimeout: %v", err)
	}
	if v, ok := result.GetVar("done"); !ok || v != true {
		t.Error("expected the fast node's output to be adopted")
	}
}
