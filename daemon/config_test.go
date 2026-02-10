package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
		Store:               NewMemoryToolStore(),
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
