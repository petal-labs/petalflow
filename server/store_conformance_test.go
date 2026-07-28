package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/loader"
)

// runWorkflowStoreConformance asserts the WorkflowStore contract. All backends run it.
func runWorkflowStoreConformance(t *testing.T, newStore func(t *testing.T) WorkflowStore) {
	t.Helper()

	t.Run("create_get", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		rec := WorkflowRecord{
			ID:         "wf-create-get",
			SchemaKind: loader.SchemaKindGraph,
			Name:       "Create Get",
			Source:     json.RawMessage(`{"nodes":[]}`),
		}
		if err := store.Create(ctx, rec); err != nil {
			t.Fatalf("Create: %v", err)
		}

		got, ok, err := store.Get(ctx, rec.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !ok {
			t.Fatalf("Get: expected record to exist")
		}
		if got.ID != rec.ID || got.Name != rec.Name || string(got.SchemaKind) != string(rec.SchemaKind) {
			t.Fatalf("Get returned %+v, want id/name/kind matching %+v", got, rec)
		}
		if string(got.Source) != string(rec.Source) {
			t.Fatalf("Get Source = %s, want %s", got.Source, rec.Source)
		}
	})

	t.Run("create_duplicate_returns_exists", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		rec := WorkflowRecord{
			ID:         "wf-dup",
			SchemaKind: loader.SchemaKindGraph,
			Source:     json.RawMessage(`{}`),
		}
		if err := store.Create(ctx, rec); err != nil {
			t.Fatalf("Create: %v", err)
		}
		err := store.Create(ctx, rec)
		if !errors.Is(err, ErrWorkflowExists) {
			t.Fatalf("Create duplicate error = %v, want ErrWorkflowExists", err)
		}
	})

	t.Run("update_missing_returns_not_found", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		rec := WorkflowRecord{
			ID:         "wf-missing-update",
			SchemaKind: loader.SchemaKindGraph,
			Source:     json.RawMessage(`{}`),
		}
		err := store.Update(ctx, rec)
		if !errors.Is(err, ErrWorkflowNotFound) {
			t.Fatalf("Update missing error = %v, want ErrWorkflowNotFound", err)
		}
	})

	t.Run("list_returns_created_records", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		ids := []string{"wf-list-a", "wf-list-b", "wf-list-c"}
		for _, id := range ids {
			rec := WorkflowRecord{
				ID:         id,
				SchemaKind: loader.SchemaKindGraph,
				Source:     json.RawMessage(`{}`),
			}
			if err := store.Create(ctx, rec); err != nil {
				t.Fatalf("Create(%s): %v", id, err)
			}
		}

		list, err := store.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		found := make(map[string]bool)
		for _, rec := range list {
			found[rec.ID] = true
		}
		for _, id := range ids {
			if !found[id] {
				t.Fatalf("List missing record %s, got %+v", id, list)
			}
		}
	})

	t.Run("delete_missing_returns_not_found", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		err := store.Delete(ctx, "wf-does-not-exist")
		if !errors.Is(err, ErrWorkflowNotFound) {
			t.Fatalf("Delete missing error = %v, want ErrWorkflowNotFound", err)
		}
	})

	t.Run("delete_existing_then_get_not_found", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		rec := WorkflowRecord{
			ID:         "wf-delete-me",
			SchemaKind: loader.SchemaKindGraph,
			Source:     json.RawMessage(`{}`),
		}
		if err := store.Create(ctx, rec); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := store.Delete(ctx, rec.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, ok, err := store.Get(ctx, rec.ID)
		if err != nil {
			t.Fatalf("Get after delete: %v", err)
		}
		if ok {
			t.Fatalf("Get after delete: expected record to be gone")
		}
	})
}

// runScheduleStoreConformance asserts the WorkflowScheduleStore contract. All backends run it.
func runScheduleStoreConformance(t *testing.T, newStore func(t *testing.T) WorkflowScheduleStore) {
	t.Helper()

	// createWorkflowFor is used when the schedule store needs a parent workflow
	// row to satisfy a foreign key (e.g. the Postgres backend).
	createWorkflowFor := func(t *testing.T, store WorkflowScheduleStore, workflowID string) {
		t.Helper()
		if ws, ok := store.(WorkflowStore); ok {
			rec := WorkflowRecord{
				ID:         workflowID,
				SchemaKind: loader.SchemaKindGraph,
				Source:     json.RawMessage(`{}`),
			}
			if err := ws.Create(context.Background(), rec); err != nil && !errors.Is(err, ErrWorkflowExists) {
				t.Fatalf("Create parent workflow %s: %v", workflowID, err)
			}
		}
	}

	t.Run("create_get", func(t *testing.T) {
		store := newStore(t)
		createWorkflowFor(t, store, "wf-sched-1")
		ctx := context.Background()

		sched := WorkflowSchedule{
			ID:         "sched-create-get",
			WorkflowID: "wf-sched-1",
			Cron:       "* * * * *",
			Enabled:    true,
			Input:      map[string]any{"foo": "bar"},
			NextRunAt:  time.Now().UTC().Add(time.Hour),
		}
		if err := store.CreateSchedule(ctx, sched); err != nil {
			t.Fatalf("CreateSchedule: %v", err)
		}

		got, ok, err := store.GetSchedule(ctx, sched.WorkflowID, sched.ID)
		if err != nil {
			t.Fatalf("GetSchedule: %v", err)
		}
		if !ok {
			t.Fatalf("GetSchedule: expected schedule to exist")
		}
		if got.ID != sched.ID || got.Cron != sched.Cron || got.Enabled != sched.Enabled {
			t.Fatalf("GetSchedule returned %+v, want matching %+v", got, sched)
		}
		if got.Input["foo"] != "bar" {
			t.Fatalf("GetSchedule Input = %+v, want foo=bar", got.Input)
		}
	})

	t.Run("create_duplicate_returns_exists", func(t *testing.T) {
		store := newStore(t)
		createWorkflowFor(t, store, "wf-sched-dup")
		ctx := context.Background()

		sched := WorkflowSchedule{
			ID:         "sched-dup",
			WorkflowID: "wf-sched-dup",
			Cron:       "* * * * *",
			Enabled:    true,
			NextRunAt:  time.Now().UTC().Add(time.Hour),
		}
		if err := store.CreateSchedule(ctx, sched); err != nil {
			t.Fatalf("CreateSchedule: %v", err)
		}
		err := store.CreateSchedule(ctx, sched)
		if !errors.Is(err, ErrWorkflowScheduleExists) {
			t.Fatalf("CreateSchedule duplicate error = %v, want ErrWorkflowScheduleExists", err)
		}
	})

	t.Run("update_missing_returns_not_found", func(t *testing.T) {
		store := newStore(t)
		createWorkflowFor(t, store, "wf-sched-missing")
		ctx := context.Background()

		sched := WorkflowSchedule{
			ID:         "sched-missing",
			WorkflowID: "wf-sched-missing",
			Cron:       "* * * * *",
			Enabled:    true,
			NextRunAt:  time.Now().UTC().Add(time.Hour),
		}
		err := store.UpdateSchedule(ctx, sched)
		if !errors.Is(err, ErrWorkflowScheduleNotFound) {
			t.Fatalf("UpdateSchedule missing error = %v, want ErrWorkflowScheduleNotFound", err)
		}
	})

	t.Run("list_schedules_by_workflow", func(t *testing.T) {
		store := newStore(t)
		createWorkflowFor(t, store, "wf-sched-list")
		ctx := context.Background()

		ids := []string{"sched-list-a", "sched-list-b"}
		for _, id := range ids {
			sched := WorkflowSchedule{
				ID:         id,
				WorkflowID: "wf-sched-list",
				Cron:       "* * * * *",
				Enabled:    true,
				NextRunAt:  time.Now().UTC().Add(time.Hour),
			}
			if err := store.CreateSchedule(ctx, sched); err != nil {
				t.Fatalf("CreateSchedule(%s): %v", id, err)
			}
		}

		list, err := store.ListSchedules(ctx, "wf-sched-list")
		if err != nil {
			t.Fatalf("ListSchedules: %v", err)
		}
		if len(list) != len(ids) {
			t.Fatalf("ListSchedules returned %d schedules, want %d", len(list), len(ids))
		}
	})

	t.Run("list_due_schedules_only_enabled_and_due", func(t *testing.T) {
		store := newStore(t)
		createWorkflowFor(t, store, "wf-sched-due")
		ctx := context.Background()

		now := time.Now().UTC()

		due := WorkflowSchedule{
			ID:         "sched-due",
			WorkflowID: "wf-sched-due",
			Cron:       "* * * * *",
			Enabled:    true,
			NextRunAt:  now.Add(-time.Minute),
		}
		notDueYet := WorkflowSchedule{
			ID:         "sched-not-due",
			WorkflowID: "wf-sched-due",
			Cron:       "* * * * *",
			Enabled:    true,
			NextRunAt:  now.Add(time.Hour),
		}
		disabledDue := WorkflowSchedule{
			ID:         "sched-disabled-due",
			WorkflowID: "wf-sched-due",
			Cron:       "* * * * *",
			Enabled:    false,
			NextRunAt:  now.Add(-time.Minute),
		}
		for _, sched := range []WorkflowSchedule{due, notDueYet, disabledDue} {
			if err := store.CreateSchedule(ctx, sched); err != nil {
				t.Fatalf("CreateSchedule(%s): %v", sched.ID, err)
			}
		}

		results, err := store.ListDueSchedules(ctx, now, 0)
		if err != nil {
			t.Fatalf("ListDueSchedules: %v", err)
		}
		if len(results) != 1 || results[0].ID != due.ID {
			t.Fatalf("ListDueSchedules = %+v, want only %s", results, due.ID)
		}
	})

	t.Run("delete_schedules_by_workflow", func(t *testing.T) {
		store := newStore(t)
		createWorkflowFor(t, store, "wf-sched-delete-all")
		ctx := context.Background()

		ids := []string{"sched-del-a", "sched-del-b"}
		for _, id := range ids {
			sched := WorkflowSchedule{
				ID:         id,
				WorkflowID: "wf-sched-delete-all",
				Cron:       "* * * * *",
				Enabled:    true,
				NextRunAt:  time.Now().UTC().Add(time.Hour),
			}
			if err := store.CreateSchedule(ctx, sched); err != nil {
				t.Fatalf("CreateSchedule(%s): %v", id, err)
			}
		}

		if err := store.DeleteSchedulesByWorkflow(ctx, "wf-sched-delete-all"); err != nil {
			t.Fatalf("DeleteSchedulesByWorkflow: %v", err)
		}

		list, err := store.ListSchedules(ctx, "wf-sched-delete-all")
		if err != nil {
			t.Fatalf("ListSchedules after delete-all: %v", err)
		}
		if len(list) != 0 {
			t.Fatalf("ListSchedules after delete-all = %+v, want empty", list)
		}
	})

	t.Run("delete_schedule_missing_returns_not_found", func(t *testing.T) {
		store := newStore(t)
		createWorkflowFor(t, store, "wf-sched-delete-missing")
		ctx := context.Background()

		err := store.DeleteSchedule(ctx, "wf-sched-delete-missing", "sched-does-not-exist")
		if !errors.Is(err, ErrWorkflowScheduleNotFound) {
			t.Fatalf("DeleteSchedule missing error = %v, want ErrWorkflowScheduleNotFound", err)
		}
	})
}

func TestSQLiteWorkflowStoreConformance(t *testing.T) {
	runWorkflowStoreConformance(t, func(t *testing.T) WorkflowStore {
		store, err := NewSQLiteStore(SQLiteStoreConfig{DSN: filepath.Join(t.TempDir(), "wf.db")})
		if err != nil {
			t.Fatalf("NewSQLiteStore: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	})
}

func TestSQLiteScheduleStoreConformance(t *testing.T) {
	runScheduleStoreConformance(t, func(t *testing.T) WorkflowScheduleStore {
		store, err := NewSQLiteStore(SQLiteStoreConfig{DSN: filepath.Join(t.TempDir(), "wf.db")})
		if err != nil {
			t.Fatalf("NewSQLiteStore: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	})
}
