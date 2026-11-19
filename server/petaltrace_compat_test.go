package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/runtime"
)

func TestPetalTraceExecuteCompatibilityRoutes(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		var runStarted runtime.Event
		handler := NewServer(ServerConfig{
			ContentCapture:   runtime.ContentCapture{Enabled: true},
			CaptureSnapshots: true,
			RuntimeEvents: func(event runtime.Event) {
				if event.Kind == runtime.EventRunStarted {
					runStarted = event
				}
			},
		}).Handler()
		body := []byte(`{
			"workflow_id":"petaltrace-live",
			"workflow_name":"PetalTrace live replay",
			"graph":{
				"id":"petaltrace-live",
				"schema_version":"v1",
				"kind":"graph",
				"nodes":[{"id":"start","type":"noop"}],
				"edges":[],
				"entry":"start"
			},
			"input":{"message":"hello"},
			"config":{"environment":"test"},
			"trigger_source":"petaltrace-replay"
		}`)
		request := httptest.NewRequest(http.MethodPost, "/api/execute", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("POST /api/execute status = %d, body = %s", response.Code, response.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode execute response: %v", err)
		}
		if result["run_id"] == "" || result["status"] != "completed" {
			t.Fatalf("execute response = %#v, want a completed run with an ID", result)
		}
		for _, key := range []string{"graph_definition", "inputs", "config"} {
			if runStarted.Payload[key] == nil {
				t.Errorf("run.started payload %q = nil, want snapshot data", key)
			}
		}
	})

	t.Run("hybrid tool mock", func(t *testing.T) {
		var toolOutputs map[string]any
		var runStarted runtime.Event
		handler := NewServer(ServerConfig{
			ContentCapture:   runtime.ContentCapture{Enabled: true},
			CaptureSnapshots: true,
			RunStore:         runtime.NewMemoryRunStore(),
			RuntimeEvents: func(event runtime.Event) {
				if event.Kind == runtime.EventRunStarted {
					runStarted = event
				}
				if event.Kind == runtime.EventToolResult {
					toolOutputs, _ = event.Payload["outputs"].(map[string]any)
				}
			},
		}).Handler()
		body := []byte(`{
			"workflow_id":"petaltrace-hybrid",
			"workflow_name":"PetalTrace hybrid replay",
			"graph":{
				"id":"petaltrace-hybrid",
				"schema_version":"v1",
				"kind":"graph",
				"nodes":[{
					"id":"render",
					"type":"tool",
					"config":{
						"tool_name":"template_render.render",
						"static_args":{"template":"live result"},
						"output_key":"rendered"
					}
				}],
				"edges":[],
				"entry":"render"
			},
			"input":{},
			"tool_mock_mode":"all",
			"mocked_tools":{"render":{"rendered":"captured result"}}
		}`)
		request := httptest.NewRequest(http.MethodPost, "/api/execute/hybrid", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("POST /api/execute/hybrid status = %d, body = %s", response.Code, response.Body.String())
		}
		responseBody := response.Body.String()
		var result struct {
			RunID  string `json:"run_id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(responseBody), &result); err != nil {
			t.Fatalf("decode hybrid response: %v", err)
		}
		if result.RunID == "" || result.Status != "completed" {
			t.Fatalf("hybrid response = %#v, want a completed run with an ID", result)
		}
		for _, key := range []string{"graph_definition", "inputs"} {
			if runStarted.Payload[key] == nil {
				t.Errorf("durable run.started payload %q = nil, want snapshot data", key)
			}
		}
		if got := toolOutputs["rendered"]; got != "captured result" {
			t.Fatalf("hybrid tool event outputs = %#v, want captured result; response = %#v", toolOutputs, result)
		}
	})

	t.Run("hybrid all mode rejects missing fixture", func(t *testing.T) {
		handler := NewServer(ServerConfig{}).Handler()
		body := []byte(`{
			"workflow_id":"petaltrace-hybrid-missing",
			"graph":{
				"id":"petaltrace-hybrid-missing",
				"schema_version":"v1",
				"kind":"graph",
				"nodes":[{
					"id":"render",
					"type":"tool",
					"config":{"tool_name":"template_render.render"}
				}],
				"edges":[],
				"entry":"render"
			},
			"input":{},
			"tool_mock_mode":"all",
			"mocked_tools":{}
		}`)
		request := httptest.NewRequest(http.MethodPost, "/api/execute/hybrid", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/execute/hybrid status = %d, want 400, body = %s", response.Code, response.Body.String())
		}
		if !bytes.Contains(response.Body.Bytes(), []byte("missing captured result")) {
			t.Fatalf("error response = %s, want missing captured result", response.Body.String())
		}
	})
}

func TestPetalTraceExecuteCompatibilityRoutesRejectInvalidRequests(t *testing.T) {
	validGraph := `{"id":"workflow-1","schema_version":"v1","kind":"graph","nodes":[{"id":"render","type":"tool","config":{"tool_name":"template_render.render"}}],"edges":[],"entry":"render"}`
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "invalid live JSON", path: "/api/execute", body: `{`},
		{name: "missing graph", path: "/api/execute", body: `{"workflow_id":"workflow-1"}`},
		{name: "invalid graph", path: "/api/execute", body: `{"graph":"not-a-graph"}`},
		{name: "invalid input", path: "/api/execute", body: `{"graph":` + validGraph + `,"input":"not-an-object"}`},
		{name: "invalid tool mock mode", path: "/api/execute/hybrid", body: `{"graph":` + validGraph + `,"tool_mock_mode":"external"}`},
		{name: "invalid tool mock output", path: "/api/execute/hybrid", body: `{"graph":` + validGraph + `,"mocked_tools":{"render":[]}}`},
		{name: "null tool mock output", path: "/api/execute/hybrid", body: `{"graph":` + validGraph + `,"mocked_tools":{"render":null}}`},
		{name: "unknown tool node mock", path: "/api/execute/hybrid", body: `{"graph":` + validGraph + `,"mocked_tools":{"render":{},"unknown":{}}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewServer(ServerConfig{}).Handler()
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestApplyPetalTraceOverrides(t *testing.T) {
	temperature := 0.25
	maxTokens := 128
	definition := &graph.GraphDefinition{
		Nodes: []graph.NodeDef{
			{ID: "prompt", Type: "llm_prompt", Config: map[string]any{"model": "old", "provider": "old"}},
			{ID: "llm", Type: "llm"},
			{ID: "noop", Type: "noop"},
		},
	}

	applyPetalTraceOverrides(definition, PetalTraceExecuteRequest{
		ModelOverride:       "new-model",
		ProviderOverride:    "new-provider",
		TemperatureOverride: &temperature,
		MaxTokensOverride:   &maxTokens,
	})

	for _, node := range definition.Nodes[:2] {
		if node.Config["model"] != "new-model" || node.Config["provider"] != "new-provider" ||
			node.Config["temperature"] != temperature || node.Config["max_tokens"] != maxTokens {
			t.Errorf("node %q config = %#v, want applied LLM overrides", node.ID, node.Config)
		}
	}
	if len(definition.Nodes[2].Config) != 0 {
		t.Errorf("non-LLM node config = %#v, want unchanged", definition.Nodes[2].Config)
	}
}
