package bus

import (
	"sync"
	"time"

	"github.com/petal-labs/petalflow/runtime"
)

// ThrottleConfig controls the behavior of ThrottledEmitter.
type ThrottleConfig struct {
	// CoalesceInterval is how often to flush coalesced delta events.
	// Default: 100ms
	CoalesceInterval time.Duration
}

// ThrottledEmitter wraps a runtime.EventEmitter and coalesces high-frequency
// node.output.delta events. Non-delta events pass through immediately.
// Delta events are coalesced per node: only the latest delta for each node
// is kept within each coalesce interval. A background ticker flushes
// coalesced deltas at the configured interval.
type ThrottledEmitter struct {
	emit     runtime.EventEmitter
	interval time.Duration

	mu      sync.Mutex
	pending map[string]runtime.Event // nodeID -> latest delta event
	closed  bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewThrottledEmitter creates a new ThrottledEmitter that wraps the given
// emitter and coalesces EventNodeOutputDelta events at the configured interval.
func NewThrottledEmitter(emit runtime.EventEmitter, cfg ThrottleConfig) *ThrottledEmitter {
	interval := cfg.CoalesceInterval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}

	te := &ThrottledEmitter{
		emit:     emit,
		interval: interval,
		pending:  make(map[string]runtime.Event),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}

	go te.run()

	return te
}

// Emit sends an event through the throttled emitter. Non-delta events pass
// through immediately to the wrapped emitter. Delta events (EventNodeOutputDelta)
// are coalesced: only the latest delta per node is kept and flushed at the
// configured interval.
func (te *ThrottledEmitter) Emit(e runtime.Event) {
	if e.Kind != runtime.EventNodeOutputDelta {
		// Non-delta events pass through immediately.
		te.emit(e)
		return
	}

	// Delta events are coalesced per NodeID.
	te.mu.Lock()
	defer te.mu.Unlock()

	if te.closed {
		return
	}

	te.pending[e.NodeID] = e
}

// Close flushes any pending delta events and stops the background ticker.
// It is safe to call Close multiple times.
func (te *ThrottledEmitter) Close() {
	te.mu.Lock()
	if te.closed {
		te.mu.Unlock()
		return
	}
	te.closed = true
	te.mu.Unlock()

	// Signal the background goroutine to stop.
	close(te.stopCh)

	// Wait for the background goroutine to finish.
	<-te.doneCh
}

// run is the background goroutine that periodically flushes coalesced deltas.
func (te *ThrottledEmitter) run() {
	defer close(te.doneCh)

	ticker := time.NewTicker(te.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			te.flush()
		case <-te.stopCh:
			// Flush any remaining pending events before exiting.
			te.flush()
			return
		}
	}
}

// flush sends all pending coalesced delta events to the wrapped emitter
// and clears the pending map.
func (te *ThrottledEmitter) flush() {
	te.mu.Lock()
	if len(te.pending) == 0 {
		te.mu.Unlock()
		return
	}

	// Swap out the pending map so we can release the lock during emission.
	toFlush := te.pending
	te.pending = make(map[string]runtime.Event)
	te.mu.Unlock()

	for _, e := range toFlush {
		te.emit(e)
	}
}
