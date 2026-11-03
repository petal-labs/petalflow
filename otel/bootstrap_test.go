package otel

import (
	"context"
	"testing"
)

func TestSignalConfigured(t *testing.T) {
	tests := []struct {
		name       string
		exporter   string
		endpoint   string
		want       bool
		wantErr    bool
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
