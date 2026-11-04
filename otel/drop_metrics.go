// Dropped-event observability.
//
// Events are delivered best-effort: the in-memory bus drops events when
// a subscriber's buffer is full (the consumer fell behind), and the
// runtime drops events when the per-run Events() channel is full. Both
// paths are drop-not-block by design so a slow consumer cannot stall
// execution. NewEventDropMetrics exports the cumulative drop counters
// as the petalflow.events.dropped metric (one bounded dimension: the
// fixed source name chosen by the caller) so drop rates can be alerted
// on. See docs/operations.md for semantics and alert guidance.

package otel

import (
	"context"
	"slices"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// maxObservableDrops saturates uint64 drop counters for the int64
// metric. It stays at or below 2^53 — the integer range that survives
// the SDK's float64-based aggregation without rounding — and is far
// beyond any realistic drop total.
const maxObservableDrops = 1 << 53

// NewEventDropMetrics registers the petalflow.events.dropped observable
// counter for the given drop sources. Each source maps a fixed,
// bounded dimension value (for example "bus_subscriber" or
// "runtime_channel") to a function returning its cumulative dropped
// count. Source names are caller-chosen constants: never per-run or
// per-subscriber identifiers (unbounded metric cardinality).
func NewEventDropMetrics(meter metric.Meter, sources map[string]func() uint64) (func(), error) {
	counter, err := meter.Int64ObservableCounter("petalflow.events.dropped",
		metric.WithDescription("Cumulative number of events dropped because a consumer fell behind"),
	)
	if err != nil {
		return nil, err
	}

	// Deterministic source order for stable callback output.
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	slices.Sort(names)

	registration, err := meter.RegisterCallback(func(_ context.Context, obs metric.Observer) error {
		for _, name := range names {
			// Always observe (including zero) so rate queries see a
			// continuous series for every configured source.
			dropped := sources[name]()
			if dropped > maxObservableDrops {
				// Saturate: beyond any realistic drop total, and within
				// the exact-integer range of the metric aggregation.
				dropped = maxObservableDrops
			}
			obs.ObserveInt64(counter, int64(dropped), // #nosec G115 -- clamped to maxObservableDrops above
				metric.WithAttributes(attribute.String("source", name)))
		}
		return nil
	}, counter)
	if err != nil {
		return nil, err
	}

	return func() { _ = registration.Unregister() }, nil
}
