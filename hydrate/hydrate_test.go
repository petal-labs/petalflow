package hydrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
)

func TestResolveProviders_FromFlags(t *testing.T) {
	// Point config to a non-existent path so config file is skipped
	t.Setenv("PETALFLOW_CONFIG", filepath.Join(t.TempDir(), "nonexistent.json"))

	flags := map[string]string{
		"anthropic": "sk-ant-test-key",
		"openai":    "sk-openai-test-key",
	}

	providers, err := ResolveProviders(flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := providers["anthropic"].APIKey; got != "sk-ant-test-key" {
		t.Errorf("anthropic API key = %q, want %q", got, "sk-ant-test-key")
	}
	if got := providers["openai"].APIKey; got != "sk-openai-test-key" {
		t.Errorf("openai API key = %q, want %q", got, "sk-openai-test-key")
	}
}

func TestResolveProviders_FromEnv(t *testing.T) {
	// Point config to a non-existent path so config file is skipped
	t.Setenv("PETALFLOW_CONFIG", filepath.Join(t.TempDir(), "nonexistent.json"))

	t.Setenv("PETALFLOW_PROVIDER_TESTPROV_API_KEY", "env-api-key-123")
	t.Setenv("PETALFLOW_PROVIDER_TESTPROV_BASE_URL", "https://custom.api.example.com")

	providers, err := ResolveProviders(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pc, ok := providers["testprov"]
	if !ok {
		t.Fatal("expected provider 'testprov' to exist")
	}
	if pc.APIKey != "env-api-key-123" {
		t.Errorf("API key = %q, want %q", pc.APIKey, "env-api-key-123")
	}
	if pc.BaseURL != "https://custom.api.example.com" {
		t.Errorf("base URL = %q, want %q", pc.BaseURL, "https://custom.api.example.com")
	}
}

func TestResolveProviders_FlagOverridesEnv(t *testing.T) {
	// Point config to a non-existent path so config file is skipped
	t.Setenv("PETALFLOW_CONFIG", filepath.Join(t.TempDir(), "nonexistent.json"))

	t.Setenv("PETALFLOW_PROVIDER_MYPROV_API_KEY", "env-key")

	flags := map[string]string{
		"myprov": "flag-key",
	}

	providers, err := ResolveProviders(flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := providers["myprov"].APIKey; got != "flag-key" {
		t.Errorf("API key = %q, want %q (flag should override env)", got, "flag-key")
	}
}

func TestResolveProviders_FromConfigFile(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"anthropic": {APIKey: "config-key", BaseURL: "https://config.example.com"},
		},
		Defaults: map[string]string{
			"provider": "anthropic",
		},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshaling config: %v", err)
	}

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	t.Setenv("PETALFLOW_CONFIG", cfgPath)

	providers, err := ResolveProviders(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pc, ok := providers["anthropic"]
	if !ok {
		t.Fatal("expected provider 'anthropic' from config file")
	}
	if pc.APIKey != "config-key" {
		t.Errorf("API key = %q, want %q", pc.APIKey, "config-key")
	}
	if pc.BaseURL != "https://config.example.com" {
		t.Errorf("base URL = %q, want %q", pc.BaseURL, "https://config.example.com")
	}
}

func TestResolveProviders_ConfigFileNotFound(t *testing.T) {
	t.Setenv("PETALFLOW_CONFIG", filepath.Join(t.TempDir(), "does-not-exist.json"))

	providers, err := ResolveProviders(nil)
	if err != nil {
		t.Fatalf("expected no error for missing config file, got: %v", err)
	}
	if len(providers) != 0 {
		t.Errorf("expected empty providers, got %d", len(providers))
	}
}

func TestResolveProviders_PriorityOrder(t *testing.T) {
	// Set up all three sources for the same provider

	// Config file: lowest priority
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"myprov": {APIKey: "config-key", BaseURL: "https://config.example.com"},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshaling config: %v", err)
	}
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}
	t.Setenv("PETALFLOW_CONFIG", cfgPath)

	// Env var: medium priority (overrides config API key)
	t.Setenv("PETALFLOW_PROVIDER_MYPROV_API_KEY", "env-key")

	// Flag: highest priority (overrides both)
	flags := map[string]string{
		"myprov": "flag-key",
	}

	providers, err := ResolveProviders(flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pc := providers["myprov"]

	// API key should come from flags (highest priority)
	if pc.APIKey != "flag-key" {
		t.Errorf("API key = %q, want %q (flag > env > config)", pc.APIKey, "flag-key")
	}

	// Base URL should come from config (only source for it)
	if pc.BaseURL != "https://config.example.com" {
		t.Errorf("base URL = %q, want %q (from config, no env/flag override)", pc.BaseURL, "https://config.example.com")
	}
}

func TestHydrateGraph_Success(t *testing.T) {
	def := &graph.GraphDefinition{
		ID:      "test-graph",
		Version: "1.0",
		Nodes: []graph.NodeDef{
			{ID: "start", Type: "noop"},
			{ID: "end", Type: "noop"},
		},
		Edges: []graph.EdgeDef{
			{Source: "start", Target: "end"},
		},
		Entry: "start",
	}

	factory := func(nd graph.NodeDef) (core.Node, error) {
		return core.NewFuncNode(nd.ID, nil), nil
	}

	g, err := HydrateGraph(def, nil, factory)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if g.Name() != "test-graph" {
		t.Errorf("graph name = %q, want %q", g.Name(), "test-graph")
	}
	if len(g.Nodes()) != 2 {
		t.Errorf("node count = %d, want 2", len(g.Nodes()))
	}
	if g.Entry() != "start" {
		t.Errorf("entry = %q, want %q", g.Entry(), "start")
	}
	succs := g.Successors("start")
	if len(succs) != 1 || succs[0] != "end" {
		t.Errorf("start successors = %v, want [end]", succs)
	}
}

func TestHydrateGraph_NilDefinition(t *testing.T) {
	_, err := HydrateGraph(nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil definition, got nil")
	}
	if got := err.Error(); got != "graph definition is nil" {
		t.Errorf("error = %q, want %q", got, "graph definition is nil")
	}
}

func TestHydrateGraph_DefaultFactory_MissingProvider(t *testing.T) {
	def := &graph.GraphDefinition{
		ID:      "llm-graph",
		Version: "1.0",
		Nodes: []graph.NodeDef{
			{
				ID:   "prompt",
				Type: "llm_prompt",
				Config: map[string]any{
					"provider": "anthropic",
				},
			},
		},
		Entry: "prompt",
	}

	// Empty providers -- "anthropic" is not configured
	providers := ProviderMap{}

	_, err := HydrateGraph(def, providers, nil)
	if err == nil {
		t.Fatal("expected error for missing provider, got nil")
	}

	want := `provider "anthropic" not configured (needed by node "prompt")`
	if got := err.Error(); !contains(got, want) {
		t.Errorf("error = %q, want it to contain %q", got, want)
	}
}

func TestHydrateGraph_DefaultFactory_ProviderPresent(t *testing.T) {
	def := &graph.GraphDefinition{
		ID:      "llm-graph",
		Version: "1.0",
		Nodes: []graph.NodeDef{
			{
				ID:   "prompt",
				Type: "llm_prompt",
				Config: map[string]any{
					"provider": "anthropic",
				},
			},
		},
		Entry: "prompt",
	}

	providers := ProviderMap{
		"anthropic": ProviderConfig{APIKey: "sk-test"},
	}

	g, err := HydrateGraph(def, providers, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Nodes()) != 1 {
		t.Errorf("node count = %d, want 1", len(g.Nodes()))
	}
}

func TestParseProviderFlags_Valid(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
		want  map[string]string
	}{
		{
			name:  "single flag",
			flags: []string{"anthropic=sk-ant-key123"},
			want:  map[string]string{"anthropic": "sk-ant-key123"},
		},
		{
			name:  "multiple flags",
			flags: []string{"anthropic=sk-ant-key", "openai=sk-openai-key"},
			want:  map[string]string{"anthropic": "sk-ant-key", "openai": "sk-openai-key"},
		},
		{
			name:  "value with equals sign",
			flags: []string{"provider=key=with=equals"},
			want:  map[string]string{"provider": "key=with=equals"},
		},
		{
			name:  "empty list",
			flags: []string{},
			want:  map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProviderFlags(tt.flags)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d entries, want %d", len(got), len(tt.want))
			}
			for k, wantV := range tt.want {
				if gotV, ok := got[k]; !ok || gotV != wantV {
					t.Errorf("got[%q] = %q, want %q", k, gotV, wantV)
				}
			}
		})
	}
}

func TestParseProviderFlags_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
	}{
		{name: "no equals", flags: []string{"anthropic"}},
		{name: "empty name", flags: []string{"=sk-key"}},
		{name: "empty value", flags: []string{"anthropic="}},
		{name: "just equals", flags: []string{"="}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseProviderFlags(tt.flags)
			if err == nil {
				t.Fatal("expected error for invalid flag, got nil")
			}
		})
	}
}

// contains checks if s contains substr. Using a helper instead of
// strings.Contains to keep imports minimal and consistent.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
