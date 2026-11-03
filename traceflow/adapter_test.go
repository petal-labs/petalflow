package traceflow

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"

	"github.com/petal-labs/petalflow/daemon"
)

func TestParseCaptureMode(t *testing.T) {
	tests := []struct {
		input    string
		expected CaptureMode
	}{
		{"minimal", CaptureMinimal},
		{"standard", CaptureStandard},
		{"full", CaptureFull},
		{"unknown", CaptureMinimal}, // Fails closed: metadata only
		{"", CaptureMinimal},        // Fails closed: metadata only
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseCaptureMode(tt.input)
			if got != tt.expected {
				t.Errorf("ParseCaptureMode(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCaptureModeString(t *testing.T) {
	tests := []struct {
		mode     CaptureMode
		expected string
	}{
		{CaptureMinimal, "minimal"},
		{CaptureStandard, "standard"},
		{CaptureFull, "full"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := tt.mode.String()
			if got != tt.expected {
				t.Errorf("CaptureMode(%v).String() = %q, want %q", tt.mode, got, tt.expected)
			}
		})
	}
}

func TestNewAdapter(t *testing.T) {
	t.Run("nil config returns nil", func(t *testing.T) {
		adapter := NewAdapter(nil)
		if adapter != nil {
			t.Error("NewAdapter(nil) should return nil")
		}
	})

	t.Run("valid config creates adapter", func(t *testing.T) {
		cfg := &daemon.PetalTraceConfig{
			Enabled:             true,
			Endpoint:            "http://localhost:4318",
			CaptureMode:         "standard",
			SampleRate:          1.0,
			AlwaysCaptureErrors: true,
			Tags:                map[string]string{"env": "test"},
		}

		adapter := NewAdapter(cfg)
		if adapter == nil {
			t.Fatal("NewAdapter should return non-nil adapter")
		}

		if adapter.Endpoint != cfg.Endpoint {
			t.Errorf("Endpoint = %q, want %q", adapter.Endpoint, cfg.Endpoint)
		}
		if adapter.CaptureMode != CaptureStandard {
			t.Errorf("CaptureMode = %v, want %v", adapter.CaptureMode, CaptureStandard)
		}
		if adapter.SampleRate != cfg.SampleRate {
			t.Errorf("SampleRate = %v, want %v", adapter.SampleRate, cfg.SampleRate)
		}
		if len(adapter.Tags) != 1 || adapter.Tags["env"] != "test" {
			t.Errorf("Tags not set correctly")
		}
	})
}

func TestNewAdapterWithEndpoint(t *testing.T) {
	endpoint := "http://localhost:4318"
	adapter := NewAdapterWithEndpoint(endpoint)

	if adapter == nil {
		t.Fatal("NewAdapterWithEndpoint should return non-nil adapter")
	}

	if adapter.Endpoint != endpoint {
		t.Errorf("Endpoint = %q, want %q", adapter.Endpoint, endpoint)
	}
	if adapter.CaptureMode != CaptureMinimal {
		t.Errorf("CaptureMode = %v, want %v (metadata-only default)", adapter.CaptureMode, CaptureMinimal)
	}
	if adapter.SampleRate != 1.0 {
		t.Errorf("SampleRate = %v, want 1.0", adapter.SampleRate)
	}
	if !adapter.AlwaysCaptureErrors {
		t.Error("AlwaysCaptureErrors should be true by default")
	}
}

func TestAdapterShouldCapture(t *testing.T) {
	tests := []struct {
		mode       CaptureMode
		llmContent bool
		edgeData   bool
		snapshots  bool
	}{
		{CaptureMinimal, false, false, false},
		{CaptureStandard, true, false, false},
		{CaptureFull, true, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			adapter := &Adapter{CaptureMode: tt.mode}

			if got := adapter.ShouldCaptureLLMContent(); got != tt.llmContent {
				t.Errorf("ShouldCaptureLLMContent() = %v, want %v", got, tt.llmContent)
			}
			if got := adapter.ShouldCaptureEdgeData(); got != tt.edgeData {
				t.Errorf("ShouldCaptureEdgeData() = %v, want %v", got, tt.edgeData)
			}
			if got := adapter.ShouldCaptureSnapshots(); got != tt.snapshots {
				t.Errorf("ShouldCaptureSnapshots() = %v, want %v", got, tt.snapshots)
			}
		})
	}
}

func TestRunSnapshotStorage(t *testing.T) {
	adapter := NewAdapterWithEndpoint("http://localhost:4318")
	adapter.CaptureMode = CaptureFull

	runID := "test-run-123"

	// Initially no snapshot
	if snapshot := adapter.GetRunSnapshot(runID); snapshot != nil {
		t.Error("GetRunSnapshot should return nil for unknown run")
	}

	// Store a snapshot manually (simulating captureRunStart)
	adapter.snapshotsMu.Lock()
	adapter.runSnapshots[runID] = &RunSnapshot{
		GraphDefinition: []byte(`{"id":"test"}`),
	}
	adapter.snapshotsMu.Unlock()

	// Should retrieve the snapshot
	snapshot := adapter.GetRunSnapshot(runID)
	if snapshot == nil {
		t.Fatal("GetRunSnapshot should return the stored snapshot")
	}
	if string(snapshot.GraphDefinition) != `{"id":"test"}` {
		t.Error("GraphDefinition not stored correctly")
	}
}

func TestAdapterContentCaptureLadder(t *testing.T) {
	tests := []struct {
		mode    CaptureMode
		enabled bool
	}{
		{CaptureMinimal, false},
		{CaptureStandard, true},
		{CaptureFull, true},
	}
	for _, tt := range tests {
		adapter := &Adapter{CaptureMode: tt.mode}
		if got := adapter.ContentCapture().Enabled; got != tt.enabled {
			t.Errorf("ContentCapture(%v).Enabled = %v, want %v", tt.mode, got, tt.enabled)
		}
	}
}

func TestEffectiveSampleRate(t *testing.T) {
	tests := []struct {
		name      string
		rate      float64
		tags      map[string]string
		overrides map[string]float64
		want      float64
	}{
		{
			name: "no overrides keeps base rate",
			rate: 0.2,
			want: 0.2,
		},
		{
			name:      "matching tag override applies",
			rate:      0.2,
			tags:      map[string]string{"environment": "staging"},
			overrides: map[string]float64{"environment:staging": 1.0},
			want:      1.0,
		},
		{
			name:      "non-matching override ignored",
			rate:      0.2,
			tags:      map[string]string{"environment": "production"},
			overrides: map[string]float64{"environment:staging": 1.0},
			want:      0.2,
		},
		{
			name:      "highest matching override wins",
			rate:      0.1,
			tags:      map[string]string{"environment": "staging", "team": "platform"},
			overrides: map[string]float64{"environment:staging": 0.5, "team:platform": 1.0},
			want:      1.0,
		},
		{
			name:      "lowering override never reduces the base rate",
			rate:      0.8,
			tags:      map[string]string{"environment": "production"},
			overrides: map[string]float64{"environment:production": 0.1},
			want:      0.8,
		},
		{
			name:      "malformed override key ignored",
			rate:      0.3,
			tags:      map[string]string{"environment": "staging"},
			overrides: map[string]float64{"environment": 1.0},
			want:      0.3,
		},
		{
			name:      "overrides without tags ignored",
			rate:      0.3,
			overrides: map[string]float64{"environment:staging": 1.0},
			want:      0.3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &Adapter{SampleRate: tt.rate, Tags: tt.tags, SampleOverrides: tt.overrides}
			if got := adapter.EffectiveSampleRate(); got != tt.want {
				t.Errorf("EffectiveSampleRate() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAdapterInitializeSamplerBranches drives Initialize with the
// effective rate at each sampler branch (never, ratio, always) against
// a local collector; globals are restored afterwards.
func TestAdapterInitializeSamplerBranches(t *testing.T) {
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)

	prevTracer := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prevTracer) })

	for _, rate := range []float64{0.0, 0.5, 1.0} {
		t.Run(fmt.Sprintf("rate %.1f", rate), func(t *testing.T) {
			adapter := NewAdapter(&daemon.PetalTraceConfig{
				Enabled:     true,
				Endpoint:    collector.URL,
				CaptureMode: "minimal",
				SampleRate:  rate,
			})
			if err := adapter.Initialize(context.Background()); err != nil {
				t.Fatalf("Initialize: %v", err)
			}
			if adapter.Tracer() == nil {
				t.Fatal("tracer missing after Initialize")
			}
			if err := adapter.Shutdown(context.Background()); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
		})
	}
}
