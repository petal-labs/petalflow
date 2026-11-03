package otel

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/petal-labs/petalflow/runtime"
)

// Metric attribute cardinality rules (test-enforced, see
// TestMetricsHandlerAttributeCardinality):
//
//   - No attribute can grow per run: run_id, node_id, schedule_id, and
//     webhook_trigger_id never appear on metric attributes (they are
//     correlation attributes for spans and events only).
//   - Allowed dimensions are bounded by deployment configuration:
//     node_kind, trigger, webhook_method, workflow_id, tenant_id
//     (number of workflows/tenants), tool_name, action, transport,
//     provider, model (registered tools / configured providers and
//     models), plus fixed enums (success, error_code, state, status).
//
// The handler tracks tenant per active run (populated from run.started,
// evicted on run.finished) so node and run metrics can be sliced by
// tenant without carrying run identifiers.

// MetricsHandler translates PetalFlow runtime events into OpenTelemetry metrics.
// It records counters and histograms for node executions, failures, and run
// durations, plus GenAI client metrics (token usage and operation duration)
// for LLM calls.
type MetricsHandler struct {
	nodeExecutions metric.Int64Counter
	nodeFailures   metric.Int64Counter
	nodeDuration   metric.Float64Histogram
	runDuration    metric.Float64Histogram
	tokenUsage     metric.Float64Histogram
	llmDuration    metric.Float64Histogram

	// runTenants maps active run IDs to tenant IDs (populated on
	// run.started, evicted on run.finished) so per-event metrics can be
	// attributed to a tenant without unbounded run attributes.
	mu         sync.RWMutex
	runTenants map[string]string
}

// NewMetricsHandler creates a MetricsHandler that uses the given meter to create
// instruments for recording PetalFlow runtime metrics.
func NewMetricsHandler(meter metric.Meter) (*MetricsHandler, error) {
	nodeExec, err := meter.Int64Counter("petalflow.node.executions",
		metric.WithDescription("Number of node executions"),
	)
	if err != nil {
		return nil, err
	}

	nodeFail, err := meter.Int64Counter("petalflow.node.failures",
		metric.WithDescription("Number of node failures"),
	)
	if err != nil {
		return nil, err
	}

	nodeDur, err := meter.Float64Histogram("petalflow.node.duration",
		metric.WithDescription("Duration of node execution in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	runDur, err := meter.Float64Histogram("petalflow.run.duration",
		metric.WithDescription("Duration of workflow run in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	// GenAI semantic convention metrics: token usage and operation
	// duration, tagged only with bounded dimensions (operation, model,
	// provider).
	tokenHist, err := meter.Float64Histogram("gen_ai.client.token.usage",
		metric.WithDescription("Number of input and output tokens consumed per GenAI operation"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, err
	}

	llmDur, err := meter.Float64Histogram("gen_ai.client.operation.duration",
		metric.WithDescription("Duration of GenAI operation in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	return &MetricsHandler{
		nodeExecutions: nodeExec,
		nodeFailures:   nodeFail,
		nodeDuration:   nodeDur,
		runDuration:    runDur,
		tokenUsage:     tokenHist,
		llmDuration:    llmDur,
		runTenants:     make(map[string]string),
	}, nil
}

// Handle processes a runtime event and records the appropriate metrics.
// It implements runtime.EventHandler semantics.
func (h *MetricsHandler) Handle(e runtime.Event) {
	switch e.Kind {
	case runtime.EventRunStarted:
		h.handleRunStarted(e)
	case runtime.EventNodeFinished:
		h.handleNodeFinished(e)
	case runtime.EventNodeFailed:
		h.handleNodeFailed(e)
	case runtime.EventLLMResponse:
		h.handleLLMResponse(e)
	case runtime.EventRunFinished:
		h.handleRunFinished(e)
	}
}

// tenantFor returns the tenant recorded for a run (empty when unknown).
func (h *MetricsHandler) tenantFor(runID string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.runTenants[runID]
}

// handleRunStarted records the run's tenant for attribute attribution.
func (h *MetricsHandler) handleRunStarted(e runtime.Event) {
	tenant, _ := e.Payload["tenant_id"].(string)
	if tenant == "" {
		return
	}
	h.mu.Lock()
	h.runTenants[e.RunID] = tenant
	h.mu.Unlock()
}

// boundedRunAttributes returns the bounded attribute set shared by node
// and run metrics: tenant (when known) plus the given kind.
func (h *MetricsHandler) boundedRunAttributes(runID, kind string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("node_kind", kind),
	}
	if tenant := h.tenantFor(runID); tenant != "" {
		attrs = append(attrs, attribute.String("tenant_id", tenant))
	}
	return attrs
}

// handleNodeFinished increments the execution counter and records duration.
func (h *MetricsHandler) handleNodeFinished(e runtime.Event) {
	ctx := context.Background()
	attrs := h.boundedRunAttributes(e.RunID, string(e.NodeKind))
	options := metric.WithAttributes(attrs...)
	h.nodeExecutions.Add(ctx, 1, options)
	h.nodeDuration.Record(ctx, e.Elapsed.Seconds(), options)
}

// handleNodeFailed increments the failure counter.
func (h *MetricsHandler) handleNodeFailed(e runtime.Event) {
	ctx := context.Background()
	attrs := h.boundedRunAttributes(e.RunID, string(e.NodeKind))
	h.nodeFailures.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// handleRunFinished records the workflow run duration and evicts the
// run's tenant entry.
func (h *MetricsHandler) handleRunFinished(e runtime.Event) {
	ctx := context.Background()
	attrList := []attribute.KeyValue{}
	if tenant := h.tenantFor(e.RunID); tenant != "" {
		attrList = append(attrList, attribute.String("tenant_id", tenant))
	}
	if kind, ok := e.Payload["graph"].(string); ok && kind != "" {
		attrList = append(attrList, attribute.String("graph", kind))
	}
	if trigger, ok := e.Payload["trigger"].(string); ok && trigger != "" {
		attrList = append(attrList, attribute.String("trigger", trigger))
	}
	if workflowID, ok := e.Payload["workflow_id"].(string); ok && workflowID != "" {
		attrList = append(attrList, attribute.String("workflow_id", workflowID))
	}
	if method, ok := e.Payload["webhook_method"].(string); ok && method != "" {
		attrList = append(attrList, attribute.String("webhook_method", method))
	}
	h.runDuration.Record(ctx, e.Elapsed.Seconds(), metric.WithAttributes(attrList...))

	h.mu.Lock()
	delete(h.runTenants, e.RunID)
	h.mu.Unlock()
}

// handleLLMResponse records the GenAI client metrics for one LLM call:
// token usage (split by input/output) and operation duration, tagged
// only with bounded dimensions.
func (h *MetricsHandler) handleLLMResponse(e runtime.Event) {
	ctx := context.Background()
	baseAttrs := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "chat"),
	}
	if model, ok := e.Payload["model"].(string); ok && model != "" {
		baseAttrs = append(baseAttrs, attribute.String("gen_ai.request.model", model))
	}
	if provider, ok := e.Payload["provider"].(string); ok && provider != "" {
		baseAttrs = append(baseAttrs, attribute.String("gen_ai.provider.name", provider))
	}
	if tenant := h.tenantFor(e.RunID); tenant != "" {
		baseAttrs = append(baseAttrs, attribute.String("tenant_id", tenant))
	}

	// Operation duration: bounded dimensions plus a low-cardinality
	// error type on failure.
	durAttrs := append([]attribute.KeyValue(nil), baseAttrs...)
	if status, ok := e.Payload["status"].(string); ok && status == "error" {
		errType := "error"
		if code, found := e.Payload["error_code"].(string); found && code != "" {
			errType = code
		}
		durAttrs = append(durAttrs, attribute.String("error.type", errType))
	}
	if latencyMS, ok := payloadInt64(e, "latency_ms"); ok {
		h.llmDuration.Record(ctx, float64(latencyMS)/1000.0, metric.WithAttributes(durAttrs...))
	}

	// Token usage (successful calls with reported usage only).
	if inputTokens, ok := payloadInt64(e, "input_tokens"); ok && inputTokens > 0 {
		h.tokenUsage.Record(ctx, float64(inputTokens),
			metric.WithAttributes(append(append([]attribute.KeyValue(nil), baseAttrs...),
				attribute.String("gen_ai.token.type", "input"))...))
	}
	if outputTokens, ok := payloadInt64(e, "output_tokens"); ok && outputTokens > 0 {
		h.tokenUsage.Record(ctx, float64(outputTokens),
			metric.WithAttributes(append(append([]attribute.KeyValue(nil), baseAttrs...),
				attribute.String("gen_ai.token.type", "output"))...))
	}
}

// payloadInt64 extracts an integer payload field across the numeric
// types events may carry.
func payloadInt64(e runtime.Event, key string) (int64, bool) {
	switch v := e.Payload[key].(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}
