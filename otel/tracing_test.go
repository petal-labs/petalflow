package otel_test

import (
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	otelcodes "go.opentelemetry.io/otel/codes"

	"github.com/petal-labs/petalflow/core"
	petalotel "github.com/petal-labs/petalflow/otel"
	"github.com/petal-labs/petalflow/runtime"
)

// newTestTracer returns a tracer backed by an in-memory span exporter.
func newTestTracer() (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	return exporter, tp
}

func TestTracingHandler_RunStartedCreatesRootSpan(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

	// Emit run.started
	h.Handle(runtime.Event{
		Kind:  runtime.EventRunStarted,
		RunID: "run-1",
		Time:  now,
		Payload: map[string]any{
			"graph": "myGraph",
		},
	})

	// Verify active run span context is valid
	sc := h.ActiveRunSpanContext("run-1")
	if !sc.IsValid() {
		t.Fatal("expected valid run span context after run.started")
	}

	// End the run to flush the span
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-1",
		Time:    now.Add(100 * time.Millisecond),
		Elapsed: 100 * time.Millisecond,
		Payload: map[string]any{"status": "completed"},
	})

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	runSpan := spans[0]
	if runSpan.Name != "run:myGraph" {
		t.Errorf("expected span name 'run:myGraph', got %q", runSpan.Name)
	}

	// Verify run_id attribute
	found := false
	for _, attr := range runSpan.Attributes {
		if string(attr.Key) == "petalflow.run_id" && attr.Value.AsString() == "run-1" {
			found = true
		}
	}
	if !found {
		t.Error("expected petalflow.run_id attribute on run span")
	}
}

func TestTracingHandler_RunStartedUsesRunIDWhenNoGraphName(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-no-graph",
		Time:    now,
		Payload: map[string]any{},
	})

	h.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-no-graph",
		Time:    now.Add(50 * time.Millisecond),
		Elapsed: 50 * time.Millisecond,
		Payload: map[string]any{"status": "completed"},
	})

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}
	if spans[0].Name != "run:run-no-graph" {
		t.Errorf("expected span name 'run:run-no-graph', got %q", spans[0].Name)
	}
}

func TestTracingHandler_NodeStartedCreatesChildSpan(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

	// Start a run
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "g"},
	})

	// Start a node
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeStarted,
		RunID:    "run-1",
		NodeID:   "node-a",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(10 * time.Millisecond),
	})

	// Verify active node span context
	sc := h.ActiveSpanContext("run-1", "node-a")
	if !sc.IsValid() {
		t.Fatal("expected valid node span context after node.started")
	}

	// The node span should be a child of the run span
	runSC := h.ActiveRunSpanContext("run-1")
	if sc.TraceID() != runSC.TraceID() {
		t.Error("expected node span to share trace ID with run span")
	}

	// Finish node and run to flush
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeFinished,
		RunID:    "run-1",
		NodeID:   "node-a",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(20 * time.Millisecond),
		Elapsed:  10 * time.Millisecond,
	})
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-1",
		Time:    now.Add(30 * time.Millisecond),
		Elapsed: 30 * time.Millisecond,
		Payload: map[string]any{"status": "completed"},
	})

	spans := exporter.GetSpans()
	if len(spans) < 2 {
		t.Fatalf("expected at least 2 spans, got %d", len(spans))
	}

	// Find node span
	var nodeSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "node:node-a" {
			nodeSpan = &spans[i]
			break
		}
	}
	if nodeSpan == nil {
		t.Fatal("did not find node:node-a span")
	}

	// Verify parent-child relationship
	if nodeSpan.Parent.TraceID() != runSC.TraceID() {
		t.Error("expected node span parent trace ID to match run span trace ID")
	}
	if nodeSpan.Parent.SpanID() != runSC.SpanID() {
		t.Error("expected node span parent span ID to match run span span ID")
	}

	// Check node_kind attribute
	foundKind := false
	for _, attr := range nodeSpan.Attributes {
		if string(attr.Key) == "petalflow.node_kind" && attr.Value.AsString() == "llm" {
			foundKind = true
		}
	}
	if !foundKind {
		t.Error("expected petalflow.node_kind attribute on node span")
	}
}

func TestTracingHandler_NodeFinishedEndsSpan(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

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
		Time:     now.Add(10 * time.Millisecond),
	})

	// Node is active
	sc := h.ActiveSpanContext("run-1", "node-a")
	if !sc.IsValid() {
		t.Fatal("expected valid span before finish")
	}

	// Finish node
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeFinished,
		RunID:    "run-1",
		NodeID:   "node-a",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(20 * time.Millisecond),
		Elapsed:  10 * time.Millisecond,
	})

	// Node span context should no longer be valid (span removed from map)
	sc = h.ActiveSpanContext("run-1", "node-a")
	if sc.IsValid() {
		t.Error("expected invalid span context after node.finished")
	}

	// End run to flush
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-1",
		Time:    now.Add(30 * time.Millisecond),
		Elapsed: 30 * time.Millisecond,
		Payload: map[string]any{"status": "completed"},
	})

	spans := exporter.GetSpans()
	// Find node span and verify status
	for _, s := range spans {
		if s.Name == "node:node-a" {
			if s.Status.Code != otelcodes.Ok {
				t.Errorf("expected Ok status on finished node span, got %v", s.Status.Code)
			}
			return
		}
	}
	t.Error("node:node-a span not found in exported spans")
}

func TestTracingHandler_NodeFailedSetsErrorStatus(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "g"},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeStarted,
		RunID:    "run-1",
		NodeID:   "node-fail",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(10 * time.Millisecond),
	})

	// Fail the node
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeFailed,
		RunID:    "run-1",
		NodeID:   "node-fail",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(20 * time.Millisecond),
		Elapsed:  10 * time.Millisecond,
		Payload:  map[string]any{"error": "something went wrong"},
	})

	// End run
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-1",
		Time:    now.Add(30 * time.Millisecond),
		Elapsed: 30 * time.Millisecond,
		Payload: map[string]any{"status": "failed", "error": "something went wrong"},
	})

	spans := exporter.GetSpans()
	for _, s := range spans {
		if s.Name == "node:node-fail" {
			if s.Status.Code != otelcodes.Error {
				t.Errorf("expected Error status, got %v", s.Status.Code)
			}
			if s.Status.Description != "something went wrong" {
				t.Errorf("expected error description 'something went wrong', got %q", s.Status.Description)
			}
			// Verify error event was recorded
			if len(s.Events) == 0 {
				t.Error("expected at least one event (error) on failed span")
			}
			foundException := false
			for _, ev := range s.Events {
				if ev.Name == "exception" {
					foundException = true
				}
			}
			if !foundException {
				t.Error("expected exception event on failed span")
			}
			return
		}
	}
	t.Error("node:node-fail span not found")
}

func TestTracingHandler_ToolEventsBecomeSpanEvents(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "g"},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeStarted,
		RunID:    "run-1",
		NodeID:   "node-tool",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(10 * time.Millisecond),
	})

	// Emit tool.call
	h.Handle(runtime.Event{
		Kind:     runtime.EventToolCall,
		RunID:    "run-1",
		NodeID:   "node-tool",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(15 * time.Millisecond),
		Payload:  map[string]any{"tool": "calculator"},
	})

	// Emit tool.result
	h.Handle(runtime.Event{
		Kind:     runtime.EventToolResult,
		RunID:    "run-1",
		NodeID:   "node-tool",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(18 * time.Millisecond),
		Payload:  map[string]any{"tool": "calculator"},
	})

	// Finish node and run
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeFinished,
		RunID:    "run-1",
		NodeID:   "node-tool",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(20 * time.Millisecond),
		Elapsed:  10 * time.Millisecond,
	})
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-1",
		Time:    now.Add(30 * time.Millisecond),
		Elapsed: 30 * time.Millisecond,
		Payload: map[string]any{"status": "completed"},
	})

	spans := exporter.GetSpans()
	for _, s := range spans {
		if s.Name == "node:node-tool" {
			if len(s.Events) < 2 {
				t.Fatalf("expected at least 2 span events (tool.call + tool.result), got %d", len(s.Events))
			}
			var foundCall, foundResult bool
			for _, ev := range s.Events {
				switch ev.Name {
				case "tool.call":
					foundCall = true
					// Check tool name attribute
					for _, attr := range ev.Attributes {
						if string(attr.Key) == "petalflow.tool_name" && attr.Value.AsString() == "calculator" {
							break
						}
					}
				case "tool.result":
					foundResult = true
				}
			}
			if !foundCall {
				t.Error("expected tool.call span event")
			}
			if !foundResult {
				t.Error("expected tool.result span event")
			}
			return
		}
	}
	t.Error("node:node-tool span not found")
}

func TestTracingHandler_RunFinishedEndsRootSpan(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "testGraph"},
	})

	// Run span should be active
	sc := h.ActiveRunSpanContext("run-1")
	if !sc.IsValid() {
		t.Fatal("expected valid run span context before finish")
	}

	h.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-1",
		Time:    now.Add(100 * time.Millisecond),
		Elapsed: 100 * time.Millisecond,
		Payload: map[string]any{"status": "completed"},
	})

	// Run span context should no longer be accessible
	sc = h.ActiveRunSpanContext("run-1")
	if sc.IsValid() {
		t.Error("expected invalid run span context after run.finished")
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name != "run:testGraph" {
		t.Errorf("expected span name 'run:testGraph', got %q", spans[0].Name)
	}
	if spans[0].Status.Code != otelcodes.Ok {
		t.Errorf("expected Ok status on completed run, got %v", spans[0].Status.Code)
	}
}

func TestTracingHandler_RunFinishedWithFailedStatus(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-fail",
		Time:    now,
		Payload: map[string]any{"graph": "failGraph"},
	})

	h.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-fail",
		Time:    now.Add(50 * time.Millisecond),
		Elapsed: 50 * time.Millisecond,
		Payload: map[string]any{"status": "failed", "error": "node exploded"},
	})

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Status.Code != otelcodes.Error {
		t.Errorf("expected Error status on failed run, got %v", spans[0].Status.Code)
	}
}

func TestTracingHandler_FullLifecycle(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()

	// Full lifecycle: run starts, node starts, tool call, tool result, node finishes, run finishes
	events := []runtime.Event{
		{Kind: runtime.EventRunStarted, RunID: "r1", Time: now, Payload: map[string]any{"graph": "pipeline"}},
		{Kind: runtime.EventNodeStarted, RunID: "r1", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(1 * time.Millisecond)},
		{Kind: runtime.EventToolCall, RunID: "r1", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(2 * time.Millisecond), Payload: map[string]any{"tool": "search"}},
		{Kind: runtime.EventToolResult, RunID: "r1", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(3 * time.Millisecond), Payload: map[string]any{"tool": "search"}},
		{Kind: runtime.EventNodeFinished, RunID: "r1", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(4 * time.Millisecond), Elapsed: 3 * time.Millisecond},
		{Kind: runtime.EventNodeStarted, RunID: "r1", NodeID: "n2", NodeKind: core.NodeKindTool, Time: now.Add(5 * time.Millisecond)},
		{Kind: runtime.EventNodeFailed, RunID: "r1", NodeID: "n2", NodeKind: core.NodeKindTool, Time: now.Add(6 * time.Millisecond), Elapsed: 1 * time.Millisecond, Payload: map[string]any{"error": "timeout"}},
		{Kind: runtime.EventRunFinished, RunID: "r1", Time: now.Add(7 * time.Millisecond), Elapsed: 7 * time.Millisecond, Payload: map[string]any{"status": "failed", "error": "timeout"}},
	}

	for _, e := range events {
		h.Handle(e)
	}

	spans := exporter.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("expected 3 spans (run + 2 nodes), got %d", len(spans))
	}

	// Verify span names
	names := map[string]bool{}
	for _, s := range spans {
		names[s.Name] = true
	}
	for _, expected := range []string{"run:pipeline", "node:n1", "node:n2"} {
		if !names[expected] {
			t.Errorf("expected span %q not found", expected)
		}
	}

	// Verify all spans share the same trace ID
	traceID := spans[0].SpanContext.TraceID()
	for _, s := range spans {
		if s.SpanContext.TraceID() != traceID {
			t.Error("expected all spans to share the same trace ID")
		}
	}
}
