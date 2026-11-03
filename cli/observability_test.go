package cli

import (
	"context"
	"testing"
)

func TestSetupObservabilityNoopDefault(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	stack, err := setupObservability(context.Background(), "")
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

	stack, err := setupObservability(context.Background(), "")
	if err != nil {
		t.Fatalf("setupObservability: %v", err)
	}
	if stack.enabled {
		t.Error("observability enabled despite exporter=none")
	}
}

func TestSetupObservabilityPetaltraceLadderDrivesCapture(t *testing.T) {
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

	stack, err := setupObservability(context.Background(), configPath)
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
