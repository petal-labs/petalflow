package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/petal-labs/petalflow/daemon"
	"github.com/petal-labs/petalflow/runtime"
	toolpkg "github.com/petal-labs/petalflow/tool"
)

// newDaemonWorkflowLifecycleHandlerWithPolicy mirrors
// newDaemonWorkflowLifecycleHandler with a statically configured default
// tenant tool policy so runs execute under governance. The returned
// events function captures runtime events for assertions.
func newDaemonWorkflowLifecycleHandlerWithPolicy(t *testing.T, policy *toolpkg.ToolPolicy) (http.Handler, func() []runtime.Event) {
	t.Helper()

	toolStore := newTestToolStore(t)
	daemonServer, err := daemon.NewServer(daemon.ServerConfig{Store: toolStore})
	if err != nil {
		t.Fatalf("new daemon server: %v", err)
	}

	var mu sync.Mutex
	var captured []runtime.Event
	capture := func(e runtime.Event) {
		mu.Lock()
		captured = append(captured, e)
		mu.Unlock()
	}

	cfg := workflowLifecycleServerConfig(t)
	cfg.ToolStore = toolStore
	cfg.RunStore = runtime.NewMemoryRunStore()
	cfg.RuntimeEvents = runtime.EventHandler(capture)
	cfg.DefaultTenantPolicy = policy
	workflowServer := NewServer(cfg)

	mux := http.NewServeMux()
	workflowServer.RegisterRoutes(mux)
	daemonHandler := daemonServer.Handler()
	mux.Handle("/api/tools/", daemonHandler)
	mux.Handle("/api/tools", daemonHandler)

	return mux, func() []runtime.Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]runtime.Event(nil), captured...)
	}
}

func policyTestToolWorkflow(t *testing.T, handler http.Handler, workflowID string) {
	t.Helper()
	createGraphWorkflow(t, handler, graphWorkflowPayload(workflowID, map[string]any{
		"id":   "governed_tool",
		"type": "tool",
		"config": map[string]any{
			"tool_name":  "template_render.render",
			"output_key": "tool_output",
			"static_args": map[string]any{
				"template": "hello {{.name}}",
				"values": map[string]any{
					"name": "PetalFlow",
				},
			},
		},
	}))
}

func findPolicyRejectedEvent(events []runtime.Event) *runtime.Event {
	for i := range events {
		if events[i].Kind == runtime.EventToolResult && events[i].Payload["policy_rejected"] == true {
			return &events[i]
		}
	}
	return nil
}

func TestWorkflowToolPolicyEnforcement_RunLevel(t *testing.T) {
	t.Run("default tenant deny blocks invocation", func(t *testing.T) {
		handler, capturedEvents := newDaemonWorkflowLifecycleHandlerWithPolicy(t, &toolpkg.ToolPolicy{
			Deny: []string{"template_render"},
		})
		policyTestToolWorkflow(t, handler, "policy_deny_wf")

		body := mustJSON(t, RunRequest{Input: map[string]any{"name": "PetalFlow"}, Options: RunReqOptions{Timeout: "30s"}})
		req := httptest.NewRequest(http.MethodPost, "/api/workflows/policy_deny_wf/run", bytes.NewReader(body))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("run status=%d, want %d body=%s", w.Code, http.StatusInternalServerError, w.Body.String())
		}

		rejected := findPolicyRejectedEvent(capturedEvents())
		if rejected == nil {
			t.Fatalf("expected a policy_rejected tool.result event, got %d events", len(capturedEvents()))
		}
		if rejected.Payload["error_code"] != toolpkg.ToolErrorCodePolicyDenied {
			t.Fatalf("error_code = %v, want %q", rejected.Payload["error_code"], toolpkg.ToolErrorCodePolicyDenied)
		}
	})

	t.Run("allowlist governs", func(t *testing.T) {
		handler, capturedEvents := newDaemonWorkflowLifecycleHandlerWithPolicy(t, &toolpkg.ToolPolicy{
			Allow: []string{"template_render"},
		})
		policyTestToolWorkflow(t, handler, "policy_allow_wf")

		body := mustJSON(t, RunRequest{Input: map[string]any{"name": "PetalFlow"}, Options: RunReqOptions{Timeout: "30s"}})
		req := httptest.NewRequest(http.MethodPost, "/api/workflows/policy_allow_wf/run", bytes.NewReader(body))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("run status=%d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
		}

		var resp RunResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out, ok := resp.Output.Vars["tool_output"]; !ok {
			t.Fatalf("expected tool_output in %v", resp.Output.Vars)
		} else if rendered, ok := out.(map[string]any); !ok || rendered["rendered"] != "hello PetalFlow" {
			t.Fatalf("tool_output = %v, want rendered payload", out)
		}
		if findPolicyRejectedEvent(capturedEvents()) != nil {
			t.Fatal("allowed run must not include a policy_rejected event")
		}
	})

	t.Run("ungoverned server invokes freely", func(t *testing.T) {
		handler, _ := newDaemonWorkflowLifecycleHandlerWithPolicy(t, nil)
		policyTestToolWorkflow(t, handler, "policy_ungoverned_wf")

		body := mustJSON(t, RunRequest{Input: map[string]any{"name": "PetalFlow"}, Options: RunReqOptions{Timeout: "30s"}})
		req := httptest.NewRequest(http.MethodPost, "/api/workflows/policy_ungoverned_wf/run", bytes.NewReader(body))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("run status=%d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
		}
	})

	t.Run("workflow policy override denies run", func(t *testing.T) {
		handler, capturedEvents := newDaemonWorkflowLifecycleHandlerWithPolicy(t, nil)
		policyTestToolWorkflow(t, handler, "policy_override_wf")

		// Store a workflow-scoped policy denying the tool.
		policyBody := mustJSON(t, policyEnvelope{Policy: &toolpkg.ToolPolicy{
			Deny: []string{"template_render"},
		}})
		putReq := httptest.NewRequest(http.MethodPut, "/api/workflows/policy_override_wf/policy", bytes.NewReader(policyBody))
		putW := httptest.NewRecorder()
		handler.ServeHTTP(putW, putReq)
		if putW.Code != http.StatusOK {
			t.Fatalf("put workflow policy status=%d body=%s", putW.Code, putW.Body.String())
		}

		body := mustJSON(t, RunRequest{Input: map[string]any{"name": "PetalFlow"}, Options: RunReqOptions{Timeout: "30s"}})
		req := httptest.NewRequest(http.MethodPost, "/api/workflows/policy_override_wf/run", bytes.NewReader(body))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("run status=%d, want %d body=%s", w.Code, http.StatusInternalServerError, w.Body.String())
		}

		rejected := findPolicyRejectedEvent(capturedEvents())
		if rejected == nil {
			t.Fatal("expected policy_rejected event for workflow-level deny")
		}
	})
}
