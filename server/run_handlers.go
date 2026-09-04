package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/runtime"
)

// RunStatusResponse is the public, non-sensitive subset of a durable run
// record. Checkpoint envelopes are intentionally not returned by status APIs.
type RunStatusResponse struct {
	ID              string                        `json:"id"`
	WorkflowID      string                        `json:"workflow_id,omitempty"`
	Status          runtime.RunStatus             `json:"status"`
	StartedAt       time.Time                     `json:"started_at"`
	UpdatedAt       time.Time                     `json:"updated_at"`
	CompletedAt     time.Time                     `json:"completed_at,omitempty"`
	Error           string                        `json:"error,omitempty"`
	CancelRequested bool                          `json:"cancel_requested,omitempty"`
	CheckpointID    string                        `json:"checkpoint_id,omitempty"`
	NodeStatuses    map[string]runtime.NodeStatus `json:"node_statuses,omitempty"`
	PendingAction   *runtime.PendingAction        `json:"pending_action,omitempty"`
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	record, err := s.getRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		s.writeRunStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runStatusResponse(record))
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	if s.runStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "durable run store not configured")
		return
	}
	runID := r.PathValue("run_id")
	if err := s.runStore.Cancel(r.Context(), runID); err != nil {
		s.writeRunStoreError(w, err)
		return
	}
	s.activeMu.Lock()
	cancel := s.activeRuns[runID]
	s.activeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	record, err := s.runStore.Get(r.Context(), runID)
	if err != nil {
		s.writeRunStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runStatusResponse(record))
}

func (s *Server) handleResumeRun(w http.ResponseWriter, r *http.Request) {
	if s.runStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "durable run store not configured")
		return
	}
	record, err := s.getRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		s.writeRunStoreError(w, err)
		return
	}
	if record.WorkflowID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_RUN", "run has no workflow identity and cannot be resumed")
		return
	}
	plan, err := s.planWorkflowRun(r.Context(), record.WorkflowID, RunRequest{})
	if err != nil {
		writeRunAPIError(w, err)
		return
	}
	plan.env.Trace.RunID = record.ID
	plan.resume = true
	resp, runErr := s.executeWorkflowRunSync(r.Context(), record.WorkflowID, plan, nil)
	if runErr != nil {
		var apiErr *runAPIError
		if errors.As(runErr, &apiErr) && apiErr.Status == http.StatusAccepted {
			writeJSON(w, http.StatusAccepted, resp)
			return
		}
		writeRunAPIError(w, runErr)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetPendingAction(w http.ResponseWriter, r *http.Request) {
	record, err := s.getRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		s.writeRunStoreError(w, err)
		return
	}
	var actions []runtime.PendingAction
	if record.PendingAction != nil {
		actions = append(actions, *record.PendingAction)
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending_actions": actions})
}

func (s *Server) handleCompletePendingAction(w http.ResponseWriter, r *http.Request) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	record, err := s.getRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		s.writeRunStoreError(w, err)
		return
	}
	pending := record.PendingAction
	if pending == nil || pending.ID != r.PathValue("action_id") {
		writeError(w, http.StatusNotFound, "PENDING_ACTION_NOT_FOUND", "pending action not found")
		return
	}
	if pending.Completed || pending.Response != nil {
		writeError(w, http.StatusConflict, "ALREADY_COMPLETED", "pending action was already completed")
		return
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", "invalid human response")
		return
	}
	var responseBytes json.RawMessage
	if nested, ok := raw["response"]; ok {
		responseBytes = nested
	} else {
		encoded, err := json.Marshal(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "PARSE_ERROR", "invalid human response")
			return
		}
		responseBytes = encoded
	}
	var responseObject map[string]json.RawMessage
	if err := json.Unmarshal(responseBytes, &responseObject); err != nil || responseObject == nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", "human response must be a JSON object")
		return
	}
	var response nodes.HumanResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", "invalid human response")
		return
	}
	response.RequestID = pending.ID
	if response.RespondedAt.IsZero() {
		response.RespondedAt = time.Now().UTC()
	}
	if completer, ok := s.runStore.(runtime.PendingActionCompleter); ok {
		completed, err := completer.CompletePendingAction(r.Context(), record.ID, pending.ID, response)
		if err != nil {
			s.writePendingActionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, runStatusResponse(completed))
		return
	}
	pending.Response = response
	pending.RespondedAt = response.RespondedAt
	record.PendingAction = pending
	record.UpdatedAt = time.Now().UTC()
	if err := s.runStore.Update(r.Context(), record); err != nil {
		s.writeRunStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runStatusResponse(record))
}

func (s *Server) writePendingActionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, runtime.ErrRunNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "run not found")
	case errors.Is(err, runtime.ErrPendingNotFound):
		writeError(w, http.StatusNotFound, "PENDING_ACTION_NOT_FOUND", "pending action not found")
	case errors.Is(err, runtime.ErrPendingCompleted):
		writeError(w, http.StatusConflict, "ALREADY_COMPLETED", "pending action was already completed")
	default:
		s.writeRunStoreError(w, err)
	}
}

func (s *Server) getRun(ctx context.Context, runID string) (*runtime.RunRecord, error) {
	if s.runStore == nil {
		return nil, errors.New("durable run store not configured")
	}
	if runID == "" {
		return nil, runtime.ErrRunNotFound
	}
	return s.runStore.Get(ctx, runID)
}

func runStatusResponse(record *runtime.RunRecord) RunStatusResponse {
	var checkpointID string
	var nodeStatuses map[string]runtime.NodeStatus
	if record.Checkpoint != nil {
		checkpointID = record.Checkpoint.ID
		nodeStatuses = make(map[string]runtime.NodeStatus, len(record.Checkpoint.NodeStatuses))
		for nodeID, status := range record.Checkpoint.NodeStatuses {
			nodeStatuses[nodeID] = status
		}
	}
	return RunStatusResponse{
		ID: record.ID, WorkflowID: record.WorkflowID, Status: record.Status,
		StartedAt: record.StartedAt, UpdatedAt: record.UpdatedAt,
		CompletedAt: record.CompletedAt, Error: record.Error,
		CancelRequested: record.CancelRequested, CheckpointID: checkpointID,
		NodeStatuses:  nodeStatuses,
		PendingAction: record.PendingAction,
	}
}

func (s *Server) writeRunStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, runtime.ErrRunNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "run not found")
	case errors.Is(err, runtime.ErrRunAlreadySettled):
		writeError(w, http.StatusConflict, "RUN_SETTLED", "run is already settled")
	default:
		writeError(w, http.StatusInternalServerError, "RUN_STORE_ERROR", fmt.Sprintf("run store operation failed: %v", err))
	}
}
