package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpclient "github.com/petal-labs/petalflow/tool/mcp"
)

func TestDiscoverMCPManifestFromStdio(t *testing.T) {
	manifest, err := DiscoverMCPManifest(context.Background(), MCPDiscoveryConfig{
		Name: "s3_fetch",
		Transport: MCPTransport{
			Mode:    MCPModeStdio,
			Command: os.Args[0],
			Args:    []string{"-test.run=TestToolMCPHelperProcess", "--"},
			Env: map[string]string{
				"GO_WANT_TOOL_MCP_HELPER": "1",
			},
		},
	})
	if err != nil {
		t.Fatalf("DiscoverMCPManifest() error = %v", err)
	}

	if manifest.Transport.Type != TransportTypeMCP {
		t.Fatalf("manifest transport type = %q, want %q", manifest.Transport.Type, TransportTypeMCP)
	}
	if _, ok := manifest.Actions["list_s3_objects"]; !ok {
		t.Fatalf("expected action list_s3_objects in discovered manifest")
	}
}

func TestBuildMCPRegistrationWithOverlay(t *testing.T) {
	overlayPath := writeOverlayFile(t, `
overlay_version: "1.0"
group_actions:
  list: list_s3_objects
output_schemas:
  list:
    keys:
      type: array
      items:
        type: string
config:
  token:
    type: string
    required: true
    sensitive: true
    env_var: MCP_TOKEN
health:
  strategy: ping
`)
	reg, err := BuildMCPRegistration(context.Background(), "s3_fetch", MCPTransport{
		Mode:    MCPModeStdio,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestToolMCPHelperProcess", "--"},
		Env: map[string]string{
			"GO_WANT_TOOL_MCP_HELPER": "1",
		},
	}, map[string]string{"token": "abc"}, overlayPath)
	if err != nil {
		t.Fatalf("BuildMCPRegistration() error = %v", err)
	}

	if reg.Overlay == nil || reg.Overlay.Path != overlayPath {
		t.Fatalf("overlay path = %#v, want %q", reg.Overlay, overlayPath)
	}
	if _, ok := reg.Manifest.Actions["list"]; !ok {
		t.Fatalf("expected grouped action \"list\"")
	}
	if reg.Manifest.Actions["list"].MCPToolName != "list_s3_objects" {
		t.Fatalf("action MCPToolName = %q, want list_s3_objects", reg.Manifest.Actions["list"].MCPToolName)
	}
	if reg.Status != StatusReady {
		t.Fatalf("registration status = %q, want ready", reg.Status)
	}
}

func TestMCPAdapterInvoke(t *testing.T) {
	reg := Registration{
		Name:   "s3_fetch",
		Origin: OriginMCP,
		Manifest: Manifest{
			Schema:          SchemaToolV1,
			ManifestVersion: ManifestVersionV1,
			Tool: ToolMetadata{
				Name: "s3_fetch",
			},
			Transport: NewMCPTransport(MCPTransport{
				Mode:    MCPModeStdio,
				Command: os.Args[0],
				Args:    []string{"-test.run=TestToolMCPHelperProcess", "--"},
				Env: map[string]string{
					"GO_WANT_TOOL_MCP_HELPER": "1",
				},
			}),
			Actions: map[string]ActionSpec{
				"list": {
					MCPToolName: "list_s3_objects",
					Outputs: map[string]FieldSpec{
						"keys": {Type: TypeArray, Items: &FieldSpec{Type: TypeString}},
					},
				},
			},
		},
		Config: map[string]string{},
	}

	adapter, err := NewMCPAdapter(context.Background(), reg)
	if err != nil {
		t.Fatalf("NewMCPAdapter() error = %v", err)
	}
	defer adapter.Close(context.Background())

	resp, err := adapter.Invoke(context.Background(), InvokeRequest{
		Action: "list",
		Inputs: map[string]any{
			"bucket": "reports",
		},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	keys, ok := resp.Outputs["keys"].([]any)
	if !ok || len(keys) == 0 {
		t.Fatalf("outputs[keys] = %#v, want non-empty []any", resp.Outputs["keys"])
	}
}

func TestParseMCPOverlayYAMLValidation(t *testing.T) {
	_, diags, err := ParseMCPOverlayYAML([]byte(`
overlay_version: "2.0"
health:
  strategy: invalid
`))
	if err != nil {
		t.Fatalf("ParseMCPOverlayYAML() error = %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("expected validation diagnostics")
	}
	fields := make([]string, 0, len(diags))
	for _, diag := range diags {
		fields = append(fields, diag.Field)
	}
	if !contains(fields, "overlay_version") {
		t.Fatalf("expected overlay_version diagnostic, got %v", fields)
	}
	if !contains(fields, "health.strategy") {
		t.Fatalf("expected health.strategy diagnostic, got %v", fields)
	}
}

func TestParseMCPCallResultRawFallback(t *testing.T) {
	result := mcpclient.ToolsCallResult{
		Content: []mcpclient.ContentBlock{
			{Type: "text", Text: "hello"},
			{Type: "image", Data: "abc", MimeType: "image/png"},
		},
	}
	parsed, err := ParseMCPCallResult(result, ActionSpec{})
	if err != nil {
		t.Fatalf("ParseMCPCallResult() error = %v", err)
	}
	raw, ok := parsed["result"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want map", parsed["result"])
	}
	if raw["text"] != "hello" {
		t.Fatalf("result.text = %#v, want hello", raw["text"])
	}
}

func TestEvaluateMCPHealthPing(t *testing.T) {
	reg := Registration{
		Name: "s3_fetch",
		Manifest: Manifest{
			Schema:          SchemaToolV1,
			ManifestVersion: ManifestVersionV1,
			Tool: ToolMetadata{
				Name: "s3_fetch",
			},
			Transport: NewMCPTransport(MCPTransport{
				Mode:    MCPModeStdio,
				Command: os.Args[0],
				Args:    []string{"-test.run=TestToolMCPHelperProcess", "--"},
				Env: map[string]string{
					"GO_WANT_TOOL_MCP_HELPER": "1",
				},
			}),
			Actions: map[string]ActionSpec{
				"list_s3_objects": {MCPToolName: "list_s3_objects"},
			},
		},
		Origin: OriginMCP,
	}

	report := EvaluateMCPHealth(context.Background(), reg)
	if report.State != HealthHealthy {
		t.Fatalf("health state = %q, want healthy (err=%s)", report.State, report.ErrorMessage)
	}
}

func TestMergeMCPOverlay(t *testing.T) {
	base := NewManifest("s3_fetch")
	base.Transport = NewMCPTransport(MCPTransport{Mode: MCPModeStdio, Command: "server"})
	base.Actions["list_s3_objects"] = ActionSpec{
		MCPToolName: "list_s3_objects",
		Inputs: map[string]FieldSpec{
			"bucket": {Type: TypeString},
		},
	}

	overlay := MCPOverlay{
		OverlayVersion: MCPOverlayVersionV1,
		GroupActions: map[string]string{
			"list": "list_s3_objects",
		},
		ActionModes: map[string]string{
			"list": "standalone",
		},
		OutputSchemas: map[string]map[string]FieldSpec{
			"list": {
				"keys": {Type: TypeArray, Items: &FieldSpec{Type: TypeString}},
			},
		},
	}

	merged, _, err := MergeMCPOverlay(base, overlay)
	if err != nil {
		t.Fatalf("MergeMCPOverlay() error = %v", err)
	}
	if _, ok := merged.Actions["list"]; !ok {
		t.Fatalf("merged action list not found")
	}
	if merged.Actions["list"].MCPToolName != "list_s3_objects" {
		t.Fatalf("MCPToolName = %q, want list_s3_objects", merged.Actions["list"].MCPToolName)
	}
	if merged.Actions["list"].LLMCallable == nil || *merged.Actions["list"].LLMCallable {
		t.Fatalf("list llm_callable = %#v, want false", merged.Actions["list"].LLMCallable)
	}
}

func TestParseMCPOverlayYAML_ActionModesValidation(t *testing.T) {
	_, diags, err := ParseMCPOverlayYAML([]byte(`
overlay_version: "1.0"
action_modes:
  list: unknown
`))
	if err != nil {
		t.Fatalf("ParseMCPOverlayYAML() error = %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("expected diagnostics for invalid action_modes value")
	}
}

func TestToolMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_TOOL_MCP_HELPER") != "1" {
		return
	}

	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for {
		var req mcpclient.Message
		if err := decoder.Decode(&req); err != nil {
			os.Exit(0)
		}

		switch req.Method {
		case "initialize":
			_ = encoder.Encode(mcpclient.Message{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustRawMessageJSON(t, mcpclient.InitializeResult{
					ProtocolVersion: "2025-06-18",
					ServerInfo: mcpclient.ServerInfo{
						Name: "mock-server",
					},
				}),
			})
		case "tools/list":
			_ = encoder.Encode(mcpclient.Message{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustRawMessageJSON(t, mcpclient.ToolsListResult{
					Tools: []mcpclient.Tool{
						{
							Name:        "list_s3_objects",
							Description: "List objects",
							InputSchema: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"bucket": map[string]any{"type": "string"},
								},
								"required": []string{"bucket"},
							},
						},
					},
				}),
			})
		case "tools/call":
			var params map[string]any
			_ = json.Unmarshal(req.Params, &params)
			name, _ := params["name"].(string)
			if name == "list_s3_objects" {
				_ = encoder.Encode(mcpclient.Message{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: mustRawMessageJSON(t, mcpclient.ToolsCallResult{
						Content: []mcpclient.ContentBlock{
							{Type: "text", Text: `{"keys":["a.pdf","b.pdf"]}`},
						},
					}),
				})
			} else {
				_ = encoder.Encode(mcpclient.Message{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &mcpclient.RPCError{
						Code:    -32001,
						Message: "unknown tool",
					},
				})
			}
		default:
			// notifications and close have no responses.
		}
	}
}

func writeOverlayFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overlay.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func mustRawMessageJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
