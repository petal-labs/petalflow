package bus

import (
	"context"
	"log/slog"

	"github.com/petal-labs/petalflow/runtime"
)

// StoreSubscriber writes events to an EventStore.
// It implements EventHandler semantics for use as a bus subscriber handler.
type StoreSubscriber struct {
	store  EventStore
	logger *slog.Logger
}

// NewStoreSubscriber creates a new StoreSubscriber.
func NewStoreSubscriber(store EventStore, logger *slog.Logger) *StoreSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &StoreSubscriber{
		store:  store,
		logger: logger,
	}
}

// Handle persists a single event to the store.
func (s *StoreSubscriber) Handle(event runtime.Event) {
	if err := s.store.Append(context.Background(), event); err != nil {
		s.logger.Error("failed to persist event",
			"run_id", event.RunID,
			"kind", event.Kind,
			"seq", event.Seq,
			"error", err,
		)
	}
}
