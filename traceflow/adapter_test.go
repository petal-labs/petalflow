package traceflow

import (
	"testing"

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
		{"unknown", CaptureStandard}, // Default
		{"", CaptureStandard},        // Default
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
	if adapter.CaptureMode != CaptureStandard {
		t.Errorf("CaptureMode = %v, want %v", adapter.CaptureMode, CaptureStandard)
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
		mode              CaptureMode
		llmContent        bool
		edgeData          bool
		snapshots         bool
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
