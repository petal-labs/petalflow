package otel_test

import (
	"errors"
	"math"
	"sync/atomic"
	"testing"

	metricapi "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	petalotel "github.com/petal-labs/petalflow/otel"
)

func TestEventDropMetricsExportsCounters(t *testing.T) {
	reader, mp := newTestMeter()
	meter := mp.Meter("test-drops")

	var busDrops, channelDrops atomic.Uint64
	unregister, err := petalotel.NewEventDropMetrics(meter, map[string]func() uint64{
		"bus_subscriber":  func() uint64 { return busDrops.Load() },
		"runtime_channel": func() uint64 { return channelDrops.Load() },
	})
	if err != nil {
		t.Fatalf("NewEventDropMetrics: %v", err)
	}
	t.Cleanup(unregister)

	// Zero state is observed (continuous series for rate alerting).
	rm := collectMetrics(t, reader)
	dropped := findMetric(rm, "petalflow.events.dropped")
	if dropped == nil {
		t.Fatal("petalflow.events.dropped metric not found")
	}
	bySource := dropSumsBySource(t, dropped)
	if bySource["bus_subscriber"] != 0 || bySource["runtime_channel"] != 0 {
		t.Errorf("initial drop sums = %v, want zeros", bySource)
	}

	// Counters move and re-export on the next collection.
	busDrops.Store(3)
	channelDrops.Store(5)
	rm = collectMetrics(t, reader)
	dropped = findMetric(rm, "petalflow.events.dropped")
	if dropped == nil {
		t.Fatal("petalflow.events.dropped metric not found after increments")
	}
	bySource = dropSumsBySource(t, dropped)
	if bySource["bus_subscriber"] != 3 {
		t.Errorf("bus_subscriber drops = %v, want 3", bySource["bus_subscriber"])
	}
	if bySource["runtime_channel"] != 5 {
		t.Errorf("runtime_channel drops = %v, want 5", bySource["runtime_channel"])
	}

	// Unregistering stops further observations.
	unregister()
	busDrops.Store(100)
	rm = collectMetrics(t, reader)
	if dropped := findMetric(rm, "petalflow.events.dropped"); dropped != nil {
		for _, dp := range dropDataPoints(t, dropped) {
			if dp.Value != 3 {
				t.Errorf("metric still reporting after unregister: %+v", dp)
			}
		}
	}
}

func dropDataPoints(t *testing.T, m *metricdata.Metrics) []metricdata.DataPoint[int64] {
	t.Helper()
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64] (observable counter), got %T", m.Data)
	}
	return sum.DataPoints
}

func dropSumsBySource(t *testing.T, m *metricdata.Metrics) map[string]int64 {
	t.Helper()
	sums := map[string]int64{}
	for _, dp := range dropDataPoints(t, m) {
		source := ""
		for _, kv := range dp.Attributes.ToSlice() {
			if string(kv.Key) == "source" {
				source = kv.Value.AsString()
			}
		}
		sums[source] = dp.Value
	}
	return sums
}

func TestEventDropMetricsSaturatesOversizedCounters(t *testing.T) {
	reader, mp := newTestMeter()
	meter := mp.Meter("test-saturate")

	unregister, err := petalotel.NewEventDropMetrics(meter, map[string]func() uint64{
		"flood": func() uint64 { return math.MaxUint64 },
	})
	if err != nil {
		t.Fatalf("NewEventDropMetrics: %v", err)
	}
	t.Cleanup(unregister)

	rm := collectMetrics(t, reader)
	dropped := findMetric(rm, "petalflow.events.dropped")
	if dropped == nil {
		t.Fatal("petalflow.events.dropped metric not found")
	}
	points := dropDataPoints(t, dropped)
	if len(points) != 1 || points[0].Value != 1<<53 {
		t.Fatalf("drop points = %+v, want saturated %d", points, int64(1<<53))
	}
}

// failingDropMeter fails instrument creation.
type failingDropMeter struct {
	metricapi.Meter
}

func (m *failingDropMeter) Int64ObservableCounter(string, ...metricapi.Int64ObservableCounterOption) (metricapi.Int64ObservableCounter, error) {
	return nil, errors.New("instrument creation failed")
}

func TestNewEventDropMetricsPropagatesInstrumentErrors(t *testing.T) {
	if _, err := petalotel.NewEventDropMetrics(&failingDropMeter{}, map[string]func() uint64{
		"source": func() uint64 { return 0 },
	}); err == nil {
		t.Error("NewEventDropMetrics should fail when the counter cannot be created")
	}
}
