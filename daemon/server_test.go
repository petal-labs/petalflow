package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/agent"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/registry"
	"github.com/petal-labs/petalflow/tool"
)

func TestServer_ToolCRUDEndpoints(t *testing.T) {
	server := newTestServer(t)

	manifest := tool.NewManifest("pdf_extract")
	manifest.Transport = tool.NewHTTPTransport(tool.HTTPTransport{Endpoint: "http://example.invalid"})
	manifest.Actions["extract"] = tool.ActionSpec{
		Inputs: map[string]tool.FieldSpec{
			"path": {Type: tool.TypeString, Required: true},
		},
		Outputs: map[string]tool.FieldSpec{
			"text": {Type: tool.TypeString},
		},
	}
	manifest.Config = map[string]tool.FieldSpec{
		"region": {Type: tool.TypeString, Required: true},
	}

	resp := requestJSON(t, server.Handler(), http.MethodPost, "/api/tools", map[string]any{
		"name":     "pdf_extract",
		"type":     "http",
		"manifest": manifest,
		"config": map[string]string{
			"region": "us-west-2",
		},
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /api/tools status = %d, want 201; body=%s", resp.Code, resp.Body.String())
	}

	listResp := requestJSON(t, server.Handler(), http.MethodGet, "/api/tools", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("GET /api/tools status = %d, want 200; body=%s", listResp.Code, listResp.Body.String())
	}
	var listed struct {
		Tools []tool.ToolRegistration `json:"tools"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal tools list: %v", err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != "pdf_extract" {
		t.Fatalf("tools list = %#v, want [pdf_extract]", listed.Tools)
	}

	getResp := requestJSON(t, server.Handler(), http.MethodGet, "/api/tools/pdf_extract?include_builtins=false", nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET /api/tools/{name} status = %d, want 200; body=%s", getResp.Code, getResp.Body.String())
	}
	var got tool.ToolRegistration
	if err := json.Unmarshal(getResp.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal registration: %v", err)
	}
	if got.Config["region"] != "us-west-2" {
		t.Fatalf("region = %q, want us-west-2", got.Config["region"])
	}

	updateResp := requestJSON(t, server.Handler(), http.MethodPut, "/api/tools/pdf_extract", map[string]any{
		"config": map[string]string{"region": "us-east-1"},
	})
	if updateResp.Code != http.StatusOK {
		t.Fatalf("PUT /api/tools/{name} status = %d, want 200; body=%s", updateResp.Code, updateResp.Body.String())
	}

	deleteResp := requestJSON(t, server.Handler(), http.MethodDelete, "/api/tools/pdf_extract", nil)
	if deleteResp.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/tools/{name} status = %d, want 204; body=%s", deleteResp.Code, deleteResp.Body.String())
	}

	missingResp := requestJSON(t, server.Handler(), http.MethodGet, "/api/tools/pdf_extract?include_builtins=false", nil)
	if missingResp.Code != http.StatusNotFound {
		t.Fatalf("GET deleted tool status = %d, want 404; body=%s", missingResp.Code, missingResp.Body.String())
	}
}

func TestServer_ActionEndpointsAndCatalog(t *testing.T) {
	server := newTestServer(t)

	register := requestJSON(t, server.Handler(), http.MethodPost, "/api/tools", map[string]any{
		"name": "s3_fetch",
		"type": "mcp",
		"transport": map[string]any{
			"mode":    "stdio",
			"command": "mock-mcp",
		},
		"config": map[string]string{
			"region": "us-west-2",
		},
	})
	if register.Code != http.StatusCreated {
		t.Fatalf("register mcp status = %d, want 201; body=%s", register.Code, register.Body.String())
	}

	overlay := requestJSON(t, server.Handler(), http.MethodPut, "/api/tools/s3_fetch/overlay", map[string]any{
		"overlay_path": "/tmp/s3.overlay.yaml",
	})
	if overlay.Code != http.StatusOK {
		t.Fatalf("overlay update status = %d, want 200; body=%s", overlay.Code, overlay.Body.String())
	}

	refresh := requestJSON(t, server.Handler(), http.MethodPost, "/api/tools/s3_fetch/refresh", nil)
	if refresh.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200; body=%s", refresh.Code, refresh.Body.String())
	}

	disable := requestJSON(t, server.Handler(), http.MethodPut, "/api/tools/s3_fetch/disable", nil)
	if disable.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want 200; body=%s", disable.Code, disable.Body.String())
	}
	enable := requestJSON(t, server.Handler(), http.MethodPut, "/api/tools/s3_fetch/enable", nil)
	if enable.Code != http.StatusOK {
		t.Fatalf("enable status = %d, want 200; body=%s", enable.Code, enable.Body.String())
	}

	health := requestJSON(t, server.Handler(), http.MethodGet, "/api/tools/s3_fetch/health", nil)
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200; body=%s", health.Code, health.Body.String())
	}
	var healthPayload struct {
		Tool   tool.ToolRegistration `json:"tool"`
		Health tool.HealthReport     `json:"health"`
	}
	if err := json.Unmarshal(health.Body.Bytes(), &healthPayload); err != nil {
		t.Fatalf("unmarshal health payload: %v", err)
	}
	if healthPayload.Health.State != tool.HealthUnhealthy {
		t.Fatalf("health state = %q, want unhealthy", healthPayload.Health.State)
	}

	testAction := requestJSON(t, server.Handler(), http.MethodPost, "/api/tools/template_render/test", map[string]any{
		"action": "render",
		"inputs": map[string]any{
			"template": "Hello {{.name}}",
			"name":     "Ada",
		},
	})
	if testAction.Code != http.StatusOK {
		t.Fatalf("test action status = %d, want 200; body=%s", testAction.Code, testAction.Body.String())
	}
	var testResult tool.ToolTestResult
	if err := json.Unmarshal(testAction.Body.Bytes(), &testResult); err != nil {
		t.Fatalf("unmarshal test response: %v", err)
	}
	if rendered, _ := testResult.Outputs["rendered"].(string); rendered != "Hello Ada" {
		t.Fatalf("rendered output = %v, want Hello Ada", testResult.Outputs["rendered"])
	}

	nodeTypes := requestJSON(t, server.Handler(), http.MethodGet, "/api/node-types", nil)
	if nodeTypes.Code != http.StatusOK {
		t.Fatalf("GET /api/node-types status = %d, want 200; body=%s", nodeTypes.Code, nodeTypes.Body.String())
	}
	var catalog struct {
		NodeTypes []registry.NodeTypeDef `json:"node_types"`
	}
	if err := json.Unmarshal(nodeTypes.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("unmarshal node types: %v", err)
	}

	download := findNodeType(catalog.NodeTypes, "s3_fetch.download")
	if download == nil {
		t.Fatal("expected s3_fetch.download in node type catalog")
	}
	if download.ToolMode != "function_call" {
		t.Fatalf("s3_fetch.download mode = %q, want function_call (overlay override)", download.ToolMode)
	}

	raw := findNodeType(catalog.NodeTypes, "s3_fetch.raw")
	if raw == nil {
		t.Fatal("expected s3_fetch.raw in node type catalog")
	}
	if raw.ToolMode != "standalone" {
		t.Fatalf("s3_fetch.raw mode = %q, want standalone (bytes heuristic)", raw.ToolMode)
	}
	configSchema, ok := raw.ConfigSchema.(map[string]any)
	if !ok {
		t.Fatalf("raw.ConfigSchema type = %T, want map[string]any", raw.ConfigSchema)
	}
	if _, ok := configSchema["tool_config"]; !ok {
		t.Fatalf("raw.ConfigSchema = %#v, expected tool_config entry", raw.ConfigSchema)
	}
}

func TestServer_MasksSensitiveConfigInResponses(t *testing.T) {
	server := newTestServer(t)

	manifest := tool.NewManifest("secure_http")
	manifest.Transport = tool.NewHTTPTransport(tool.HTTPTransport{Endpoint: "http://example.invalid"})
	manifest.Actions["run"] = tool.ActionSpec{
		Outputs: map[string]tool.FieldSpec{
			"ok": {Type: tool.TypeBoolean},
		},
	}
	manifest.Config = map[string]tool.FieldSpec{
		"region": {Type: tool.TypeString},
		"token":  {Type: tool.TypeString, Sensitive: true},
	}

	createResp := requestJSON(t, server.Handler(), http.MethodPost, "/api/tools", map[string]any{
		"name":     "secure_http",
		"type":     "http",
		"manifest": manifest,
		"config": map[string]string{
			"region": "us-east-1",
			"token":  "plain-secret-token",
		},
	})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST /api/tools status = %d, want 201; body=%s", createResp.Code, createResp.Body.String())
	}

	var created tool.ToolRegistration
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if got := created.Config["token"]; got != tool.MaskedSecretValue {
		t.Fatalf("create response token = %q, want masked", got)
	}

	getResp := requestJSON(t, server.Handler(), http.MethodGet, "/api/tools/secure_http?include_builtins=false", nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET /api/tools/{name} status = %d, want 200; body=%s", getResp.Code, getResp.Body.String())
	}
	var fetched tool.ToolRegistration
	if err := json.Unmarshal(getResp.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("unmarshal fetch response: %v", err)
	}
	if got := fetched.Config["token"]; got != tool.MaskedSecretValue {
		t.Fatalf("get response token = %q, want masked", got)
	}

	listResp := requestJSON(t, server.Handler(), http.MethodGet, "/api/tools", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("GET /api/tools status = %d, want 200; body=%s", listResp.Code, listResp.Body.String())
	}
	var listed struct {
		Tools []tool.ToolRegistration `json:"tools"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if len(listed.Tools) == 0 {
		t.Fatal("list response should include secure_http")
	}
	for _, reg := range listed.Tools {
		if reg.Name != "secure_http" {
			continue
		}
		if got := reg.Config["token"]; got != tool.MaskedSecretValue {
			t.Fatalf("list response token = %q, want masked", got)
		}
		return
	}
	t.Fatal("secure_http not found in list response")
}

func TestServer_ReturnsStructuredToolErrors(t *testing.T) {
	server := newTestServer(t)

	manifest := tool.NewManifest("unstable_http")
	manifest.Transport = tool.NewHTTPTransport(tool.HTTPTransport{
		Endpoint: "http://127.0.0.1:1",
		Retry: tool.RetryPolicy{
			MaxAttempts: 2,
			BackoffMS:   0,
		},
	})
	manifest.Actions["run"] = tool.ActionSpec{
		Outputs: map[string]tool.FieldSpec{
			"ok": {Type: tool.TypeBoolean},
		},
	}

	createResp := requestJSON(t, server.Handler(), http.MethodPost, "/api/tools", map[string]any{
		"name":     "unstable_http",
		"type":     "http",
		"manifest": manifest,
	})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST /api/tools status = %d, want 201; body=%s", createResp.Code, createResp.Body.String())
	}

	testResp := requestJSON(t, server.Handler(), http.MethodPost, "/api/tools/unstable_http/test", map[string]any{
		"action": "run",
	})
	if testResp.Code != http.StatusBadGateway && testResp.Code != http.StatusGatewayTimeout {
		t.Fatalf("test endpoint status = %d, want 502 or 504; body=%s", testResp.Code, testResp.Body.String())
	}

	var payload struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(testResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal structured error response: %v", err)
	}
	if payload.Error.Code == "" {
		t.Fatalf("error.code should be set, payload=%s", testResp.Body.String())
	}
	if payload.Error.Message == "" {
		t.Fatalf("error.message should be set, payload=%s", testResp.Body.String())
	}
	if _, ok := payload.Error.Details["retryable"]; !ok {
		t.Fatalf("error.details.retryable missing, payload=%s", testResp.Body.String())
	}
}

func TestEndToEnd_ConfigToRegistryAPIAndAgentCompile(t *testing.T) {
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
  pdf_extract:
    type: http
    manifest: ./pdf.tool.json
    endpoint: http://localhost:9802
    config:
      region: us-west-2
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	service, err := tool.NewDaemonToolService(tool.DaemonToolServiceConfig{
		Store:               NewMemoryToolStore(),
		ReachabilityChecker: noopReachabilityChecker{},
	})
	if err != nil {
		t.Fatalf("NewDaemonToolService() error = %v", err)
	}

	server, err := NewServer(ServerConfig{Service: service})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if _, err := RegisterToolsFromConfig(context.Background(), service, configPath); err != nil {
		t.Fatalf("RegisterToolsFromConfig() error = %v", err)
	}
	if err := server.SyncRegistry(context.Background()); err != nil {
		t.Fatalf("SyncRegistry() error = %v", err)
	}

	nodeTypesResp := requestJSON(t, server.Handler(), http.MethodGet, "/api/node-types", nil)
	if nodeTypesResp.Code != http.StatusOK {
		t.Fatalf("GET /api/node-types status = %d, want 200; body=%s", nodeTypesResp.Code, nodeTypesResp.Body.String())
	}
	var catalog struct {
		NodeTypes []registry.NodeTypeDef `json:"node_types"`
	}
	if err := json.Unmarshal(nodeTypesResp.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("unmarshal catalog: %v", err)
	}
	if findNodeType(catalog.NodeTypes, "pdf_extract.extract") == nil {
		t.Fatalf("catalog does not include pdf_extract.extract: %#v", catalog.NodeTypes)
	}

	wf := &agent.AgentWorkflow{
		Version: "1.0",
		Kind:    "agent_workflow",
		ID:      "phase3_e2e",
		Name:    "phase3_e2e",
		Agents: map[string]agent.Agent{
			"researcher": {
				Role:     "Researcher",
				Goal:     "Analyze files",
				Provider: "openai",
				Model:    "gpt-4",
				Tools:    []string{"pdf_extract.extract"},
				ToolConfig: map[string]map[string]any{
					"pdf_extract": {
						"region": "us-west-2",
					},
				},
			},
		},
		Tasks: map[string]agent.Task{
			"analyze": {
				Description:    "Analyze the PDF",
				Agent:          "researcher",
				ExpectedOutput: "summary",
			},
		},
		Execution: agent.ExecutionConfig{
			Strategy:  "sequential",
			TaskOrder: []string{"analyze"},
		},
	}

	diags := agent.Validate(wf)
	for _, diag := range diags {
		if diag.Severity == "error" {
			t.Fatalf("unexpected validation error: %+v", diag)
		}
	}

	compiled, err := agent.Compile(wf)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(compiled.Nodes) == 0 {
		t.Fatal("compiled graph should contain nodes")
	}
	llm := findCompiledNode(compiled.Nodes, "llm_prompt")
	if llm == nil {
		t.Fatalf("compiled graph missing llm node: %#v", compiled.Nodes)
	}
	tools, ok := llm.Config["tools"].([]string)
	if !ok {
		t.Fatalf("llm tools type = %T, want []string", llm.Config["tools"])
	}
	if len(tools) != 1 || tools[0] != "pdf_extract.extract" {
		t.Fatalf("llm tools = %#v, want [pdf_extract.extract]", tools)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()

	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)

	mcpBuilder := func(ctx context.Context, name string, transport tool.MCPTransport, config map[string]string, overlayPath string) (tool.Registration, error) {
		manifest := tool.NewManifest(name)
		manifest.Transport = tool.NewMCPTransport(transport)
		overrideCallable := true
		manifest.Actions["download"] = tool.ActionSpec{
			Description: "Download bytes",
			Outputs: map[string]tool.FieldSpec{
				"blob": {Type: tool.TypeBytes},
			},
			LLMCallable: &overrideCallable,
		}
		manifest.Actions["raw"] = tool.ActionSpec{
			Description: "Raw bytes action",
			Outputs: map[string]tool.FieldSpec{
				"blob": {Type: tool.TypeBytes},
			},
		}
		manifest.Config = map[string]tool.FieldSpec{
			"region": {Type: tool.TypeString},
		}

		reg := tool.Registration{
			Name:     name,
			Origin:   tool.OriginMCP,
			Manifest: manifest,
			Config:   cloneStringMap(config),
			Status:   tool.StatusReady,
			Enabled:  true,
		}
		if overlayPath != "" {
			reg.Overlay = &tool.ToolOverlay{Path: overlayPath}
		}
		return reg, nil
	}

	mcpRefresher := func(ctx context.Context, existing tool.Registration) (tool.Registration, error) {
		next := existing
		next.Manifest.Actions["list"] = tool.ActionSpec{
			Description: "List objects",
			Outputs: map[string]tool.FieldSpec{
				"count": {Type: tool.TypeInteger},
			},
		}
		next.Status = tool.StatusReady
		return next, nil
	}

	service, err := tool.NewDaemonToolService(tool.DaemonToolServiceConfig{
		Store:               NewMemoryToolStore(),
		ReachabilityChecker: noopReachabilityChecker{},
		MCPBuilder:          mcpBuilder,
		MCPRefresher:        mcpRefresher,
		MCPHealthEvaluator: func(ctx context.Context, reg tool.Registration) tool.HealthReport {
			return tool.HealthReport{
				ToolName:  reg.Name,
				State:     tool.HealthUnhealthy,
				CheckedAt: now,
			}
		},
	})
	if err != nil {
		t.Fatalf("NewDaemonToolService() error = %v", err)
	}

	server, err := NewServer(ServerConfig{Service: service})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal(body) error = %v", err)
		}
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func findNodeType(nodeTypes []registry.NodeTypeDef, typeName string) *registry.NodeTypeDef {
	for i := range nodeTypes {
		if nodeTypes[i].Type == typeName {
			return &nodeTypes[i]
		}
	}
	return nil
}

func findCompiledNode(nodes []graph.NodeDef, nodeType string) *graph.NodeDef {
	for i := range nodes {
		if nodes[i].Type == nodeType {
			return &nodes[i]
		}
	}
	return nil
}
