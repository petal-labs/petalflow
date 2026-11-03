package otel_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

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

func TestTracingHandler_ToolEventsBecomeToolSpans(t *testing.T) {
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
		Payload:  map[string]any{"tool_name": "calculator", "tool_description": "Multiply two numbers"},
	})

	// The tool span is active while the invocation runs.
	if sc := h.ActiveToolSpanContext("run-1", "node-tool"); !sc.IsValid() {
		t.Error("expected active tool span context between tool.call and tool.result")
	}

	// Emit tool.result
	h.Handle(runtime.Event{
		Kind:     runtime.EventToolResult,
		RunID:    "run-1",
		NodeID:   "node-tool",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(18 * time.Millisecond),
		Payload:  map[string]any{"tool_name": "calculator", "is_error": true, "error_code": "timeout"},
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
	var toolSpan, nodeSpan *tracetest.SpanStub
	for i := range spans {
		switch spans[i].Name {
		case "calculator":
			toolSpan = &spans[i]
		case "node:node-tool":
			nodeSpan = &spans[i]
		}
	}
	if toolSpan == nil {
		t.Fatal("tool span (named after the tool) not found")
	}
	if nodeSpan == nil {
		t.Fatal("node:node-tool span not found")
	}

	// GenAI semantic conventions on the tool span.
	if got := spanAttributeValue(*toolSpan, "gen_ai.operation.name"); got != "execute_tool" {
		t.Errorf("gen_ai.operation.name = %q, want execute_tool", got)
	}
	if got := spanAttributeValue(*toolSpan, "gen_ai.tool.name"); got != "calculator" {
		t.Errorf("gen_ai.tool.name = %q, want calculator", got)
	}
	if got := spanAttributeValue(*toolSpan, "gen_ai.tool.description"); got != "Multiply two numbers" {
		t.Errorf("gen_ai.tool.description = %q, want tool description", got)
	}
	// Correlation attributes.
	if got := spanAttributeValue(*toolSpan, "petalflow.run_id"); got != "run-1" {
		t.Errorf("petalflow.run_id = %q, want run-1", got)
	}
	// The tool span is a child of the node span and records the failure.
	if toolSpan.Parent.SpanID() != nodeSpan.SpanContext.SpanID() {
		t.Errorf("tool span parent = %v, want node span %v", toolSpan.Parent.SpanID(), nodeSpan.SpanContext.SpanID())
	}
	if toolSpan.Status.Code != otelcodes.Error {
		t.Errorf("tool span status = %v, want Error (is_error result)", toolSpan.Status.Code)
	}
	if got := spanAttributeValue(*toolSpan, "petalflow.error_code"); got != "timeout" {
		t.Errorf("petalflow.error_code = %q, want timeout", got)
	}
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
		{Kind: runtime.EventRunStarted, RunID: "r1", Time: now, Payload: map[string]any{"graph": "pipeline", "tenant_id": "tenant-a"}},
		{Kind: runtime.EventNodeStarted, RunID: "r1", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(1 * time.Millisecond)},
		{Kind: runtime.EventToolCall, RunID: "r1", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(2 * time.Millisecond), Payload: map[string]any{"tool_name": "search", "tool_description": "Search the web"}},
		{Kind: runtime.EventToolResult, RunID: "r1", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(3 * time.Millisecond), Payload: map[string]any{"tool_name": "search"}},
		{Kind: runtime.EventNodeFinished, RunID: "r1", NodeID: "n1", NodeKind: core.NodeKindLLM, Time: now.Add(4 * time.Millisecond), Elapsed: 3 * time.Millisecond},
		{Kind: runtime.EventNodeStarted, RunID: "r1", NodeID: "n2", NodeKind: core.NodeKindTool, Time: now.Add(5 * time.Millisecond)},
		{Kind: runtime.EventNodeFailed, RunID: "r1", NodeID: "n2", NodeKind: core.NodeKindTool, Time: now.Add(6 * time.Millisecond), Elapsed: 1 * time.Millisecond, Payload: map[string]any{"error": "timeout"}},
		{Kind: runtime.EventRunFinished, RunID: "r1", Time: now.Add(7 * time.Millisecond), Elapsed: 7 * time.Millisecond, Payload: map[string]any{"status": "failed", "error": "timeout"}},
	}

	for _, e := range events {
		h.Handle(e)
	}

	spans := exporter.GetSpans()
	if len(spans) != 4 {
		t.Fatalf("expected 4 spans (run + 2 nodes + tool), got %d", len(spans))
	}

	// Verify span names
	names := map[string]bool{}
	for _, s := range spans {
		names[s.Name] = true
	}
	for _, expected := range []string{"run:pipeline", "node:n1", "node:n2", "search"} {
		if !names[expected] {
			t.Errorf("expected span %q not found", expected)
		}
	}

	// Verify all spans share the same trace ID (one correlated trace
	// tree per run).
	traceID := spans[0].SpanContext.TraceID()
	for _, s := range spans {
		if s.SpanContext.TraceID() != traceID {
			t.Error("expected all spans to share the same trace ID")
		}
	}

	// Tenant correlation rides on the run span.
	for _, s := range spans {
		if s.Name == "run:pipeline" {
			if got := spanAttributeValue(s, "petalflow.tenant_id"); got != "tenant-a" {
				t.Errorf("run span petalflow.tenant_id = %q, want tenant-a", got)
			}
		}
	}
}

// spanAttributeValue returns the value of a span attribute as a
// string, or "" when absent. Non-string values (numbers, booleans) are
// rendered.
func spanAttributeValue(s tracetest.SpanStub, key string) string {
	for _, attr := range s.Attributes {
		if string(attr.Key) == key {
			return fmt.Sprint(attr.Value.Emit())
		}
	}
	return ""
}

func hasSpanAttribute(s tracetest.SpanStub, key string) bool {
	for _, attr := range s.Attributes {
		if string(attr.Key) == key {
			return true
		}
	}
	return false
}

// feedLLMEvents drives one LLM call/response cycle carrying content
// payloads through the handler.
func feedLLMEvents(h *petalotel.TracingHandler, now time.Time) {
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-priv",
		Time:    now,
		Payload: map[string]any{"graph": "g"},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeStarted,
		RunID:    "run-priv",
		NodeID:   "llm-1",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(10 * time.Millisecond),
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventLLMCall,
		RunID:    "run-priv",
		NodeID:   "llm-1",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(20 * time.Millisecond),
		Payload: map[string]any{
			"model":         "test-model",
			"system_prompt": "You are a test assistant.",
			"messages":      `[{"role":"user","content":"hello"}]`,
			"input_text":    "hello",
		},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventLLMResponse,
		RunID:    "run-priv",
		NodeID:   "llm-1",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(30 * time.Millisecond),
		Payload: map[string]any{
			"status":     "success",
			"provider":   "stub",
			"completion": "the answer",
			"tool_calls": `[{"name":"lookup"}]`,
		},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeFinished,
		RunID:    "run-priv",
		NodeID:   "llm-1",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(40 * time.Millisecond),
		Elapsed:  30 * time.Millisecond,
	})
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-priv",
		Time:    now.Add(50 * time.Millisecond),
		Elapsed: 50 * time.Millisecond,
		Payload: map[string]any{"status": "completed"},
	})
}

func TestTracingHandler_DefaultDropsContentAttributes(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	feedLLMEvents(h, time.Now())

	spans := exporter.GetSpans()
	var llmSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "chat test-model" {
			llmSpan = &spans[i]
		}
	}
	if llmSpan == nil {
		t.Fatal("llm span not found")
	}

	for _, key := range []string{
		"petalflow.llm.system_prompt",
		"petalflow.llm.messages",
		"petalflow.llm.completion",
		"petalflow.llm.tool_calls",
	} {
		if hasSpanAttribute(*llmSpan, key) {
			t.Errorf("span carries content attribute %q by default", key)
		}
	}
	// Metadata attributes survive.
	if spanAttributeValue(*llmSpan, "gen_ai.request.model") != "test-model" {
		t.Errorf("gen_ai.request.model missing")
	}
	if spanAttributeValue(*llmSpan, "gen_ai.system") != "stub" {
		t.Errorf("gen_ai.system missing")
	}
}

func TestTracingHandlerWithCapture_KeepsContentAttributes(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandlerWithCapture(tracer, runtime.ContentCapture{Enabled: true})

	feedLLMEvents(h, time.Now())

	spans := exporter.GetSpans()
	var llmSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "chat test-model" {
			llmSpan = &spans[i]
		}
	}
	if llmSpan == nil {
		t.Fatal("llm span not found")
	}

	for key, want := range map[string]string{
		"petalflow.llm.system_prompt": "You are a test assistant.",
		"petalflow.llm.messages":      `[{"role":"user","content":"hello"}]`,
		"petalflow.llm.completion":    "the answer",
		"petalflow.llm.tool_calls":    `[{"name":"lookup"}]`,
	} {
		if got := spanAttributeValue(*llmSpan, key); got != want {
			t.Errorf("span attribute %q = %q, want %q", key, got, want)
		}
	}
}

func TestTracingHandler_EdgeDataPreviewGated(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	now := time.Now()

	edgeEvent := runtime.Event{
		Kind:  runtime.EventEdgeTransfer,
		RunID: "run-edge",
		Time:  now,
		Payload: map[string]any{
			"source_node":     "a",
			"source_port":     "out",
			"target_node":     "b",
			"target_port":     "in",
			"data_size_bytes": int64(512),
			"data_preview":    `{"secret_input":"hello world"}`,
		},
	}

	runStart := runtime.Event{Kind: runtime.EventRunStarted, RunID: "run-edge", Time: now, Payload: map[string]any{"graph": "g"}}

	h := petalotel.NewTracingHandler(tracer)
	h.Handle(runStart)
	h.Handle(edgeEvent)

	for _, s := range exporter.GetSpans() {
		if s.Name == "edge:a->b" {
			if hasSpanAttribute(s, "petalflow.edge.data_preview") {
				t.Error("edge span carries data_preview by default")
			}
			if !hasSpanAttribute(s, "petalflow.edge.data_size_bytes") {
				t.Error("edge span missing data_size_bytes")
			}
		}
	}

	exporter.Reset()
	h = petalotel.NewTracingHandlerWithCapture(tracer, runtime.ContentCapture{Enabled: true})
	h.Handle(runStart)
	h.Handle(edgeEvent)
	for _, s := range exporter.GetSpans() {
		if s.Name == "edge:a->b" {
			if !hasSpanAttribute(s, "petalflow.edge.data_preview") {
				t.Error("edge span missing data_preview under capture opt-in")
			}
		}
	}
}

func TestTracingHandler_NodeFailedScrubsCredentialAndRecordsCode(t *testing.T) {
	exporter, tp := newTestTracer()
	tracer := tp.Tracer("test")
	h := petalotel.NewTracingHandler(tracer)

	now := time.Now()
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunStarted,
		RunID:   "run-err",
		Time:    now,
		Payload: map[string]any{"graph": "g"},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeStarted,
		RunID:    "run-err",
		NodeID:   "n1",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(10 * time.Millisecond),
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeFailed,
		RunID:    "run-err",
		NodeID:   "n1",
		NodeKind: core.NodeKindTool,
		Time:     now.Add(20 * time.Millisecond),
		Payload: map[string]any{
			"error":      "provider rejected api_key=sk-abcdef123456789",
			"error_code": "error",
		},
	})

	for _, s := range exporter.GetSpans() {
		if s.Name != "node:n1" {
			continue
		}
		if s.Status.Code != otelcodes.Error {
			t.Errorf("expected Error status, got %v", s.Status.Code)
		}
		if strings.Contains(s.Status.Description, "sk-abcdef123456789") {
			t.Errorf("span status leaks credential: %q", s.Status.Description)
		}
		if got := spanAttributeValue(s, "petalflow.error_code"); got != "error" {
			t.Errorf("petalflow.error_code = %q, want \"error\"", got)
		}
	}
}

// TestTracingHandler_RunSpanCorrelationAttributes feeds a run.started
// event carrying every correlation field and asserts each lands on the
// run span.
func TestTracingHandler_RunSpanCorrelationAttributes(t *testing.T) {
	exporter, tp := newTestTracer()
	h := petalotel.NewTracingHandler(tp.Tracer("test"))

	now := time.Now()
	h.Handle(runtime.Event{
		Kind:  runtime.EventRunStarted,
		RunID: "run-full",
		Time:  now,
		Payload: map[string]any{
			"graph":              "myGraph",
			"trigger":            "schedule",
			"schedule_id":        "sched-1",
			"workflow_id":        "wf-1",
			"workflow_version":   "3",
			"webhook_trigger_id": "hook-1",
			"webhook_method":     "POST",
			"scheduled_at":       "2026-01-02T03:04:05Z",
			"tenant_id":          "tenant-a",
		},
	})
	h.Handle(runtime.Event{
		Kind:    runtime.EventRunFinished,
		RunID:   "run-full",
		Time:    now.Add(time.Second),
		Elapsed: time.Second,
		Payload: map[string]any{"status": "completed"},
	})

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	runSpan := spans[0]
	if runSpan.Name != "run:myGraph" {
		t.Errorf("span name = %q, want run:myGraph", runSpan.Name)
	}
	for key, want := range map[string]string{
		"petalflow.run_id":             "run-full",
		"petalflow.graph":              "myGraph",
		"petalflow.trigger":            "schedule",
		"petalflow.schedule_id":        "sched-1",
		"petalflow.workflow_id":        "wf-1",
		"petalflow.webhook_trigger_id": "hook-1",
		"petalflow.webhook_method":     "POST",
		"petalflow.scheduled_at":       "2026-01-02T03:04:05Z",
		"petalflow.tenant_id":          "tenant-a",
	} {
		if got := spanAttributeValue(runSpan, key); got != want {
			t.Errorf("run span %s = %q, want %q", key, got, want)
		}
	}
}

// TestTracingHandler_LLMRequestAttributes covers the request-parameter
// span attributes (temperature, max_tokens) and the no-parent fallback
// for LLM spans without a node span.
func TestTracingHandler_LLMRequestAttributes(t *testing.T) {
	exporter, tp := newTestTracer()
	h := petalotel.NewTracingHandler(tp.Tracer("test"))

	now := time.Now()
	h.Handle(runtime.Event{Kind: runtime.EventRunStarted, RunID: "r", Time: now, Payload: map[string]any{"graph": "g"}})
	h.Handle(runtime.Event{
		Kind:     runtime.EventNodeStarted,
		RunID:    "r",
		NodeID:   "n1",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(time.Millisecond),
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventLLMCall,
		RunID:    "r",
		NodeID:   "n1",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(2 * time.Millisecond),
		Payload: map[string]any{
			"model":       "test-model",
			"temperature": 0.7,
			"max_tokens":  256,
		},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventLLMResponse,
		RunID:    "r",
		NodeID:   "n1",
		NodeKind: core.NodeKindLLM,
		Time:     now.Add(3 * time.Millisecond),
		Payload: map[string]any{
			"status":      "success",
			"latency_ms":  42,
			"ttft_ms":     int64(12),
			"request_id":  "resp-1",
			"cost_usd":    0.02,
			"stop_reason": "end_turn",
		},
	})
	h.Handle(runtime.Event{Kind: runtime.EventRunFinished, RunID: "r", Time: now.Add(4 * time.Millisecond), Payload: map[string]any{"status": "completed"}})

	var chat *tracetest.SpanStub
	for i, s := range exporter.GetSpans() {
		if s.Name == "chat test-model" {
			chat = &exporter.GetSpans()[i]
		}
	}
	if chat == nil {
		t.Fatal("chat span missing")
	}
	if got := spanAttributeValue(*chat, "gen_ai.request.temperature"); got != "0.7" {
		t.Errorf("temperature = %q, want 0.7", got)
	}
	if got := spanAttributeValue(*chat, "gen_ai.request.max_tokens"); got != "256" {
		t.Errorf("max_tokens = %q, want 256", got)
	}
	if got := spanAttributeValue(*chat, "gen_ai.response.id"); got != "resp-1" {
		t.Errorf("gen_ai.response.id = %q, want resp-1", got)
	}
	if got := spanAttributeValue(*chat, "petalflow.llm.request_id"); got != "resp-1" {
		t.Errorf("petalflow.llm.request_id = %q, want resp-1", got)
	}
	if got := spanAttributeValue(*chat, "petalflow.llm.cost_usd"); got != "0.02" {
		t.Errorf("cost_usd = %q, want 0.02", got)
	}
	if got := spanAttributeValue(*chat, "petalflow.llm.ttft_ms"); got != "12" {
		t.Errorf("ttft_ms = %q, want 12", got)
	}
	// Finish reasons: array convention plus the deprecated alias.
	if got := spanAttributeValue(*chat, "gen_ai.response.finish_reasons"); got != `["end_turn"]` {
		t.Errorf("finish_reasons = %q, want [\"end_turn\"]", got)
	}
	if got := spanAttributeValue(*chat, "gen_ai.response.finish_reason"); got != "end_turn" {
		t.Errorf("finish_reason = %q, want end_turn", got)
	}
}

// TestTracingHandler_LLMAndToolEventsWithoutNodeSpanAreSkipped covers
// the no-parent fallbacks: LLM and tool events for unknown nodes
// create no spans.
func TestTracingHandler_LLMAndToolEventsWithoutNodeSpanAreSkipped(t *testing.T) {
	exporter, tp := newTestTracer()
	h := petalotel.NewTracingHandler(tp.Tracer("test"))

	now := time.Now()
	h.Handle(runtime.Event{
		Kind:     runtime.EventLLMCall,
		RunID:    "orphan",
		NodeID:   "missing",
		NodeKind: core.NodeKindLLM,
		Time:     now,
		Payload:  map[string]any{"model": "m"},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventToolCall,
		RunID:    "orphan",
		NodeID:   "missing",
		NodeKind: core.NodeKindTool,
		Time:     now,
		Payload:  map[string]any{"tool_name": "t"},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventToolResult,
		RunID:    "orphan",
		NodeID:   "missing",
		NodeKind: core.NodeKindTool,
		Time:     now,
		Payload:  map[string]any{"tool_name": "t"},
	})
	h.Handle(runtime.Event{
		Kind:     runtime.EventLLMResponse,
		RunID:    "orphan",
		NodeID:   "missing",
		NodeKind: core.NodeKindLLM,
		Time:     now,
		Payload:  map[string]any{"status": "success"},
	})
	h.Handle(runtime.Event{
		Kind:    runtime.EventEdgeTransfer,
		RunID:   "orphan",
		Time:    now,
		Payload: map[string]any{"source_node": "a", "target_node": "b"},
	})

	if got := len(exporter.GetSpans()); got != 0 {
		t.Errorf("orphan events created %d spans, want 0", got)
	}
}
