package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkflowScheduleHandlers_CRUD(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	mustCreateWorkflowForScheduleHandlers(t, handler, "schedule-crud")

	createBody := mustJSON(t, workflowScheduleRequest{
		Cron:  "*/5 * * * *",
		Input: map[string]any{"topic": "cron"},
		Options: &RunReqOptions{
			Timeout: "30s",
			Human: &RunReqHumanOptions{
				Mode: "strict",
			},
		},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/workflows/schedule-crud/schedules", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	handler.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("create schedule status=%d, want %d body=%s", createW.Code, http.StatusCreated, createW.Body.String())
	}

	var created WorkflowSchedule
	if err := json.Unmarshal(createW.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created schedule id is empty")
	}
	if created.Cron != "*/5 * * * *" {
		t.Fatalf("created cron=%q, want %q", created.Cron, "*/5 * * * *")
	}
	if created.NextRunAt.IsZero() {
		t.Fatal("created next_run_at is zero")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/workflows/schedule-crud/schedules", nil)
	listW := httptest.NewRecorder()
	handler.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("list schedule status=%d, want %d body=%s", listW.Code, http.StatusOK, listW.Body.String())
	}
	var schedules []WorkflowSchedule
	if err := json.Unmarshal(listW.Body.Bytes(), &schedules); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("list count=%d, want 1", len(schedules))
	}

	updateBody := mustJSON(t, workflowScheduleRequest{
		Enabled: boolPtr(false),
		Cron:    "0 * * * *",
	})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/workflows/schedule-crud/schedules/"+created.ID, bytes.NewReader(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	handler.ServeHTTP(updateW, updateReq)
	if updateW.Code != http.StatusOK {
		t.Fatalf("update schedule status=%d, want %d body=%s", updateW.Code, http.StatusOK, updateW.Body.String())
	}

	var updated WorkflowSchedule
	if err := json.Unmarshal(updateW.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal update response: %v", err)
	}
	if updated.Enabled {
		t.Fatal("updated enabled=true, want false")
	}
	if updated.Cron != "0 * * * *" {
		t.Fatalf("updated cron=%q, want %q", updated.Cron, "0 * * * *")
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/workflows/schedule-crud/schedules/"+created.ID, nil)
	deleteW := httptest.NewRecorder()
	handler.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusNoContent {
		t.Fatalf("delete schedule status=%d, want %d body=%s", deleteW.Code, http.StatusNoContent, deleteW.Body.String())
	}
}

func TestWorkflowScheduleHandlers_Validation(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()
	mustCreateWorkflowForScheduleHandlers(t, handler, "schedule-validation")

	invalidCronBody := mustJSON(t, workflowScheduleRequest{Cron: "bad cron"})
	req := httptest.NewRequest(http.MethodPost, "/api/workflows/schedule-validation/schedules", bytes.NewReader(invalidCronBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid cron status=%d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	streamBody := mustJSON(t, workflowScheduleRequest{
		Cron: "* * * * *",
		Options: &RunReqOptions{
			Stream: true,
		},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/workflows/schedule-validation/schedules", bytes.NewReader(streamBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("stream option status=%d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func mustCreateWorkflowForScheduleHandlers(t *testing.T, handler http.Handler, workflowID string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(validGraphJSON(workflowID)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create workflow status=%d, want %d body=%s", w.Code, http.StatusCreated, w.Body.String())
	}
}

func boolPtr(v bool) *bool {
	return &v
}
