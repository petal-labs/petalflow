package otel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	otelapi "go.opentelemetry.io/otel"
)

func TestSignalConfigured(t *testing.T) {
	tests := []struct {
		name     string
		exporter string
		endpoint string
		want     bool
		wantErr  bool
	}{
		{name: "zero configuration is no-op", exporter: "", endpoint: "", want: false},
		{name: "none disables", exporter: "none", endpoint: "http://localhost:4318", want: false},
		{name: "none disables without endpoint", exporter: "none", endpoint: "", want: false},
		{name: "explicit otlp enables", exporter: "otlp", endpoint: "", want: true},
		{name: "endpoint enables default exporter", exporter: "", endpoint: "http://localhost:4318", want: true},
		{name: "unsupported exporter errors", exporter: "zipkin", endpoint: "", want: false, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_TRACES_EXPORTER", tt.exporter)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tt.endpoint)
			got, err := signalConfigured("OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error for unsupported exporter")
				}
				return
			}
			if err != nil {
				t.Fatalf("signalConfigured: %v", err)
			}
			if got != tt.want {
				t.Errorf("signalConfigured = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSignalConfiguredSignalSpecificEndpoint(t *testing.T) {
	// The shared endpoint unset but the signal-specific endpoint set.
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://localhost:4318")

	got, err := signalConfigured("OTEL_TRACES_EXPORTER",
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	if err != nil {
		t.Fatalf("signalConfigured: %v", err)
	}
	if !got {
		t.Error("signal-specific endpoint should enable the pipeline")
	}
}

func TestSetupOTelNoopDefaultInstallsNothing(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	result, err := SetupOTel(context.Background(), SetupOptions{})
	if err != nil {
		t.Fatalf("SetupOTel: %v", err)
	}
	if result.TracesEnabled || result.MetricsEnabled {
		t.Errorf("pipelines enabled with zero configuration: traces=%v metrics=%v",
			result.TracesEnabled, result.MetricsEnabled)
	}
	if err := result.Shutdown(context.Background()); err != nil {
		t.Errorf("no-op shutdown: %v", err)
	}
}

func TestSetupOTelExplicitNoneDisablesPipelines(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")

	result, err := SetupOTel(context.Background(), SetupOptions{})
	if err != nil {
		t.Fatalf("SetupOTel: %v", err)
	}
	if result.TracesEnabled || result.MetricsEnabled {
		t.Errorf("pipelines enabled despite exporter=none: traces=%v metrics=%v",
			result.TracesEnabled, result.MetricsEnabled)
	}
}

func TestSetupOTelRejectsUnsupportedExporter(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "zipkin")

	if _, err := SetupOTel(context.Background(), SetupOptions{}); err == nil {
		t.Fatal("expected error for unsupported trace exporter")
	}
}

func TestSetupOTelSkipOptionsLeaveSignalsUnconfigured(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")

	result, err := SetupOTel(context.Background(), SetupOptions{SkipTraces: true, SkipMetrics: true})
	if err != nil {
		t.Fatalf("SetupOTel: %v", err)
	}
	if result.TracesEnabled || result.MetricsEnabled {
		t.Errorf("pipelines enabled despite skip options: traces=%v metrics=%v",
			result.TracesEnabled, result.MetricsEnabled)
	}
}

// newOTLPCollector starts a local HTTP server that accepts OTLP/HTTP
// protobuf exports and counts them per path.
func newOTLPCollector(t *testing.T) (*atomic.Int64, *atomic.Int64, string) {
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
// on cleanup so bootstrap tests do not leak state into other tests.
func restoreOTelGlobals(t *testing.T) {
	t.Helper()
	prevTracer := otelapi.GetTracerProvider()
	prevMeter := otelapi.GetMeterProvider()
	t.Cleanup(func() {
		otelapi.SetTracerProvider(prevTracer)
		otelapi.SetMeterProvider(prevMeter)
	})
}

func TestSetupOTelExportsTracesAndMetricsToLocalCollector(t *testing.T) {
	restoreOTelGlobals(t)
	traces, metrics, endpoint := newOTLPCollector(t)

	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")

	result, err := SetupOTel(context.Background(), SetupOptions{})
	if err != nil {
		t.Fatalf("SetupOTel: %v", err)
	}
	if !result.TracesEnabled || !result.MetricsEnabled {
		t.Fatalf("pipelines not enabled: traces=%v metrics=%v", result.TracesEnabled, result.MetricsEnabled)
	}

	// Emit one span and one metric through the installed global
	// providers, then shut down: both pipelines must flush to the
	// collector.
	tracer := otelapi.GetTracerProvider().Tracer("test")
	_, span := tracer.Start(context.Background(), "round-trip")
	span.End()

	counter, err := otelapi.GetMeterProvider().Meter("test").Int64Counter("round.trip")
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	counter.Add(context.Background(), 1)

	if err := result.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	if traces.Load() == 0 {
		t.Error("no trace export reached the collector")
	}
	if metrics.Load() == 0 {
		t.Error("no metric export reached the collector")
	}
}

func TestSetupOTelSignalSpecificEndpointEnablesOnlyThatSignal(t *testing.T) {
	restoreOTelGlobals(t)
	traces, _, endpoint := newOTLPCollector(t)

	// Only the traces endpoint is configured. Signal-specific endpoint
	// variables carry the full URL including the signal path (per the
	// OTLP spec, unlike the generic endpoint variable).
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", endpoint+"/v1/traces")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_INSECURE", "true")

	result, err := SetupOTel(context.Background(), SetupOptions{})
	if err != nil {
		t.Fatalf("SetupOTel: %v", err)
	}
	if !result.TracesEnabled {
		t.Error("traces pipeline should be enabled by the signal-specific endpoint")
	}
	if result.MetricsEnabled {
		t.Error("metrics pipeline should stay disabled without a metrics endpoint")
	}

	// The enabled trace pipeline flushes a real export on shutdown.
	tracer := otelapi.GetTracerProvider().Tracer("test")
	_, span := tracer.Start(context.Background(), "signal-specific")
	span.End()
	if err := result.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if traces.Load() == 0 {
		t.Error("no trace export reached the collector")
	}

	// The metrics endpoint alone enables only metrics (traces skipped).
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", endpoint+"/v1/metrics")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_INSECURE", "true")

	result, err = SetupOTel(context.Background(), SetupOptions{})
	if err != nil {
		t.Fatalf("SetupOTel (metrics only): %v", err)
	}
	if result.TracesEnabled {
		t.Error("traces pipeline should stay disabled without a traces endpoint")
	}
	if !result.MetricsEnabled {
		t.Error("metrics pipeline should be enabled by the signal-specific endpoint")
	}
	if err := result.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestSetupOTelSkipOptionsSuppressEnabledSignal(t *testing.T) {
	restoreOTelGlobals(t)
	_, _, endpoint := newOTLPCollector(t)

	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")

	result, err := SetupOTel(context.Background(), SetupOptions{SkipMetrics: true})
	if err != nil {
		t.Fatalf("SetupOTel: %v", err)
	}
	if !result.TracesEnabled {
		t.Error("traces pipeline should be enabled")
	}
	if result.MetricsEnabled {
		t.Error("metrics pipeline should be suppressed by SkipMetrics")
	}
	if err := result.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
