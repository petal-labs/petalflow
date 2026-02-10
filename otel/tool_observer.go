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
type ToolObserver struct {
	tracer trace.Tracer

	invocations metric.Int64Counter
	retries     metric.Int64Counter
	health      metric.Int64Counter
	latency     metric.Float64Histogram
}

// NewToolObserver creates a tool observer bound to the provided meter/tracer.
func NewToolObserver(meter metric.Meter, tracer trace.Tracer) (*ToolObserver, error) {
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

	return &ToolObserver{
		tracer:      tracer,
		invocations: invocations,
		retries:     retries,
		health:      health,
		latency:     latency,
	}, nil
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

	ctx := context.Background()
	options := metric.WithAttributes(attrs...)
	o.invocations.Add(ctx, 1, options)
	o.latency.Record(ctx, float64(time.Duration(observation.DurationMS)*time.Millisecond)/float64(time.Second), options)

	if o.tracer == nil {
		return
	}
	_, span := o.tracer.Start(ctx, "tool.invoke", trace.WithAttributes(attrs...))
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
