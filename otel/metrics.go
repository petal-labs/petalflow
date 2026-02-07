package otel

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/petal-labs/petalflow/runtime"
)

// MetricsHandler translates PetalFlow runtime events into OpenTelemetry metrics.
// It records counters and histograms for node executions, failures, and run durations.
type MetricsHandler struct {
	nodeExecutions metric.Int64Counter
	nodeFailures   metric.Int64Counter
	nodeDuration   metric.Float64Histogram
	runDuration    metric.Float64Histogram
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

	return &MetricsHandler{
		nodeExecutions: nodeExec,
		nodeFailures:   nodeFail,
		nodeDuration:   nodeDur,
		runDuration:    runDur,
	}, nil
}

// Handle processes a runtime event and records the appropriate metrics.
// It implements runtime.EventHandler semantics.
func (h *MetricsHandler) Handle(e runtime.Event) {
	switch e.Kind {
	case runtime.EventNodeFinished:
		h.handleNodeFinished(e)
	case runtime.EventNodeFailed:
		h.handleNodeFailed(e)
	case runtime.EventRunFinished:
		h.handleRunFinished(e)
	}
}

// handleNodeFinished increments the execution counter and records duration.
func (h *MetricsHandler) handleNodeFinished(e runtime.Event) {
	ctx := context.Background()
	attrs := metric.WithAttributes(
		attribute.String("node_kind", string(e.NodeKind)),
		attribute.String("node_id", e.NodeID),
	)
	h.nodeExecutions.Add(ctx, 1, attrs)
	h.nodeDuration.Record(ctx, e.Elapsed.Seconds(), attrs)
}

// handleNodeFailed increments the failure counter.
func (h *MetricsHandler) handleNodeFailed(e runtime.Event) {
	ctx := context.Background()
	attrs := metric.WithAttributes(
		attribute.String("node_kind", string(e.NodeKind)),
		attribute.String("node_id", e.NodeID),
	)
	h.nodeFailures.Add(ctx, 1, attrs)
}

// handleRunFinished records the workflow run duration.
func (h *MetricsHandler) handleRunFinished(e runtime.Event) {
	ctx := context.Background()
	attrs := metric.WithAttributes(
		attribute.String("run_id", e.RunID),
	)
	h.runDuration.Record(ctx, e.Elapsed.Seconds(), attrs)
}
