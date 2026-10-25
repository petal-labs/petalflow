package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/runtime"
)

// runWorkerTestHarness builds a server + SQLite job store + a trivial
// workflow for exercising the async run pipeline end to end.
type runWorkerTestHarness struct {
	store *SQLiteStore
	srv   *Server
}

func newRunWorkerTestHarness(t *testing.T, workflowID string) *runWorkerTestHarness {
	t.Helper()
	store := newTestSQLiteStore(t)
	srv := NewServer(ServerConfig{
		Store:         store,
		ScheduleStore: store,
		ToolStore:     newTestToolStore(t),
		JobStore:      store,
		RunStore:      newTestRunStore(t),
		Providers:     hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) { return nil, nil },
		Bus:           bus.NewMemBus(bus.MemBusConfig{}),
		EventStore:    newTestEventStore(t),
	})
	if workflowID != "" {
		createWorkflowForScheduler(t, srv.Handler(), workflowID)
	}
	return &runWorkerTestHarness{store: store, srv: srv}
}

// submitAsyncJob submits an async run through the HTTP API and returns
// the 202 payload.
func (h *runWorkerTestHarness) submitAsyncJob(t *testing.T, workflowID string) AsyncRunResponse {
	t.Helper()
	body, err := json.Marshal(RunRequest{
		Input:   map[string]any{"x": "y"},
		Options: RunReqOptions{Async: true},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workflows/"+workflowID+"/run", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("async submit status=%d body=%s", w.Code, w.Body.String())
	}
	var resp AsyncRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode async response: %v", err)
	}
	if resp.RunID == "" || resp.Status != RunJobStatusQueued {
		t.Fatalf("async response = %+v", resp)
	}
	return resp
}

func waitForRunJobStatus(t *testing.T, store RunJobStore, id string, statuses []string, timeout time.Duration) RunJob {
	t.Helper()
	want := map[string]bool{}
	for _, s := range statuses {
		want[s] = true
	}
	deadline := time.Now().Add(timeout)
	for {
		job, err := store.GetRunJob(context.Background(), id)
		if err == nil && want[job.Status] {
			return job
		}
		if time.Now().After(deadline) {
			job, _ := store.GetRunJob(context.Background(), id)
			t.Fatalf("timeout waiting for job status in %v; last=%+v", statuses, job)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func getRunStatus(t *testing.T, handler http.Handler, runID string) (int, RunStatusResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/runs/"+runID, nil))
	var status RunStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode run status (code %d): %v", w.Code, err)
	}
	return w.Code, status
}

func TestRunWorkerExecutesSubmittedJob(t *testing.T) {
	h := newRunWorkerTestHarness(t, "worker-wf")
	submitted := h.submitAsyncJob(t, "worker-wf")

	worker, err := NewRunWorker(RunWorkerConfig{Runner: h.srv, Store: h.store})
	if err != nil {
		t.Fatalf("NewRunWorker: %v", err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	job := waitForRunJobStatus(t, h.store, submitted.RunID, []string{RunJobStatusCompleted}, 5*time.Second)
	if job.Attempt != 1 || job.RunID == "" {
		t.Fatalf("completed job = %+v", job)
	}

	// The durable run record exists under the job ID and is completed.
	record, err := h.srv.runStore.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("runStore.Get: %v", err)
	}
	if record.Status != runtime.RunStatusCompleted {
		t.Fatalf("run status = %q, want completed", record.Status)
	}

	// A settled job is never claimed again.
	claimedAgain, err := h.store.ClaimRunJobs(context.Background(), "worker-x", time.Now().UTC(), time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimRunJobs after completion: %v", err)
	}
	if len(claimedAgain) != 0 {
		t.Fatalf("terminal job was claimed again: %+v", claimedAgain)
	}
}

func TestAsyncSubmitPollAndCancelLifecycle(t *testing.T) {
	h := newRunWorkerTestHarness(t, "worker-wf")
	submitted := h.submitAsyncJob(t, "worker-wf")

	// Poll before execution: the job is queued and visible through
	// GET /api/runs/{run_id} without a durable run record existing yet.
	code, status := getRunStatus(t, h.srv.Handler(), submitted.RunID)
	if code != http.StatusOK || status.Status != runtime.RunStatusQueued {
		t.Fatalf("queued poll = (%d, %+v)", code, status)
	}

	// Cancel the queued job through the API.
	w := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/runs/"+submitted.RunID+"/cancel", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("cancel queued job status=%d body=%s", w.Code, w.Body.String())
	}
	code, status = getRunStatus(t, h.srv.Handler(), submitted.RunID)
	if code != http.StatusOK || status.Status != runtime.RunStatusCanceled {
		t.Fatalf("canceled poll = (%d, %+v)", code, status)
	}

	// The worker never executes a canceled job.
	worker, err := NewRunWorker(RunWorkerConfig{Runner: h.srv, Store: h.store})
	if err != nil {
		t.Fatalf("NewRunWorker: %v", err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	_, status = getRunStatus(t, h.srv.Handler(), submitted.RunID)
	if status.Status != runtime.RunStatusCanceled {
		t.Fatalf("status after worker pass = %q, want canceled", status.Status)
	}
}

func TestAsyncSubmitRejectsStreamingAndUnconfiguredStore(t *testing.T) {
	h := newRunWorkerTestHarness(t, "worker-wf")

	body, _ := json.Marshal(RunRequest{Options: RunReqOptions{Async: true, Stream: true}})
	w := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/workflows/worker-wf/run", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("streaming async submit = %d, want 400", w.Code)
	}

	// Without a job store, async submission is explicitly unsupported.
	store := newTestSQLiteStore(t)
	srv := NewServer(ServerConfig{
		Store:         store,
		ScheduleStore: store,
		ToolStore:     newTestToolStore(t),
		Providers:     hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) { return nil, nil },
	})
	createWorkflowForScheduler(t, srv.Handler(), "no-store-wf")
	body, _ = json.Marshal(RunRequest{Options: RunReqOptions{Async: true}})
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, httptest.NewRequest(http.MethodPost, "/api/workflows/no-store-wf/run", bytes.NewReader(body)))
	if w2.Code != http.StatusNotImplemented {
		t.Fatalf("async submit without job store = %d, want 501", w2.Code)
	}
}

func TestRunWorkerRetriesFailuresWithBackoff(t *testing.T) {
	h := newRunWorkerTestHarness(t, "retry-wf")

	var attempts atomic.Int64
	original := h.srv.planWorkflowRunDefault
	h.srv.planHook = func(ctx context.Context, workflowID string, req RunRequest) (*workflowRunPlan, error) {
		if attempts.Add(1) == 1 {
			return nil, &runAPIError{Status: 500, Code: "SIMULATED", Message: "transient failure"}
		}
		return original(ctx, workflowID, req)
	}

	now := time.Now().UTC()
	if err := h.store.EnqueueRunJob(context.Background(), RunJob{
		ID: "retry-job", WorkflowID: "retry-wf", Status: RunJobStatusQueued,
		MaxAttempts: 3, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueRunJob: %v", err)
	}

	worker, err := NewRunWorker(RunWorkerConfig{
		Runner:       h.srv,
		Store:        h.store,
		RetryBackoff: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRunWorker: %v", err)
	}

	// First pass fails; the job goes back to queued with backoff.
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce 1: %v", err)
	}
	job := waitForRunJobStatus(t, h.store, "retry-job", []string{RunJobStatusQueued}, 5*time.Second)
	if job.Attempt != 1 || job.LastError == "" {
		t.Fatalf("failed-then-requeued job = %+v", job)
	}
	if !job.NextAttemptAt.After(now) {
		t.Fatalf("next_attempt_at = %s, want pushed into the future for backoff", job.NextAttemptAt)
	}

	// After the (tiny) backoff window the retry succeeds.
	deadline := job.NextAttemptAt.Add(time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce 2: %v", err)
	}
	job = waitForRunJobStatus(t, h.store, "retry-job", []string{RunJobStatusCompleted}, 5*time.Second)
	if job.Attempt != 2 {
		t.Fatalf("retried job attempt = %d, want 2", job.Attempt)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

// executeClaimed executes a job the test claimed manually.
func (h *runWorkerTestHarness) executeClaimed(t *testing.T, worker *RunWorker, job RunJob) RunJob {
	t.Helper()
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	return waitForRunJobStatus(t, h.store, job.ID, []string{RunJobStatusCompleted, RunJobStatusDead, RunJobStatusFailed}, 5*time.Second)
}

func TestRunWorkerDeadLettersAfterAttemptBudget(t *testing.T) {
	h := newRunWorkerTestHarness(t, "dead-wf")

	h.srv.planHook = func(ctx context.Context, workflowID string, req RunRequest) (*workflowRunPlan, error) {
		return nil, &runAPIError{Status: 500, Code: "SIMULATED", Message: "permanent failure"}
	}

	now := time.Now().UTC()
	if err := h.store.EnqueueRunJob(context.Background(), RunJob{
		ID: "dead-job", WorkflowID: "dead-wf", Status: RunJobStatusQueued,
		MaxAttempts: 2, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueRunJob: %v", err)
	}

	worker, err := NewRunWorker(RunWorkerConfig{
		Runner:       h.srv,
		Store:        h.store,
		RetryBackoff: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRunWorker: %v", err)
	}

	// Each pass consumes one attempt: attempt 1 -> queued, attempt 2 -> dead.
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce 1: %v", err)
	}
	requeued := waitForRunJobStatus(t, h.store, "dead-job", []string{RunJobStatusQueued}, 5*time.Second)
	for time.Now().Before(requeued.NextAttemptAt.Add(time.Second)) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce 2: %v", err)
	}
	job := waitForRunJobStatus(t, h.store, "dead-job", []string{RunJobStatusDead}, 5*time.Second)
	if job.LastError == "" {
		t.Fatal("dead-lettered job has no last_error")
	}

	// The retry endpoint can resurrect a dead-lettered job.
	w := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/runs/dead-job/retry", nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("retry dead job status=%d body=%s", w.Code, w.Body.String())
	}
	reset, err := h.store.GetRunJob(context.Background(), "dead-job")
	if err != nil {
		t.Fatalf("GetRunJob: %v", err)
	}
	if reset.Status != RunJobStatusQueued || reset.Attempt != 0 {
		t.Fatalf("reset job = %+v", reset)
	}
}

func TestRunWorkerStopWaitsForInFlightJob(t *testing.T) {
	h := newRunWorkerTestHarness(t, "blocker-wf")
	blocker := make(chan struct{})
	var executed atomic.Bool
	h.srv.planHook = func(ctx context.Context, workflowID string, req RunRequest) (*workflowRunPlan, error) {
		executed.Store(true)
		<-blocker
		// Execute a real trivial workflow so the run completes.
		return h.srv.planWorkflowRunDefault(ctx, "blocker-wf", req)
	}

	worker, err := NewRunWorker(RunWorkerConfig{
		Runner:       h.srv,
		Store:        h.store,
		PollInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRunWorker: %v", err)
	}

	// Start with a cancellable context; the worker loop claims the job.
	ctx, cancel := context.WithCancel(context.Background())
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	now := time.Now().UTC()
	if err := h.store.EnqueueRunJob(context.Background(), RunJob{
		ID: "inflight-job", WorkflowID: "wf", Status: RunJobStatusQueued,
		MaxAttempts: 1, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueRunJob: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if executed.Load() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job was never claimed")
		}
		time.Sleep(10 * time.Millisecond)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Stop(stopCtx) }()

	// Stop must not return while the job is executing.
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned before in-flight job finished (err=%v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(blocker)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop after settle: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after job finished")
	}
	cancel()
}

func TestRunWorkerReclaimsLeaseAfterCrash(t *testing.T) {
	h := newRunWorkerTestHarness(t, "recovery-wf")

	now := time.Now().UTC()
	if err := h.store.EnqueueRunJob(context.Background(), RunJob{
		ID: "crash-job", WorkflowID: "recovery-wf", Status: RunJobStatusQueued,
		MaxAttempts: 3, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueRunJob: %v", err)
	}

	// "Worker A" claims with a tiny lease, then "crashes" (never settles).
	claimed, err := h.store.ClaimRunJobs(context.Background(), "worker-a", now, 10*time.Millisecond, 1)
	if err != nil {
		t.Fatalf("ClaimRunJobs: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %+v", claimed)
	}

	// "Worker B" on another instance reclaims after the lease expires
	// and completes the run.
	workerB, err := NewRunWorker(RunWorkerConfig{
		Runner:   h.srv,
		Store:    h.store,
		WorkerID: "worker-b",
		Now:      func() time.Time { return now.Add(time.Second) },
	})
	if err != nil {
		t.Fatalf("NewRunWorker: %v", err)
	}
	if err := workerB.RunOnce(context.Background()); err != nil {
		t.Fatalf("worker B RunOnce: %v", err)
	}
	job := waitForRunJobStatus(t, h.store, "crash-job", []string{RunJobStatusCompleted}, 5*time.Second)
	if job.Attempt != 2 {
		t.Fatalf("recovered job = %+v, want attempt 2 after reclaim", job)
	}
}

func newTestRunStore(t *testing.T) runtime.RunStore {
	t.Helper()
	store, err := runtime.NewSQLiteRunStore(runtime.SQLiteRunStoreConfig{DSN: filepath.Join(t.TempDir(), "runs.db")})
	if err != nil {
		t.Fatalf("NewSQLiteRunStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
