// Observability wiring for `petalflow serve`: OTel SDK bootstrap from
// standard OTEL_* environment variables (no-op default), PetalTrace
// ownership of the trace pipeline when configured, and the runtime
// tracing/metrics handlers that turn run events into correlated spans
// and bounded-cardinality metrics.

package cli

import (
	"context"
	"fmt"

	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/petal-labs/petalflow/daemon"
	petalotel "github.com/petal-labs/petalflow/otel"
	petalruntime "github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/traceflow"
)

// observabilityStack carries the telemetry wiring for one serve
// process.
type observabilityStack struct {
	tracer  trace.Tracer
	meter   metric.Meter
	tracing *petalotel.TracingHandler
	// capture is the content capture policy from the PetalTrace ladder
	// (standard/full enable capture; minimal stays metadata-only).
	capture petalruntime.ContentCapture
	// enabled reports whether any telemetry pipeline is active. When
	// false, serve runs with no runtime event handlers wired (the
	// documented no-op default).
	enabled bool
	// shutdown flushes and stops the active providers.
	shutdown func(context.Context) error
}

// setupObservability bootstraps telemetry for serve:
//
//   - When observability.petaltrace is configured and valid, the
//     PetalTrace (traceflow) adapter owns the trace pipeline and its
//     capture ladder drives event content capture.
//   - Otherwise, standard OTEL_* environment variables bootstrap OTLP
//     trace and/or metric pipelines (no-op when unconfigured).
func setupObservability(ctx context.Context, configPath string) (*observabilityStack, error) {
	stack := &observabilityStack{shutdown: func(context.Context) error { return nil }}
	var shutdowns []func(context.Context) error

	petaltraceCfg, err := daemon.LoadPetalTraceConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading petaltrace config: %w", err)
	}
	petaltraceOwnsTraces := false
	if petaltraceCfg != nil && petaltraceCfg.Enabled && petaltraceCfg.IsValid() {
		adapter := traceflow.NewAdapter(petaltraceCfg)
		if err := adapter.Initialize(ctx); err != nil {
			return nil, fmt.Errorf("initializing petaltrace adapter: %w", err)
		}
		shutdowns = append(shutdowns, adapter.Shutdown)
		stack.tracer = adapter.Tracer()
		stack.capture = adapter.ContentCapture()
		petaltraceOwnsTraces = true
		stack.enabled = true
	}

	setup, err := petalotel.SetupOTel(ctx, petalotel.SetupOptions{SkipTraces: petaltraceOwnsTraces})
	if err != nil {
		return nil, fmt.Errorf("configuring opentelemetry: %w", err)
	}
	shutdowns = append(shutdowns, setup.Shutdown)
	if setup.TracesEnabled {
		stack.tracer = otelapi.GetTracerProvider().Tracer("petalflow")
		stack.enabled = true
	}
	if setup.MetricsEnabled {
		stack.meter = otelapi.GetMeterProvider().Meter("petalflow")
		stack.enabled = true
	}

	if stack.tracer == nil {
		// No trace pipeline: the global no-op tracer keeps handler
		// construction valid (nothing is recorded).
		stack.tracer = otelapi.GetTracerProvider().Tracer("petalflow")
	}

	stack.tracing = petalotel.NewTracingHandler(stack.tracer)

	if len(shutdowns) > 0 {
		stack.shutdown = func(ctx context.Context) error {
			var errs []error
			for _, shutdown := range shutdowns {
				if err := shutdown(ctx); err != nil {
					errs = append(errs, err)
				}
			}
			switch len(errs) {
			case 0:
				return nil
			case 1:
				return errs[0]
			default:
				return fmt.Errorf("observability shutdown: %v", errs)
			}
		}
	}
	return stack, nil
}

// toolSpanLookup resolves the active span for a tool invocation so
// transport-level tool spans join the workflow run's trace tree: the
// tool span first, then the node span, then the run span.
func (s *observabilityStack) toolSpanLookup(runID, nodeID string) trace.SpanContext {
	if s == nil || s.tracing == nil {
		return trace.SpanContext{}
	}
	if sc := s.tracing.ActiveToolSpanContext(runID, nodeID); sc.IsValid() {
		return sc
	}
	if sc := s.tracing.ActiveSpanContext(runID, nodeID); sc.IsValid() {
		return sc
	}
	return s.tracing.ActiveRunSpanContext(runID)
}

// runtimeEvents returns the run-event handler chain (tracing + metrics)
// for ServerConfig.RuntimeEvents, or nil when telemetry is disabled.
func (s *observabilityStack) runtimeEvents() petalruntime.EventHandler {
	if !s.enabled {
		return nil
	}
	handlers := []petalruntime.EventHandler{s.tracing.Handle}
	if s.meter != nil {
		metrics, err := petalotel.NewMetricsHandler(s.meter)
		if err == nil {
			handlers = append(handlers, metrics.Handle)
		}
	}
	return petalruntime.MultiEventHandler(handlers...)
}

// emitDecorator returns the emitter decorator that stamps trace/span
// IDs onto emitted events, or nil when telemetry is disabled.
func (s *observabilityStack) emitDecorator() petalruntime.EventEmitterDecorator {
	if !s.enabled {
		return nil
	}
	return func(emit petalruntime.EventEmitter) petalruntime.EventEmitter {
		return petalotel.EnrichEmitter(emit, s.tracing)
	}
}
