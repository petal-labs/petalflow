package otel

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/petal-labs/petalflow/tool"
)

// ToolObserver records tool/daemon hardening signals into OpenTelemetry.
//
// Correlation: when an observation carries run correlation (workflow-run
// invocations), metrics gain the bounded tenant dimension and spans are
// parented into the run's trace tree via the configured span lookup
// (see WithSpanLookup), carrying gen_ai.tool.name and petalflow.* run
// correlation attributes. Daemon-level calls without run context emit
// standalone spans.
type ToolObserver struct {
	tracer trace.Tracer

	// spanLookup resolves the active span context for a run/node pair
	// so transport-level spans join the workflow trace tree.
	spanLookup SpanLookup

	invocations metric.Int64Counter
	retries     metric.Int64Counter
	health      metric.Int64Counter
	latency     metric.Float64Histogram
}

// SpanLookup returns the active span context for a workflow run/node
// pair, or an empty SpanContext when none is active. TracingHandler
// exposes suitable lookups (ActiveToolSpanContext, ActiveSpanContext,
// ActiveRunSpanContext).
type SpanLookup func(runID, nodeID string) trace.SpanContext

// ObserverOption configures a ToolObserver.
type ObserverOption func(*ToolObserver)

// WithSpanLookup parents transport-level tool spans into the workflow
// run's trace tree.
func WithSpanLookup(lookup SpanLookup) ObserverOption {
	return func(o *ToolObserver) {
		o.spanLookup = lookup
	}
}

// NewToolObserver creates a tool observer bound to the provided meter/tracer.
func NewToolObserver(meter metric.Meter, tracer trace.Tracer, opts ...ObserverOption) (*ToolObserver, error) {
	invocations, err := meter.Int64Counter(
		"petalflow.tool.invocations",
		metric.WithDescription("Number of tool invocations"),
	)
	if err != nil {
		return nil, err
	}
	retries, err := meter.Int64Counter(
		"petalflow.tool.retries",
		metric.WithDescription("Number of tool retry attempts"),
	)
	if err != nil {
		return nil, err
	}
	health, err := meter.Int64Counter(
		"petalflow.tool.health.checks",
		metric.WithDescription("Number of tool health checks"),
	)
	if err != nil {
		return nil, err
	}
	latency, err := meter.Float64Histogram(
		"petalflow.tool.latency",
		metric.WithDescription("Tool latency in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	observer := &ToolObserver{
		tracer:      tracer,
		invocations: invocations,
		retries:     retries,
		health:      health,
		latency:     latency,
	}
	for _, opt := range opts {
		opt(observer)
	}
	return observer, nil
}

// observeContext resolves the context for a transport-level span: the
// run trace tree when correlation and a span lookup are available,
// background otherwise.
func (o *ToolObserver) observeContext(correlation tool.ObservationCorrelation) (context.Context, []attribute.KeyValue) {
	attrs := []attribute.KeyValue{}
	if correlation.TenantID != "" {
		attrs = append(attrs, attribute.String("tenant_id", correlation.TenantID))
	}
	if o.spanLookup == nil || correlation.RunID == "" {
		return context.Background(), attrs
	}
	sc := o.spanLookup(correlation.RunID, correlation.NodeID)
	if sc.IsValid() {
		attrs = append(attrs,
			attribute.String("petalflow.run_id", correlation.RunID),
		)
		if correlation.NodeID != "" {
			attrs = append(attrs, attribute.String("petalflow.node_id", correlation.NodeID))
		}
		return trace.ContextWithSpanContext(context.Background(), sc), attrs
	}
	return context.Background(), attrs
}

// ObserveInvoke records one invocation result.
func (o *ToolObserver) ObserveInvoke(observation tool.ToolInvokeObservation) {
	if o == nil {
		return
	}

	attrs := []attribute.KeyValue{
		attribute.String("tool_name", observation.ToolName),
		attribute.String("action", observation.Action),
		attribute.String("transport", string(observation.Transport)),
		attribute.Bool("success", observation.Success),
	}
	if observation.ErrorCode != "" {
		attrs = append(attrs, attribute.String("error_code", observation.ErrorCode))
	}
	if observation.Correlation.TenantID != "" {
		attrs = append(attrs, attribute.String("tenant_id", observation.Correlation.TenantID))
	}

	ctx := context.Background()
	options := metric.WithAttributes(attrs...)
	o.invocations.Add(ctx, 1, options)
	o.latency.Record(ctx, float64(time.Duration(observation.DurationMS)*time.Millisecond)/float64(time.Second), options)

	if o.tracer == nil {
		return
	}
	spanCtx, correlationAttrs := o.observeContext(observation.Correlation)
	spanAttrs := append([]attribute.KeyValue{
		// OTel GenAI semantic conventions for tool execution.
		attribute.String("gen_ai.operation.name", "execute_tool"),
		attribute.String("gen_ai.tool.name", observation.ToolName),
	}, correlationAttrs...)
	spanAttrs = append(spanAttrs, attrs...)
	_, span := o.tracer.Start(spanCtx, observation.ToolName, trace.WithAttributes(spanAttrs...))
	if !observation.Success {
		span.SetStatus(codes.Error, observation.ErrorCode)
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}

// ObserveRetry records one retry attempt.
func (o *ToolObserver) ObserveRetry(observation tool.ToolRetryObservation) {
	if o == nil {
		return
	}

	attrs := []attribute.KeyValue{
		attribute.String("tool_name", observation.ToolName),
		attribute.String("action", observation.Action),
		attribute.String("transport", string(observation.Transport)),
		attribute.Int("attempt", observation.Attempt),
	}
	if observation.ErrorCode != "" {
		attrs = append(attrs, attribute.String("error_code", observation.ErrorCode))
	}
	if observation.Correlation.TenantID != "" {
		attrs = append(attrs, attribute.String("tenant_id", observation.Correlation.TenantID))
	}
	o.retries.Add(context.Background(), 1, metric.WithAttributes(attrs...))
}

// ObserveHealth records one background health-check result.
func (o *ToolObserver) ObserveHealth(observation tool.ToolHealthObservation) {
	if o == nil {
		return
	}

	attrs := []attribute.KeyValue{
		attribute.String("tool_name", observation.ToolName),
		attribute.String("state", string(observation.State)),
		attribute.String("status", string(observation.Status)),
		attribute.Int("failure_count", observation.FailureCount),
		attribute.String("previous_status", string(observation.PreviousState)),
	}
	if observation.ErrorCode != "" {
		attrs = append(attrs, attribute.String("error_code", observation.ErrorCode))
	}

	ctx := context.Background()
	options := metric.WithAttributes(attrs...)
	o.health.Add(ctx, 1, options)
	o.latency.Record(ctx, float64(time.Duration(observation.DurationMS)*time.Millisecond)/float64(time.Second), options)

	if o.tracer == nil {
		return
	}
	_, span := o.tracer.Start(ctx, "tool.health.check", trace.WithAttributes(attrs...))
	if observation.ErrorCode != "" {
		span.SetStatus(codes.Error, observation.ErrorCode)
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}

var _ tool.Observer = (*ToolObserver)(nil)
