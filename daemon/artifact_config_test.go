package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestArtifactsConfigParse(t *testing.T) {
	parsed, err := (&ArtifactsConfig{
		OffloadThresholdBytes: 128,
		MaxSizeBytes:          1024,
		MaxRunBytes:           4096,
		MaxArtifacts:          12,
		AllowedContentTypes:   []string{"text/plain; charset=utf-8", "application/json"},
		Retention:             "24h",
		TenantRetention:       map[string]string{"tenant-a": "12h"},
		WorkflowRetention:     map[string]string{"workflow-a": "1h"},
		PruneInterval:         "15m",
		MaxRetries:            3,
		RetryBackoff:          "20ms",
	}).Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.OffloadThresholdBytes != 128 || parsed.MaxSizeBytes != 1024 || parsed.MaxRunBytes != 4096 || parsed.MaxArtifacts != 12 || parsed.MaxRetries != 3 || parsed.RetryBackoff != 20*time.Millisecond || parsed.Retention != 24*time.Hour || parsed.PruneInterval != 15*time.Minute {
		t.Fatalf("Parse = %+v, want configured values", parsed)
	}
	if parsed.TenantRetention["tenant-a"] != 12*time.Hour || parsed.WorkflowRetention["workflow-a"] != time.Hour {
		t.Fatalf("retention overrides = tenants %v workflows %v", parsed.TenantRetention, parsed.WorkflowRetention)
	}
	wantTypes := []string{"text/plain; charset=utf-8", "application/json"}
	if !reflect.DeepEqual(parsed.AllowedContentTypes, wantTypes) {
		t.Fatalf("AllowedContentTypes = %v, want %v", parsed.AllowedContentTypes, wantTypes)
	}
}

func TestArtifactsConfigDefaultsAndValidation(t *testing.T) {
	parsed, err := (&ArtifactsConfig{}).Parse()
	if err != nil {
		t.Fatalf("Parse defaults: %v", err)
	}
	if parsed.MaxSizeBytes != DefaultArtifactMaxSizeBytes || parsed.Retention != DefaultArtifactRetention || parsed.PruneInterval != time.Hour {
		t.Fatalf("defaults = %+v", parsed)
	}

	tests := []ArtifactsConfig{
		{MaxSizeBytes: -1},
		{AllowedContentTypes: []string{"invalid"}},
		{Retention: "invalid"},
		{Retention: "-1h"},
		{PruneInterval: "0s"},
		{TenantRetention: map[string]string{"bad tenant": "1h"}},
		{TenantRetention: map[string]string{"tenant": "-1h"}},
		{WorkflowRetention: map[string]string{"workflow": "invalid"}},
	}
	for _, config := range tests {
		if _, err := config.Parse(); err == nil {
			t.Errorf("Parse(%+v) error = nil, want validation error", config)
		}
	}
}

func TestLoadArtifactsConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "petalflow.yaml")
	data := []byte("artifacts:\n  max_size_bytes: 2048\n  allowed_content_types: [text/plain]\n  retention: 48h\n  tenant_retention:\n    tenant-a: 24h\n  workflow_retention:\n    workflow-a: 12h\n  prune_interval: 30m\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	config, err := LoadArtifactsConfig(path)
	if err != nil {
		t.Fatalf("LoadArtifactsConfig: %v", err)
	}
	if config == nil || config.MaxSizeBytes != 2048 || config.Retention != 48*time.Hour ||
		config.TenantRetention["tenant-a"] != 24*time.Hour || config.WorkflowRetention["workflow-a"] != 12*time.Hour {
		t.Fatalf("config = %+v, want loaded artifacts section", config)
	}
	missing, err := LoadArtifactsConfig("")
	if err != nil || missing != nil {
		t.Fatalf("LoadArtifactsConfig(empty) = %+v, %v; want nil, nil", missing, err)
	}

	withoutSection := filepath.Join(t.TempDir(), "without-artifacts.yaml")
	if err := os.WriteFile(withoutSection, []byte("tools: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile without section: %v", err)
	}
	missing, err = LoadArtifactsConfig(withoutSection)
	if err != nil || missing != nil {
		t.Fatalf("LoadArtifactsConfig(without section) = %+v, %v; want nil, nil", missing, err)
	}

	invalid := filepath.Join(t.TempDir(), "invalid-artifacts.yaml")
	if err := os.WriteFile(invalid, []byte("artifacts:\n  retention: invalid\n"), 0o600); err != nil {
		t.Fatalf("WriteFile invalid section: %v", err)
	}
	if _, err := LoadArtifactsConfig(invalid); err == nil {
		t.Fatal("LoadArtifactsConfig invalid section error = nil")
	}
}
