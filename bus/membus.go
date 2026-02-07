package bus

import (
	"sync"

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
	b.subs[runID] = append(b.subs[runID], sub)
	return sub
}

// SubscribeAll registers a subscriber that receives events from all runs.
// Returns a Subscription that must be closed when done.
func (b *MemBus) SubscribeAll() Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub := newMemSub(b.bufSize)
	b.globalSubs = append(b.globalSubs, sub)
	return sub
}

// Close shuts down the bus and all active subscriptions.
func (b *MemBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true

	// Close all run-specific subscriptions.
	for _, subs := range b.subs {
		for _, sub := range subs {
			sub.close()
		}
	}

	// Close all global subscriptions.
	for _, sub := range b.globalSubs {
		sub.close()
	}

	return nil
}

// memSub is an in-memory subscription.
type memSub struct {
	ch     chan runtime.Event
	mu     sync.Mutex
	closed bool
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

// close performs the actual channel close, guarded against double-close.
func (s *memSub) close() {
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
		// Drop if channel full.
	}
}

// Compile-time interface checks.
var _ EventBus = (*MemBus)(nil)
var _ Subscription = (*memSub)(nil)
