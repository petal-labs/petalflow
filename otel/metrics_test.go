package otel_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/petal-labs/petalflow/core"
	petalotel "github.com/petal-labs/petalflow/otel"
	"github.com/petal-labs/petalflow/runtime"
)

// newTestMeter returns a meter backed by a manual reader for collecting metrics in tests.
func newTestMeter() (*metric.ManualReader, *metric.MeterProvider) {
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	return reader, mp
}

// collectMetrics reads all metrics from the reader.
func collectMetrics(t *testing.T, reader *metric.ManualReader) *metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}
	return &rm
}

// findMetric searches for a metric by name in the collected data.
func findMetric(rm *metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for _, scope := range rm.ScopeMetrics {
		for i := range scope.Metrics {
			if scope.Metrics[i].Name == name {
				return &scope.Metrics[i]
			}
		}
	}
	return nil
}

func TestMetricsHandler_NodeFinishedIncrementsCounterAndRecordsHistogram(t *testing.T) {
	reader, mp := newTestMeter()
	meter := mp.Meter("test")

	h, err := petalotel.NewMetricsHandler(meter)
	if err != nil {
		t.Fatalf("NewMetricsHandler: %v", err)
	}

	now := time.Now()

	// Emit node.finished
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeFinished,
		RunID:    "run-1",
		NodeID:   "node-a",
		NodeKind: core.NodeKindLLM,
		Time:     now,
		Elapsed:  150 * time.Millisecond,
	})

	// Emit another node.finished with different node
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeFinished,
		RunID:    "run-1",
		NodeID:   "node-b",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(100 * time.Millisecond),
		Elapsed:  50 * time.Millisecond,
	})

	rm := collectMetrics(t, reader)

	// Check petalflow.node.executions counter
	execMetric := findMetric(rm, "petalflow.node.executions")
	if execMetric == nil {
		t.Fatal("petalflow.node.executions metric not found")
	}
	sumData, ok := execMetric.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64] data, got %T", execMetric.Data)
	}
	// Should have 2 data points (one per node)
	if len(sumData.DataPoints) != 2 {
		t.Fatalf("expected 2 data points, got %d", len(sumData.DataPoints))
	}
	// Each should have value 1
	for _, dp := range sumData.DataPoints {
		if dp.Value != 1 {
			t.Errorf("expected counter value 1, got %d", dp.Value)
		}
	}

	// Check petalflow.node.duration histogram
	durMetric := findMetric(rm, "petalflow.node.duration")
	if durMetric == nil {
		t.Fatal("petalflow.node.duration metric not found")
	}
	histData, ok := durMetric.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("expected Histogram[float64] data, got %T", durMetric.Data)
	}
	if len(histData.DataPoints) != 2 {
		t.Fatalf("expected 2 histogram data points, got %d", len(histData.DataPoints))
	}
	// Verify at least one data point has count > 0
	for _, dp := range histData.DataPoints {
		if dp.Count != 1 {
			t.Errorf("expected histogram count 1, got %d", dp.Count)
		}
	}
}

func TestMetricsHandler_NodeFailedIncrementsFailureCounter(t *testing.T) {
	reader, mp := newTestMeter()
	meter := mp.Meter("test")

	h, err := petalotel.NewMetricsHandler(meter)
	if err != nil {
		t.Fatalf("NewMetricsHandler: %v", err)
	}

	now := time.Now()

	// Emit node.failed
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeFailed,
		RunID:    "run-1",
		NodeID:   "node-fail",
		NodeKind: core.NodeKindLLM,
		Time:     now,
		Elapsed:  10 * time.Millisecond,
		Payload:  map[string]any{"error": "timeout"},
	})

	// Emit another failure for the same node
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeFailed,
		RunID:    "run-1",
		NodeID:   "node-fail",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(100 * time.Millisecond),
		Elapsed:  20 * time.Millisecond,
		Payload:  map[string]any{"error": "timeout again"},
	})

	rm := collectMetrics(t, reader)

	failMetric := findMetric(rm, "petalflow.node.failures")
	if failMetric == nil {
		t.Fatal("petalflow.node.failures metric not found")
	}
	sumData, ok := failMetric.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64] data, got %T", failMetric.Data)
	}
	if len(sumData.DataPoints) != 1 {
		t.Fatalf("expected 1 data point (same attributes), got %d", len(sumData.DataPoints))
	}
	if sumData.DataPoints[0].Value != 2 {
		t.Errorf("expected failure counter value 2, got %d", sumData.DataPoints[0].Value)
	}

	// Verify node_kind attribute
	nodeKindFound := false
	for _, attr := range sumData.DataPoints[0].Attributes.ToSlice() {
		if string(attr.Key) == "node_kind" && attr.Value.AsString() == "llm" {
			nodeKindFound = true
		}
	}
	if !nodeKindFound {
		t.Error("expected node_kind attribute on failure counter")
	}
}

func TestMetricsHandler_RunFinishedRecordsWorkflowDuration(t *testing.T) {
	reader, mp := newTestMeter()
	meter := mp.Meter("test")

	h, err := petalotel.NewMetricsHandler(meter)
	if err != nil {
		t.Fatalf("NewMetricsHandler: %v", err)
	}

	now := time.Now()

	// Emit run.finished
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-1",
		Time:    now,
		Elapsed: 2 * time.Second,
		Payload: map[string]any{"status": "completed"},
	})

	rm := collectMetrics(t, reader)

	runDurMetric := findMetric(rm, "petalflow.run.duration")
	if runDurMetric == nil {
		t.Fatal("petalflow.run.duration metric not found")
	}
	histData, ok := runDurMetric.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("expected Histogram[float64] data, got %T", runDurMetric.Data)
	}
	if len(histData.DataPoints) != 1 {
		t.Fatalf("expected 1 histogram data point, got %d", len(histData.DataPoints))
	}
	dp := histData.DataPoints[0]
	if dp.Count != 1 {
		t.Errorf("expected histogram count 1, got %d", dp.Count)
	}
	if dp.Sum != 2.0 {
		t.Errorf("expected histogram sum 2.0 (seconds), got %f", dp.Sum)
	}

	// Verify run_id attribute
	runIDFound := false
	for _, attr := range dp.Attributes.ToSlice() {
		if string(attr.Key) == "run_id" && attr.Value.AsString() == "run-1" {
			runIDFound = true
		}
	}
	if !runIDFound {
		t.Error("expected run_id attribute on run duration histogram")
	}
}

func TestMetricsHandler_IgnoresIrrelevantEvents(t *testing.T) {
	reader, mp := newTestMeter()
	meter := mp.Meter("test")

	h, err := petalotel.NewMetricsHandler(meter)
	if err != nil {
		t.Fatalf("NewMetricsHandler: %v", err)
	}

	now := time.Now()

	// Send events that should be ignored by the metrics handler
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "g"},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeStarted,
		RunID:    "run-1",
		NodeID:   "n1",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(1 * time.Millisecond),
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventToolCall,
		RunID:    "run-1",
		NodeID:   "n1",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(2 * time.Millisecond),
		Payload:  map[string]any{"tool": "search"},
	})

	rm := collectMetrics(t, reader)

	// Should have no metrics recorded (all events were irrelevant)
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range data.DataPoints {
					if dp.Value != 0 {
						t.Errorf("expected no metrics recorded, but %s has value %d", m.Name, dp.Value)
					}
				}
			case metricdata.Histogram[float64]:
				for _, dp := range data.DataPoints {
					if dp.Count != 0 {
						t.Errorf("expected no metrics recorded, but %s has count %d", m.Name, dp.Count)
					}
				}
			}
		}
	}
}

func TestMetricsHandler_FullLifecycle(t *testing.T) {
	reader, mp := newTestMeter()
	meter := mp.Meter("test")

	h, err := petalotel.NewMetricsHandler(meter)
	if err != nil {
		t.Fatalf("NewMetricsHandler: %v", err)
	}

	now := time.Now()

	events := []runtime.Event{
		{Kind: runtime.EventRunStarted, RunID: "r1", Time: now, Payload: map[string]any{"graph": "pipeline"}},
		{Kind: runtime.EventNodeStarted, RunID: "r1", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(1 * time.Millisecond)},
		{Kind: runtime.EventNodeFinished, RunID: "r1", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(100 * time.Millisecond), Elapsed: 99 * time.Millisecond},
		{Kind: runtime.EventNodeStarted, RunID: "r1", NodeID: "n2", NodeKind: core.NodeKindTool, Time: now.Add(101 * time.Millisecond)},
		{Kind: runtime.EventNodeFailed, RunID: "r1", NodeID: "n2", NodeKind: core.NodeKindTool, Time: now.Add(120 * time.Millisecond), Elapsed: 19 * time.Millisecond, Payload: map[string]any{"error": "boom"}},
		{Kind: runtime.EventRunFinished, RunID: "r1", Time: now.Add(200 * time.Millisecond), Elapsed: 200 * time.Millisecond, Payload: map[string]any{"status": "failed"}},
	}

	for _, e := range events {
		h.Handle(e)
	}

	rm := collectMetrics(t, reader)

	// node.executions should have 1 data point (only n1 finished successfully)
	execMetric := findMetric(rm, "petalflow.node.executions")
	if execMetric == nil {
		t.Fatal("petalflow.node.executions not found")
	}
	sumData, ok := execMetric.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64], got %T", execMetric.Data)
	}
	if len(sumData.DataPoints) != 1 {
		t.Fatalf("expected 1 execution data point, got %d", len(sumData.DataPoints))
	}
	if sumData.DataPoints[0].Value != 1 {
		t.Errorf("expected 1 execution, got %d", sumData.DataPoints[0].Value)
	}

	// node.failures should have 1 data point (n2 failed)
	failMetric := findMetric(rm, "petalflow.node.failures")
	if failMetric == nil {
		t.Fatal("petalflow.node.failures not found")
	}
	failSum, ok := failMetric.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64], got %T", failMetric.Data)
	}
	if len(failSum.DataPoints) != 1 {
		t.Fatalf("expected 1 failure data point, got %d", len(failSum.DataPoints))
	}
	if failSum.DataPoints[0].Value != 1 {
		t.Errorf("expected 1 failure, got %d", failSum.DataPoints[0].Value)
	}

	// run.duration should have 1 data point
	runDurMetric := findMetric(rm, "petalflow.run.duration")
	if runDurMetric == nil {
		t.Fatal("petalflow.run.duration not found")
	}
	histData, ok := runDurMetric.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("expected Histogram[float64], got %T", runDurMetric.Data)
	}
	if len(histData.DataPoints) != 1 {
		t.Fatalf("expected 1 run duration data point, got %d", len(histData.DataPoints))
	}
	if histData.DataPoints[0].Count != 1 {
		t.Errorf("expected 1 run duration recorded, got %d", histData.DataPoints[0].Count)
	}
	// 200ms = 0.2s
	if histData.DataPoints[0].Sum != 0.2 {
		t.Errorf("expected run duration sum 0.2s, got %f", histData.DataPoints[0].Sum)
	}
}
