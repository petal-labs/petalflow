package tool

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNewManifestDefaults(t *testing.T) {
	man := NewManifest("s3_fetch")

	if man.Schema != SchemaToolV1 {
		t.Errorf("Schema = %q, want %q", man.Schema, SchemaToolV1)
	}
	if man.ManifestVersion != ManifestVersionV1 {
		t.Errorf("ManifestVersion = %q, want %q", man.ManifestVersion, ManifestVersionV1)
	}
	if man.Tool.Name != "s3_fetch" {
		t.Errorf("Tool.Name = %q, want %q", man.Tool.Name, "s3_fetch")
	}
	if man.Actions == nil {
		t.Fatal("Actions should be initialized")
	}
}

func TestRegistrationActionNamesSorted(t *testing.T) {
	reg := Registration{
		Manifest: ToolManifest{
			Actions: map[string]ActionSpec{
				"download": {},
				"list":     {},
				"delete":   {},
			},
		},
	}

	got := reg.ActionNames()
	want := []string{"delete", "download", "list"}

	if len(got) != len(want) {
		t.Fatalf("len(ActionNames) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ActionNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestToolManifestJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		transport TransportSpec
	}{
		{
			name:      "native",
			transport: NewNativeTransport(),
		},
		{
			name: "http",
			transport: NewHTTPTransport(HTTPTransport{
				Endpoint:  "http://localhost:9801",
				TimeoutMS: 30000,
				Retry: RetryPolicy{
					MaxAttempts:    3,
					BackoffMS:      1000,
					RetryableCodes: []int{429, 502, 503},
				},
			}),
		},
		{
			name: "stdio",
			transport: NewStdioTransport(StdioTransport{
				Command:   "./pdf_extract",
				Args:      []string{"--serve"},
				Env:       map[string]string{"LOG_LEVEL": "warn"},
				TimeoutMS: 15000,
			}),
		},
		{
			name: "mcp-stdio",
			transport: NewMCPTransport(MCPTransport{
				Mode:    MCPModeStdio,
				Command: "npx",
				Args:    []string{"-y", "@acme/s3-mcp-server"},
			}),
		},
		{
			name: "mcp-sse",
			transport: NewMCPTransport(MCPTransport{
				Mode:     MCPModeSSE,
				Endpoint: "http://localhost:9801/sse",
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := ToolManifest{
				Schema:          SchemaToolV1,
				ManifestVersion: ManifestVersionV1,
				Tool: ToolMetadata{
					Name:        "s3_fetch",
					Version:     "1.2.0",
					Description: "S3 operations",
				},
				Transport: tt.transport,
				Actions: map[string]ActionSpec{
					"list": {
						Description: "List objects",
						Inputs: map[string]FieldSpec{
							"bucket": {Type: "string", Required: true},
							"prefix": {Type: "string", Default: ""},
						},
						Outputs: map[string]FieldSpec{
							"keys": {
								Type: "array",
								Items: &FieldSpec{
									Type: "string",
								},
							},
						},
						Idempotent: true,
					},
				},
				Config: map[string]FieldSpec{
					"credentials": {
						Type:      "string",
						Required:  true,
						Sensitive: true,
					},
				},
				Health: &HealthConfig{
					Endpoint:           "/health",
					Method:             "GET",
					IntervalSeconds:    30,
					TimeoutMS:          5000,
					UnhealthyThreshold: 3,
				},
			}

			raw, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}

			var decoded ToolManifest
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}

			if !reflect.DeepEqual(decoded, original) {
				t.Fatalf("round-trip mismatch:\n got: %#v\nwant: %#v", decoded, original)
			}
		})
	}
}

func TestTransportTypedViews(t *testing.T) {
	httpSpec := NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	httpCfg, ok := httpSpec.AsHTTP()
	if !ok {
		t.Fatal("AsHTTP() ok = false, want true")
	}
	if httpCfg.Endpoint != "http://localhost:9801" {
		t.Errorf("AsHTTP().Endpoint = %q, want %q", httpCfg.Endpoint, "http://localhost:9801")
	}
	if _, ok := httpSpec.AsMCP(); ok {
		t.Fatal("AsMCP() ok = true, want false for HTTP transport")
	}

	mcpSpec := NewMCPTransport(MCPTransport{
		Mode:     MCPModeSSE,
		Endpoint: "http://localhost:9801/sse",
	})
	mcpCfg, ok := mcpSpec.AsMCP()
	if !ok {
		t.Fatal("AsMCP() ok = false, want true")
	}
	if mcpCfg.Mode != MCPModeSSE {
		t.Errorf("AsMCP().Mode = %q, want %q", mcpCfg.Mode, MCPModeSSE)
	}
	if _, ok := mcpSpec.AsStdio(); ok {
		t.Fatal("AsStdio() ok = true, want false for MCP transport")
	}
}
