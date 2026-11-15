package otel_test

import (
	"context"
	"testing"

	"github.com/petal-labs/petalflow/artifact"
	petalotel "github.com/petal-labs/petalflow/otel"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestArtifactGCMetricsRecordsCountsBytesAndErrors(t *testing.T) {
	reader, provider := newTestMeter()
	observer, err := petalotel.NewArtifactGCMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("NewArtifactGCMetrics: %v", err)
	}
	observer.ObserveArtifactGC(context.Background(), artifact.GCObservation{
		Candidates: 5, Deleted: 2, Retained: 3, BytesDeleted: 4096, Errors: 1,
	})

	metrics := collectMetrics(t, reader)
	for name, want := range map[string]int64{
		"petalflow.artifacts.gc.candidates": 5,
		"petalflow.artifacts.gc.deletions":  2,
		"petalflow.artifacts.gc.bytes":      4096,
		"petalflow.artifacts.gc.errors":     1,
	} {
		metric := findMetric(metrics, name)
		if metric == nil {
			t.Fatalf("metric %s not found", name)
		}
		sum, ok := metric.Data.(metricdata.Sum[int64])
		if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != want {
			t.Fatalf("metric %s = %#v, want %d", name, metric.Data, want)
		}
	}
}
