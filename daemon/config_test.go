package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/tool"
)

func TestDiscoverToolConfigPathFrom_FirstMatchWins(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	projectConfig := filepath.Join(cwd, "petalflow.yaml")
	if err := os.WriteFile(projectConfig, []byte("tools: {}"), 0o600); err != nil {
		t.Fatalf("WriteFile(project config) error = %v", err)
	}

	homeConfigDir := filepath.Join(home, ".petalflow")
	if err := os.MkdirAll(homeConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(home config dir) error = %v", err)
	}
	homeConfig := filepath.Join(homeConfigDir, "config.yaml")
	if err := os.WriteFile(homeConfig, []byte("tools: {}"), 0o600); err != nil {
		t.Fatalf("WriteFile(home config) error = %v", err)
	}

	got, found, err := DiscoverToolConfigPathFrom("", cwd, home)
	if err != nil {
		t.Fatalf("DiscoverToolConfigPathFrom() error = %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if got != projectConfig {
		t.Fatalf("path = %q, want %q", got, projectConfig)
	}
}

func TestDiscoverToolConfigPathFrom_ExplicitNotFound(t *testing.T) {
	_, found, err := DiscoverToolConfigPathFrom("/tmp/does-not-exist.yaml", t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing explicit config path")
	}
	if found {
		t.Fatal("found = true, want false")
	}
}

func TestRegisterToolsFromConfig_RegistersDeclarativeTools(t *testing.T) {
	baseDir := t.TempDir()
	manifestPath := filepath.Join(baseDir, "pdf.tool.json")
	manifest := `{
  "manifest_version": "1.0",
  "tool": { "name": "pdf_extract", "description": "Extract text" },
  "transport": { "type": "http", "endpoint": "http://example.invalid" },
  "actions": {
    "extract": {
      "inputs": { "path": { "type": "string", "required": true } },
      "outputs": { "text": { "type": "string" } }
    }
  },
  "config": {
    "region": { "type": "string", "required": true }
  }
}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}

	configPath := filepath.Join(baseDir, "petalflow.yaml")
	configYAML := `
tools:
  template_render:
    type: native
  pdf_extract:
    type: http
    manifest: ./pdf.tool.json
    endpoint: http://localhost:9802
    config:
      region: ${TEST_REGION}
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	originalRegion := os.Getenv("TEST_REGION")
	t.Cleanup(func() {
		_ = os.Setenv("TEST_REGION", originalRegion)
	})
	if err := os.Setenv("TEST_REGION", "us-west-2"); err != nil {
		t.Fatalf("Setenv(TEST_REGION) error = %v", err)
	}

	service, err := tool.NewDaemonToolService(tool.DaemonToolServiceConfig{
		Store:               newSQLiteToolStore(t),
		ReachabilityChecker: noopReachabilityChecker{},
	})
	if err != nil {
		t.Fatalf("NewDaemonToolService() error = %v", err)
	}

	registered, err := RegisterToolsFromConfig(context.Background(), service, configPath)
	if err != nil {
		t.Fatalf("RegisterToolsFromConfig() error = %v", err)
	}
	if len(registered) != 2 {
		t.Fatalf("registered count = %d, want 2", len(registered))
	}

	byName := make(map[string]tool.ToolRegistration, len(registered))
	for _, reg := range registered {
		byName[reg.Name] = reg
	}

	native := byName["template_render"]
	if native.Origin != tool.OriginNative {
		t.Fatalf("template_render origin = %q, want native", native.Origin)
	}

	httpTool := byName["pdf_extract"]
	if httpTool.Origin != tool.OriginHTTP {
		t.Fatalf("pdf_extract origin = %q, want http", httpTool.Origin)
	}
	if got := httpTool.Config["region"]; got != "us-west-2" {
		t.Fatalf("pdf_extract config region = %q, want us-west-2", got)
	}
	if got := httpTool.Manifest.Transport.Endpoint; got != "http://localhost:9802" {
		t.Fatalf("pdf_extract endpoint = %q, want http://localhost:9802", got)
	}
}

type noopReachabilityChecker struct{}

func (noopReachabilityChecker) CheckHTTP(ctx context.Context, endpoint string) error      { return nil }
func (noopReachabilityChecker) CheckStdio(ctx context.Context, command string) error      { return nil }
func (noopReachabilityChecker) CheckMCP(ctx context.Context, reg tool.Registration) error { return nil }

func TestLoadDefaultToolPolicy(t *testing.T) {
	baseDir := t.TempDir()
	configPath := filepath.Join(baseDir, "petalflow.yaml")

	// No config file -> nil policy, no error.
	policy, err := LoadDefaultToolPolicy(filepath.Join(baseDir, "missing.yaml"))
	if policy != nil || err != nil {
		t.Fatalf("LoadDefaultToolPolicy(missing) = %v, %v; want nil, nil", policy, err)
	}

	configYAML := `
tool_policy:
  allow:
    - "mcp/*"
    - email-send
  deny:
    - fs/write
  require_approval:
    - email-send
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	policy, err = LoadDefaultToolPolicy(configPath)
	if err != nil {
		t.Fatalf("LoadDefaultToolPolicy() error = %v", err)
	}
	if policy == nil {
		t.Fatal("LoadDefaultToolPolicy() = nil, want policy")
	}
	if got, want := policy.Evaluate("email-send"), tool.PolicyNeedsApproval; got != want {
		t.Fatalf("email-send = %q, want %q", got, want)
	}
	if got, want := policy.Evaluate("fs/write"), tool.PolicyDeny; got != want {
		t.Fatalf("fs/write = %q, want %q", got, want)
	}
	if got, want := policy.Evaluate("mcp/grep"), tool.PolicyAllow; got != want {
		t.Fatalf("mcp/grep = %q, want %q", got, want)
	}

	// Config without a policy section -> nil.
	noPolicyPath := filepath.Join(baseDir, "no-policy.yaml")
	if err := os.WriteFile(noPolicyPath, []byte("tools: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(no-policy) error = %v", err)
	}
	policy, err = LoadDefaultToolPolicy(noPolicyPath)
	if policy != nil || err != nil {
		t.Fatalf("LoadDefaultToolPolicy(no policy section) = %v, %v; want nil, nil", policy, err)
	}

	// Invalid rule -> error.
	invalidPath := filepath.Join(baseDir, "invalid.yaml")
	if err := os.WriteFile(invalidPath, []byte("tool_policy:\n  allow:\n    - \"fs read\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(invalid) error = %v", err)
	}
	policy, err = LoadDefaultToolPolicy(invalidPath)
	if err == nil || policy != nil {
		t.Fatalf("LoadDefaultToolPolicy(invalid) = %v, %v; want nil, error", policy, err)
	}
}

func TestPetalTraceCaptureModeDefaultsToMinimal(t *testing.T) {
	t.Setenv("PETALTRACE_ENDPOINT", "http://localhost:4318")
	t.Setenv("PETALTRACE_CAPTURE_MODE", "")

	cfg := PetalTraceConfigFromEnv()
	if cfg == nil {
		t.Fatal("expected config with endpoint set")
	}
	if cfg.CaptureMode != CaptureModeMinimal {
		t.Errorf("CaptureMode = %q, want %q (metadata-only default)", cfg.CaptureMode, CaptureModeMinimal)
	}
	if !cfg.IsValid() {
		t.Error("default config should be valid")
	}

	// An empty capture mode (YAML without capture_mode) is valid and
	// resolves to minimal.
	empty := &PetalTraceConfig{Enabled: true, Endpoint: "http://localhost:4318"}
	if !empty.IsValid() {
		t.Error("empty capture mode should resolve to minimal and be valid")
	}

	invalid := &PetalTraceConfig{Enabled: true, Endpoint: "http://localhost:4318", CaptureMode: "loud"}
	if invalid.IsValid() {
		t.Error("unknown capture mode should be invalid")
	}
}

func TestEventsRetentionConfigParse(t *testing.T) {
	t.Run("nil config keeps safe defaults", func(t *testing.T) {
		var cfg *EventsRetentionConfig
		parsed, err := cfg.Parse()
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if parsed.Age != 0 || parsed.MaxPerRun != 0 || parsed.Interval != time.Hour {
			t.Errorf("parsed = %+v, want disabled retention with 1h interval", parsed)
		}
	})

	t.Run("valid config", func(t *testing.T) {
		parsed, err := (&EventsRetentionConfig{
			Retention:       "720h",
			MaxEventsPerRun: 1000,
			PruneInterval:   "15m",
		}).Parse()
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if parsed.Age != 720*time.Hour || parsed.MaxPerRun != 1000 || parsed.Interval != 15*time.Minute {
			t.Errorf("parsed = %+v", parsed)
		}
	})

	t.Run("invalid retention duration rejected", func(t *testing.T) {
		if _, err := (&EventsRetentionConfig{Retention: "30 days"}).Parse(); err == nil {
			t.Error("expected error for non-Go duration")
		}
	})

	t.Run("negative values rejected", func(t *testing.T) {
		if _, err := (&EventsRetentionConfig{Retention: "-1h"}).Parse(); err == nil {
			t.Error("expected error for negative retention")
		}
		if _, err := (&EventsRetentionConfig{MaxEventsPerRun: -1}).Parse(); err == nil {
			t.Error("expected error for negative max events")
		}
	})

	t.Run("non-positive prune interval rejected", func(t *testing.T) {
		if _, err := (&EventsRetentionConfig{PruneInterval: "0s"}).Parse(); err == nil {
			t.Error("expected error for zero prune interval")
		}
	})

	t.Run("loads from yaml", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "petalflow.yaml")
		yaml := []byte(`
observability:
  events:
    retention: 168h
    max_events_per_run: 500
`)
		if err := os.WriteFile(path, yaml, 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		cfg, err := LoadObservabilityConfig(path)
		if err != nil {
			t.Fatalf("LoadObservabilityConfig: %v", err)
		}
		if cfg == nil || cfg.Events == nil {
			t.Fatalf("events retention config missing: %+v", cfg)
		}
		parsed, err := cfg.Events.Parse()
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if parsed.Age != 168*time.Hour || parsed.MaxPerRun != 500 {
			t.Errorf("parsed = %+v", parsed)
		}
	})
}
