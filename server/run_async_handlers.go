package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/petal-labs/petalflow/runtime"
)

// handleRunAsync enqueues a workflow run as an asynchronous job and
// returns 202 immediately without holding the request open.
func (s *Server) handleRunAsync(w http.ResponseWriter, r *http.Request, workflowID string, req RunRequest) {
	if s.jobStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_CONFIGURED", "asynchronous runs require a run job store and worker")
		return
	}
	if req.Options.Stream {
		writeError(w, http.StatusBadRequest, "INVALID_OPTIONS", "options.stream is not supported for asynchronous runs; stream via GET /api/runs/{run_id}/events")
		return
	}

	// Plan up front so invalid workflows and inputs fail at submit time
	// rather than surfacing as dead-lettered jobs.
	plan, err := s.planWorkflowRun(r.Context(), workflowID, req)
	if err != nil {
		writeRunAPIError(w, err)
		return
	}

	runID := plan.env.Trace.RunID
	if runID == "" {
		runID = uuid.New().String()
	}

	now := time.Now().UTC()
	job := RunJob{
		ID:            runID,
		WorkflowID:    workflowID,
		TenantID:      plan.tenantID,
		Trigger:       RunJobTriggerAPI,
		Input:         req.Input,
		Options:       req.Options,
		Status:        RunJobStatusQueued,
		MaxAttempts:   defaultRunJobMaxAttempts,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.jobStore.EnqueueRunJob(r.Context(), job); err != nil {
		if errors.Is(err, ErrRunJobExists) {
			writeError(w, http.StatusConflict, "RUN_JOB_EXISTS", "a run job with this id already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "RUN_JOB_STORE_ERROR", "enqueue run job failed")
		return
	}

	writeJSON(w, http.StatusAccepted, AsyncRunResponse{
		RunID:       job.ID,
		WorkflowID:  workflowID,
		Status:      RunJobStatusQueued,
		SubmittedAt: job.CreatedAt,
		StatusURL:   "/api/runs/" + job.ID,
		EventsURL:   "/api/runs/" + job.ID + "/events",
	})
}

// AsyncRunResponse is the 202 payload for asynchronous run submission.
type AsyncRunResponse struct {
	RunID       string    `json:"run_id"`
	WorkflowID  string    `json:"workflow_id"`
	Status      string    `json:"status"`
	SubmittedAt time.Time `json:"submitted_at"`
	StatusURL   string    `json:"status_url"`
	EventsURL   string    `json:"events_url"`
}

// handleRetryRun requeues a terminal asynchronous run job. The worker
// re-executes the run from scratch with a fresh attempt budget; durable
// runs resume from their last checkpoint.
func (s *Server) handleRetryRun(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil {
		writeError(w, http.StatusNotImplemented, "NOT_CONFIGURED", "run retry requires a run job store and worker")
		return
	}
	runID := r.PathValue("run_id")
	job, err := s.jobStore.GetRunJob(r.Context(), runID)
	if err != nil {
		if errors.Is(err, ErrRunJobNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "run job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "RUN_JOB_STORE_ERROR", "load run job failed")
		return
	}
	if !s.ownsTenant(r, job.TenantID) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "run job not found")
		return
	}

	now := time.Now().UTC()
	if err := s.jobStore.ResetRunJob(r.Context(), runID, now, defaultRunJobMaxAttempts); err != nil {
		if errors.Is(err, ErrRunJobConflict) {
			writeError(w, http.StatusConflict, "RUN_NOT_RETRYABLE", "run is still queued or executing; cancel it first")
			return
		}
		writeError(w, http.StatusInternalServerError, "RUN_JOB_STORE_ERROR", "reset run job failed")
		return
	}

	job, err = s.jobStore.GetRunJob(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RUN_JOB_STORE_ERROR", "load run job failed")
		return
	}
	writeJSON(w, http.StatusAccepted, runJobResponse(job))
}

// runJobResponse projects a run job for the status API.
func runJobResponse(job RunJob) RunStatusResponse {
	status := job.Status
	if status == RunJobStatusDead {
		status = string(runtime.RunStatusFailed)
	}
	response := RunStatusResponse{
		ID:         job.ID,
		WorkflowID: job.WorkflowID,
		Status:     runtime.RunStatus(status),
		StartedAt:  job.CreatedAt,
		UpdatedAt:  job.UpdatedAt,
		Error:      job.LastError,
		Attempt:    job.Attempt,
	}
	if job.NextAttemptAt.After(time.Now()) && !isRunJobTerminal(job.Status) {
		response.NextAttemptAt = &job.NextAttemptAt
	}
	return response
}
