package bus

import (
	"context"

	"github.com/petal-labs/petalflow/runtime"
)

// EventStore persists events for replay.
type EventStore interface {
	// Append stores an event.
	Append(ctx context.Context, event runtime.Event) error

	// List returns events for a run, optionally filtered.
	// afterSeq: return events with Seq > afterSeq (0 means all)
	// limit: max events to return (0 means no limit)
	List(ctx context.Context, runID string, afterSeq uint64, limit int) ([]runtime.Event, error)

	// LatestSeq returns the highest Seq for a run (0 if no events).
	LatestSeq(ctx context.Context, runID string) (uint64, error)
}
