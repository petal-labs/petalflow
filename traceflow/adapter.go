// Package traceflow provides the PetalTrace SDK adapter for enriching
// PetalFlow execution traces with observability data.
//
// The adapter wraps PetalFlow's event system to capture and enrich spans
// with PetalFlow-specific semantic attributes, enabling rich visualization
// and analysis in PetalTrace.
package traceflow

import (
	"context"
	"encoding/json"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/petal-labs/petalflow/daemon"
	"github.com/petal-labs/petalflow/runtime"
)

// CaptureMode controls the level of detail captured in traces.
type CaptureMode int

const (
	// CaptureMinimal captures OTel spans only (latency, status, token counts).
	// Storage impact: ~1 KB/span. Use case: Production monitoring.
	CaptureMinimal CaptureMode = iota

	// CaptureStandard captures prompts, completions, tool args/results.
	// Storage impact: ~10-100 KB/span. Use case: Development, debugging.
	CaptureStandard

	// CaptureFull captures all data including edge transfers and graph snapshots.
	// Storage impact: ~100 KB-1 MB/span. Use case: Replay-capable runs.
	CaptureFull
)

// String returns the string representation of CaptureMode.
func (m CaptureMode) String() string {
	switch m {
	case CaptureMinimal:
		return "minimal"
	case CaptureStandard:
		return "standard"
	case CaptureFull:
		return "full"
	default:
		return "unknown"
	}
}

// ParseCaptureMode parses a string into a CaptureMode.
func ParseCaptureMode(s string) CaptureMode {
	switch s {
	case "minimal":
		return CaptureMinimal
	case "standard":
		return CaptureStandard
	case "full":
		return CaptureFull
	default:
		return CaptureStandard
	}
}

// Adapter wraps PetalFlow's execution to emit PetalTrace-enriched spans.
// It provides hooks that can be attached to PetalFlow's event system
// to capture rich observability data.
type Adapter struct {
	// Endpoint is the PetalTrace collector endpoint (OTLP/HTTP).
	Endpoint string

	// CaptureMode controls the level of detail captured.
	CaptureMode CaptureMode

	// SampleRate controls the percentage of runs to trace (0.0 - 1.0).
	SampleRate float64

	// AlwaysCaptureErrors ensures failed runs are always captured.
	AlwaysCaptureErrors bool

	// Tags are key-value pairs attached to all traces.
	Tags map[string]string

	// provider is the trace provider (initialized on first use)
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer

	// mu protects provider initialization
	mu sync.RWMutex

	// runSnapshots stores graph/input snapshots per run for full capture mode
	runSnapshots map[string]*RunSnapshot
	snapshotsMu  sync.RWMutex
}

// RunSnapshot captures the initial state of a run for replay support.
type RunSnapshot struct {
	GraphDefinition json.RawMessage `json:"graph_definition,omitempty"`
	Inputs          json.RawMessage `json:"inputs,omitempty"`
	Config          json.RawMessage `json:"config,omitempty"`
}

// NewAdapter creates a new PetalTrace adapter from configuration.
func NewAdapter(cfg *daemon.PetalTraceConfig) *Adapter {
	if cfg == nil {
		return nil
	}

	return &Adapter{
		Endpoint:            cfg.Endpoint,
		CaptureMode:         ParseCaptureMode(cfg.CaptureMode),
		SampleRate:          cfg.SampleRate,
		AlwaysCaptureErrors: cfg.AlwaysCaptureErrors,
		Tags:                cfg.Tags,
		runSnapshots:        make(map[string]*RunSnapshot),
	}
}

// NewAdapterWithEndpoint creates a simple adapter with just an endpoint.
func NewAdapterWithEndpoint(endpoint string) *Adapter {
	return &Adapter{
		Endpoint:            endpoint,
		CaptureMode:         CaptureStandard,
		SampleRate:          1.0,
		AlwaysCaptureErrors: true,
		runSnapshots:        make(map[string]*RunSnapshot),
	}
}

// Initialize sets up the OTLP exporter and trace provider.
// This must be called before the adapter can emit traces.
func (a *Adapter) Initialize(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.provider != nil {
		return nil // Already initialized
	}

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(a.Endpoint),
		otlptracehttp.WithInsecure(), // Development default; configure TLS for production
	)
	if err != nil {
		return err
	}

	// Build resource with service info and tags
	attrs := []attribute.KeyValue{
		semconv.ServiceNameKey.String("petalflow"),
		attribute.String("petalflow.capture_mode", a.CaptureMode.String()),
	}
	for k, v := range a.Tags {
		attrs = append(attrs, attribute.String("petalflow.tag."+k, v))
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return err
	}

	// Configure sampler based on sample rate
	var sampler sdktrace.Sampler
	if a.SampleRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if a.SampleRate <= 0.0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.TraceIDRatioBased(a.SampleRate)
	}

	a.provider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(a.provider)
	a.tracer = a.provider.Tracer("petalflow.traceflow")

	return nil
}

// Shutdown flushes pending traces and shuts down the provider.
func (a *Adapter) Shutdown(ctx context.Context) error {
	a.mu.RLock()
	provider := a.provider
	a.mu.RUnlock()

	if provider != nil {
		return provider.Shutdown(ctx)
	}
	return nil
}

// Tracer returns the underlying OTel tracer for manual instrumentation.
func (a *Adapter) Tracer() trace.Tracer {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tracer
}

// EventHandler returns a runtime.EventHandler that enriches events
// with PetalTrace-specific attributes based on capture mode.
func (a *Adapter) EventHandler() runtime.EventHandler {
	return func(e runtime.Event) {
		a.handleEvent(e)
	}
}

// EventEmitterDecorator returns a decorator that enriches emitted events.
func (a *Adapter) EventEmitterDecorator() runtime.EventEmitterDecorator {
	return func(emitter runtime.EventEmitter) runtime.EventEmitter {
		return func(e runtime.Event) {
			a.enrichEvent(&e)
			emitter(e)
		}
	}
}

// handleEvent processes events and stores snapshots for replay support.
func (a *Adapter) handleEvent(e runtime.Event) {
	switch e.Kind {
	case runtime.EventRunStarted:
		a.captureRunStart(e)
	case runtime.EventRunFinished:
		a.captureRunEnd(e)
	}
}

// enrichEvent adds PetalTrace-specific attributes to events based on capture mode.
func (a *Adapter) enrichEvent(e *runtime.Event) {
	if e.Payload == nil {
		e.Payload = make(map[string]any)
	}

	// Add capture mode marker
	e.Payload["_petaltrace_capture_mode"] = a.CaptureMode.String()

	// Add tags
	for k, v := range a.Tags {
		e.Payload["_petaltrace_tag_"+k] = v
	}
}

// captureRunStart captures initial state for replay support (full mode only).
func (a *Adapter) captureRunStart(e runtime.Event) {
	if a.CaptureMode != CaptureFull {
		return
	}

	snapshot := &RunSnapshot{}

	// Capture graph definition if available
	if graphDef, ok := e.Payload["graph_definition"]; ok {
		if jsonData, err := json.Marshal(graphDef); err == nil {
			snapshot.GraphDefinition = jsonData
		}
	}

	// Capture inputs if available
	if inputs, ok := e.Payload["inputs"]; ok {
		if jsonData, err := json.Marshal(inputs); err == nil {
			snapshot.Inputs = jsonData
		}
	}

	// Capture config if available
	if cfg, ok := e.Payload["config"]; ok {
		if jsonData, err := json.Marshal(cfg); err == nil {
			snapshot.Config = jsonData
		}
	}

	a.snapshotsMu.Lock()
	a.runSnapshots[e.RunID] = snapshot
	a.snapshotsMu.Unlock()
}

// captureRunEnd cleans up run state.
func (a *Adapter) captureRunEnd(e runtime.Event) {
	a.snapshotsMu.Lock()
	delete(a.runSnapshots, e.RunID)
	a.snapshotsMu.Unlock()
}

// GetRunSnapshot retrieves the captured snapshot for a run (full mode only).
func (a *Adapter) GetRunSnapshot(runID string) *RunSnapshot {
	a.snapshotsMu.RLock()
	defer a.snapshotsMu.RUnlock()
	return a.runSnapshots[runID]
}

// ShouldCaptureLLMContent returns true if LLM prompts/completions should be captured.
func (a *Adapter) ShouldCaptureLLMContent() bool {
	return a.CaptureMode >= CaptureStandard
}

// ShouldCaptureEdgeData returns true if edge transfer data should be captured.
func (a *Adapter) ShouldCaptureEdgeData() bool {
	return a.CaptureMode >= CaptureFull
}

// ShouldCaptureSnapshots returns true if graph/input snapshots should be captured.
func (a *Adapter) ShouldCaptureSnapshots() bool {
	return a.CaptureMode == CaptureFull
}
