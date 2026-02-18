package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/loader"
)

var _ WorkflowStore = (*SQLiteStore)(nil)
var _ WorkflowScheduleStore = (*SQLiteStore)(nil)

func newSQLiteWorkflowStore(t *testing.T) *SQLiteStore {
	t.Helper()

	path := filepath.Join(t.TempDir(), "workflows.db")
	store, err := NewSQLiteStore(SQLiteStoreConfig{DSN: path})
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestSQLiteStore_CRUD(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteWorkflowStore(t)

	now := time.Now().UTC().Round(0)
	rec := WorkflowRecord{
		ID:         "wf-1",
		SchemaKind: loader.SchemaKindGraph,
		Name:       "test workflow",
		Source:     json.RawMessage(`{"nodes":[],"edges":[]}`),
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.Create(ctx, rec); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	if err := s.Create(ctx, rec); err != ErrWorkflowExists {
		t.Fatalf("Create duplicate: got %v, want ErrWorkflowExists", err)
	}

	got, ok, err := s.Get(ctx, "wf-1")
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("Get: expected ok=true")
	}
	if got.ID != "wf-1" || got.Name != "test workflow" {
		t.Fatalf("Get: got %+v", got)
	}

	_, ok, err = s.Get(ctx, "missing")
	if err != nil {
		t.Fatalf("Get missing: unexpected error: %v", err)
	}
	if ok {
		t.Fatal("Get missing: expected ok=false")
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: got %d items, want 1", len(list))
	}

	rec.Name = "updated"
	rec.UpdatedAt = now.Add(time.Second)
	if err := s.Update(ctx, rec); err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}
	got, _, _ = s.Get(ctx, "wf-1")
	if got.Name != "updated" {
		t.Fatalf("Update: name not updated, got %q", got.Name)
	}

	missing := WorkflowRecord{ID: "missing"}
	if err := s.Update(ctx, missing); err != ErrWorkflowNotFound {
		t.Fatalf("Update missing: got %v, want ErrWorkflowNotFound", err)
	}

	if err := s.Delete(ctx, "wf-1"); err != nil {
		t.Fatalf("Delete: unexpected error: %v", err)
	}
	_, ok, _ = s.Get(ctx, "wf-1")
	if ok {
		t.Fatal("Delete: record still exists")
	}
	list, _ = s.List(ctx)
	if len(list) != 0 {
		t.Fatalf("Delete: list still has %d items", len(list))
	}

	if err := s.Delete(ctx, "wf-1"); err != ErrWorkflowNotFound {
		t.Fatalf("Delete missing: got %v, want ErrWorkflowNotFound", err)
	}
}

func TestSQLiteStore_ListOrder(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteWorkflowStore(t)

	for _, id := range []string{"c", "a", "b"} {
		if err := s.Create(ctx, WorkflowRecord{
			ID:         id,
			SchemaKind: loader.SchemaKindGraph,
			Source:     json.RawMessage(`{}`),
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Create(%s): %v", id, err)
		}
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("got %d items, want 3", len(list))
	}

	want := []string{"c", "a", "b"}
	for i, rec := range list {
		if rec.ID != want[i] {
			t.Errorf("list[%d].ID = %q, want %q", i, rec.ID, want[i])
		}
	}
}

func TestSQLiteStore_PersistenceAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workflows.db")

	store1, err := NewSQLiteStore(SQLiteStoreConfig{DSN: path})
	if err != nil {
		t.Fatalf("NewSQLiteStore(store1): %v", err)
	}

	rec := WorkflowRecord{
		ID:         "wf-persist",
		SchemaKind: loader.SchemaKindGraph,
		Source:     json.RawMessage(`{"nodes":[{"id":"n1"}]}`),
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store1.Create(ctx, rec); err != nil {
		t.Fatalf("store1.Create: %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("store1.Close: %v", err)
	}

	store2, err := NewSQLiteStore(SQLiteStoreConfig{DSN: path})
	if err != nil {
		t.Fatalf("NewSQLiteStore(store2): %v", err)
	}
	t.Cleanup(func() {
		_ = store2.Close()
	})

	got, ok, err := store2.Get(ctx, "wf-persist")
	if err != nil {
		t.Fatalf("store2.Get: %v", err)
	}
	if !ok {
		t.Fatal("store2.Get: expected persisted record")
	}
	if got.ID != "wf-persist" {
		t.Fatalf("got ID = %q, want %q", got.ID, "wf-persist")
	}
}

func TestSQLiteStore_MigratesLegacyWorkflowsSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workflows.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	legacySchema := `
CREATE TABLE IF NOT EXISTS workflows (
	id TEXT PRIMARY KEY,
	name TEXT,
	source BLOB NOT NULL,
	compiled BLOB,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("create legacy workflows schema error = %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO workflows (id, name, source, compiled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"wf-legacy",
		"legacy workflow",
		[]byte(`{"nodes":[],"edges":[]}`),
		nil,
		"2026-02-17T00:00:00Z",
		"2026-02-17T00:00:00Z",
	); err != nil {
		t.Fatalf("insert legacy workflow row error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	store, err := NewSQLiteStore(SQLiteStoreConfig{DSN: path})
	if err != nil {
		t.Fatalf("NewSQLiteStore() migration error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	legacy, ok, err := store.Get(ctx, "wf-legacy")
	if err != nil {
		t.Fatalf("Get(wf-legacy) error = %v", err)
	}
	if !ok {
		t.Fatal("Get(wf-legacy) ok = false, want true")
	}
	if legacy.SchemaKind != loader.SchemaKindGraph {
		t.Fatalf("legacy schema_kind = %q, want %q", legacy.SchemaKind, loader.SchemaKindGraph)
	}

	if err := store.Create(ctx, WorkflowRecord{
		ID:         "wf-new",
		SchemaKind: loader.SchemaKindGraph,
		Name:       "new workflow",
		Source:     json.RawMessage(`{"nodes":[],"edges":[]}`),
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Create(wf-new) after migration error = %v", err)
	}

	var (
		schemaKind string
		seq        sql.NullInt64
	)
	if err := store.db.QueryRow(`SELECT schema_kind, seq FROM workflows WHERE id = ?`, "wf-new").Scan(&schemaKind, &seq); err != nil {
		t.Fatalf("query migrated workflow row error = %v", err)
	}
	if schemaKind != string(loader.SchemaKindGraph) {
		t.Fatalf("wf-new schema_kind = %q, want %q", schemaKind, loader.SchemaKindGraph)
	}
	if !seq.Valid || seq.Int64 <= 0 {
		t.Fatalf("wf-new seq = %+v, want positive non-null value", seq)
	}
}

func TestSQLiteStore_MigratesLegacyWorkflowsKindColumn(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workflows.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	legacySchema := `
CREATE TABLE IF NOT EXISTS workflows (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	name TEXT,
	source BLOB NOT NULL,
	compiled BLOB,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("create legacy workflows(kind) schema error = %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO workflows (id, kind, name, source, compiled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"wf-legacy-kind",
		string(loader.SchemaKindGraph),
		"legacy workflow",
		[]byte(`{"nodes":[],"edges":[]}`),
		nil,
		"2026-02-17T00:00:00Z",
		"2026-02-17T00:00:00Z",
	); err != nil {
		t.Fatalf("insert legacy workflow(kind) row error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	store, err := NewSQLiteStore(SQLiteStoreConfig{DSN: path})
	if err != nil {
		t.Fatalf("NewSQLiteStore() migration error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	legacy, ok, err := store.Get(ctx, "wf-legacy-kind")
	if err != nil {
		t.Fatalf("Get(wf-legacy-kind) error = %v", err)
	}
	if !ok {
		t.Fatal("Get(wf-legacy-kind) ok = false, want true")
	}
	if legacy.SchemaKind != loader.SchemaKindGraph {
		t.Fatalf("legacy schema_kind = %q, want %q", legacy.SchemaKind, loader.SchemaKindGraph)
	}

	newRec := WorkflowRecord{
		ID:         "wf-new-kind",
		SchemaKind: loader.SchemaKindAgent,
		Name:       "new workflow",
		Source:     json.RawMessage(`{"nodes":[],"edges":[]}`),
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.Create(ctx, newRec); err != nil {
		t.Fatalf("Create(wf-new-kind) after migration error = %v", err)
	}

	var kind string
	if err := store.db.QueryRow(`SELECT kind FROM workflows WHERE id = ?`, "wf-new-kind").Scan(&kind); err != nil {
		t.Fatalf("query kind for wf-new-kind error = %v", err)
	}
	if kind != string(loader.SchemaKindAgent) {
		t.Fatalf("wf-new-kind kind = %q, want %q", kind, loader.SchemaKindAgent)
	}

	newRec.SchemaKind = loader.SchemaKindGraph
	newRec.UpdatedAt = time.Now().UTC()
	if err := store.Update(ctx, newRec); err != nil {
		t.Fatalf("Update(wf-new-kind) after migration error = %v", err)
	}

	var schemaKind string
	if err := store.db.QueryRow(`SELECT kind, schema_kind FROM workflows WHERE id = ?`, "wf-new-kind").Scan(&kind, &schemaKind); err != nil {
		t.Fatalf("query kind/schema_kind for wf-new-kind error = %v", err)
	}
	if kind != string(loader.SchemaKindGraph) {
		t.Fatalf("wf-new-kind kind after update = %q, want %q", kind, loader.SchemaKindGraph)
	}
	if schemaKind != string(loader.SchemaKindGraph) {
		t.Fatalf("wf-new-kind schema_kind after update = %q, want %q", schemaKind, loader.SchemaKindGraph)
	}
}

func TestSQLiteStore_MigratesLegacyWorkflowsSourceJSONColumn(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workflows.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	legacySchema := `
CREATE TABLE IF NOT EXISTS workflows (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	name TEXT,
	source_json BLOB NOT NULL,
	compiled_json BLOB,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("create legacy workflows(source_json) schema error = %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO workflows (id, kind, name, source_json, compiled_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"wf-legacy-sourcejson",
		string(loader.SchemaKindGraph),
		"legacy workflow",
		[]byte(`{"nodes":[],"edges":[]}`),
		nil,
		"2026-02-17T00:00:00Z",
		"2026-02-17T00:00:00Z",
	); err != nil {
		t.Fatalf("insert legacy workflow(source_json) row error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	store, err := NewSQLiteStore(SQLiteStoreConfig{DSN: path})
	if err != nil {
		t.Fatalf("NewSQLiteStore() migration error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	legacy, ok, err := store.Get(ctx, "wf-legacy-sourcejson")
	if err != nil {
		t.Fatalf("Get(wf-legacy-sourcejson) error = %v", err)
	}
	if !ok {
		t.Fatal("Get(wf-legacy-sourcejson) ok = false, want true")
	}
	if legacy.SchemaKind != loader.SchemaKindGraph {
		t.Fatalf("legacy schema_kind = %q, want %q", legacy.SchemaKind, loader.SchemaKindGraph)
	}

	if err := store.Create(ctx, WorkflowRecord{
		ID:         "wf-new-sourcejson",
		SchemaKind: loader.SchemaKindGraph,
		Name:       "new workflow",
		Source:     json.RawMessage(`{"nodes":[],"edges":[]}`),
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Create(wf-new-sourcejson) after migration error = %v", err)
	}

	var sourceJSON []byte
	if err := store.db.QueryRow(`SELECT source_json FROM workflows WHERE id = ?`, "wf-new-sourcejson").Scan(&sourceJSON); err != nil {
		t.Fatalf("query source_json for wf-new-sourcejson error = %v", err)
	}
	if len(sourceJSON) == 0 {
		t.Fatal("wf-new-sourcejson source_json should be populated")
	}
}

func TestSQLiteStore_ScheduleCRUD(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteWorkflowStore(t)
	mustCreateWorkflowForSchedule(t, store, "wf-schedule")

	nextRun := time.Now().UTC().Add(5 * time.Minute).Round(0)
	schedule := WorkflowSchedule{
		ID:         "schedule-1",
		WorkflowID: "wf-schedule",
		Cron:       "*/5 * * * *",
		Enabled:    true,
		Input: map[string]any{
			"topic": "cron",
		},
		Options: RunReqOptions{
			Timeout: "30s",
			Human: &RunReqHumanOptions{
				Mode: "strict",
			},
		},
		NextRunAt: nextRun,
		CreatedAt: time.Now().UTC().Round(0),
		UpdatedAt: time.Now().UTC().Round(0),
	}

	if err := store.CreateSchedule(ctx, schedule); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if err := store.CreateSchedule(ctx, schedule); err != ErrWorkflowScheduleExists {
		t.Fatalf("CreateSchedule duplicate: got %v, want ErrWorkflowScheduleExists", err)
	}

	got, found, err := store.GetSchedule(ctx, "wf-schedule", "schedule-1")
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if !found {
		t.Fatal("GetSchedule: expected found=true")
	}
	if got.Cron != "*/5 * * * *" {
		t.Fatalf("GetSchedule cron=%q, want %q", got.Cron, "*/5 * * * *")
	}
	if got.NextRunAt.Format(time.RFC3339Nano) != nextRun.Format(time.RFC3339Nano) {
		t.Fatalf("GetSchedule next_run_at=%s, want %s", got.NextRunAt, nextRun)
	}
	if got.Options.Timeout != "30s" {
		t.Fatalf("GetSchedule options.timeout=%q, want %q", got.Options.Timeout, "30s")
	}

	list, err := store.ListSchedules(ctx, "wf-schedule")
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListSchedules count=%d, want 1", len(list))
	}

	updateRun := time.Now().UTC().Round(0)
	got.Enabled = false
	got.Cron = "0 * * * *"
	got.LastStatus = ScheduleRunStatusCompleted
	got.LastRunID = "run-123"
	got.LastError = ""
	got.LastRunAt = &updateRun
	got.NextRunAt = updateRun.Add(time.Hour)
	got.UpdatedAt = time.Now().UTC().Round(0)
	if err := store.UpdateSchedule(ctx, got); err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}

	updated, found, err := store.GetSchedule(ctx, "wf-schedule", "schedule-1")
	if err != nil {
		t.Fatalf("GetSchedule updated: %v", err)
	}
	if !found {
		t.Fatal("GetSchedule updated: expected found=true")
	}
	if updated.Enabled {
		t.Fatal("updated.Enabled=true, want false")
	}
	if updated.LastStatus != ScheduleRunStatusCompleted {
		t.Fatalf("updated.LastStatus=%q, want %q", updated.LastStatus, ScheduleRunStatusCompleted)
	}
	if updated.LastRunID != "run-123" {
		t.Fatalf("updated.LastRunID=%q, want %q", updated.LastRunID, "run-123")
	}

	if err := store.DeleteSchedule(ctx, "wf-schedule", "schedule-1"); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	if err := store.DeleteSchedule(ctx, "wf-schedule", "schedule-1"); err != ErrWorkflowScheduleNotFound {
		t.Fatalf("DeleteSchedule missing: got %v, want ErrWorkflowScheduleNotFound", err)
	}
}

func TestSQLiteStore_ListDueSchedules(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteWorkflowStore(t)
	mustCreateWorkflowForSchedule(t, store, "wf-a")
	mustCreateWorkflowForSchedule(t, store, "wf-b")

	now := time.Now().UTC().Round(0)
	s1 := WorkflowSchedule{
		ID:         "due-1",
		WorkflowID: "wf-a",
		Cron:       "* * * * *",
		Enabled:    true,
		NextRunAt:  now.Add(-2 * time.Minute),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	s2 := WorkflowSchedule{
		ID:         "due-2",
		WorkflowID: "wf-b",
		Cron:       "* * * * *",
		Enabled:    true,
		NextRunAt:  now.Add(-1 * time.Minute),
		CreatedAt:  now.Add(time.Second),
		UpdatedAt:  now.Add(time.Second),
	}
	s3 := WorkflowSchedule{
		ID:         "future",
		WorkflowID: "wf-a",
		Cron:       "* * * * *",
		Enabled:    true,
		NextRunAt:  now.Add(2 * time.Minute),
		CreatedAt:  now.Add(2 * time.Second),
		UpdatedAt:  now.Add(2 * time.Second),
	}

	for _, schedule := range []WorkflowSchedule{s1, s2, s3} {
		if err := store.CreateSchedule(ctx, schedule); err != nil {
			t.Fatalf("CreateSchedule(%s): %v", schedule.ID, err)
		}
	}

	due, err := store.ListDueSchedules(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListDueSchedules: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("ListDueSchedules count=%d, want 2", len(due))
	}
	if due[0].ID != "due-1" || due[1].ID != "due-2" {
		t.Fatalf("ListDueSchedules order=%v, want [due-1 due-2]", []string{due[0].ID, due[1].ID})
	}
}

func TestSQLiteStore_DeleteWorkflowCascadesSchedules(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteWorkflowStore(t)
	mustCreateWorkflowForSchedule(t, store, "wf-cascade")

	schedule := WorkflowSchedule{
		ID:         "cascade-1",
		WorkflowID: "wf-cascade",
		Cron:       "* * * * *",
		Enabled:    true,
		NextRunAt:  time.Now().UTC().Add(time.Minute).Round(0),
		CreatedAt:  time.Now().UTC().Round(0),
		UpdatedAt:  time.Now().UTC().Round(0),
	}
	if err := store.CreateSchedule(ctx, schedule); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	if err := store.Delete(ctx, "wf-cascade"); err != nil {
		t.Fatalf("Delete workflow: %v", err)
	}

	_, found, err := store.GetSchedule(ctx, "wf-cascade", "cascade-1")
	if err != nil {
		t.Fatalf("GetSchedule after workflow delete: %v", err)
	}
	if found {
		t.Fatal("GetSchedule found=true after workflow delete, want false")
	}
}

func mustCreateWorkflowForSchedule(t *testing.T, store *SQLiteStore, workflowID string) {
	t.Helper()

	err := store.Create(context.Background(), WorkflowRecord{
		ID:         workflowID,
		SchemaKind: loader.SchemaKindGraph,
		Source:     json.RawMessage(`{"id":"` + workflowID + `","version":"1.0","nodes":[{"id":"n1","type":"func"}],"edges":[],"entry":"n1"}`),
		CreatedAt:  time.Now().UTC().Round(0),
		UpdatedAt:  time.Now().UTC().Round(0),
	})
	if err != nil {
		t.Fatalf("Create workflow %s: %v", workflowID, err)
	}
}
