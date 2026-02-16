package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/nodes"
)

func TestBuildRunHumanHandler_DefaultStrict(t *testing.T) {
	handler, err := buildRunHumanHandler(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = handler.Request(context.Background(), &nodes.HumanRequest{
		ID:   "req-1",
		Type: nodes.HumanRequestApproval,
	})
	if err == nil {
		t.Fatal("expected strict handler to fail")
	}
	if !strings.Contains(err.Error(), "options.human.mode") {
		t.Fatalf("strict handler error = %q, want guidance for options.human.mode", err.Error())
	}
}

func TestBuildRunHumanHandler_AutoApprove(t *testing.T) {
	handler, err := buildRunHumanHandler(&RunReqHumanOptions{
		Mode:        "auto_approve",
		Choice:      "approve",
		Notes:       "looks good",
		RespondedBy: "ci",
		Delay:       "1ms",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, err := handler.Request(context.Background(), &nodes.HumanRequest{
		ID:   "req-1",
		Type: nodes.HumanRequestApproval,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !resp.Approved {
		t.Fatal("expected auto approve response to be approved")
	}
	if resp.RespondedBy != "ci" {
		t.Fatalf("responded_by = %q, want %q", resp.RespondedBy, "ci")
	}
	if resp.Notes != "looks good" {
		t.Fatalf("notes = %q, want %q", resp.Notes, "looks good")
	}
}

func TestBuildRunHumanHandler_AutoReject(t *testing.T) {
	handler, err := buildRunHumanHandler(&RunReqHumanOptions{Mode: "auto_reject"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, err := handler.Request(context.Background(), &nodes.HumanRequest{
		ID:   "req-1",
		Type: nodes.HumanRequestApproval,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if resp.Approved {
		t.Fatal("expected auto reject response to be rejected")
	}
}

func TestBuildRunHumanHandler_InvalidOptions(t *testing.T) {
	_, err := buildRunHumanHandler(&RunReqHumanOptions{Mode: "invalid"})
	if err == nil || !strings.Contains(err.Error(), "options.human.mode") {
		t.Fatalf("invalid mode error = %v", err)
	}

	_, err = buildRunHumanHandler(&RunReqHumanOptions{Mode: "auto_approve", Delay: "not-a-duration"})
	if err == nil || !strings.Contains(err.Error(), "options.human.delay") {
		t.Fatalf("invalid delay error = %v", err)
	}
}

func TestRunWorkflow_InvalidHumanOptions(t *testing.T) {
	srv := NewServer(ServerConfig{
		Store:     NewMemoryStore(),
		Providers: hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) {
			return nil, nil
		},
	})
	handler := srv.Handler()

	createBody := mustJSON(t, map[string]any{
		"id":      "bad-human-options",
		"version": "1.0",
		"nodes": []map[string]any{
			{"id": "echo", "type": "func"},
		},
		"edges": []map[string]any{},
		"entry": "echo",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(createBody))
	createW := httptest.NewRecorder()
	handler.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("create workflow failed: status=%d body=%s", createW.Code, createW.Body.String())
	}

	runBody := mustJSON(t, RunRequest{
		Options: RunReqOptions{
			Timeout: "1s",
			Human: &RunReqHumanOptions{
				Mode: "invalid",
			},
		},
	})
	runReq := httptest.NewRequest(http.MethodPost, "/api/workflows/bad-human-options/run", bytes.NewReader(runBody))
	runW := httptest.NewRecorder()
	handler.ServeHTTP(runW, runReq)

	if runW.Code != http.StatusBadRequest {
		t.Fatalf("run status=%d, want %d body=%s", runW.Code, http.StatusBadRequest, runW.Body.String())
	}

	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(runW.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if payload.Error.Code != "INVALID_HUMAN_OPTIONS" {
		t.Fatalf("error code = %q, want %q", payload.Error.Code, "INVALID_HUMAN_OPTIONS")
	}
}

func TestBuildRunHumanHandler_AutoApproveDelay(t *testing.T) {
	handler, err := buildRunHumanHandler(&RunReqHumanOptions{
		Mode:  "auto_approve",
		Delay: "10ms",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	start := time.Now()
	_, err = handler.Request(context.Background(), &nodes.HumanRequest{
		ID:   "req-2",
		Type: nodes.HumanRequestApproval,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Fatalf("expected handler delay >=10ms, got %s", elapsed)
	}
}
