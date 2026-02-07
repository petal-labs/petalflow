package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

func TestRuntime_Run_WithEventBus(t *testing.T) {
	b := bus.NewMemBus(bus.MemBusConfig{})
	defer b.Close()

	g := graph.NewGraph("bus-test")
	g.AddNode(core.NewNoopNode("start"))
	g.SetEntry("start")

	globalSub := b.SubscribeAll()
	defer globalSub.Close()

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.EventBus = b

	_, err := rt.Run(context.Background(), g, core.NewEnvelope(), opts)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Should receive events via bus
	count := 0
	for {
		select {
		case <-globalSub.Events():
			count++
		case <-time.After(100 * time.Millisecond):
			goto done
		}
	}
done:
	// run.started + node.started + node.finished + run.finished = 4
	if count < 4 {
		t.Errorf("received %d events via bus, want >= 4", count)
	}
}

func TestRuntime_Run_ContextEmitterInjected(t *testing.T) {
	// Verify that nodes receive an emitter via context
	var gotEmitter bool

	g := graph.NewGraph("emitter-test")
	g.AddNode(core.NewFuncNode("check", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		emit := runtime.EmitterFromContext(ctx)
		// The emitter should be non-nil (from runtime injection)
		emit(runtime.NewEvent(runtime.EventNodeOutput, env.Trace.RunID).WithNode("check", "noop"))
		gotEmitter = true
		return env, nil
	}))
	g.SetEntry("check")

	rt := runtime.NewRuntime()
	var events []runtime.Event
	opts := runtime.DefaultRunOptions()
	opts.EventHandler = func(e runtime.Event) {
		events = append(events, e)
	}

	_, err := rt.Run(context.Background(), g, core.NewEnvelope(), opts)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !gotEmitter {
		t.Error("node should have received emitter from context")
	}

	// The node.output event emitted via context should appear in events
	var hasNodeOutput bool
	for _, e := range events {
		if e.Kind == runtime.EventNodeOutput {
			hasNodeOutput = true
		}
	}
	if !hasNodeOutput {
		t.Error("expected node.output event from context emitter")
	}
}
