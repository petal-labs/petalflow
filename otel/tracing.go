// Package otel provides OpenTelemetry integration for PetalFlow runtime events.
package otel

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/petal-labs/petalflow/runtime"
)

// TracingHandler translates PetalFlow runtime events into OpenTelemetry spans.
// It maintains maps of active run and node spans, creating and ending them
// based on event kind.
type TracingHandler struct {
	tracer trace.Tracer

	mu        sync.RWMutex
	runSpans  map[string]trace.Span    // runID -> span
	runCtxs   map[string]context.Context // runID -> context (for child spans)
	nodeSpans map[string]trace.Span    // runID:nodeID -> span
}

// NewTracingHandler creates a new TracingHandler that uses the given tracer
// to create spans from runtime events.
func NewTracingHandler(tracer trace.Tracer) *TracingHandler {
	return &TracingHandler{
		tracer:    tracer,
		runSpans:  make(map[string]trace.Span),
		runCtxs:   make(map[string]context.Context),
		nodeSpans: make(map[string]trace.Span),
	}
}

// Handle processes a runtime event and creates or ends spans accordingly.
// It implements runtime.EventHandler semantics.
func (h *TracingHandler) Handle(e runtime.Event) {
	switch e.Kind {
	case runtime.EventRunStarted:
		h.handleRunStarted(e)
	case runtime.EventNodeStarted:
		h.handleNodeStarted(e)
	case runtime.EventNodeFinished:
		h.handleNodeFinished(e)
	case runtime.EventNodeFailed:
		h.handleNodeFailed(e)
	case runtime.EventToolCall:
		h.handleToolEvent(e)
	case runtime.EventToolResult:
		h.handleToolEvent(e)
	case runtime.EventRunFinished:
		h.handleRunFinished(e)
	}
}

// handleRunStarted creates a root span for the run.
func (h *TracingHandler) handleRunStarted(e runtime.Event) {
	graphName := ""
	if name, ok := e.Payload["graph"]; ok {
		if s, ok := name.(string); ok {
			graphName = s
		}
	}

	spanName := "run:" + e.RunID
	if graphName != "" {
		spanName = "run:" + graphName
	}

	ctx, span := h.tracer.Start(context.Background(), spanName,
		trace.WithAttributes(
			attribute.String("petalflow.run_id", e.RunID),
		),
		trace.WithTimestamp(e.Time),
	)

	if graphName != "" {
		span.SetAttributes(attribute.String("petalflow.graph", graphName))
	}

	h.mu.Lock()
	h.runSpans[e.RunID] = span
	h.runCtxs[e.RunID] = ctx
	h.mu.Unlock()
}

// handleNodeStarted creates a child span under the run span.
func (h *TracingHandler) handleNodeStarted(e runtime.Event) {
	h.mu.RLock()
	parentCtx, ok := h.runCtxs[e.RunID]
	h.mu.RUnlock()

	if !ok {
		// No parent run span; start from background context.
		parentCtx = context.Background()
	}

	spanName := "node:" + e.NodeID

	_, span := h.tracer.Start(parentCtx, spanName,
		trace.WithAttributes(
			attribute.String("petalflow.run_id", e.RunID),
			attribute.String("petalflow.node_id", e.NodeID),
			attribute.String("petalflow.node_kind", string(e.NodeKind)),
		),
		trace.WithTimestamp(e.Time),
	)

	key := e.RunID + ":" + e.NodeID
	h.mu.Lock()
	h.nodeSpans[key] = span
	h.mu.Unlock()
}

// handleNodeFinished ends the node span with success status.
func (h *TracingHandler) handleNodeFinished(e runtime.Event) {
	key := e.RunID + ":" + e.NodeID

	h.mu.Lock()
	span, ok := h.nodeSpans[key]
	if ok {
		delete(h.nodeSpans, key)
	}
	h.mu.Unlock()

	if ok {
		span.SetAttributes(
			attribute.String("petalflow.duration", e.Elapsed.String()),
		)
		span.SetStatus(codes.Ok, "")
		span.End(trace.WithTimestamp(e.Time))
	}
}

// handleNodeFailed ends the node span with error status.
func (h *TracingHandler) handleNodeFailed(e runtime.Event) {
	key := e.RunID + ":" + e.NodeID

	h.mu.Lock()
	span, ok := h.nodeSpans[key]
	if ok {
		delete(h.nodeSpans, key)
	}
	h.mu.Unlock()

	if ok {
		errMsg := "unknown error"
		if msg, found := e.Payload["error"]; found {
			if s, ok := msg.(string); ok {
				errMsg = s
			}
		}
		span.SetStatus(codes.Error, errMsg)
		span.RecordError(
			spanError(errMsg),
			trace.WithTimestamp(e.Time),
		)
		span.End(trace.WithTimestamp(e.Time))
	}
}

// handleToolEvent adds a span event for tool.call and tool.result events.
func (h *TracingHandler) handleToolEvent(e runtime.Event) {
	key := e.RunID + ":" + e.NodeID

	h.mu.RLock()
	span, ok := h.nodeSpans[key]
	h.mu.RUnlock()

	if !ok {
		return
	}

	attrs := []attribute.KeyValue{
		attribute.String("petalflow.event_kind", string(e.Kind)),
	}

	if toolName, found := e.Payload["tool"]; found {
		if s, ok := toolName.(string); ok {
			attrs = append(attrs, attribute.String("petalflow.tool_name", s))
		}
	}

	span.AddEvent(string(e.Kind), trace.WithTimestamp(e.Time), trace.WithAttributes(attrs...))
}

// handleRunFinished ends the root run span.
func (h *TracingHandler) handleRunFinished(e runtime.Event) {
	h.mu.Lock()
	span, ok := h.runSpans[e.RunID]
	if ok {
		delete(h.runSpans, e.RunID)
		delete(h.runCtxs, e.RunID)
	}
	h.mu.Unlock()

	if ok {
		status := ""
		if s, found := e.Payload["status"]; found {
			if str, ok := s.(string); ok {
				status = str
			}
		}

		span.SetAttributes(
			attribute.String("petalflow.duration", e.Elapsed.String()),
			attribute.String("petalflow.status", status),
		)

		if status == "failed" {
			errMsg := "run failed"
			if msg, found := e.Payload["error"]; found {
				if s, ok := msg.(string); ok {
					errMsg = s
				}
			}
			span.SetStatus(codes.Error, errMsg)
		} else {
			span.SetStatus(codes.Ok, "")
		}

		span.End(trace.WithTimestamp(e.Time))
	}
}

// ActiveSpanContext returns the SpanContext for the active node span
// identified by runID and nodeID. Returns an empty SpanContext if not found.
func (h *TracingHandler) ActiveSpanContext(runID, nodeID string) trace.SpanContext {
	key := runID + ":" + nodeID

	h.mu.RLock()
	span, ok := h.nodeSpans[key]
	h.mu.RUnlock()

	if !ok {
		return trace.SpanContext{}
	}
	return span.SpanContext()
}

// ActiveRunSpanContext returns the SpanContext for the active run span
// identified by runID. Returns an empty SpanContext if not found.
func (h *TracingHandler) ActiveRunSpanContext(runID string) trace.SpanContext {
	h.mu.RLock()
	span, ok := h.runSpans[runID]
	h.mu.RUnlock()

	if !ok {
		return trace.SpanContext{}
	}
	return span.SpanContext()
}

// spanError is a simple error type for recording span errors.
type spanError string

func (e spanError) Error() string { return string(e) }
