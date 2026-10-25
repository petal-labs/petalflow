package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// Attempt is the execution attempt counter for asynchronous run jobs.
	Attempt int `json:"attempt,omitempty"`
	// NextAttemptAt is set for queued asynchronous jobs waiting on retry
	// backoff.
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	record, err := s.getOwnedRun(r)
	if err == nil {
		writeJSON(w, http.StatusOK, runStatusResponse(record))
		return
	}
	if !errors.Is(err, runtime.ErrRunNotFound) {
		s.writeRunStoreError(w, err)
		return
	}
	// Fall back to the run job: queued or worker-executed runs are
	// observable before the durable run record exists.
	if job, jobErr := s.getOwnedRunJob(r); jobErr == nil {
		writeJSON(w, http.StatusOK, runJobResponse(job))
		return
	} else if !errors.Is(jobErr, ErrRunJobNotFound) && !errors.Is(jobErr, errRunJobUnavailable) {
		writeError(w, http.StatusInternalServerError, "RUN_JOB_STORE_ERROR", "load run job failed")
		return
	}
	s.writeRunStoreError(w, err)
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	if s.runStore == nil && s.jobStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "durable run store not configured")
		return
	}
	runID := r.PathValue("run_id")

	_, runErr := s.getOwnedRun(r)
	if runErr != nil && !errors.Is(runErr, runtime.ErrRunNotFound) {
		s.writeRunStoreError(w, runErr)
		return
	}
	job, jobErr := s.getOwnedRunJob(r)
	if jobErr != nil && !errors.Is(jobErr, ErrRunJobNotFound) && !errors.Is(jobErr, errRunJobUnavailable) {
		writeError(w, http.StatusInternalServerError, "RUN_JOB_STORE_ERROR", "load run job failed")
		return
	}
	if runErr != nil && jobErr != nil {
		// Neither the run record nor the job exists.
		s.writeRunStoreError(w, runErr)
		return
	}

	// Cancel the asynchronous job, if one exists.
	if jobErr == nil && !isRunJobTerminal(job.Status) {
		if err := s.jobStore.CancelRunJob(r.Context(), runID, time.Now().UTC()); err != nil && !errors.Is(err, ErrRunJobConflict) {
			writeError(w, http.StatusInternalServerError, "RUN_JOB_STORE_ERROR", "cancel run job failed")
			return
		}
	}

	// Cancel the durable run record, if one exists, and interrupt the
	// local execution context when this process owns the run.
	if runErr == nil {
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
		updated, err := s.runStore.Get(r.Context(), runID)
		if err != nil {
			s.writeRunStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, runStatusResponse(updated))
		return
	}

	refreshed, err := s.jobStore.GetRunJob(r.Context(), runID)
	if err != nil {
		s.writeRunStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runJobResponse(refreshed))
}

// errRunJobUnavailable marks a missing job store in fallback paths.
var errRunJobUnavailable = errors.New("run job store not configured")

// getOwnedRunJob returns the run job for the request path after a
// tenant ownership check.
func (s *Server) getOwnedRunJob(r *http.Request) (RunJob, error) {
	if s.jobStore == nil {
		return RunJob{}, errRunJobUnavailable
	}
	job, err := s.jobStore.GetRunJob(r.Context(), r.PathValue("run_id"))
	if err != nil {
		return RunJob{}, err
	}
	if !s.ownsTenant(r, job.TenantID) {
		return RunJob{}, ErrRunJobNotFound
	}
	return job, nil
}

func (s *Server) handleResumeRun(w http.ResponseWriter, r *http.Request) {
	if s.runStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "durable run store not configured")
		return
	}
	record, err := s.getOwnedRun(r)
	if err != nil {
		s.writeRunStoreError(w, err)
		return
	}
	var req RunResumeRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "PARSE_ERROR", "invalid resume request")
			return
		}
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
	plan.resumeToken = req.ResumeToken
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
	record, err := s.getOwnedRun(r)
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
	record, err := s.getOwnedRun(r)
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

func (s *Server) getOwnedRun(r *http.Request) (*runtime.RunRecord, error) {
	record, err := s.getRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		return nil, err
	}
	if !s.ownsRun(r, record) {
		return nil, runtime.ErrRunNotFound
	}
	return record, nil
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
	errorMessage := ""
	if record.Error != "" {
		errorMessage = "workflow execution failed"
	}
	return RunStatusResponse{
		ID: record.ID, WorkflowID: record.WorkflowID, Status: record.Status,
		StartedAt: record.StartedAt, UpdatedAt: record.UpdatedAt,
		CompletedAt: record.CompletedAt, Error: errorMessage,
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
