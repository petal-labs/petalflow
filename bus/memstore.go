package bus

import (
	"context"
	"sync"

	"github.com/petal-labs/petalflow/runtime"
)

// MemEventStore is a thread-safe in-memory event store.
type MemEventStore struct {
	mu     sync.RWMutex
	events map[string][]runtime.Event // runID -> events
}

// NewMemEventStore creates a new in-memory event store.
func NewMemEventStore() *MemEventStore {
	return &MemEventStore{
		events: make(map[string][]runtime.Event),
	}
}

func (s *MemEventStore) Append(_ context.Context, event runtime.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[event.RunID] = append(s.events[event.RunID], event)
	return nil
}

func (s *MemEventStore) List(_ context.Context, runID string, afterSeq uint64, limit int) ([]runtime.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := s.events[runID]
	var result []runtime.Event

	for _, e := range all {
		if afterSeq > 0 && e.Seq <= afterSeq {
			continue
		}
		result = append(result, e)
		if limit > 0 && len(result) >= limit {
			break
		}
	}

	return result, nil
}

func (s *MemEventStore) LatestSeq(_ context.Context, runID string) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.events[runID]
	if len(events) == 0 {
		return 0, nil
	}

	var maxSeq uint64
	for _, e := range events {
		if e.Seq > maxSeq {
			maxSeq = e.Seq
		}
	}
	return maxSeq, nil
}

// Compile-time interface check.
var _ EventStore = (*MemEventStore)(nil)
