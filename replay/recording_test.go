package replay

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validRecording() *Recording {
	return &Recording{
		Version:    FormatVersion,
		RunID:      "run-1",
		StartedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC),
		Status:     "succeeded",
		Nodes: []NodeRecord{
			{NodeID: "a", NodeKind: "noop", Output: &EnvelopeState{Input: "x"}},
		},
	}
}

func TestRecordingValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Recording)
		wantErr bool
	}{
		{name: "valid", mutate: func(r *Recording) {}},
		{
			name:    "wrong version",
			mutate:  func(r *Recording) { r.Version = FormatVersion + 1 },
			wantErr: true,
		},
		{
			name:    "missing run id",
			mutate:  func(r *Recording) { r.RunID = "" },
			wantErr: true,
		},
		{
			name:    "node missing id",
			mutate:  func(r *Recording) { r.Nodes[0].NodeID = "" },
			wantErr: true,
		},
		{
			name: "empty node record",
			mutate: func(r *Recording) {
				r.Nodes[0] = NodeRecord{NodeID: "a"}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := validRecording()
			tt.mutate(rec)
			err := rec.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrInvalidRecording) {
				t.Errorf("error should wrap ErrInvalidRecording, got %v", err)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	rec := validRecording()
	rec.WorkflowID = "wf-1"
	rec.Nodes[0].LLM = []LLMCallRecord{{
		NodeID:     "a",
		Model:      "test-model",
		Completion: "hello",
		Usage:      UsageRecord{InputTokens: 3, OutputTokens: 2, TotalTokens: 5},
	}}

	path := filepath.Join(t.TempDir(), "recording.json")
	if err := Save(rec, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.RunID != rec.RunID || loaded.Status != rec.Status {
		t.Errorf("round-trip mismatch: %+v", loaded)
	}
	if len(loaded.Nodes) != 1 || len(loaded.Nodes[0].LLM) != 1 {
		t.Fatalf("round-trip nodes = %+v", loaded.Nodes)
	}
	if loaded.Nodes[0].LLM[0].Usage.TotalTokens != 5 {
		t.Errorf("usage = %+v", loaded.Nodes[0].LLM[0].Usage)
	}
}

func TestLoadRejectsCorruptRecording(t *testing.T) {
	tests := []struct {
		name string
		file string
		data string
	}{
		{
			name: "not json",
			file: "bad.json",
			data: "not json at all",
		},
		{
			name: "wrong version",
			file: "v99.json",
			data: `{"version":99,"run_id":"r"}`,
		},
		{
			name: "missing run id",
			file: "noid.json",
			data: `{"version":1}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tt.file)
			if err := os.WriteFile(path, []byte(tt.data), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, err := Load(path); err == nil {
				t.Fatalf("Load expected error, got nil")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSummarize(t *testing.T) {
	rec := validRecording()
	rec.GraphName = "test-graph"
	rec.Nodes[0].LLM = []LLMCallRecord{{Usage: UsageRecord{TotalTokens: 42, CostUSD: 0.01}}}

	summary := Summarize(rec)
	for _, want := range []string{"run-1", "succeeded", "test-graph", "nodes=1", "tokens=42"} {
		if !strings.Contains(summary, want) {
			t.Errorf("Summarize() = %q, missing %q", summary, want)
		}
	}
	if strings.Contains(summary, "hello") {
		t.Error("Summarize must not contain content")
	}
	if Summarize(nil) != "" {
		t.Error("Summarize(nil) should be empty")
	}
}
