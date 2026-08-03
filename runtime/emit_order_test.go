package runtime_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

// TestRuntime_Run_ParallelEmitSerializedAndOrdered verifies that events are
// delivered to the EventHandler serially and in monotonic Seq order even under
// concurrent execution. The handler appends without a lock on purpose: that is
// safe only if emit serializes handler invocation. Run under `go test -race`,
// unserialized emit is a data race; and out-of-order delivery breaks the
// strictly-increasing assertion.
func TestRuntime_Run_ParallelEmitSerializedAndOrdered(t *testing.T) {
	var seqs []uint64
	handler := func(e runtime.Event) {
		seqs = append(seqs, e.Seq)
	}

	g := graph.NewGraph("fanout")
	g.AddNode(core.NewNoopNode("start"))
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		g.AddNode(core.NewNoopNode(id))
		g.AddEdge("start", id)
	}
	g.SetEntry("start")

	rt := runtime.NewRuntime()
	opts := runtime.DefaultRunOptions()
	opts.Concurrency = 6
	opts.EventHandler = handler

	if _, err := rt.Run(context.Background(), g, core.NewEnvelope(), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(seqs) < 2 {
		t.Fatalf("expected multiple events, got %d", len(seqs))
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("event seqs not strictly increasing at index %d: %v", i, seqs)
		}
	}
}

func TestRuntime_Run_CountsDroppedEvents(t *testing.T) {
	// A long chain emits far more events than the (unread) event channel buffers.
	g := graph.NewGraph("chain")
	prev := ""
	for i := 0; i < 150; i++ {
		id := fmt.Sprintf("n%d", i)
		g.AddNode(core.NewNoopNode(id))
		if prev != "" {
			g.AddEdge(prev, id)
		}
		prev = id
	}
	g.SetEntry("n0")

	rt := runtime.NewRuntime()
	if _, err := rt.Run(context.Background(), g, core.NewEnvelope(), runtime.DefaultRunOptions()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rt.DroppedEvents() == 0 {
		t.Error("expected DroppedEvents > 0 when the event channel is not drained")
	}
}
