// OTel SDK bootstrap for PetalFlow deployments.
//
// SetupOTel configures the global tracer and meter providers from the
// standard OTEL_* environment variables with OTLP/HTTP exporters:
//
//   - OTEL_TRACES_EXPORTER / OTEL_METRICS_EXPORTER select the exporter
//     ("otlp" or "none"; other values are rejected loudly).
//   - OTEL_EXPORTER_OTLP_ENDPOINT (and the _TRACES / _METRICS variants)
//     configure the collector endpoint, headers, and protocol options
//     via the standard exporter environment handling.
//   - OTEL_SERVICE_NAME (default "petalflow") and
//     OTEL_RESOURCE_ATTRIBUTES populate the resource.
//
// The default is a documented no-op: with no OTEL_* export configuration
// present, no providers are installed, the global no-op providers stay
// in place, and no telemetry leaves the process. An exporter is only
// activated when it is explicitly selected or an OTLP endpoint is
// configured.

package otel

import (
	"context"
	"fmt"
	"os"
	"strings"

	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// defaultServiceName is the resource service name when
// OTEL_SERVICE_NAME is unset.
const defaultServiceName = "petalflow"

// SetupOptions customizes SDK bootstrap.
type SetupOptions struct {
	// SkipTraces leaves the global tracer provider untouched. Embedders
	// that own the trace pipeline (for example a configured PetalTrace
	// adapter) set this so only metrics are bootstrapped.
	SkipTraces bool

	// SkipMetrics leaves the global meter provider untouched.
	SkipMetrics bool
}

// SetupResult reports what SetupOTel configured.
type SetupResult struct {
	// TracesEnabled reports whether an OTLP trace pipeline was installed.
	TracesEnabled bool

	// MetricsEnabled reports whether an OTLP metric pipeline was
	// installed.
	MetricsEnabled bool

	// Shutdown flushes and stops every configured provider. It is a
	// no-op when nothing was enabled.
	Shutdown func(context.Context) error
}

// SetupOTel bootstraps the OTel SDK from standard OTEL_* environment
// variables. See the package comment for the recognized variables and
// the no-op default.
func SetupOTel(ctx context.Context, opts SetupOptions) (*SetupResult, error) {
	result := &SetupResult{
		Shutdown: func(context.Context) error { return nil },
	}

	var shutdowns []func(context.Context) error

	res, err := loadResource(ctx)
	if err != nil {
		return nil, err
	}

	if !opts.SkipTraces {
		enabled, err := signalConfigured("OTEL_TRACES_EXPORTER",
			"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
		if err != nil {
			return nil, err
		}
		if enabled {
			exporter, err := otlptracehttp.New(ctx)
			if err != nil {
				return nil, fmt.Errorf("otel: creating OTLP trace exporter: %w", err)
			}
			provider := sdktrace.NewTracerProvider(
				sdktrace.WithBatcher(exporter),
				sdktrace.WithResource(res),
			)
			otelapi.SetTracerProvider(provider)
			result.TracesEnabled = true
			shutdowns = append(shutdowns, provider.Shutdown)
		}
	}

	if !opts.SkipMetrics {
		enabled, err := signalConfigured("OTEL_METRICS_EXPORTER",
			"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
		if err != nil {
			return nil, err
		}
		if enabled {
			exporter, err := otlpmetrichttp.New(ctx)
			if err != nil {
				return nil, fmt.Errorf("otel: creating OTLP metric exporter: %w", err)
			}
			provider := metric.NewMeterProvider(
				metric.WithReader(metric.NewPeriodicReader(exporter)),
				metric.WithResource(res),
			)
			otelapi.SetMeterProvider(provider)
			result.MetricsEnabled = true
			shutdowns = append(shutdowns, provider.Shutdown)
		}
	}

	if len(shutdowns) > 0 {
		result.Shutdown = func(ctx context.Context) error {
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
				return fmt.Errorf("otel: shutdown: %v", errs)
			}
		}
	}

	return result, nil
}

// signalConfigured reports whether an OTLP pipeline for one signal is
// explicitly configured: the exporter variable is set to "otlp", or it
// is unset (the spec default) while an OTLP endpoint variable names a
// collector. "none" and zero configuration both disable the pipeline;
// unsupported exporter values are rejected loudly instead of silently
// dropping telemetry.
func signalConfigured(exporterEnv string, endpointEnvs ...string) (bool, error) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(exporterEnv)))
	switch value {
	case "none":
		return false, nil
	case "", "otlp":
	default:
		return false, fmt.Errorf("otel: %s=%q is not supported (supported exporters: otlp, none)", exporterEnv, value)
	}

	if value == "otlp" {
		return true, nil
	}
	for _, env := range endpointEnvs {
		if strings.TrimSpace(os.Getenv(env)) != "" {
			return true, nil
		}
	}
	return false, nil
}

// loadResource builds the OTel resource: the default service name,
// overridden by OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES.
func loadResource(ctx context.Context) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(semconv.ServiceName(defaultServiceName)),
		resource.WithFromEnv(),
	)
}
