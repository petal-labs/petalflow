package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolsRegisterListInspectUnregister(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tools.json")
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
	storePath := filepath.Join(t.TempDir(), "tools.json")
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
	storePath := filepath.Join(t.TempDir(), "tools.json")
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
	storePath := filepath.Join(t.TempDir(), "tools.json")
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

func writeManifestFile(t *testing.T, manifest map[string]any) string {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return writeTestFile(t, "tool-manifest.json", string(data))
}
