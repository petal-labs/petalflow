package server

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
)

// TestTwoWorkersNeverExecuteSameJob dispatches two workers against one
// store with a single queued job; only one worker may execute it.
func TestTwoWorkersNeverExecuteSameJob(t *testing.T) {
	h := newRunWorkerTestHarness(t, "coord-wf")

	var executions atomic.Int64
	h.srv.planHook = func(ctx context.Context, workflowID string, req RunRequest) (*workflowRunPlan, error) {
		executions.Add(1)
		return h.srv.planWorkflowRunDefault(ctx, workflowID, req)
	}

	now := time.Now().UTC()
	if err := h.store.EnqueueRunJob(context.Background(), RunJob{
		ID: "contended-job", WorkflowID: "coord-wf", Status: RunJobStatusQueued,
		MaxAttempts: 1, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueRunJob: %v", err)
	}

	workerA, err := NewRunWorker(RunWorkerConfig{Runner: h.srv, Store: h.store, WorkerID: "worker-a"})
	if err != nil {
		t.Fatalf("NewRunWorker: %v", err)
	}
	workerB, err := NewRunWorker(RunWorkerConfig{Runner: h.srv, Store: h.store, WorkerID: "worker-b"})
	if err != nil {
		t.Fatalf("NewRunWorker: %v", err)
	}

	// Both replicas claim in the same instant.
	done := make(chan error, 2)
	go func() { done <- workerA.RunOnce(context.Background()) }()
	go func() { done <- workerB.RunOnce(context.Background()) }()
	if err := <-done; err != nil {
		t.Fatalf("worker A pass: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("worker B pass: %v", err)
	}

	job := waitForRunJobStatus(t, h.store, "contended-job", []string{RunJobStatusCompleted, RunJobStatusFailed, RunJobStatusDead}, 5*time.Second)
	if got := executions.Load(); got != 1 {
		t.Fatalf("workflow executed %d times, want exactly 1", got)
	}
	if job.Attempt != 1 {
		t.Fatalf("attempt = %d, want 1", job.Attempt)
	}
}

// TestTwoSchedulersClaimDisjointSchedules proves the durable lease
// arbitrates concurrent claims: N due schedules claimed by two replicas
// simultaneously yield disjoint sets covering every schedule once.
func TestTwoSchedulersClaimDisjointSchedules(t *testing.T) {
	store := newTestSQLiteStore(t)
	srv := NewServer(ServerConfig{
		Store:         store,
		ScheduleStore: store,
		ToolStore:     newTestToolStore(t),
		Providers:     hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) { return nil, nil },
	})
	createWorkflowForScheduler(t, srv.Handler(), "coord-sched-wf")

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		workflowID := "coord-sched-wf"
		schedule := WorkflowSchedule{
			ID:         fmt.Sprintf("sched-%d", i),
			WorkflowID: workflowID,
			Cron:       "* * * * *",
			Enabled:    true,
			NextRunAt:  now.Add(-time.Minute),
			CreatedAt:  now.Add(-time.Hour),
			UpdatedAt:  now.Add(-time.Hour),
		}
		if err := store.CreateSchedule(context.Background(), schedule); err != nil {
			t.Fatalf("CreateSchedule: %v", err)
		}
	}

	var (
		claimedA, claimedB []WorkflowSchedule
		errA, errB         error
		done               = make(chan struct{})
	)
	go func() {
		claimedA, errA = store.ClaimDueSchedules(context.Background(), "replica-a", now, time.Hour, 10)
		done <- struct{}{}
	}()
	go func() {
		claimedB, errB = store.ClaimDueSchedules(context.Background(), "replica-b", now, time.Hour, 10)
		done <- struct{}{}
	}()
	<-done
	<-done

	if errA != nil || errB != nil {
		t.Fatalf("claims failed: %v / %v", errA, errB)
	}
	if len(claimedA)+len(claimedB) != 6 {
		t.Fatalf("claimed %d+%d schedules, want 6 total", len(claimedA), len(claimedB))
	}
	seen := map[string]int{}
	for _, s := range claimedA {
		seen[s.ID]++
		if s.LeaseOwner != "replica-a" {
			t.Fatalf("schedule %s lease owner = %q", s.ID, s.LeaseOwner)
		}
	}
	for _, s := range claimedB {
		seen[s.ID]++
		if s.LeaseOwner != "replica-b" {
			t.Fatalf("schedule %s lease owner = %q", s.ID, s.LeaseOwner)
		}
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("schedule %s claimed %d times across replicas", id, count)
		}
	}
}

// TestSchedulerDeadLettersAfterConsecutiveFailures verifies the retry
// backoff and dead-letter path for repeatedly failing scheduled runs.
func TestSchedulerDeadLettersAfterConsecutiveFailures(t *testing.T) {
	store := newTestSQLiteStore(t)
	srv := NewServer(ServerConfig{
		Store:         store,
		ScheduleStore: store,
		ToolStore:     newTestToolStore(t),
		Providers:     hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) { return nil, nil },
	})
	createWorkflowForScheduler(t, srv.Handler(), "failing-wf")
	// The workflow exists, but planning fails permanently: the run
	// cannot ever succeed.
	srv.planHook = func(ctx context.Context, workflowID string, req RunRequest) (*workflowRunPlan, error) {
		return nil, &runAPIError{Status: 422, Code: "HYDRATE_ERROR", Message: "permanent"}
	}

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	if err := store.CreateSchedule(context.Background(), WorkflowSchedule{
		ID: "sched-dl", WorkflowID: "failing-wf", Cron: "* * * * *", Enabled: true,
		NextRunAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	scheduler, err := NewWorkflowScheduler(WorkflowSchedulerConfig{
		Runner:                 srv,
		Store:                  store,
		SchedulerID:            "replica-a",
		RetryBackoff:           time.Millisecond,
		MaxConsecutiveFailures: 3,
		Now:                    func() time.Time { return clock.UTC() },
	})
	if err != nil {
		t.Fatalf("NewWorkflowScheduler: %v", err)
	}

	for i := 0; i < 3; i++ {
		// Wait for the schedule to become due again after backoff.
		for attempt := 0; attempt < 200; attempt++ {
			schedule, found, err := store.GetSchedule(context.Background(), "failing-wf", "sched-dl")
			if err != nil {
				t.Fatalf("GetSchedule: %v", err)
			}
			if found && !schedule.Enabled {
				break
			}
			if found && schedule.NextRunAt.Before(clock) || found && schedule.NextRunAt.Equal(clock) {
				break
			}
			clock = clock.Add(10 * time.Second)
		}
		if err := scheduler.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce %d: %v", i+1, err)
		}
		waitForScheduleStatus(t, store, "failing-wf", "sched-dl", 5*time.Second)
	}

	schedule, found, err := store.GetSchedule(context.Background(), "failing-wf", "sched-dl")
	if err != nil || !found {
		t.Fatalf("GetSchedule: %v (found=%v)", err, found)
	}
	if schedule.LastStatus != ScheduleRunStatusDeadLetter {
		t.Fatalf("last_status = %q, want dead_letter", schedule.LastStatus)
	}
	if schedule.Enabled {
		t.Fatal("dead-lettered schedule must be disabled")
	}
	if schedule.ConsecutiveFailures != 3 {
		t.Fatalf("consecutive_failures = %d, want 3", schedule.ConsecutiveFailures)
	}
}
