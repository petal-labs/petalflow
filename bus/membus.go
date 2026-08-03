package bus

import (
	"sync"
	"sync/atomic"

	"github.com/petal-labs/petalflow/runtime"
)

// MemBusConfig configures an in-memory event bus.
type MemBusConfig struct {
	// SubscriberBufferSize is the channel buffer size per subscriber (default: 256).
	SubscriberBufferSize int
}

// MemBus is an in-memory event bus implementation.
type MemBus struct {
	mu         sync.RWMutex
	subs       map[string][]*memSub // runID -> subscribers
	globalSubs []*memSub            // subscribers for all runs
	bufSize    int
	closed     bool
}

// NewMemBus creates a new in-memory event bus with the given configuration.
func NewMemBus(config MemBusConfig) *MemBus {
	bufSize := config.SubscriberBufferSize
	if bufSize <= 0 {
		bufSize = 256
	}
	return &MemBus{
		subs:    make(map[string][]*memSub),
		bufSize: bufSize,
	}
}

// Publish sends an event to all matching subscribers.
// Run-specific subscribers receive events matching their run ID,
// and global subscribers receive all events. If the bus is closed,
// the event is silently dropped.
func (b *MemBus) Publish(event runtime.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return
	}

	// Send to run-specific subscribers.
	for _, sub := range b.subs[event.RunID] {
		sub.send(event)
	}

	// Send to global subscribers.
	for _, sub := range b.globalSubs {
		sub.send(event)
	}
}

// Subscribe registers a subscriber for a specific run.
// Returns a Subscription that must be closed when done.
func (b *MemBus) Subscribe(runID string) Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub := newMemSub(b.bufSize)
	sub.bus = b
	sub.runID = runID
	b.subs[runID] = append(b.subs[runID], sub)
	return sub
}

// SubscribeAll registers a subscriber that receives events from all runs.
// Returns a Subscription that must be closed when done.
func (b *MemBus) SubscribeAll() Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub := newMemSub(b.bufSize)
	sub.bus = b
	sub.global = true
	b.globalSubs = append(b.globalSubs, sub)
	return sub
}

// Close shuts down the bus and all active subscriptions.
func (b *MemBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true

	// Close every subscription's channel and drop all registrations. We close
	// channels directly (markClosed) rather than via sub.close(), which would
	// re-acquire b.mu to deregister and deadlock while we hold it here.
	for _, subs := range b.subs {
		for _, sub := range subs {
			sub.markClosed()
		}
	}
	for _, sub := range b.globalSubs {
		sub.markClosed()
	}

	b.subs = make(map[string][]*memSub)
	b.globalSubs = nil

	return nil
}

// remove deregisters a subscription from the bus. It is called from
// memSub.close, which must not hold the subscription lock (Publish takes the
// bus lock and then the subscription lock, so the reverse order here would
// risk a deadlock).
func (b *MemBus) remove(sub *memSub) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if sub.global {
		b.globalSubs = removeSub(b.globalSubs, sub)
		return
	}

	remaining := removeSub(b.subs[sub.runID], sub)
	if len(remaining) == 0 {
		delete(b.subs, sub.runID)
	} else {
		b.subs[sub.runID] = remaining
	}
}

// removeSub returns subs with the first occurrence of target removed.
func removeSub(subs []*memSub, target *memSub) []*memSub {
	for i, s := range subs {
		if s == target {
			return append(subs[:i], subs[i+1:]...)
		}
	}
	return subs
}

// memSub is an in-memory subscription.
type memSub struct {
	ch      chan runtime.Event
	mu      sync.Mutex
	closed  bool
	dropped atomic.Uint64

	// bus, runID, and global identify where this subscription is registered so
	// it can deregister itself on Close. Set once at subscription time.
	bus    *MemBus
	runID  string
	global bool
}

func newMemSub(bufSize int) *memSub {
	return &memSub{
		ch: make(chan runtime.Event, bufSize),
	}
}

// Events returns a channel of events for this subscription.
func (s *memSub) Events() <-chan runtime.Event {
	return s.ch
}

// Close unsubscribes and releases resources.
func (s *memSub) Close() error {
	s.close()
	return nil
}

// close closes the subscription's channel and deregisters it from the bus.
// Deregistration happens after releasing the subscription lock so it does not
// invert the bus/subscription lock order used by Publish.
func (s *memSub) close() {
	s.markClosed()
	if s.bus != nil {
		s.bus.remove(s)
	}
}

// markClosed closes the channel, guarded against double-close, without
// deregistering from the bus.
func (s *memSub) markClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}

// send delivers an event to the subscription's channel.
// If the channel is full or the subscription is closed, the event is dropped.
func (s *memSub) send(event runtime.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	select {
	case s.ch <- event:
	default:
		// Buffer full: drop the event and record it for observability.
		s.dropped.Add(1)
	}
}

// Dropped returns the cumulative number of events dropped for this subscription
// because its buffer was full.
func (s *memSub) Dropped() uint64 {
	return s.dropped.Load()
}

// Compile-time interface checks.
var _ EventBus = (*MemBus)(nil)
var _ Subscription = (*memSub)(nil)
