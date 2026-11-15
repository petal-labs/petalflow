package otel

import (
	"context"

	"go.opentelemetry.io/otel/metric"

	"github.com/petal-labs/petalflow/artifact"
)

// ArtifactGCMetrics exports bounded aggregate artifact lifecycle counters.
type ArtifactGCMetrics struct {
	candidates metric.Int64Counter
	deletions  metric.Int64Counter
	bytes      metric.Int64Counter
	errors     metric.Int64Counter
}

// NewArtifactGCMetrics creates artifact lifecycle metric instruments.
func NewArtifactGCMetrics(meter metric.Meter) (*ArtifactGCMetrics, error) {
	candidates, err := meter.Int64Counter("petalflow.artifacts.gc.candidates",
		metric.WithDescription("Number of expired artifact candidates examined"))
	if err != nil {
		return nil, err
	}
	deletions, err := meter.Int64Counter("petalflow.artifacts.gc.deletions",
		metric.WithDescription("Number of expired unreferenced artifacts deleted"))
	if err != nil {
		return nil, err
	}
	bytes, err := meter.Int64Counter("petalflow.artifacts.gc.bytes",
		metric.WithDescription("Artifact bytes deleted by garbage collection"), metric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	errors, err := meter.Int64Counter("petalflow.artifacts.gc.errors",
		metric.WithDescription("Number of artifact garbage-collection errors"))
	if err != nil {
		return nil, err
	}
	return &ArtifactGCMetrics{candidates: candidates, deletions: deletions, bytes: bytes, errors: errors}, nil
}

// ObserveArtifactGC records one aggregate collection result.
func (m *ArtifactGCMetrics) ObserveArtifactGC(ctx context.Context, observation artifact.GCObservation) {
	m.candidates.Add(ctx, observation.Candidates)
	m.deletions.Add(ctx, observation.Deleted)
	m.bytes.Add(ctx, observation.BytesDeleted)
	m.errors.Add(ctx, observation.Errors)
}
