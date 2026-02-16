package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/loader"
)

var _ WorkflowStore = (*SQLiteStore)(nil)

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
