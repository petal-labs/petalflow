package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/loader"
)

// Compile-time interface check.
var _ WorkflowStore = (*SQLiteWorkflowStore)(nil)

func newSQLiteWorkflowStore(t *testing.T) *SQLiteWorkflowStore {
	t.Helper()
	store, err := NewSQLiteWorkflowStore(filepath.Join(t.TempDir(), "petalflow.db"))
	if err != nil {
		t.Fatalf("NewSQLiteWorkflowStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestSQLiteWorkflowStoreCRUD(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteWorkflowStore(t)

	now := time.Now().UTC().Truncate(time.Nanosecond)
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

func TestSQLiteWorkflowStoreListOrder(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteWorkflowStore(t)

	now := time.Now().UTC()
	for i, id := range []string{"c", "a", "b"} {
		err := s.Create(ctx, WorkflowRecord{
			ID:         id,
			SchemaKind: loader.SchemaKindGraph,
			Source:     json.RawMessage(`{}`),
			CreatedAt:  now.Add(time.Duration(i) * time.Second),
			UpdatedAt:  now.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
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
