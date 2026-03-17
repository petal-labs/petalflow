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
	runSpans  map[string]trace.Span      // runID -> span
	runCtxs   map[string]context.Context // runID -> context (for child spans)
	nodeSpans map[string]trace.Span      // runID:nodeID -> span
	nodeCtxs  map[string]context.Context // runID:nodeID -> context (for LLM child spans)
	llmSpans  map[string]trace.Span      // runID:nodeID:llm -> span (active LLM call)
}

// NewTracingHandler creates a new TracingHandler that uses the given tracer
// to create spans from runtime events.
func NewTracingHandler(tracer trace.Tracer) *TracingHandler {
	return &TracingHandler{
		tracer:    tracer,
		runSpans:  make(map[string]trace.Span),
		runCtxs:   make(map[string]context.Context),
		nodeSpans: make(map[string]trace.Span),
		nodeCtxs:  make(map[string]context.Context),
		llmSpans:  make(map[string]trace.Span),
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
	case runtime.EventLLMCall:
		h.handleLLMCall(e)
	case runtime.EventLLMResponse:
		h.handleLLMResponse(e)
	case runtime.EventEdgeTransfer:
		h.handleEdgeTransfer(e)
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
	trigger := ""
	if value, ok := e.Payload["trigger"]; ok {
		if s, ok := value.(string); ok {
			trigger = s
		}
	}
	scheduleID := ""
	if value, ok := e.Payload["schedule_id"]; ok {
		if s, ok := value.(string); ok {
			scheduleID = s
		}
	}
	workflowID := ""
	if value, ok := e.Payload["workflow_id"]; ok {
		if s, ok := value.(string); ok {
			workflowID = s
		}
	}
	webhookTriggerID := ""
	if value, ok := e.Payload["webhook_trigger_id"]; ok {
		if s, ok := value.(string); ok {
			webhookTriggerID = s
		}
	}
	webhookMethod := ""
	if value, ok := e.Payload["webhook_method"]; ok {
		if s, ok := value.(string); ok {
			webhookMethod = s
		}
	}
	scheduledAt := ""
	if value, ok := e.Payload["scheduled_at"]; ok {
		if s, ok := value.(string); ok {
			scheduledAt = s
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
	if trigger != "" {
		span.SetAttributes(attribute.String("petalflow.trigger", trigger))
	}
	if scheduleID != "" {
		span.SetAttributes(attribute.String("petalflow.schedule_id", scheduleID))
	}
	if workflowID != "" {
		span.SetAttributes(attribute.String("petalflow.workflow_id", workflowID))
	}
	if webhookTriggerID != "" {
		span.SetAttributes(attribute.String("petalflow.webhook_trigger_id", webhookTriggerID))
	}
	if webhookMethod != "" {
		span.SetAttributes(attribute.String("petalflow.webhook_method", webhookMethod))
	}
	if scheduledAt != "" {
		span.SetAttributes(attribute.String("petalflow.scheduled_at", scheduledAt))
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

	ctx, span := h.tracer.Start(parentCtx, spanName,
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
	h.nodeCtxs[key] = ctx
	h.mu.Unlock()
}

// handleNodeFinished ends the node span with success status.
func (h *TracingHandler) handleNodeFinished(e runtime.Event) {
	key := e.RunID + ":" + e.NodeID

	h.mu.Lock()
	span, ok := h.nodeSpans[key]
	if ok {
		delete(h.nodeSpans, key)
		delete(h.nodeCtxs, key)
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
		delete(h.nodeCtxs, key)
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

// handleLLMCall creates a child span under the node span for LLM requests.
// It uses OpenTelemetry GenAI semantic conventions plus PetalFlow-specific attributes.
func (h *TracingHandler) handleLLMCall(e runtime.Event) {
	key := e.RunID + ":" + e.NodeID

	h.mu.RLock()
	parentCtx, ok := h.nodeCtxs[key]
	h.mu.RUnlock()

	if !ok {
		// No parent node span; skip LLM span creation.
		return
	}

	model := ""
	if m, found := e.Payload["model"]; found {
		if s, ok := m.(string); ok {
			model = s
		}
	}

	spanName := "llm:" + model
	if model == "" {
		spanName = "llm:unknown"
	}

	attrs := []attribute.KeyValue{
		attribute.String("petalflow.run_id", e.RunID),
		attribute.String("petalflow.node_id", e.NodeID),
		// OTel GenAI semantic conventions
		attribute.String("gen_ai.request.model", model),
	}

	// Add system prompt if available
	if system, found := e.Payload["system_prompt"]; found {
		if s, ok := system.(string); ok && s != "" {
			attrs = append(attrs, attribute.String("petalflow.llm.system_prompt", s))
		}
	}

	// Add temperature if available
	if temp, found := e.Payload["temperature"]; found {
		if f, ok := temp.(float64); ok {
			attrs = append(attrs, attribute.Float64("gen_ai.request.temperature", f))
		}
	}

	// Add max_tokens if available
	if maxTokens, found := e.Payload["max_tokens"]; found {
		if i, ok := maxTokens.(int); ok {
			attrs = append(attrs, attribute.Int("gen_ai.request.max_tokens", i))
		}
	}

	// Add messages (as JSON string) if available
	if messages, found := e.Payload["messages"]; found {
		if s, ok := messages.(string); ok && s != "" {
			attrs = append(attrs, attribute.String("petalflow.llm.messages", s))
		}
	}

	_, span := h.tracer.Start(parentCtx, spanName,
		trace.WithAttributes(attrs...),
		trace.WithTimestamp(e.Time),
	)

	llmKey := key + ":llm"
	h.mu.Lock()
	h.llmSpans[llmKey] = span
	h.mu.Unlock()
}

// handleLLMResponse ends the LLM span with response data.
func (h *TracingHandler) handleLLMResponse(e runtime.Event) {
	key := e.RunID + ":" + e.NodeID
	llmKey := key + ":llm"

	h.mu.Lock()
	span, ok := h.llmSpans[llmKey]
	if ok {
		delete(h.llmSpans, llmKey)
	}
	h.mu.Unlock()

	if !ok {
		return
	}

	// Set response attributes using OTel GenAI semantic conventions
	attrs := []attribute.KeyValue{}

	// Provider (gen_ai.system)
	if provider, found := e.Payload["provider"]; found {
		if s, ok := provider.(string); ok {
			attrs = append(attrs, attribute.String("gen_ai.system", s))
		}
	}

	// Response model
	if model, found := e.Payload["response_model"]; found {
		if s, ok := model.(string); ok {
			attrs = append(attrs, attribute.String("gen_ai.response.model", s))
		}
	}

	// Token usage (OTel GenAI conventions)
	if inputTokens, found := e.Payload["input_tokens"]; found {
		if i, ok := inputTokens.(int); ok {
			attrs = append(attrs, attribute.Int("gen_ai.usage.input_tokens", i))
		}
	}

	if outputTokens, found := e.Payload["output_tokens"]; found {
		if i, ok := outputTokens.(int); ok {
			attrs = append(attrs, attribute.Int("gen_ai.usage.output_tokens", i))
		}
	}

	if totalTokens, found := e.Payload["total_tokens"]; found {
		if i, ok := totalTokens.(int); ok {
			attrs = append(attrs, attribute.Int("gen_ai.usage.total_tokens", i))
		}
	}

	// Stop reason
	if stopReason, found := e.Payload["stop_reason"]; found {
		if s, ok := stopReason.(string); ok {
			attrs = append(attrs, attribute.String("gen_ai.response.finish_reason", s))
		}
	}

	// Latency
	if latencyMs, found := e.Payload["latency_ms"]; found {
		if i, ok := latencyMs.(int64); ok {
			attrs = append(attrs, attribute.Int64("petalflow.llm.latency_ms", i))
		}
	}

	// Time to first token (streaming)
	if ttft, found := e.Payload["ttft_ms"]; found {
		if i, ok := ttft.(int64); ok {
			attrs = append(attrs, attribute.Int64("petalflow.llm.ttft_ms", i))
		}
	}

	// Request ID from provider
	if reqID, found := e.Payload["request_id"]; found {
		if s, ok := reqID.(string); ok {
			attrs = append(attrs, attribute.String("petalflow.llm.request_id", s))
		}
	}

	// Cost
	if cost, found := e.Payload["cost_usd"]; found {
		if f, ok := cost.(float64); ok {
			attrs = append(attrs, attribute.Float64("petalflow.llm.cost_usd", f))
		}
	}

	// Completion text (for PetalTrace capture)
	if completion, found := e.Payload["completion"]; found {
		if s, ok := completion.(string); ok {
			attrs = append(attrs, attribute.String("petalflow.llm.completion", s))
		}
	}

	// Tool calls
	if toolCalls, found := e.Payload["tool_calls"]; found {
		if s, ok := toolCalls.(string); ok && s != "" {
			attrs = append(attrs, attribute.String("petalflow.llm.tool_calls", s))
		}
	}

	span.SetAttributes(attrs...)

	// Set status based on response
	if status, found := e.Payload["status"]; found {
		if s, ok := status.(string); ok && s == "error" {
			errMsg := "LLM call failed"
			if err, found := e.Payload["error"]; found {
				if s, ok := err.(string); ok {
					errMsg = s
				}
			}
			span.SetStatus(codes.Error, errMsg)
			span.RecordError(spanError(errMsg), trace.WithTimestamp(e.Time))
		} else {
			span.SetStatus(codes.Ok, "")
		}
	} else {
		span.SetStatus(codes.Ok, "")
	}

	span.End(trace.WithTimestamp(e.Time))
}

// handleEdgeTransfer creates a span for data transfer between nodes.
func (h *TracingHandler) handleEdgeTransfer(e runtime.Event) {
	h.mu.RLock()
	parentCtx, ok := h.runCtxs[e.RunID]
	h.mu.RUnlock()

	if !ok {
		// No parent run span; skip edge span creation.
		return
	}

	sourceNode := ""
	sourcePort := ""
	targetNode := ""
	targetPort := ""

	if s, found := e.Payload["source_node"]; found {
		if str, ok := s.(string); ok {
			sourceNode = str
		}
	}
	if s, found := e.Payload["source_port"]; found {
		if str, ok := s.(string); ok {
			sourcePort = str
		}
	}
	if s, found := e.Payload["target_node"]; found {
		if str, ok := s.(string); ok {
			targetNode = str
		}
	}
	if s, found := e.Payload["target_port"]; found {
		if str, ok := s.(string); ok {
			targetPort = str
		}
	}

	spanName := "edge:" + sourceNode + "->" + targetNode

	attrs := []attribute.KeyValue{
		attribute.String("petalflow.run_id", e.RunID),
		attribute.String("petalflow.edge.source_node", sourceNode),
		attribute.String("petalflow.edge.source_port", sourcePort),
		attribute.String("petalflow.edge.target_node", targetNode),
		attribute.String("petalflow.edge.target_port", targetPort),
	}

	// Data size
	if size, found := e.Payload["data_size_bytes"]; found {
		if i, ok := size.(int64); ok {
			attrs = append(attrs, attribute.Int64("petalflow.edge.data_size_bytes", i))
		}
	}

	// Data preview (truncated)
	if preview, found := e.Payload["data_preview"]; found {
		if s, ok := preview.(string); ok {
			attrs = append(attrs, attribute.String("petalflow.edge.data_preview", s))
		}
	}

	_, span := h.tracer.Start(parentCtx, spanName,
		trace.WithAttributes(attrs...),
		trace.WithTimestamp(e.Time),
	)

	// Edge spans are instantaneous - end immediately
	span.SetStatus(codes.Ok, "")
	span.End(trace.WithTimestamp(e.Time))
}

// spanError is a simple error type for recording span errors.
type spanError string

func (e spanError) Error() string { return string(e) }
