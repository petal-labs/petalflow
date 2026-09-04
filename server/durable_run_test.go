package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/runtime"
)

func TestDurableRun_HumanApprovalSurvivesPauseAndResume(t *testing.T) {
	workflowStore := newTestSQLiteStore(t)
	srv := NewServer(ServerConfig{
		Store: workflowStore, ScheduleStore: workflowStore,
		Providers: hydrate.ProviderMap{}, Bus: bus.NewMemBus(bus.MemBusConfig{}),
		EventStore: newTestEventStore(t), RunStore: runtime.NewMemoryRunStore(),
	})
	handler := srv.Handler()

	createGraphWorkflow(t, handler, map[string]any{
		"id": "approval-workflow", "version": "1.0",
		"nodes": []map[string]any{{"id": "approval", "type": "human", "config": map[string]any{
			"mode": "approval", "prompt": "Approve release?", "output_var": "approval",
		}}},
		"edges": []map[string]any{}, "entry": "approval",
	})

	first := durableJSONRequest(t, handler, http.MethodPost, "/api/workflows/approval-workflow/run", map[string]any{})
	if first.Code != http.StatusAccepted {
		t.Fatalf("run status = %d, want 202; body=%s", first.Code, first.Body.String())
	}
	var paused RunResponse
	if err := json.Unmarshal(first.Body.Bytes(), &paused); err != nil {
		t.Fatal(err)
	}
	if paused.RunID == "" || paused.Pending == nil {
		t.Fatalf("paused response = %#v", paused)
	}

	status := durableJSONRequest(t, handler, http.MethodGet, "/api/runs/"+paused.RunID, nil)
	if status.Code != http.StatusOK || !contains(status.Body.String(), "paused") {
		t.Fatalf("status response = %d %s", status.Code, status.Body.String())
	}
	var pausedStatus RunStatusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &pausedStatus); err != nil {
		t.Fatal(err)
	}
	if !pausedStatus.CompletedAt.IsZero() {
		t.Fatalf("paused completed_at = %v, want zero", pausedStatus.CompletedAt)
	}
	invalid := durableJSONRequest(t, handler, http.MethodPost, "/api/runs/"+paused.RunID+"/pending-actions/"+paused.Pending.ID, json.RawMessage("null"))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid completion status = %d, want 400; body=%s", invalid.Code, invalid.Body.String())
	}
	complete := durableJSONRequest(t, handler, http.MethodPost, "/api/runs/"+paused.RunID+"/pending-actions/"+paused.Pending.ID, map[string]any{
		"response": map[string]any{"approved": true, "choice": "approve", "responded_by": "reviewer"},
	})
	if complete.Code != http.StatusOK {
		t.Fatalf("complete status = %d; body=%s", complete.Code, complete.Body.String())
	}
	secondComplete := durableJSONRequest(t, handler, http.MethodPost, "/api/runs/"+paused.RunID+"/pending-actions/"+paused.Pending.ID, map[string]any{
		"response": map[string]any{"approved": false},
	})
	if secondComplete.Code != http.StatusConflict {
		t.Fatalf("second complete status = %d, want 409; body=%s", secondComplete.Code, secondComplete.Body.String())
	}

	resumed := durableJSONRequest(t, handler, http.MethodPost, "/api/runs/"+paused.RunID+"/resume", map[string]any{})
	if resumed.Code != http.StatusOK {
		t.Fatalf("resume status = %d; body=%s", resumed.Code, resumed.Body.String())
	}
	var result RunResponse
	if err := json.Unmarshal(resumed.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" {
		t.Fatalf("resumed result = %#v", result)
	}
	if approved, ok := result.Output.Vars["approval_approved"].(bool); !ok || !approved {
		t.Fatalf("approval output = %#v", result.Output.Vars)
	}
}

func durableJSONRequest(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		encoded = mustJSON(t, body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func contains(value, want string) bool {
	for i := 0; i+len(want) <= len(value); i++ {
		if value[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
