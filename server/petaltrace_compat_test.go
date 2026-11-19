package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petal-labs/petalflow/runtime"
)

func TestPetalTraceExecuteCompatibilityRoutes(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		handler := NewServer(ServerConfig{}).Handler()
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
	})

	t.Run("hybrid tool mock", func(t *testing.T) {
		var toolOutputs map[string]any
		handler := NewServer(ServerConfig{
			ContentCapture: runtime.ContentCapture{Enabled: true},
			RuntimeEvents: func(event runtime.Event) {
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
