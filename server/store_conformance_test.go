package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/loader"
	"github.com/petal-labs/petalflow/tool"
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

	t.Run("claim_due_schedules_is_atomic", func(t *testing.T) {
		store := newStore(t)
		createWorkflowFor(t, store, "wf-sched-claim")
		ctx := context.Background()

		now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		for _, id := range []string{"sched-c1", "sched-c2", "sched-c3"} {
			if err := store.CreateSchedule(ctx, WorkflowSchedule{
				ID: id, WorkflowID: "wf-sched-claim", Cron: "* * * * *", Enabled: true,
				NextRunAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
			}); err != nil {
				t.Fatalf("CreateSchedule %s: %v", id, err)
			}
		}

		claimedA, err := store.ClaimDueSchedules(ctx, "replica-a", now, time.Hour, 10)
		if err != nil {
			t.Fatalf("ClaimDueSchedules (A): %v", err)
		}
		claimedB, err := store.ClaimDueSchedules(ctx, "replica-b", now, time.Hour, 10)
		if err != nil {
			t.Fatalf("ClaimDueSchedules (B): %v", err)
		}
		if len(claimedA)+len(claimedB) != 3 {
			t.Fatalf("claimed %d+%d schedules, want 3 total", len(claimedA), len(claimedB))
		}
		seen := map[string]string{}
		for _, s := range claimedA {
			seen[s.ID] = "replica-a"
		}
		for _, s := range claimedB {
			if _, dup := seen[s.ID]; dup {
				t.Fatalf("schedule %s claimed by both replicas", s.ID)
			}
			seen[s.ID] = "replica-b"
		}

		// A lease blocks claims until it expires.
		none, err := store.ClaimDueSchedules(ctx, "replica-c", now.Add(time.Minute), time.Hour, 10)
		if err != nil {
			t.Fatalf("ClaimDueSchedules while leased: %v", err)
		}
		if len(none) != 0 {
			t.Fatalf("claimed leased schedules: %+v", none)
		}
		reclaimed, err := store.ClaimDueSchedules(ctx, "replica-c", now.Add(2*time.Hour), time.Hour, 10)
		if err != nil {
			t.Fatalf("ClaimDueSchedules after expiry: %v", err)
		}
		if len(reclaimed) != 3 {
			t.Fatalf("reclaimed %d schedules after lease expiry, want 3", len(reclaimed))
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

// policyStoreBackends is the combined store capability each backend must
// provide for the policy conformance suite.
type policyStoreBackends interface {
	WorkflowStore
	TenantPolicyStore
}

// runPolicyStoreConformance asserts the tool policy persistence contract.
// All backends run it.
func runPolicyStoreConformance(t *testing.T, newStore func(t *testing.T) policyStoreBackends) {
	t.Helper()
	ctx := context.Background()

	t.Run("workflow_policy_round_trip", func(t *testing.T) {
		store := newStore(t)
		rec := WorkflowRecord{
			ID:         "wf-policy-1",
			SchemaKind: loader.SchemaKindGraph,
			Source:     json.RawMessage(`{}`),
			Policy: &tool.ToolPolicy{
				Version:         2,
				Allow:           []string{"mcp/*", "email-send"},
				Deny:            []string{"fs/write"},
				RequireApproval: []string{"email-send"},
			},
		}
		if err := store.Create(ctx, rec); err != nil {
			t.Fatalf("Create: %v", err)
		}

		got, ok, err := store.Get(ctx, rec.ID)
		if err != nil || !ok {
			t.Fatalf("Get: ok=%v err=%v", ok, err)
		}
		if got.Policy == nil {
			t.Fatalf("Get returned nil policy")
		}
		if !reflect.DeepEqual(*got.Policy, *rec.Policy) {
			t.Fatalf("policy round trip = %+v, want %+v", *got.Policy, *rec.Policy)
		}
	})

	t.Run("workflow_policy_nil_stays_nil", func(t *testing.T) {
		store := newStore(t)
		rec := WorkflowRecord{
			ID:         "wf-policy-nil",
			SchemaKind: loader.SchemaKindGraph,
			Source:     json.RawMessage(`{}`),
		}
		if err := store.Create(ctx, rec); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, ok, err := store.Get(ctx, rec.ID)
		if err != nil || !ok {
			t.Fatalf("Get: ok=%v err=%v", ok, err)
		}
		if got.Policy != nil {
			t.Fatalf("Get returned unexpected policy %+v", got.Policy)
		}
	})

	t.Run("workflow_policy_update_replaces_and_clears", func(t *testing.T) {
		store := newStore(t)
		rec := WorkflowRecord{
			ID:         "wf-policy-update",
			SchemaKind: loader.SchemaKindGraph,
			Source:     json.RawMessage(`{}`),
			Policy:     &tool.ToolPolicy{Allow: []string{"a"}},
		}
		if err := store.Create(ctx, rec); err != nil {
			t.Fatalf("Create: %v", err)
		}

		rec.Policy = &tool.ToolPolicy{Allow: []string{"b"}, Deny: []string{"c"}}
		if err := store.Update(ctx, rec); err != nil {
			t.Fatalf("Update with policy: %v", err)
		}
		got, ok, err := store.Get(ctx, rec.ID)
		if err != nil || !ok {
			t.Fatalf("Get: ok=%v err=%v", ok, err)
		}
		if got.Policy == nil || !reflect.DeepEqual(*got.Policy, *rec.Policy) {
			t.Fatalf("Update did not store replacement policy: %+v", got.Policy)
		}

		rec.Policy = nil
		if err := store.Update(ctx, rec); err != nil {
			t.Fatalf("Update clearing policy: %v", err)
		}
		got, ok, err = store.Get(ctx, rec.ID)
		if err != nil || !ok {
			t.Fatalf("Get: ok=%v err=%v", ok, err)
		}
		if got.Policy != nil {
			t.Fatalf("Update did not clear policy: %+v", got.Policy)
		}
	})

	t.Run("workflow_policy_explicit_empty_allow_survives", func(t *testing.T) {
		store := newStore(t)
		rec := WorkflowRecord{
			ID:         "wf-policy-empty",
			SchemaKind: loader.SchemaKindGraph,
			Source:     json.RawMessage(`{}`),
			Policy:     &tool.ToolPolicy{Allow: []string{}},
		}
		if err := store.Create(ctx, rec); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, ok, err := store.Get(ctx, rec.ID)
		if err != nil || !ok {
			t.Fatalf("Get: ok=%v err=%v", ok, err)
		}
		if got.Policy == nil || got.Policy.Allow == nil {
			t.Fatalf("explicit empty allow did not survive persistence: %+v", got.Policy)
		}
		if effective := tool.ResolveEffectivePolicy(nil, got.Policy); effective.Evaluate("anything") != tool.PolicyDeny {
			t.Fatalf("explicit empty allow must deny everything")
		}
	})

	t.Run("tenant_policy_crud", func(t *testing.T) {
		store := newStore(t)

		if _, ok, err := store.GetTenantPolicy(ctx, "tenant-1"); ok || err != nil {
			t.Fatalf("GetTenantPolicy missing: ok=%v err=%v", ok, err)
		}

		created, err := store.UpsertTenantPolicy(ctx, TenantPolicyRecord{
			TenantID: "tenant-1",
			Policy:   tool.ToolPolicy{Allow: []string{"email-send", "mcp/*"}},
		})
		if err != nil {
			t.Fatalf("UpsertTenantPolicy: %v", err)
		}
		if created.Policy.Version != 1 {
			t.Fatalf("first upsert version = %d, want 1", created.Policy.Version)
		}

		stored, ok, err := store.GetTenantPolicy(ctx, "tenant-1")
		if err != nil || !ok {
			t.Fatalf("GetTenantPolicy: ok=%v err=%v", ok, err)
		}
		if !reflect.DeepEqual(stored.Policy, created.Policy) {
			t.Fatalf("stored = %+v, want %+v", stored.Policy, created.Policy)
		}

		// Re-upserting identical content must not churn the version.
		same, err := store.UpsertTenantPolicy(ctx, TenantPolicyRecord{
			TenantID: "tenant-1",
			Policy:   tool.ToolPolicy{Allow: []string{"mcp/*", "email-send"}},
		})
		if err != nil {
			t.Fatalf("UpsertTenantPolicy identical: %v", err)
		}
		if same.Policy.Version != created.Policy.Version {
			t.Fatalf("identical re-upsert bumped version %d -> %d", created.Policy.Version, same.Policy.Version)
		}

		// Changing content must bump the version.
		changed, err := store.UpsertTenantPolicy(ctx, TenantPolicyRecord{
			TenantID: "tenant-1",
			Policy:   tool.ToolPolicy{Allow: []string{"email-send"}, Deny: []string{"mcp/write"}, Version: 9},
		})
		if err != nil {
			t.Fatalf("UpsertTenantPolicy changed: %v", err)
		}
		if changed.Policy.Version != 2 {
			t.Fatalf("changed upsert version = %d, want 2", changed.Policy.Version)
		}

		if err := store.DeleteTenantPolicy(ctx, "tenant-1"); err != nil {
			t.Fatalf("DeleteTenantPolicy: %v", err)
		}
		if _, ok, err := store.GetTenantPolicy(ctx, "tenant-1"); ok || err != nil {
			t.Fatalf("GetTenantPolicy after delete: ok=%v err=%v", ok, err)
		}
		if err := store.DeleteTenantPolicy(ctx, "tenant-1"); !errors.Is(err, ErrTenantPolicyNotFound) {
			t.Fatalf("DeleteTenantPolicy missing error = %v, want ErrTenantPolicyNotFound", err)
		}
	})

	t.Run("tenant_policy_validation", func(t *testing.T) {
		store := newStore(t)
		_, err := store.UpsertTenantPolicy(ctx, TenantPolicyRecord{
			TenantID: "tenant-bad",
			Policy:   tool.ToolPolicy{Allow: []string{""}},
		})
		if err == nil {
			t.Fatalf("expected validation error for empty rule")
		}
	})
}

func TestSQLitePolicyStoreConformance(t *testing.T) {
	runPolicyStoreConformance(t, func(t *testing.T) policyStoreBackends {
		store, err := NewSQLiteStore(SQLiteStoreConfig{DSN: filepath.Join(t.TempDir(), "wf.db")})
		if err != nil {
			t.Fatalf("NewSQLiteStore: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	})
}
