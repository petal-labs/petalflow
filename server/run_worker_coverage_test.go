package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/runtime"
)

// --- Unit helpers ---

func TestRunJobHelpers(t *testing.T) {
	if isRunJobTerminal(RunJobStatusQueued) || isRunJobTerminal(RunJobStatusRunning) || isRunJobTerminal(RunJobStatusPaused) {
		t.Fatal("queued/running/paused are not terminal")
	}
	for _, status := range []string{RunJobStatusCompleted, RunJobStatusFailed, RunJobStatusCanceled, RunJobStatusDead} {
		if !isRunJobTerminal(status) {
			t.Fatalf("status %q should be terminal", status)
		}
	}

	// parseJobTime tolerates the fixed-width and RFC3339Nano layouts
	// but rejects garbage.
	for _, value := range []string{
		"2026-03-01T12:00:00.000000Z",
		"2026-03-01T12:00:00.5Z",
		"2026-03-01T12:00:00Z",
	} {
		if _, err := parseJobTime(value); err != nil {
			t.Fatalf("parseJobTime(%q) = %v", value, err)
		}
	}
	if _, err := parseJobTime("not-a-time"); err == nil {
		t.Fatal("parseJobTime(garbage) should fail")
	}

	if formatNullableJobTime(nil) != nil {
		t.Fatal("formatNullableJobTime(nil) should be nil")
	}
	zero := time.Time{}
	if formatNullableJobTime(&zero) != nil {
		t.Fatal("formatNullableJobTime(zero) should be nil")
	}

	if _, err := unmarshalRunJobInput([]byte("not json")); err == nil {
		t.Fatal("unmarshalRunJobInput(garbage) should fail")
	}

	options, err := marshalRunJobOptions(RunReqOptions{})
	if err != nil || string(options) == "" {
		t.Fatalf("marshalRunJobOptions = (%q, %v)", options, err)
	}
}

// --- Worker settlement paths ---

func TestRunWorkerHandleRunErrorPaths(t *testing.T) {
	h := newRunWorkerTestHarness(t, "")
	worker, err := NewRunWorker(RunWorkerConfig{Runner: h.srv, Store: h.store})
	if err != nil {
		t.Fatalf("NewRunWorker: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()

	newJob := func(id string, maxAttempts int) RunJob {
		return RunJob{
			ID: id, WorkflowID: "wf", Status: RunJobStatusRunning,
			MaxAttempts: maxAttempts, Attempt: 0,
			NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		}
	}
	enqueue := func(job RunJob) {
		job.Status = RunJobStatusQueued
		if err := h.store.EnqueueRunJob(ctx, job); err != nil {
			t.Fatalf("EnqueueRunJob: %v", err)
		}
		if _, err := h.store.ClaimRunJobs(ctx, worker.worker, now, time.Minute, 1); err != nil {
			t.Fatalf("ClaimRunJobs: %v", err)
		}
	}

	// Retryable failure: backoff + requeued.
	job := newJob("err-retry", 3)
	enqueue(job)
	worker.handleRunError(ctx, job, errors.New("boom"), RunResponse{}, now)
	settled := waitForRunJobStatus(t, h.store, job.ID, []string{RunJobStatusQueued}, 2*time.Second)
	if settled.Attempt != 1 || settled.LastError != "boom" {
		t.Fatalf("retryable settlement = %+v", settled)
	}

	// Dead-letter via attempt budget.
	job = newJob("err-dead", 1)
	enqueue(job)
	worker.handleRunError(ctx, job, errors.New("permanent"), RunResponse{}, now)
	settled = waitForRunJobStatus(t, h.store, job.ID, []string{RunJobStatusDead}, 2*time.Second)
	if settled.LastError != "permanent" {
		t.Fatalf("dead settlement = %+v", settled)
	}

	// Pause (human input pending).
	job = newJob("err-pause", 3)
	enqueue(job)
	worker.handleRunError(ctx, job, &runAPIError{Status: 202, Code: "RUN_PAUSED", Message: "waiting"}, RunResponse{RunID: job.ID}, now)
	settled = waitForRunJobStatus(t, h.store, job.ID, []string{RunJobStatusPaused}, 2*time.Second)
	if settled.RunID != job.ID {
		t.Fatalf("paused settlement = %+v", settled)
	}

	// Explicit cancel while running: settled canceled, not retried.
	job = newJob("err-cancel", 3)
	enqueue(job)
	if err := h.store.CancelRunJob(ctx, job.ID, now); err != nil {
		t.Fatalf("CancelRunJob: %v", err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	worker.handleRunError(cancelCtx, job, context.Canceled, RunResponse{}, now)
	settled = waitForRunJobStatus(t, h.store, job.ID, []string{RunJobStatusCanceled}, 2*time.Second)
	if settled.Status != RunJobStatusCanceled {
		t.Fatalf("canceled settlement = %+v", settled)
	}

	// Worker shutdown (ctx canceled, no cancel request): the job stays
	// leased so the lease expiry reassigns it.
	job = newJob("err-shutdown", 3)
	enqueue(job)
	shutdownCtx, shutdown := context.WithCancel(ctx)
	shutdown()
	worker.handleRunError(shutdownCtx, job, context.Canceled, RunResponse{}, now)
	settled, err = h.store.GetRunJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetRunJob after shutdown: %v", err)
	}
	if settled.Status != RunJobStatusRunning {
		t.Fatalf("shutdown settlement = %+v, want job left leased for reassignment", settled)
	}
}

func TestRunWorkerTerminalFailureDeadLettersImmediately(t *testing.T) {
	h := newRunWorkerTestHarness(t, "det-wf")
	h.srv.planHook = func(ctx context.Context, workflowID string, req RunRequest) (*workflowRunPlan, error) {
		return nil, &runAPIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "workflow gone"}
	}

	now := time.Now().UTC()
	if err := h.store.EnqueueRunJob(context.Background(), RunJob{
		ID: "det-job", WorkflowID: "missing-wf", Status: RunJobStatusQueued,
		MaxAttempts: 9, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueRunJob: %v", err)
	}

	worker, err := NewRunWorker(RunWorkerConfig{Runner: h.srv, Store: h.store})
	if err != nil {
		t.Fatalf("NewRunWorker: %v", err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	job := waitForRunJobStatus(t, h.store, "det-job", []string{RunJobStatusDead}, 2*time.Second)
	if !strings.Contains(job.LastError, "NOT_FOUND") {
		t.Fatalf("dead job = %+v", job)
	}
}

func TestRunWorkerRenewLeaseFailureCancelsExecution(t *testing.T) {
	h := newRunWorkerTestHarness(t, "")
	worker, err := NewRunWorker(RunWorkerConfig{
		Runner:       h.srv,
		Store:        h.store,
		WorkerID:     "honest-worker",
		LeaseDuration: 30 * time.Millisecond, // renewal tick = 10ms
	})
	if err != nil {
		t.Fatalf("NewRunWorker: %v", err)
	}

	now := time.Now().UTC()
	if err := h.store.EnqueueRunJob(context.Background(), RunJob{
		ID: "stolen-job", WorkflowID: "wf", Status: RunJobStatusQueued,
		MaxAttempts: 1, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueRunJob: %v", err)
	}
	// Another owner holds the lease.
	if _, err := h.store.ClaimRunJobs(context.Background(), "thief", now, time.Minute, 1); err != nil {
		t.Fatalf("ClaimRunJobs: %v", err)
	}

	jobCtx, cancel := context.WithCancel(context.Background())
	stop := worker.renewLeaseLoop(jobCtx, "stolen-job", cancel)
	defer close(stop)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if jobCtx.Err() != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("lease renewal failure did not cancel the execution context")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunWorkerPrunesTerminalJobs(t *testing.T) {
	h := newRunWorkerTestHarness(t, "")

	now := time.Now().UTC()
	if err := h.store.EnqueueRunJob(context.Background(), RunJob{
		ID: "prune-job", WorkflowID: "wf", Status: RunJobStatusQueued,
		MaxAttempts: 1, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueRunJob: %v", err)
	}
	if err := h.store.CompleteRunJob(context.Background(), "prune-job", "run-1", now); err != nil {
		t.Fatalf("CompleteRunJob: %v", err)
	}
	// Backdate the terminal job past the retention window.
	if _, err := h.store.db.Exec(`UPDATE run_jobs SET updated_at = ? WHERE id = ?`,
		formatJobTime(now.Add(-2*time.Hour)), "prune-job"); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	worker, err := NewRunWorker(RunWorkerConfig{
		Runner:       h.srv,
		Store:        h.store,
		RetentionAge: time.Hour,
		PruneInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRunWorker: %v", err)
	}
	worker.pruneTerminalJobs(context.Background())

	if _, err := h.store.GetRunJob(context.Background(), "prune-job"); !errors.Is(err, ErrRunJobNotFound) {
		t.Fatalf("pruned job lookup = %v, want ErrRunJobNotFound", err)
	}
}

// --- Scheduler lifecycle ---

func TestSchedulerStartStopLifecycle(t *testing.T) {
	h := newRunWorkerTestHarness(t, "lifecycle-wf")

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if err := h.store.CreateSchedule(context.Background(), WorkflowSchedule{
		ID: "sched-lc", WorkflowID: "lifecycle-wf", Cron: "* * * * *", Enabled: true,
		NextRunAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	scheduler, err := NewWorkflowScheduler(WorkflowSchedulerConfig{
		Runner:       h.srv,
		Store:        h.store,
		SchedulerID:  "lc-replica",
		PollInterval: 20 * time.Millisecond,
		LeaseDuration: time.Minute,
		Now:          func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("NewWorkflowScheduler: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Idempotent start.
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	// Wait for the scheduled run to complete.
	deadline := time.Now().Add(10 * time.Second)
	for {
		schedule, found, err := h.store.GetSchedule(context.Background(), "lifecycle-wf", "sched-lc")
		if err != nil {
			t.Fatalf("GetSchedule: %v", err)
		}
		if found && schedule.LastStatus == ScheduleRunStatusCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scheduled run never completed: %+v", schedule)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := scheduler.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestSchedulerDisablesScheduleWithInvalidCron(t *testing.T) {
	h := newRunWorkerTestHarness(t, "badcron-wf")

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	// Bypass HTTP validation to persist a bad cron expression directly.
	if err := h.store.CreateSchedule(context.Background(), WorkflowSchedule{
		ID: "sched-badcron", WorkflowID: "badcron-wf", Cron: "not a cron", Enabled: true,
		NextRunAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	scheduler, err := NewWorkflowScheduler(WorkflowSchedulerConfig{
		Runner: h.srv, Store: h.store, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewWorkflowScheduler: %v", err)
	}
	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	schedule, found, err := h.store.GetSchedule(context.Background(), "badcron-wf", "sched-badcron")
	if err != nil || !found {
		t.Fatalf("GetSchedule: %v (found=%v)", err, found)
	}
	if schedule.Enabled || schedule.LastStatus != ScheduleRunStatusDeadLetter {
		t.Fatalf("invalid-cron schedule = %+v, want disabled with dead_letter", schedule)
	}
}

// --- Async run API edges ---

func TestHandleRunAsyncConflict(t *testing.T) {
	h := newRunWorkerTestHarness(t, "worker-wf")
	h.srv.planHook = func(ctx context.Context, workflowID string, req RunRequest) (*workflowRunPlan, error) {
		plan, err := h.srv.planWorkflowRunDefault(ctx, workflowID, req)
		if err != nil {
			return nil, err
		}
		plan.env.Trace.RunID = "pre-seeded-run"
		return plan, nil
	}

	now := time.Now().UTC()
	if err := h.store.EnqueueRunJob(context.Background(), RunJob{
		ID: "pre-seeded-run", WorkflowID: "worker-wf", Status: RunJobStatusQueued,
		MaxAttempts: 1, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueRunJob: %v", err)
	}

	body := []byte(`{"input":{},"options":{"async":true}}`)
	w := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/workflows/worker-wf/run", bytes.NewReader(body)))
	if w.Code != http.StatusConflict {
		t.Fatalf("conflicting async submit = %d, want 409 (body %s)", w.Code, w.Body.String())
	}
}

func TestHandleRetryRunEdges(t *testing.T) {
	h := newRunWorkerTestHarness(t, "worker-wf")
	handler := h.srv.Handler()

	// Unknown job.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/runs/nope/retry", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("retry unknown = %d, want 404", w.Code)
	}

	// Non-terminal (queued) job conflicts.
	now := time.Now().UTC()
	if err := h.store.EnqueueRunJob(context.Background(), RunJob{
		ID: "queued-job", WorkflowID: "worker-wf", Status: RunJobStatusQueued,
		MaxAttempts: 1, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueRunJob: %v", err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/runs/queued-job/retry", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("retry queued = %d, want 409", w.Code)
	}
}

func TestRunJobResponseProjectsDeadAndBackoff(t *testing.T) {
	now := time.Now().UTC()

	dead := runJobResponse(RunJob{
		ID: "d", WorkflowID: "wf", Status: RunJobStatusDead, Attempt: 3,
		LastError: "exhausted", CreatedAt: now, UpdatedAt: now,
	})
	if dead.Status != runtime.RunStatusFailed || dead.Error != "exhausted" || dead.Attempt != 3 {
		t.Fatalf("dead projection = %+v", dead)
	}

	future := now.Add(time.Hour)
	queued := runJobResponse(RunJob{
		ID: "q", WorkflowID: "wf", Status: RunJobStatusQueued, Attempt: 1,
		NextAttemptAt: future, CreatedAt: now, UpdatedAt: now,
	})
	if queued.NextAttemptAt == nil || !queued.NextAttemptAt.After(now) {
		t.Fatalf("queued projection = %+v, want next_attempt_at", queued)
	}

	terminal := runJobResponse(RunJob{
		ID: "c", WorkflowID: "wf", Status: RunJobStatusCompleted,
		NextAttemptAt: future, CreatedAt: now, UpdatedAt: now,
	})
	if terminal.NextAttemptAt != nil {
		t.Fatalf("terminal projection = %+v, want no next_attempt_at", terminal)
	}
}
