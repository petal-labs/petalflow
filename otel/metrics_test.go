package otel_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
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

	// Tenant context from run.started is attributed to the run and its
	// node metrics.
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "g", "tenant_id": "tenant-a"},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeFinished,
		RunID:    "run-1",
		NodeID:   "n1",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(time.Millisecond),
		Elapsed:  time.Millisecond,
	})

	// Emit run.finished
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-1",
		Time:    now,
		Elapsed: 2 * time.Second,
		Payload: map[string]any{
			"status":      "completed",
			"tenant_id":   "tenant-a",
			"workflow_id": "wf-1",
			"trigger":     "api",
		},
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

	// Bounded run attributes survive.
	attrMap := attributeMap(dp.Attributes)
	if attrMap["tenant_id"] != "tenant-a" {
		t.Errorf("tenant_id = %q, want tenant-a", attrMap["tenant_id"])
	}
	if attrMap["workflow_id"] != "wf-1" {
		t.Errorf("workflow_id = %q, want wf-1", attrMap["workflow_id"])
	}
	if attrMap["trigger"] != "api" {
		t.Errorf("trigger = %q, want api", attrMap["trigger"])
	}

	// Cardinality rule: no per-run identifiers on metric attributes.
	for _, forbidden := range []string{"run_id", "node_id", "schedule_id", "webhook_trigger_id"} {
		if _, present := attrMap[forbidden]; present {
			t.Errorf("run duration metric carries unbounded attribute %q", forbidden)
		}
	}

	// Node metrics carry the tenant dimension too.
	nodeMetric := findMetric(rm, "petalflow.node.executions")
	if nodeMetric == nil {
		t.Fatal("petalflow.node.executions not found")
	}
	nodeSum, ok := nodeMetric.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64], got %T", nodeMetric.Data)
	}
	if len(nodeSum.DataPoints) != 1 {
		t.Fatalf("expected 1 node execution data point, got %d", len(nodeSum.DataPoints))
	}
	nodeAttrs := attributeMap(nodeSum.DataPoints[0].Attributes)
	if nodeAttrs["tenant_id"] != "tenant-a" {
		t.Errorf("node execution tenant_id = %q, want tenant-a", nodeAttrs["tenant_id"])
	}
	if nodeAttrs["node_kind"] != "llm" {
		t.Errorf("node execution node_kind = %q, want llm", nodeAttrs["node_kind"])
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

// attributeMap converts an attribute set into a plain map for assertions.
func attributeMap(attrs attribute.Set) map[string]string {
	out := map[string]string{}
	for _, kv := range attrs.ToSlice() {
		out[string(kv.Key)] = kv.Value.AsString()
	}
	return out
}

func TestMetricsHandler_LLMResponseGenAIMetrics(t *testing.T) {
	reader, mp := newTestMeter()
	meter := mp.Meter("test")

	h, err := petalotel.NewMetricsHandler(meter)
	if err != nil {
		t.Fatalf("NewMetricsHandler: %v", err)
	}

	now := time.Now()

	// A successful LLM call with usage.
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"tenant_id": "tenant-a"},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventLLMResponse,
		RunID:    "run-1",
		NodeID:   "llm-1",
		NodeKind: core.NodeKindLLM,
		Time:     now,
		Payload: map[string]any{
			"status":        "success",
			"model":         "test-model",
			"provider":      "openai",
			"latency_ms":    1500,
			"input_tokens":  10,
			"output_tokens": 5,
		},
	})

	rm := collectMetrics(t, reader)

	tokens := findMetric(rm, "gen_ai.client.token.usage")
	if tokens == nil {
		t.Fatal("gen_ai.client.token.usage metric not found")
	}
	tokenData, ok := tokens.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("expected Histogram[float64], got %T", tokens.Data)
	}
	byType := map[string]float64{}
	for _, dp := range tokenData.DataPoints {
		attrs := attributeMap(dp.Attributes)
		if attrs["gen_ai.token.type"] == "" || attrs["gen_ai.operation.name"] != "chat" {
			t.Errorf("token usage data point missing GenAI attributes: %v", attrs)
		}
		if attrs["gen_ai.request.model"] != "test-model" || attrs["gen_ai.provider.name"] != "openai" {
			t.Errorf("token usage model/provider attributes wrong: %v", attrs)
		}
		byType[attrs["gen_ai.token.type"]] = dp.Sum
	}
	if byType["input"] != 10 || byType["output"] != 5 {
		t.Errorf("token sums = input %v output %v, want 10 and 5", byType["input"], byType["output"])
	}

	duration := findMetric(rm, "gen_ai.client.operation.duration")
	if duration == nil {
		t.Fatal("gen_ai.client.operation.duration metric not found")
	}
	durData, ok := duration.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("expected Histogram[float64], got %T", duration.Data)
	}
	if len(durData.DataPoints) != 1 {
		t.Fatalf("expected 1 duration data point, got %d", len(durData.DataPoints))
	}
	durAttrs := attributeMap(durData.DataPoints[0].Attributes)
	if durAttrs["tenant_id"] != "tenant-a" {
		t.Errorf("duration tenant_id = %q, want tenant-a", durAttrs["tenant_id"])
	}
	if durData.DataPoints[0].Sum != 1.5 {
		t.Errorf("duration sum = %f, want 1.5s", durData.DataPoints[0].Sum)
	}
}

func TestMetricsHandler_LLMResponseErrorDuration(t *testing.T) {
	reader, mp := newTestMeter()
	meter := mp.Meter("test")

	h, err := petalotel.NewMetricsHandler(meter)
	if err != nil {
		t.Fatalf("NewMetricsHandler: %v", err)
	}

	h.Handle(runtime.Event{
		Kind:     runtime.EventLLMResponse,
		RunID:    "run-1",
		NodeID:   "llm-1",
		NodeKind: core.NodeKindLLM,
		Time:     time.Now(),
		Payload: map[string]any{
			"status":     "error",
			"model":      "test-model",
			"latency_ms": 100,
			"error_code": "deadline_exceeded",
		},
	})

	rm := collectMetrics(t, reader)
	duration := findMetric(rm, "gen_ai.client.operation.duration")
	if duration == nil {
		t.Fatal("gen_ai.client.operation.duration metric not found")
	}
	durData, ok := duration.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("expected Histogram[float64], got %T", duration.Data)
	}
	if len(durData.DataPoints) != 1 {
		t.Fatalf("expected 1 duration data point, got %d", len(durData.DataPoints))
	}
	attrs := attributeMap(durData.DataPoints[0].Attributes)
	if attrs["error.type"] != "deadline_exceeded" {
		t.Errorf("error.type = %q, want deadline_exceeded", attrs["error.type"])
	}

	// Failed calls record no token usage (the instrument may not appear
	// at all when it recorded no points).
	if tokens := findMetric(rm, "gen_ai.client.token.usage"); tokens != nil {
		tokenData, ok := tokens.Data.(metricdata.Histogram[float64])
		if !ok {
			t.Fatalf("expected Histogram[float64], got %T", tokens.Data)
		}
		for _, dp := range tokenData.DataPoints {
			if dp.Count != 0 {
				t.Errorf("failed call recorded token usage: count %d", dp.Count)
			}
		}
	}
}

// TestMetricsHandlerAttributeCardinality enforces the metric
// cardinality rules: no attribute can grow per run. run_id, node_id,
// schedule_id, and webhook_trigger_id must never appear on any metric
// attribute set, and every dimension outside the bounded allow-list
// fails the test.
func TestMetricsHandlerAttributeCardinality(t *testing.T) {
	reader, mp := newTestMeter()
	meter := mp.Meter("test")

	h, err := petalotel.NewMetricsHandler(meter)
	if err != nil {
		t.Fatalf("NewMetricsHandler: %v", err)
	}

	now := time.Now()
	// A full event stream whose payloads carry per-run identifiers.
	events := []runtime.Event{
		{Kind: runtime.EventRunStarted, RunID: "run-77", Time: now, Payload: map[string]any{
			"graph": "pipeline", "tenant_id": "tenant-a", "workflow_id": "wf-1",
			"schedule_id": "sched-9", "webhook_trigger_id": "hook-42", "webhook_method": "POST",
		}},
		{Kind: runtime.EventNodeStarted, RunID: "run-77", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(time.Millisecond)},
		{Kind: runtime.EventLLMResponse, RunID: "run-77", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(2 * time.Millisecond), Payload: map[string]any{
			"status": "success", "model": "test-model", "provider": "openai", "latency_ms": 50,
			"input_tokens": 3, "output_tokens": 2,
		}},
		{Kind: runtime.EventNodeFinished, RunID: "run-77", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(3 * time.Millisecond), Elapsed: 2 * time.Millisecond},
		{Kind: runtime.EventNodeFailed, RunID: "run-77", NodeID: "n2", NodeKind: core.NodeKindTool, Time: now.Add(4 * time.Millisecond), Elapsed: time.Millisecond, Payload: map[string]any{"error": "boom"}},
		{Kind: runtime.EventRunFinished, RunID: "run-77", Time: now.Add(5 * time.Millisecond), Elapsed: 5 * time.Millisecond, Payload: map[string]any{
			"status": "failed", "schedule_id": "sched-9", "webhook_trigger_id": "hook-42",
			"trigger": "webhook", "workflow_id": "wf-1", "webhook_method": "POST",
		}},
	}
	for _, e := range events {
		h.Handle(e)
	}

	rm := collectMetrics(t, reader)

	// The bounded allow-list (dimensions grow only with deployment
	// configuration: tenants, workflows, providers, models, tools).
	// Per-run identifiers (run_id, node_id, schedule_id,
	// webhook_trigger_id) are enforced inside assertBoundedAttributes.
	allowed := map[string]struct{}{
		"node_kind": {}, "tenant_id": {}, "workflow_id": {}, "graph": {},
		"trigger": {}, "webhook_method": {},
		"gen_ai.operation.name": {}, "gen_ai.request.model": {}, "gen_ai.provider.name": {},
		"gen_ai.token.type": {}, "error.type": {},
	}

	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			var histPoints []metricdata.HistogramDataPoint[float64]
			var sumPoints []metricdata.DataPoint[int64]
			switch data := m.Data.(type) {
			case metricdata.Histogram[float64]:
				histPoints = data.DataPoints
			case metricdata.Sum[int64]:
				sumPoints = data.DataPoints
			}
			for _, dp := range histPoints {
				assertBoundedAttributes(t, m.Name, dp.Attributes, allowed)
			}
			for _, dp := range sumPoints {
				assertBoundedAttributes(t, m.Name, dp.Attributes, allowed)
			}
		}
	}
}

func assertBoundedAttributes(t *testing.T, metric string, attrs attribute.Set, allowed map[string]struct{}) {
	t.Helper()
	forbidden := map[string]struct{}{
		"run_id": {}, "node_id": {}, "schedule_id": {}, "webhook_trigger_id": {},
	}
	for _, kv := range attrs.ToSlice() {
		key := string(kv.Key)
		if _, bad := forbidden[key]; bad {
			t.Errorf("metric %s carries per-run attribute %q (unbounded cardinality)", metric, key)
			continue
		}
		if _, ok := allowed[key]; !ok {
			t.Errorf("metric %s carries attribute %q outside the bounded allow-list", metric, key)
		}
	}
}
