package bus

import (
	"sync"
	"testing"

	"github.com/petal-labs/petalflow/runtime"
)

func runSubscriberCount(b *MemBus, runID string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[runID])
}

func globalSubscriberCount(b *MemBus) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.globalSubs)
}

func TestMemBus_Close_DeregistersRunSubscriber(t *testing.T) {
	b := NewMemBus(MemBusConfig{})
	sub := b.Subscribe("run-1")
	if got := runSubscriberCount(b, "run-1"); got != 1 {
		t.Fatalf("expected 1 subscriber before close, got %d", got)
	}

	if err := sub.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if got := runSubscriberCount(b, "run-1"); got != 0 {
		t.Errorf("expected subscriber deregistered after Close, got %d", got)
	}
	b.mu.RLock()
	_, exists := b.subs["run-1"]
	b.mu.RUnlock()
	if exists {
		t.Error("expected the run-1 key to be deleted once empty")
	}
}

func TestMemBus_Close_DeregistersGlobalSubscriber(t *testing.T) {
	b := NewMemBus(MemBusConfig{})
	sub := b.SubscribeAll()
	if got := globalSubscriberCount(b); got != 1 {
		t.Fatalf("expected 1 global subscriber before close, got %d", got)
	}

	if err := sub.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if got := globalSubscriberCount(b); got != 0 {
		t.Errorf("expected global subscriber deregistered after Close, got %d", got)
	}
}

func TestMemBus_Close_RemovesOnlyClosedSubscriber(t *testing.T) {
	b := NewMemBus(MemBusConfig{})
	s1 := b.Subscribe("run-1")
	s2 := b.Subscribe("run-1")

	if err := s1.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if got := runSubscriberCount(b, "run-1"); got != 1 {
		t.Fatalf("expected 1 remaining subscriber, got %d", got)
	}

	// s2 must still receive published events.
	b.Publish(runtime.Event{RunID: "run-1"})
	select {
	case _, ok := <-s2.Events():
		if !ok {
			t.Error("s2 channel closed unexpectedly")
		}
	default:
		t.Error("s2 should still receive events after s1 closed")
	}
}

func TestMemBus_ConcurrentSubscribeCloseAndPublish(t *testing.T) {
	b := NewMemBus(MemBusConfig{})

	done := make(chan struct{})
	var pub sync.WaitGroup
	pub.Add(1)
	go func() {
		defer pub.Done()
		for {
			select {
			case <-done:
				return
			default:
				b.Publish(runtime.Event{RunID: "r"})
			}
		}
	}()

	var churn sync.WaitGroup
	for i := 0; i < 100; i++ {
		churn.Add(1)
		go func() {
			defer churn.Done()
			s := b.Subscribe("r")
			s.Close()
		}()
	}
	churn.Wait()
	close(done)
	pub.Wait()

	if got := runSubscriberCount(b, "r"); got != 0 {
		t.Errorf("expected 0 remaining subscribers after churn, got %d", got)
	}
}
