package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/daemon"

	otelapi "go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	petalotel "github.com/petal-labs/petalflow/otel"
	petalruntime "github.com/petal-labs/petalflow/runtime"
)

func TestSetupObservabilityNoopDefault(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	stack, err := setupObservability(context.Background(), nil)
	if err != nil {
		t.Fatalf("setupObservability: %v", err)
	}
	if stack.enabled {
		t.Error("observability enabled with zero configuration, want no-op default")
	}
	if stack.runtimeEvents() != nil {
		t.Error("runtime event handler wired while telemetry disabled")
	}
	if stack.emitDecorator() != nil {
		t.Error("emit decorator wired while telemetry disabled")
	}
	if stack.capture.Enabled {
		t.Error("capture enabled by default, want metadata-only")
	}
	if err := stack.shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestSetupObservabilityExplicitNoneStaysDisabled(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")

	stack, err := setupObservability(context.Background(), nil)
	if err != nil {
		t.Fatalf("setupObservability: %v", err)
	}
	if stack.enabled {
		t.Error("observability enabled despite exporter=none")
	}
}

func TestSetupObservabilityPetaltraceLadderDrivesCapture(t *testing.T) {
	restoreOTelGlobals(t)
	// PetalTrace configured for standard capture mode: the adapter owns
	// traces and the ladder enables run event content capture. The
	// exporter never dials within the test (Initialize only builds the
	// pipeline), but shutdown may fail against the unreachable
	// endpoint — that is acceptable and ignored here.
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("PETALTRACE_ENDPOINT", "")
	t.Setenv("PETALTRACE_CAPTURE_MODE", "")

	configPath := writeTestFile(t, "petalflow.yaml", `
observability:
  petaltrace:
    enabled: true
    endpoint: "http://localhost:4318"
    capture_mode: standard
`)

	petaltraceCfg, err := daemon.LoadPetalTraceConfig(configPath)
	if err != nil {
		t.Fatalf("LoadPetalTraceConfig: %v", err)
	}
	stack, err := setupObservability(context.Background(), petaltraceCfg)
	if err != nil {
		t.Fatalf("setupObservability: %v", err)
	}
	if !stack.enabled {
		t.Fatal("petaltrace configuration should enable telemetry")
	}
	if !stack.capture.Enabled {
		t.Error("standard capture mode should enable run event content capture")
	}
	if stack.runtimeEvents() == nil {
		t.Error("runtime event handlers missing under petaltrace")
	}
	_ = stack.shutdown(context.Background())
}

func TestSetupObservabilityOTelEnabledWiresHandlers(t *testing.T) {
	restoreOTelGlobals(t)
	_, _, endpoint := newLocalOTLPEndpoint(t)

	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")

	stack, err := setupObservability(context.Background(), nil)
	if err != nil {
		t.Fatalf("setupObservability: %v", err)
	}
	if !stack.enabled {
		t.Fatal("observability should be enabled with an OTLP endpoint configured")
	}
	if stack.capture.Enabled {
		t.Error("OTEL_* export must not enable run event content capture (minimal ladder)")
	}

	events := stack.runtimeEvents()
	if events == nil {
		t.Fatal("runtime event handlers missing while telemetry enabled")
	}
	decorator := stack.emitDecorator()
	if decorator == nil {
		t.Fatal("emit decorator missing while telemetry enabled")
	}

	// The handler chain processes events and the decorator stamps trace
	// correlation IDs from the active run span.
	now := time.Now()
	events(petalruntime.Event{
		Kind:    petalruntime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "g", "tenant_id": "tenant-a"},
	})
	events(petalruntime.Event{
		Kind:     petalruntime.EventNodeStarted,
		RunID:    "run-1",
		NodeID:   "node-1",
		NodeKind: "tool",
		Time:     now.Add(time.Millisecond),
	})
	var enriched petalruntime.Event
	capture := decorator(func(e petalruntime.Event) { enriched = e })
	capture(petalruntime.Event{
		Kind:     petalruntime.EventNodeStarted,
		RunID:    "run-1",
		NodeID:   "node-1",
		NodeKind: "tool",
		Time:     now.Add(2 * time.Millisecond),
	})
	if enriched.TraceID == "" {
		t.Error("emit decorator did not stamp trace correlation IDs")
	}

	if err := stack.shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestObservabilityToolSpanLookupFallbacks(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	stack := &observabilityStack{tracing: petalotel.NewTracingHandler(tp.Tracer("test"))}

	// No spans: invalid.
	if sc := stack.toolSpanLookup("run-1", "node-1"); sc.IsValid() {
		t.Error("lookup should be invalid before any span exists")
	}

	now := time.Now()
	stack.tracing.Handle(petalruntime.Event{
		Kind:    petalruntime.EventRunStarted,
		RunID:   "run-1",
		Time:    now,
		Payload: map[string]any{"graph": "g"},
	})
	runSpan := stack.toolSpanLookup("run-1", "node-1")
	if !runSpan.IsValid() {
		t.Fatal("run span should be returned as fallback")
	}

	// Node span wins over run span.
	stack.tracing.Handle(petalruntime.Event{
		Kind:     petalruntime.EventNodeStarted,
		RunID:    "run-1",
		NodeID:   "node-1",
		NodeKind: "tool",
		Time:     now.Add(time.Millisecond),
	})
	nodeSpan := stack.toolSpanLookup("run-1", "node-1")
	if !nodeSpan.IsValid() || nodeSpan.SpanID() == runSpan.SpanID() {
		t.Fatal("node span should take precedence over the run span")
	}

	// Tool span wins over node span.
	stack.tracing.Handle(petalruntime.Event{
		Kind:     petalruntime.EventToolCall,
		RunID:    "run-1",
		NodeID:   "node-1",
		NodeKind: "tool",
		Time:     now.Add(2 * time.Millisecond),
		Payload:  map[string]any{"tool_name": "search"},
	})
	toolSpan := stack.toolSpanLookup("run-1", "node-1")
	if !toolSpan.IsValid() || toolSpan.SpanID() == nodeSpan.SpanID() {
		t.Fatal("tool span should take precedence over the node span")
	}
}

func TestSetupObservabilityPetaltraceWithOTelMetrics(t *testing.T) {
	restoreOTelGlobals(t)
	_, _, endpoint := newLocalOTLPEndpoint(t)

	// PetalTrace owns traces; OTEL_* still bootstraps metrics.
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", endpoint+"/v1/metrics")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_INSECURE", "true")
	t.Setenv("PETALTRACE_ENDPOINT", "")
	t.Setenv("PETALTRACE_CAPTURE_MODE", "")

	configPath := writeTestFile(t, "petalflow.yaml", `
observability:
  petaltrace:
    enabled: true
    endpoint: "http://localhost:4318"
    capture_mode: full
`)

	petaltraceCfg, err := daemon.LoadPetalTraceConfig(configPath)
	if err != nil {
		t.Fatalf("LoadPetalTraceConfig: %v", err)
	}
	stack, err := setupObservability(context.Background(), petaltraceCfg)
	if err != nil {
		t.Fatalf("setupObservability: %v", err)
	}
	if !stack.enabled {
		t.Fatal("telemetry should be enabled")
	}
	if !stack.capture.Enabled {
		t.Error("full capture mode should enable run event content capture")
	}
	if stack.runtimeEvents() == nil {
		t.Error("runtime event handlers missing")
	}
	_ = stack.shutdown(context.Background())
}

// newLocalOTLPEndpoint starts a local OTLP/HTTP collector for CLI-level
// bootstrap tests.
func newLocalOTLPEndpoint(t *testing.T) (*atomic.Int64, *atomic.Int64, string) {
	t.Helper()
	var traces, metrics atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/traces":
			traces.Add(1)
		case "/v1/metrics":
			metrics.Add(1)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return &traces, &metrics, server.URL
}

// restoreOTelGlobals snapshots the global providers and restores them
// on cleanup.
func restoreOTelGlobals(t *testing.T) {
	t.Helper()
	prevTracer := otelapi.GetTracerProvider()
	prevMeter := otelapi.GetMeterProvider()
	t.Cleanup(func() {
		otelapi.SetTracerProvider(prevTracer)
		otelapi.SetMeterProvider(prevMeter)
	})
}
