package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/hydrate"
)

func TestWorkflowScheduler_RunOnceExecutesDueSchedule(t *testing.T) {
	store := newTestSQLiteStore(t)
	eventStore := newTestEventStore(t)
	srv := NewServer(ServerConfig{
		Store:         store,
		ScheduleStore: store,
		Providers:     hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) { return nil, nil },
		Bus:           bus.NewMemBus(bus.MemBusConfig{}),
		EventStore:    eventStore,
	})
	createWorkflowForScheduler(t, srv.Handler(), "scheduler-run")

	now := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	schedule := WorkflowSchedule{
		ID:         "sched-run",
		WorkflowID: "scheduler-run",
		Cron:       "* * * * *",
		Enabled:    true,
		Input:      map[string]any{"x": "y"},
		NextRunAt:  now.Add(-time.Minute),
		CreatedAt:  now.Add(-time.Hour),
		UpdatedAt:  now.Add(-time.Hour),
	}
	if err := store.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	scheduler, err := NewWorkflowScheduler(WorkflowSchedulerConfig{
		Runner:       srv,
		Store:        store,
		PollInterval: time.Second,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewWorkflowScheduler: %v", err)
	}

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	updated := waitForScheduleStatus(t, store, "scheduler-run", "sched-run", 2*time.Second)
	if updated.LastStatus != ScheduleRunStatusCompleted {
		t.Fatalf("last_status=%q, want %q", updated.LastStatus, ScheduleRunStatusCompleted)
	}
	if updated.LastRunID == "" {
		t.Fatal("last_run_id is empty")
	}
	if updated.LastRunAt == nil || updated.LastRunAt.IsZero() {
		t.Fatal("last_run_at is nil/zero")
	}
	if !updated.NextRunAt.After(now) {
		t.Fatalf("next_run_at=%s, want > %s", updated.NextRunAt, now)
	}

	events, err := eventStore.List(context.Background(), updated.LastRunID, 0, 0)
	if err != nil {
		t.Fatalf("eventStore.List: %v", err)
	}
	foundTrigger := false
	for _, event := range events {
		if event.Kind != "run.started" {
			continue
		}
		if event.Payload["trigger"] == "schedule" && event.Payload["schedule_id"] == "sched-run" {
			foundTrigger = true
			break
		}
	}
	if !foundTrigger {
		t.Fatalf("expected run.started event with schedule metadata; events=%v", events)
	}
}

func TestWorkflowScheduler_LeasedScheduleNotClaimedByOtherReplica(t *testing.T) {
	store := newTestSQLiteStore(t)
	srv := NewServer(ServerConfig{
		Store:         store,
		ScheduleStore: store,
		Providers:     hydrate.ProviderMap{},
		ClientFactory: func(name string, cfg hydrate.ProviderConfig) (core.LLMClient, error) { return nil, nil },
	})
	createWorkflowForScheduler(t, srv.Handler(), "scheduler-overlap")

	now := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	schedule := WorkflowSchedule{
		ID:         "sched-overlap",
		WorkflowID: "scheduler-overlap",
		Cron:       "* * * * *",
		Enabled:    true,
		NextRunAt:  now.Add(-time.Minute),
		CreatedAt:  now.Add(-time.Hour),
		UpdatedAt:  now.Add(-time.Hour),
	}
	if err := store.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	replicaA, err := NewWorkflowScheduler(WorkflowSchedulerConfig{
		Runner:        srv,
		Store:         store,
		SchedulerID:   "replica-a",
		LeaseDuration: time.Hour,
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewWorkflowScheduler: %v", err)
	}
	_ = replicaA
	replicaB, err := NewWorkflowScheduler(WorkflowSchedulerConfig{
		Runner:        srv,
		Store:         store,
		SchedulerID:   "replica-b",
		LeaseDuration: time.Hour,
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewWorkflowScheduler: %v", err)
	}

	// Replica A claims the schedule; its lease blocks replica B.
	claimed, err := store.ClaimDueSchedules(context.Background(), "replica-a", now, time.Hour, 10)
	if err != nil {
		t.Fatalf("ClaimDueSchedules (A): %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("replica A claimed %d schedules, want 1", len(claimed))
	}

	if err := replicaB.RunOnce(context.Background()); err != nil {
		t.Fatalf("replica B RunOnce: %v", err)
	}

	updated, found, err := store.GetSchedule(context.Background(), "scheduler-overlap", "sched-overlap")
	if err != nil || !found {
		t.Fatalf("GetSchedule: %v (found=%v)", err, found)
	}
	if updated.LeaseOwner != "replica-a" {
		t.Fatalf("lease_owner=%q, want replica-a to retain the claim", updated.LeaseOwner)
	}
	if updated.LastStatus == ScheduleRunStatusRunning || updated.LastRunID != "" {
		t.Fatalf("replica B executed a leased schedule: status=%q run_id=%q", updated.LastStatus, updated.LastRunID)
	}

	// After the lease expires, replica B may claim it.
	claimedB, err := store.ClaimDueSchedules(context.Background(), "replica-b", now.Add(2*time.Hour), time.Hour, 10)
	if err != nil {
		t.Fatalf("ClaimDueSchedules (B, after expiry): %v", err)
	}
	if len(claimedB) != 1 || claimedB[0].ID != "sched-overlap" {
		t.Fatalf("replica B claimed %+v after lease expiry, want sched-overlap", claimedB)
	}
}

func createWorkflowForScheduler(t *testing.T, handler http.Handler, workflowID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(validGraphJSON(workflowID)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create workflow status=%d, want %d body=%s", w.Code, http.StatusCreated, w.Body.String())
	}
}

func waitForScheduleStatus(t *testing.T, store WorkflowScheduleStore, workflowID, scheduleID string, timeout time.Duration) WorkflowSchedule {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		schedule, found, err := store.GetSchedule(context.Background(), workflowID, scheduleID)
		if err != nil {
			t.Fatalf("GetSchedule: %v", err)
		}
		if found && (schedule.LastStatus == ScheduleRunStatusCompleted || schedule.LastStatus == ScheduleRunStatusFailed || schedule.LastStatus == ScheduleRunStatusSkippedOverlap || schedule.LastStatus == ScheduleRunStatusDeadLetter) {
			return schedule
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for schedule status for %s", scheduleID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
