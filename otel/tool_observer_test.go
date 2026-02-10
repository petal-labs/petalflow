package otel_test

import (
	"testing"

	petalotel "github.com/petal-labs/petalflow/otel"
	"github.com/petal-labs/petalflow/tool"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace/noop"
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
