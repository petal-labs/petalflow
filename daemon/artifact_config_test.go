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
		MaxSizeBytes:        1024,
		AllowedContentTypes: []string{"text/plain; charset=utf-8", "application/json"},
		Retention:           "24h",
		PruneInterval:       "15m",
	}).Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.MaxSizeBytes != 1024 || parsed.Retention != 24*time.Hour || parsed.PruneInterval != 15*time.Minute {
		t.Fatalf("Parse = %+v, want configured values", parsed)
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
	}
	for _, config := range tests {
		if _, err := config.Parse(); err == nil {
			t.Errorf("Parse(%+v) error = nil, want validation error", config)
		}
	}
}

func TestLoadArtifactsConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "petalflow.yaml")
	data := []byte("artifacts:\n  max_size_bytes: 2048\n  allowed_content_types: [text/plain]\n  retention: 48h\n  prune_interval: 30m\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	config, err := LoadArtifactsConfig(path)
	if err != nil {
		t.Fatalf("LoadArtifactsConfig: %v", err)
	}
	if config == nil || config.MaxSizeBytes != 2048 || config.Retention != 48*time.Hour {
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
