package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/tool"
	mcpclient "github.com/petal-labs/petalflow/tool/mcp"
)

func TestToolsRegisterListInspectUnregister(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "petalflow.db")
	t.Setenv("PETALFLOW_TOOLS_STORE_PATH", storePath)

	manifest := map[string]any{
		"manifest_version": "1.0",
		"tool": map[string]any{
			"name":        "echo_stdio",
			"version":     "0.1.0",
			"description": "Echo test tool",
		},
		"transport": map[string]any{
			"type":    "stdio",
			"command": "cat",
		},
		"actions": map[string]any{
			"echo": map[string]any{
				"inputs": map[string]any{
					"value": map[string]any{"type": "string"},
				},
				"outputs": map[string]any{
					"value": map[string]any{"type": "string"},
				},
			},
		},
	}
	manifestPath := writeManifestFile(t, manifest)

	root := newTestRoot()
	stdout, _, err := executeCommand(root, "tools", "register", "echo_stdio", "--type", "stdio", "--manifest", manifestPath)
	if err != nil {
		t.Fatalf("register error = %v", err)
	}
	if !strings.Contains(stdout, "Registered tool: echo_stdio") {
		t.Fatalf("register output = %q, want success message", stdout)
	}

	root = newTestRoot()
	stdout, _, err = executeCommand(root, "tools", "list")
	if err != nil {
		t.Fatalf("list error = %v", err)
	}
	if !strings.Contains(stdout, "echo_stdio") {
		t.Fatalf("list output missing tool: %q", stdout)
	}
	if !strings.Contains(stdout, "NAME") {
		t.Fatalf("list output missing header: %q", stdout)
	}

	root = newTestRoot()
	stdout, _, err = executeCommand(root, "tools", "inspect", "echo_stdio")
	if err != nil {
		t.Fatalf("inspect error = %v", err)
	}
	if !strings.Contains(stdout, `"name": "echo_stdio"`) {
		t.Fatalf("inspect output missing name: %q", stdout)
	}

	root = newTestRoot()
	stdout, _, err = executeCommand(root, "tools", "unregister", "echo_stdio")
	if err != nil {
		t.Fatalf("unregister error = %v", err)
	}
	if !strings.Contains(stdout, "Unregistered tool: echo_stdio") {
		t.Fatalf("unregister output = %q, want success message", stdout)
	}
}

func TestToolsConfigMasksSensitiveValues(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "petalflow.db")
	t.Setenv("PETALFLOW_TOOLS_STORE_PATH", storePath)

	root := newTestRoot()
	stdout, _, err := executeCommand(
		root,
		"tools",
		"config",
		"http_fetch",
		"--set-secret", "authorization=Bearer super-secret-token",
		"--show",
	)
	if err != nil {
		t.Fatalf("config error = %v", err)
	}
	if strings.Contains(stdout, "super-secret-token") {
		t.Fatalf("config output leaked secret: %q", stdout)
	}
	if !strings.Contains(stdout, "**********") {
		t.Fatalf("config output missing masked value: %q", stdout)
	}
	if !strings.Contains(stdout, "(sensitive)") {
		t.Fatalf("config output missing sensitive marker: %q", stdout)
	}

	root = newTestRoot()
	stdout, _, err = executeCommand(root, "tools", "inspect", "http_fetch")
	if err != nil {
		t.Fatalf("inspect error = %v", err)
	}
	if strings.Contains(stdout, "super-secret-token") {
		t.Fatalf("inspect output leaked secret: %q", stdout)
	}
}

func TestToolsTestInvokesNativeBuiltin(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "petalflow.db")
	t.Setenv("PETALFLOW_TOOLS_STORE_PATH", storePath)

	root := newTestRoot()
	stdout, _, err := executeCommand(
		root,
		"tools",
		"test",
		"template_render",
		"render",
		"--input", "template=Hello, {{.name}}!",
		"--input", "name=Ada",
	)
	if err != nil {
		t.Fatalf("tools test error = %v", err)
	}
	if !strings.Contains(stdout, `"success": true`) {
		t.Fatalf("test output missing success: %q", stdout)
	}
	if !strings.Contains(stdout, `"rendered": "Hello, Ada!"`) {
		t.Fatalf("test output missing rendered value: %q", stdout)
	}
}

func TestToolsRegisterDuplicateNameShowsValidationCode(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "petalflow.db")
	t.Setenv("PETALFLOW_TOOLS_STORE_PATH", storePath)

	manifest := map[string]any{
		"manifest_version": "1.0",
		"tool": map[string]any{
			"name": "dup_stdio",
		},
		"transport": map[string]any{
			"type":    "stdio",
			"command": "cat",
		},
		"actions": map[string]any{
			"echo": map[string]any{
				"inputs": map[string]any{},
			},
		},
	}
	manifestPath := writeManifestFile(t, manifest)

	root := newTestRoot()
	_, _, err := executeCommand(root, "tools", "register", "dup_stdio", "--type", "stdio", "--manifest", manifestPath)
	if err != nil {
		t.Fatalf("first register error = %v", err)
	}

	root = newTestRoot()
	_, _, err = executeCommand(root, "tools", "register", "dup_stdio", "--type", "stdio", "--manifest", manifestPath)
	if err == nil {
		t.Fatal("second register error = nil, want duplicate name validation error")
	}
	if !strings.Contains(err.Error(), "NAME_NOT_UNIQUE") {
		t.Fatalf("duplicate register error = %q, want NAME_NOT_UNIQUE", err.Error())
	}
}

func TestToolsRegisterNativeBuiltinWithoutManifest(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "petalflow.db")
	t.Setenv("PETALFLOW_TOOLS_STORE_PATH", storePath)

	root := newTestRoot()
	stdout, _, err := executeCommand(root, "tools", "register", "template_render", "--type", "native")
	if err != nil {
		t.Fatalf("register native builtin error = %v", err)
	}
	if !strings.Contains(stdout, "Registered tool: template_render (native") {
		t.Fatalf("register output = %q, want native success message", stdout)
	}
}

func TestToolsRegisterManifestNameMismatch(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "petalflow.db")
	t.Setenv("PETALFLOW_TOOLS_STORE_PATH", storePath)

	manifest := map[string]any{
		"manifest_version": "1.0",
		"tool": map[string]any{
			"name": "different_name",
		},
		"transport": map[string]any{
			"type":    "stdio",
			"command": "cat",
		},
		"actions": map[string]any{
			"echo": map[string]any{},
		},
	}
	manifestPath := writeManifestFile(t, manifest)

	root := newTestRoot()
	_, _, err := executeCommand(root, "tools", "register", "expected_name", "--type", "stdio", "--manifest", manifestPath)
	if err == nil {
		t.Fatal("register should fail when manifest tool.name does not match registration name")
	}
	if !strings.Contains(err.Error(), "does not match registration name") {
		t.Fatalf("register error = %q, want name mismatch message", err.Error())
	}
}

func TestToolsRegisterMCPRefreshOverlayAndHealth(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "petalflow.db")
	t.Setenv("PETALFLOW_TOOLS_STORE_PATH", storePath)

	overlayPath := writeTestFile(t, "overlay.yaml", `
overlay_version: "1.0"
group_actions:
  list: list_s3_objects
output_schemas:
  list:
    keys:
      type: array
      items:
        type: string
health:
  strategy: ping
`)

	root := newTestRoot()
	stdout, _, err := executeCommand(
		root,
		"tools",
		"register",
		"s3_fetch",
		"--type", "mcp",
		"--transport-mode", "stdio",
		"--command", os.Args[0],
		"--arg", "-test.run=TestToolsMCPHelperProcess",
		"--arg", "--",
		"--env", "GO_WANT_TOOLS_MCP_HELPER=1",
		"--overlay", overlayPath,
	)
	if err != nil {
		t.Fatalf("mcp register error = %v", err)
	}
	if !strings.Contains(stdout, "Registered tool: s3_fetch (mcp") {
		t.Fatalf("register output = %q, want mcp success message", stdout)
	}

	root = newTestRoot()
	stdout, _, err = executeCommand(root, "tools", "refresh", "s3_fetch")
	if err != nil {
		t.Fatalf("refresh error = %v", err)
	}
	if !strings.Contains(stdout, "Refreshed MCP tool: s3_fetch") {
		t.Fatalf("refresh output = %q", stdout)
	}

	root = newTestRoot()
	stdout, _, err = executeCommand(root, "tools", "health", "s3_fetch")
	if err != nil {
		t.Fatalf("health error = %v", err)
	}
	if !strings.Contains(stdout, "s3_fetch") {
		t.Fatalf("health output missing tool name: %q", stdout)
	}

	root = newTestRoot()
	stdout, _, err = executeCommand(root, "tools", "overlay", "s3_fetch", "--set="+overlayPath)
	if err != nil {
		t.Fatalf("overlay update error = %v", err)
	}
	if !strings.Contains(stdout, "Updated overlay for MCP tool: s3_fetch") {
		t.Fatalf("overlay output = %q", stdout)
	}

	root = newTestRoot()
	stdout, _, err = executeCommand(root, "tools", "test", "s3_fetch", "list", "--input", "bucket=reports")
	if err != nil {
		t.Fatalf("mcp tools test error = %v", err)
	}
	if !strings.Contains(stdout, `"success": true`) {
		t.Fatalf("tools test output missing success: %q", stdout)
	}
}

func TestToolsMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_TOOLS_MCP_HELPER") != "1" {
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
				Result:  mustRawJSONForCLI(t, mcpclient.InitializeResult{ProtocolVersion: "2025-06-18", ServerInfo: mcpclient.ServerInfo{Name: "cli-helper"}}),
			})
		case "tools/list":
			_ = encoder.Encode(mcpclient.Message{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustRawJSONForCLI(t, mcpclient.ToolsListResult{
					Tools: []mcpclient.Tool{
						{
							Name: "list_s3_objects",
							InputSchema: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"bucket": map[string]any{"type": "string"},
								},
							},
						},
					},
				}),
			})
		case "tools/call":
			var params map[string]any
			_ = json.Unmarshal(req.Params, &params)
			name, _ := params["name"].(string)
			if name != "list_s3_objects" {
				_ = encoder.Encode(mcpclient.Message{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &mcpclient.RPCError{Code: -32001, Message: "unknown tool"},
				})
				continue
			}
			_ = encoder.Encode(mcpclient.Message{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustRawJSONForCLI(t, mcpclient.ToolsCallResult{
					Content: []mcpclient.ContentBlock{
						{Type: "text", Text: `{"keys":["a.pdf","b.pdf"]}`},
					},
				}),
			})
		default:
			// Notifications are ignored.
		}
	}
}

func mustRawJSONForCLI(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data
}

func TestResolveStoredRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.db")
	store, err := tool.NewSQLiteStore(tool.SQLiteStoreConfig{DSN: path, Scope: path})
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	ctx := context.Background()
	reg := tool.ToolRegistration{
		Name:     "x",
		Manifest: tool.NewManifest("x"),
		Origin:   tool.OriginNative,
		Status:   tool.StatusReady,
	}
	reg.Manifest.Transport = tool.NewNativeTransport()
	reg.Manifest.Actions["run"] = tool.ActionSpec{}
	if err := store.Upsert(ctx, reg); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	got, found, err := resolveStoredRegistration(ctx, store, "x")
	if err != nil {
		t.Fatalf("resolveStoredRegistration error = %v", err)
	}
	if !found || got.Name != "x" {
		t.Fatalf("resolveStoredRegistration got=%#v found=%v", got, found)
	}
}

func writeManifestFile(t *testing.T, manifest map[string]any) string {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return writeTestFile(t, "tool-manifest.json", string(data))
}
