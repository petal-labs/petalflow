package runtime

import (
	"context"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
)

// TestDroppedEventsTotalCountsProcessWideChannelDrops fills the
// per-run event channel (no Events() reader) and verifies the
// process-level counter rises — the counter exported as the
// petalflow.events.dropped metric.
func TestDroppedEventsTotalCountsProcessWideChannelDrops(t *testing.T) {
	before := DroppedEventsTotal()

	// A node that floods the emitter: the runtime's event channel holds
	// 100 events, so a few hundred emissions with no Events() reader
	// force drops.
	g := graph.NewGraph("drop-flood")
	g.AddNode(core.NewFuncNode("flood", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		emit := EmitterFromContext(ctx)
		for i := 0; i < 300; i++ {
			emit(NewEvent(EventNodeOutput, env.Trace.RunID).WithPayload("i", i))
		}
		return env, nil
	}))
	g.SetEntry("flood")

	if _, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), DefaultRunOptions()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if after := DroppedEventsTotal(); after <= before {
		t.Errorf("DroppedEventsTotal did not increase: before %d after %d", before, after)
	}
}
