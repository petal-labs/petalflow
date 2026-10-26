package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func runRunJobStoreConformance(t *testing.T, newStore func(t *testing.T) RunJobStore) {
	t.Helper()

	t.Run("EnqueueAndGet", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

		job := testRunJob("job-1", now)
		if err := store.EnqueueRunJob(ctx, job); err != nil {
			t.Fatalf("EnqueueRunJob: %v", err)
		}
		if err := store.EnqueueRunJob(ctx, job); !errors.Is(err, ErrRunJobExists) {
			t.Fatalf("duplicate enqueue = %v, want ErrRunJobExists", err)
		}

		got, err := store.GetRunJob(ctx, "job-1")
		if err != nil {
			t.Fatalf("GetRunJob: %v", err)
		}
		if got.ID != "job-1" || got.WorkflowID != "wf" || got.Status != RunJobStatusQueued {
			t.Fatalf("got = %+v", got)
		}
		if got.MaxAttempts != defaultRunJobMaxAttempts || got.Attempt != 0 {
			t.Fatalf("defaults not applied: attempt=%d max=%d", got.Attempt, got.MaxAttempts)
		}

		if _, err := store.GetRunJob(ctx, "missing"); !errors.Is(err, ErrRunJobNotFound) {
			t.Fatalf("missing job = %v, want ErrRunJobNotFound", err)
		}
	})

	t.Run("ClaimIsAtomicAcrossOwners", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

		for _, id := range []string{"job-a", "job-b"} {
			if err := store.EnqueueRunJob(ctx, testRunJob(id, now)); err != nil {
				t.Fatalf("EnqueueRunJob: %v", err)
			}
		}

		claimedA, err := store.ClaimRunJobs(ctx, "worker-a", now, time.Minute, 10)
		if err != nil {
			t.Fatalf("ClaimRunJobs (A): %v", err)
		}
		claimedB, err := store.ClaimRunJobs(ctx, "worker-b", now, time.Minute, 10)
		if err != nil {
			t.Fatalf("ClaimRunJobs (B): %v", err)
		}
		if len(claimedA)+len(claimedB) != 2 {
			t.Fatalf("claimed A=%d B=%d, want 2 total", len(claimedA), len(claimedB))
		}
		seen := map[string]string{}
		for _, job := range claimedA {
			seen[job.ID] = "worker-a"
			if job.LeaseOwner != "worker-a" || job.Status != RunJobStatusRunning || job.Attempt != 1 {
				t.Fatalf("claimed job = %+v", job)
			}
		}
		for _, job := range claimedB {
			seen[job.ID] = "worker-b"
		}
		if len(seen) != 2 {
			t.Fatalf("both workers claimed the same jobs: %v", seen)
		}
	})

	t.Run("ExpiredLeaseIsReclaimable", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

		if err := store.EnqueueRunJob(ctx, testRunJob("job-crash", now)); err != nil {
			t.Fatalf("EnqueueRunJob: %v", err)
		}
		if _, err := store.ClaimRunJobs(ctx, "worker-a", now, time.Minute, 1); err != nil {
			t.Fatalf("ClaimRunJobs: %v", err)
		}

		// Still leased: no claims for anyone.
		claimed, err := store.ClaimRunJobs(ctx, "worker-b", now.Add(30*time.Second), time.Minute, 10)
		if err != nil {
			t.Fatalf("ClaimRunJobs while leased: %v", err)
		}
		if len(claimed) != 0 {
			t.Fatalf("claimed leased jobs: %+v", claimed)
		}

		// After lease expiry the job is reclaimable (crashed worker).
		reclaimed, err := store.ClaimRunJobs(ctx, "worker-b", now.Add(2*time.Minute), time.Minute, 10)
		if err != nil {
			t.Fatalf("ClaimRunJobs after expiry: %v", err)
		}
		if len(reclaimed) != 1 || reclaimed[0].ID != "job-crash" {
			t.Fatalf("reclaimed = %+v", reclaimed)
		}
		if reclaimed[0].Attempt != 2 {
			t.Fatalf("attempt after reclaim = %d, want 2", reclaimed[0].Attempt)
		}
	})

	t.Run("SettlementTransitions", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

		if err := store.EnqueueRunJob(ctx, testRunJob("job-settle", now)); err != nil {
			t.Fatalf("EnqueueRunJob: %v", err)
		}
		if err := store.RenewRunJobLease(ctx, "job-settle", "nobody", now.Add(time.Minute), now); !errors.Is(err, ErrRunJobConflict) {
			t.Fatalf("renew unclaimed lease = %v, want ErrRunJobConflict", err)
		}

		if _, err := store.ClaimRunJobs(ctx, "worker-a", now, time.Minute, 1); err != nil {
			t.Fatalf("ClaimRunJobs: %v", err)
		}
		if err := store.RenewRunJobLease(ctx, "job-settle", "worker-a", now.Add(2*time.Minute), now); err != nil {
			t.Fatalf("RenewRunJobLease: %v", err)
		}
		if err := store.CompleteRunJob(ctx, "job-settle", "run-1", now); err != nil {
			t.Fatalf("CompleteRunJob: %v", err)
		}
		if err := store.CompleteRunJob(ctx, "job-settle", "run-1", now); !errors.Is(err, ErrRunJobConflict) {
			t.Fatalf("double complete = %v, want ErrRunJobConflict", err)
		}

		got, err := store.GetRunJob(ctx, "job-settle")
		if err != nil {
			t.Fatalf("GetRunJob: %v", err)
		}
		if got.Status != RunJobStatusCompleted || got.RunID != "run-1" {
			t.Fatalf("settled job = %+v", got)
		}
	})

	t.Run("FailureRetriesThenDeadLetters", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

		job := testRunJob("job-retry", now)
		job.MaxAttempts = 3
		if err := store.EnqueueRunJob(ctx, job); err != nil {
			t.Fatalf("EnqueueRunJob: %v", err)
		}

		failOnce := func(attempt int, at time.Time) string {
			claimed, err := store.ClaimRunJobs(ctx, "worker-a", at, time.Minute, 1)
			if err != nil {
				t.Fatalf("ClaimRunJobs: %v", err)
			}
			if len(claimed) != 1 {
				t.Fatalf("claimed %d jobs, want 1", len(claimed))
			}
			status, err := store.FailRunJob(ctx, "job-retry", "boom", at.Add(time.Minute), at)
			if err != nil {
				t.Fatalf("FailRunJob: %v", err)
			}
			return status
		}

		if status := failOnce(1, now); status != RunJobStatusQueued {
			t.Fatalf("first failure status = %q, want queued", status)
		}
		if status := failOnce(2, now.Add(time.Minute)); status != RunJobStatusQueued {
			t.Fatalf("second failure status = %q, want queued", status)
		}
		if status := failOnce(3, now.Add(2*time.Minute)); status != RunJobStatusDead {
			t.Fatalf("third failure status = %q, want dead", status)
		}

		got, err := store.GetRunJob(ctx, "job-retry")
		if err != nil {
			t.Fatalf("GetRunJob: %v", err)
		}
		if got.Status != RunJobStatusDead || got.LastError != "boom" {
			t.Fatalf("dead job = %+v", got)
		}
	})

	t.Run("PauseAndReset", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

		if err := store.EnqueueRunJob(ctx, testRunJob("job-pause", now)); err != nil {
			t.Fatalf("EnqueueRunJob: %v", err)
		}
		if _, err := store.ClaimRunJobs(ctx, "worker-a", now, time.Minute, 1); err != nil {
			t.Fatalf("ClaimRunJobs: %v", err)
		}
		if err := store.PauseRunJob(ctx, "job-pause", "run-1", "waiting on human", now); err != nil {
			t.Fatalf("PauseRunJob: %v", err)
		}
		if err := store.ResetRunJob(ctx, "job-pause", now, 5); err != nil {
			t.Fatalf("ResetRunJob: %v", err)
		}

		got, err := store.GetRunJob(ctx, "job-pause")
		if err != nil {
			t.Fatalf("GetRunJob: %v", err)
		}
		if got.Status != RunJobStatusQueued || got.Attempt != 0 || got.MaxAttempts != 5 {
			t.Fatalf("reset job = %+v", got)
		}

		// Canceled jobs are resettable too (retry endpoint).
		if err := store.CancelRunJob(ctx, "job-pause", now); err != nil {
			t.Fatalf("CancelRunJob: %v", err)
		}
		if err := store.ResetRunJob(ctx, "job-pause", now, 0); err != nil {
			t.Fatalf("ResetRunJob canceled: %v", err)
		}
	})

	t.Run("PruneTerminalJobs", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

		if err := store.EnqueueRunJob(ctx, testRunJob("job-old", now.Add(-2*time.Hour))); err != nil {
			t.Fatalf("EnqueueRunJob: %v", err)
		}
		if err := store.CompleteRunJob(ctx, "job-old", "run-old", now.Add(-2*time.Hour)); !errors.Is(err, ErrRunJobConflict) {
			// CompleteRunJob requires queued/running; queued is allowed.
			_ = err
		}
		if err := store.EnqueueRunJob(ctx, testRunJob("job-fresh", now)); err != nil {
			t.Fatalf("EnqueueRunJob: %v", err)
		}

		deleted, err := store.PruneRunJobs(ctx, now.Add(-time.Hour), now)
		if err != nil {
			t.Fatalf("PruneRunJobs: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("deleted = %d, want 1", deleted)
		}
		if _, err := store.GetRunJob(ctx, "job-old"); !errors.Is(err, ErrRunJobNotFound) {
			t.Fatalf("old job = %v, want ErrRunJobNotFound", err)
		}
		if _, err := store.GetRunJob(ctx, "job-fresh"); err != nil {
			t.Fatalf("fresh job pruned: %v", err)
		}
	})
}

func testRunJob(id string, now time.Time) RunJob {
	return RunJob{
		ID:         id,
		WorkflowID: "wf",
		TenantID:   "default",
		Trigger:    RunJobTriggerAPI,
		Input:      map[string]any{"k": "v"},
		Status:     RunJobStatusQueued,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func TestSQLiteRunJobStoreConformance(t *testing.T) {
	runRunJobStoreConformance(t, func(t *testing.T) RunJobStore {
		store, err := NewSQLiteStore(SQLiteStoreConfig{DSN: filepath.Join(t.TempDir(), "wf.db")})
		if err != nil {
			t.Fatalf("NewSQLiteStore: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	})
}
