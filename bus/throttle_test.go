package bus

import (
	"sync"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/runtime"
)

func TestThrottle_NonDeltaPassThrough(t *testing.T) {
	var mu sync.Mutex
	var received []runtime.Event

	emitter := func(e runtime.Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	}

	te := NewThrottledEmitter(emitter, ThrottleConfig{
		CoalesceInterval: 50 * time.Millisecond,
	})
	defer te.Close()

	// Non-delta events should pass through immediately.
	e1 := runtime.NewEvent(runtime.EventNodeStarted, "run-1")
	e1.NodeID = "node-a"
	te.Emit(e1)

	e2 := runtime.NewEvent(runtime.EventNodeFinished, "run-1")
	e2.NodeID = "node-a"
	te.Emit(e2)

	e3 := runtime.NewEvent(runtime.EventRunStarted, "run-1")
	te.Emit(e3)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 3 {
		t.Fatalf("expected 3 events, got %d", len(received))
	}
	if received[0].Kind != runtime.EventNodeStarted {
		t.Errorf("event 0: got kind %v, want %v", received[0].Kind, runtime.EventNodeStarted)
	}
	if received[1].Kind != runtime.EventNodeFinished {
		t.Errorf("event 1: got kind %v, want %v", received[1].Kind, runtime.EventNodeFinished)
	}
	if received[2].Kind != runtime.EventRunStarted {
		t.Errorf("event 2: got kind %v, want %v", received[2].Kind, runtime.EventRunStarted)
	}
}

func TestThrottle_DeltaCoalescing(t *testing.T) {
	var mu sync.Mutex
	var received []runtime.Event

	emitter := func(e runtime.Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	}

	te := NewThrottledEmitter(emitter, ThrottleConfig{
		CoalesceInterval: 100 * time.Millisecond,
	})

	// Emit several delta events for the same node rapidly.
	for i := 0; i < 10; i++ {
		e := runtime.NewEvent(runtime.EventNodeOutputDelta, "run-1")
		e.NodeID = "node-a"
		e = e.WithPayload("chunk", i)
		te.Emit(e)
	}

	// Wait less than the coalesce interval — nothing should have flushed yet.
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	countBefore := len(received)
	mu.Unlock()
	if countBefore != 0 {
		t.Errorf("expected 0 events before flush, got %d", countBefore)
	}

	// Wait for the coalesce interval to fire.
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	countAfter := len(received)
	mu.Unlock()

	// Only the latest delta per node should be flushed — exactly 1.
	if countAfter != 1 {
		t.Fatalf("expected 1 coalesced event, got %d", countAfter)
	}

	mu.Lock()
	lastPayload := received[0].Payload["chunk"]
	mu.Unlock()

	if lastPayload != 9 {
		t.Errorf("expected last chunk=9, got %v", lastPayload)
	}

	te.Close()
}

func TestThrottle_DeltaCoalescingPerNode(t *testing.T) {
	var mu sync.Mutex
	var received []runtime.Event

	emitter := func(e runtime.Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	}

	te := NewThrottledEmitter(emitter, ThrottleConfig{
		CoalesceInterval: 100 * time.Millisecond,
	})

	// Emit deltas for two different nodes.
	for i := 0; i < 5; i++ {
		ea := runtime.NewEvent(runtime.EventNodeOutputDelta, "run-1")
		ea.NodeID = "node-a"
		ea = ea.WithPayload("val", "a"+string(rune('0'+i)))
		te.Emit(ea)

		eb := runtime.NewEvent(runtime.EventNodeOutputDelta, "run-1")
		eb.NodeID = "node-b"
		eb = eb.WithPayload("val", "b"+string(rune('0'+i)))
		te.Emit(eb)
	}

	// Wait for flush.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Should receive exactly 2 events: one per node (the latest for each).
	if len(received) != 2 {
		t.Fatalf("expected 2 coalesced events (one per node), got %d", len(received))
	}

	// Build a map of nodeID -> payload val.
	nodeVals := make(map[string]string)
	for _, e := range received {
		nodeVals[e.NodeID] = e.Payload["val"].(string)
	}

	if nodeVals["node-a"] != "a4" {
		t.Errorf("node-a: got %q, want %q", nodeVals["node-a"], "a4")
	}
	if nodeVals["node-b"] != "b4" {
		t.Errorf("node-b: got %q, want %q", nodeVals["node-b"], "b4")
	}

	te.Close()
}

func TestThrottle_FlushOnClose(t *testing.T) {
	var mu sync.Mutex
	var received []runtime.Event

	emitter := func(e runtime.Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	}

	te := NewThrottledEmitter(emitter, ThrottleConfig{
		CoalesceInterval: 10 * time.Second, // very long interval
	})

	// Emit a delta — it should be pending.
	e := runtime.NewEvent(runtime.EventNodeOutputDelta, "run-1")
	e.NodeID = "node-x"
	e = e.WithPayload("data", "pending")
	te.Emit(e)

	// Close should flush the pending delta immediately.
	te.Close()

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("expected 1 flushed event on close, got %d", len(received))
	}
	if received[0].NodeID != "node-x" {
		t.Errorf("got NodeID %q, want %q", received[0].NodeID, "node-x")
	}
	if received[0].Payload["data"] != "pending" {
		t.Errorf("got data %v, want %q", received[0].Payload["data"], "pending")
	}
}

func TestThrottle_CloseIdempotent(t *testing.T) {
	emitter := func(e runtime.Event) {}

	te := NewThrottledEmitter(emitter, ThrottleConfig{
		CoalesceInterval: 50 * time.Millisecond,
	})

	// Calling Close multiple times should not panic.
	te.Close()
	te.Close()
}

func TestThrottle_DefaultCoalesceInterval(t *testing.T) {
	emitter := func(e runtime.Event) {}

	te := NewThrottledEmitter(emitter, ThrottleConfig{})
	defer te.Close()

	if te.interval != 100*time.Millisecond {
		t.Errorf("default interval = %v, want 100ms", te.interval)
	}
}

func TestThrottle_MixedDeltaAndNonDelta(t *testing.T) {
	var mu sync.Mutex
	var received []runtime.Event

	emitter := func(e runtime.Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	}

	te := NewThrottledEmitter(emitter, ThrottleConfig{
		CoalesceInterval: 100 * time.Millisecond,
	})

	// Emit a non-delta (passes through immediately).
	e1 := runtime.NewEvent(runtime.EventNodeStarted, "run-1")
	e1.NodeID = "node-a"
	te.Emit(e1)

	// Emit several deltas.
	for i := 0; i < 5; i++ {
		d := runtime.NewEvent(runtime.EventNodeOutputDelta, "run-1")
		d.NodeID = "node-a"
		d = d.WithPayload("i", i)
		te.Emit(d)
	}

	// Emit another non-delta (passes through immediately).
	e2 := runtime.NewEvent(runtime.EventNodeFinished, "run-1")
	e2.NodeID = "node-a"
	te.Emit(e2)

	// At this point, 2 non-delta events should have been received.
	mu.Lock()
	countImmediate := len(received)
	mu.Unlock()

	if countImmediate != 2 {
		t.Errorf("expected 2 immediate events, got %d", countImmediate)
	}

	// Close flushes the pending delta.
	te.Close()

	mu.Lock()
	defer mu.Unlock()

	// Total: 2 non-delta + 1 coalesced delta = 3.
	if len(received) != 3 {
		t.Fatalf("expected 3 total events, got %d", len(received))
	}

	if received[0].Kind != runtime.EventNodeStarted {
		t.Errorf("event 0: got %v, want %v", received[0].Kind, runtime.EventNodeStarted)
	}
	if received[1].Kind != runtime.EventNodeFinished {
		t.Errorf("event 1: got %v, want %v", received[1].Kind, runtime.EventNodeFinished)
	}
	if received[2].Kind != runtime.EventNodeOutputDelta {
		t.Errorf("event 2: got %v, want %v", received[2].Kind, runtime.EventNodeOutputDelta)
	}
	if received[2].Payload["i"] != 4 {
		t.Errorf("coalesced delta payload i=%v, want 4", received[2].Payload["i"])
	}
}
