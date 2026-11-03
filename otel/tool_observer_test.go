package otel_test

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/petal-labs/petalflow/core"
	petalotel "github.com/petal-labs/petalflow/otel"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/tool"
)

func TestToolObserverRecordsMetrics(t *testing.T) {
	reader, mp := newTestMeter()
	meter := mp.Meter("test-tool-observer")
	tracer := noop.NewTracerProvider().Tracer("test-tool-observer")

	observer, err := petalotel.NewToolObserver(meter, tracer)
	if err != nil {
		t.Fatalf("NewToolObserver() error = %v", err)
	}

	observer.ObserveInvoke(tool.ToolInvokeObservation{
		ToolName:   "s3_fetch",
		Action:     "list",
		Transport:  tool.TransportTypeMCP,
		Attempts:   2,
		DurationMS: 120,
		Success:    false,
		ErrorCode:  tool.ToolErrorCodeUpstreamFailure,
	})
	observer.ObserveRetry(tool.ToolRetryObservation{
		ToolName:  "s3_fetch",
		Action:    "list",
		Transport: tool.TransportTypeMCP,
		Attempt:   1,
		ErrorCode: tool.ToolErrorCodeUpstreamFailure,
	})
	observer.ObserveHealth(tool.ToolHealthObservation{
		ToolName:      "s3_fetch",
		State:         tool.HealthUnhealthy,
		Status:        tool.StatusUnhealthy,
		FailureCount:  3,
		DurationMS:    45,
		PreviousState: tool.StatusReady,
		ErrorCode:     tool.ToolErrorCodeMCPFailure,
	})

	rm := collectMetrics(t, reader)

	invocations := findMetric(rm, "petalflow.tool.invocations")
	if invocations == nil {
		t.Fatal("petalflow.tool.invocations metric not found")
	}
	if _, ok := invocations.Data.(metricdata.Sum[int64]); !ok {
		t.Fatalf("petalflow.tool.invocations type = %T, want Sum[int64]", invocations.Data)
	}

	retries := findMetric(rm, "petalflow.tool.retries")
	if retries == nil {
		t.Fatal("petalflow.tool.retries metric not found")
	}
	if _, ok := retries.Data.(metricdata.Sum[int64]); !ok {
		t.Fatalf("petalflow.tool.retries type = %T, want Sum[int64]", retries.Data)
	}

	health := findMetric(rm, "petalflow.tool.health.checks")
	if health == nil {
		t.Fatal("petalflow.tool.health.checks metric not found")
	}
	if _, ok := health.Data.(metricdata.Sum[int64]); !ok {
		t.Fatalf("petalflow.tool.health.checks type = %T, want Sum[int64]", health.Data)
	}

	latency := findMetric(rm, "petalflow.tool.latency")
	if latency == nil {
		t.Fatal("petalflow.tool.latency metric not found")
	}
	if _, ok := latency.Data.(metricdata.Histogram[float64]); !ok {
		t.Fatalf("petalflow.tool.latency type = %T, want Histogram[float64]", latency.Data)
	}
}

func TestToolObserverCorrelationParentsSpansIntoRunTrace(t *testing.T) {
	exporter, tp := newTestTracer()
	reader, meterProvider := newTestMeter()

	tracing := petalotel.NewTracingHandler(tp.Tracer("runtime"))

	now := time.Now()
	// A run with a node that is executing a tool: the node span is open.
	tracing.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "g", "tenant_id": "tenant-a"},
	})
	tracing.Handle(runtime.Event{
		Kind:     runtime.EventNodeStarted,
		RunID:    "run-1",
		NodeID:   "node-1",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(time.Millisecond),
	})
	tracing.Handle(runtime.Event{
		Kind:     runtime.EventToolCall,
		RunID:    "run-1",
		NodeID:   "node-1",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(2 * time.Millisecond),
		Payload:  map[string]any{"tool_name": "s3_fetch", "tool_description": "Fetch from S3"},
	})

	observer, err := petalotel.NewToolObserver(
		meterProvider.Meter("test-tool-observer"),
		tp.Tracer("petalflow/tool"),
		petalotel.WithSpanLookup(func(runID, nodeID string) trace.SpanContext {
			if sc := tracing.ActiveToolSpanContext(runID, nodeID); sc.IsValid() {
				return sc
			}
			if sc := tracing.ActiveSpanContext(runID, nodeID); sc.IsValid() {
				return sc
			}
			return tracing.ActiveRunSpanContext(runID)
		}),
	)
	if err != nil {
		t.Fatalf("NewToolObserver() error = %v", err)
	}

	observer.ObserveInvoke(tool.ToolInvokeObservation{
		ToolName:   "s3_fetch",
		Action:     "list",
		Transport:  tool.TransportTypeHTTP,
		DurationMS: 40,
		Success:    true,
		Correlation: tool.ObservationCorrelation{
			TenantID: "tenant-a",
			RunID:    "run-1",
			NodeID:   "node-1",
		},
	})

	// Close the open spans.
	tracing.Handle(runtime.Event{
		Kind:     runtime.EventToolResult,
		RunID:    "run-1",
		NodeID:   "node-1",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(3 * time.Millisecond),
		Payload:  map[string]any{"tool_name": "s3_fetch"},
	})
	tracing.Handle(runtime.Event{
		Kind:     runtime.EventNodeFinished,
		RunID:    "run-1",
		NodeID:   "node-1",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(4 * time.Millisecond),
		Elapsed:  3 * time.Millisecond,
	})
	tracing.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-1",
		Time:    now.Add(5 * time.Millisecond),
		Elapsed: 5 * time.Millisecond,
		Payload: map[string]any{"status": "completed"},
	})

	spans := exporter.GetSpans()
	var toolSpan, transportSpan, runSpan *tracetest.SpanStub
	for i := range spans {
		switch spans[i].Name {
		case "s3_fetch":
			// Two spans named after the tool: the event-driven tool span
			// (child of node) and the observer transport span. Identify
			// by parent/attribute.
			if spanAttributeValue(spans[i], "transport") != "" {
				transportSpan = &spans[i]
			} else {
				toolSpan = &spans[i]
			}
		case "run:g":
			runSpan = &spans[i]
		}
	}
	if toolSpan == nil || transportSpan == nil || runSpan == nil {
		t.Fatalf("expected tool, transport, and run spans; got %+v", spans)
	}

	// One trace tree: the transport span joins the run trace under the
	// event-driven tool span.
	if transportSpan.SpanContext.TraceID() != runSpan.SpanContext.TraceID() {
		t.Error("transport span is not part of the run trace")
	}
	if transportSpan.Parent.SpanID() != toolSpan.SpanContext.SpanID() {
		t.Errorf("transport span parent = %v, want tool span %v",
			transportSpan.Parent.SpanID(), toolSpan.SpanContext.SpanID())
	}

	// GenAI semconv on the transport span.
	if got := spanAttributeValue(*transportSpan, "gen_ai.operation.name"); got != "execute_tool" {
		t.Errorf("gen_ai.operation.name = %q, want execute_tool", got)
	}
	if got := spanAttributeValue(*transportSpan, "gen_ai.tool.name"); got != "s3_fetch" {
		t.Errorf("gen_ai.tool.name = %q, want s3_fetch", got)
	}
	if got := spanAttributeValue(*transportSpan, "petalflow.run_id"); got != "run-1" {
		t.Errorf("petalflow.run_id = %q, want run-1", got)
	}

	// Metrics carry the bounded tenant dimension.
	rm := collectMetrics(t, reader)
	invocations := findMetric(rm, "petalflow.tool.invocations")
	if invocations == nil {
		t.Fatal("petalflow.tool.invocations not found")
	}
	sumData, ok := invocations.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64], got %T", invocations.Data)
	}
	if len(sumData.DataPoints) != 1 {
		t.Fatalf("expected 1 data point, got %d", len(sumData.DataPoints))
	}
	attrs := attributeMap(sumData.DataPoints[0].Attributes)
	if attrs["tenant_id"] != "tenant-a" {
		t.Errorf("invocation tenant_id = %q, want tenant-a", attrs["tenant_id"])
	}
}
