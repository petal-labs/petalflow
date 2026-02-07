package otel_test

import (
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/petal-labs/petalflow/core"
	petalotel "github.com/petal-labs/petalflow/otel"
	"github.com/petal-labs/petalflow/runtime"
)

func TestEnrichEmitter_NodeSpanPopulatesTraceFields(t *testing.T) {
	_, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

	// Start run and node to create active spans.
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "g"},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeStarted,
		RunID:    "run-1",
		NodeID:   "node-a",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(1 * time.Millisecond),
	})

	// Get the expected span context for the node span.
	expectedSC := h.ActiveSpanContext("run-1", "node-a")
	if !expectedSC.IsValid() {
		t.Fatal("expected valid node span context")
	}

	var received runtime.Event
	inner := runtime.EventEmitter(func(e runtime.Event) {
		received = e
	})

	enriched := petalotel.EnrichEmitter(inner, h)

	// Emit a node-level event through the enriched emitter.
	enriched(runtime.Event{
		Kind:     runtime.EventNodeOutput,
		RunID:    "run-1",
		NodeID:   "node-a",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(2 * time.Millisecond),
	})

	if received.TraceID != expectedSC.TraceID().String() {
		t.Errorf("TraceID: got %q, want %q", received.TraceID, expectedSC.TraceID().String())
	}
	if received.SpanID != expectedSC.SpanID().String() {
		t.Errorf("SpanID: got %q, want %q", received.SpanID, expectedSC.SpanID().String())
	}
}

func TestEnrichEmitter_RunSpanFallback(t *testing.T) {
	_, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

	// Start a run but no node.
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "g"},
	})

	expectedSC := h.ActiveRunSpanContext("run-1")
	if !expectedSC.IsValid() {
		t.Fatal("expected valid run span context")
	}

	var received runtime.Event
	inner := runtime.EventEmitter(func(e runtime.Event) {
		received = e
	})

	enriched := petalotel.EnrichEmitter(inner, h)

	// Emit a run-level event (no NodeID).
	enriched(runtime.Event{
		Kind:  runtime.EventRunFinished,
		RunID: "run-1",
		Time:  now.Add(10 * time.Millisecond),
	})

	if received.TraceID != expectedSC.TraceID().String() {
		t.Errorf("TraceID: got %q, want %q", received.TraceID, expectedSC.TraceID().String())
	}
	if received.SpanID != expectedSC.SpanID().String() {
		t.Errorf("SpanID: got %q, want %q", received.SpanID, expectedSC.SpanID().String())
	}
}

func TestEnrichEmitter_NodeEventFallsBackToRunSpan(t *testing.T) {
	_, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

	// Start a run only (no node span active).
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "g"},
	})

	expectedSC := h.ActiveRunSpanContext("run-1")
	if !expectedSC.IsValid() {
		t.Fatal("expected valid run span context")
	}

	var received runtime.Event
	inner := runtime.EventEmitter(func(e runtime.Event) {
		received = e
	})

	enriched := petalotel.EnrichEmitter(inner, h)

	// Emit a node-level event for a node that has no active span.
	enriched(runtime.Event{
		Kind:     runtime.EventNodeStarted,
		RunID:    "run-1",
		NodeID:   "node-unknown",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(5 * time.Millisecond),
	})

	if received.TraceID != expectedSC.TraceID().String() {
		t.Errorf("TraceID: got %q, want %q", received.TraceID, expectedSC.TraceID().String())
	}
	if received.SpanID != expectedSC.SpanID().String() {
		t.Errorf("SpanID: got %q, want %q", received.SpanID, expectedSC.SpanID().String())
	}
}

func TestEnrichEmitter_PassthroughWhenNoSpanActive(t *testing.T) {
	_, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	var received runtime.Event
	inner := runtime.EventEmitter(func(e runtime.Event) {
		received = e
	})

	enriched := petalotel.EnrichEmitter(inner, h)

	// Emit an event with no run or node spans active.
	enriched(runtime.Event{
		Kind:  runtime.EventRunStarted,
		RunID: "run-no-span",
		Time:  time.Now(),
	})

	// TraceID and SpanID should remain empty.
	if received.TraceID != "" {
		t.Errorf("expected empty TraceID, got %q", received.TraceID)
	}
	if received.SpanID != "" {
		t.Errorf("expected empty SpanID, got %q", received.SpanID)
	}

	// The event should still be forwarded.
	if received.RunID != "run-no-span" {
		t.Errorf("expected RunID 'run-no-span', got %q", received.RunID)
	}
	if received.Kind != runtime.EventRunStarted {
		t.Errorf("expected Kind 'run.started', got %q", received.Kind)
	}
}

func TestEnrichEmitter_PreservesExistingEventFields(t *testing.T) {
	_, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

	// Start run and node.
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "g"},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeStarted,
		RunID:    "run-1",
		NodeID:   "node-a",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(1 * time.Millisecond),
	})

	var received runtime.Event
	inner := runtime.EventEmitter(func(e runtime.Event) {
		received = e
	})

	enriched := petalotel.EnrichEmitter(inner, h)

	original := runtime.Event{
		Kind:     runtime.EventToolCall,
		RunID:    "run-1",
		NodeID:   "node-a",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(5 * time.Millisecond),
		Attempt:  2,
		Elapsed:  4 * time.Millisecond,
		Seq:      7,
		Payload:  map[string]any{"tool": "calculator"},
	}

	enriched(original)

	// Verify trace fields are populated.
	if received.TraceID == "" {
		t.Error("expected TraceID to be populated")
	}
	if received.SpanID == "" {
		t.Error("expected SpanID to be populated")
	}

	// Verify other fields are preserved.
	if received.Kind != runtime.EventToolCall {
		t.Errorf("Kind: got %q, want %q", received.Kind, runtime.EventToolCall)
	}
	if received.RunID != "run-1" {
		t.Errorf("RunID: got %q, want %q", received.RunID, "run-1")
	}
	if received.NodeID != "node-a" {
		t.Errorf("NodeID: got %q, want %q", received.NodeID, "node-a")
	}
	if received.Attempt != 2 {
		t.Errorf("Attempt: got %d, want 2", received.Attempt)
	}
	if received.Elapsed != 4*time.Millisecond {
		t.Errorf("Elapsed: got %v, want 4ms", received.Elapsed)
	}
	if received.Seq != 7 {
		t.Errorf("Seq: got %d, want 7", received.Seq)
	}
	if received.Payload["tool"] != "calculator" {
		t.Errorf("Payload[tool]: got %v, want 'calculator'", received.Payload["tool"])
	}
}

// newTestTracerForEmitter is a convenience helper that mirrors the one in
// tracing_test.go. Since both files are in otel_test package, newTestTracer
// is already available; this test file reuses it directly.
func newTestTracerForEmitter() (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	return newTestTracer()
}
