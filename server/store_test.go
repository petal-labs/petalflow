package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/loader"
)

// Compile-time interface check.
var _ WorkflowStore = (*MemoryStore)(nil)

func TestMemoryStore_CRUD(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	now := time.Now()
	rec := WorkflowRecord{
		ID:         "wf-1",
		SchemaKind: loader.SchemaKindGraph,
		Name:       "test workflow",
		Source:     json.RawMessage(`{"nodes":[],"edges":[]}`),
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// Create
	if err := s.Create(ctx, rec); err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}

	// Create duplicate
	if err := s.Create(ctx, rec); err != ErrWorkflowExists {
		t.Fatalf("Create duplicate: got %v, want ErrWorkflowExists", err)
	}

	// Get
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

	// Get missing
	_, ok, err = s.Get(ctx, "missing")
	if err != nil {
		t.Fatalf("Get missing: unexpected error: %v", err)
	}
	if ok {
		t.Fatal("Get missing: expected ok=false")
	}

	// List
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: got %d items, want 1", len(list))
	}

	// Update
	rec.Name = "updated"
	rec.UpdatedAt = now.Add(time.Second)
	if err := s.Update(ctx, rec); err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}
	got, _, _ = s.Get(ctx, "wf-1")
	if got.Name != "updated" {
		t.Fatalf("Update: name not updated, got %q", got.Name)
	}

	// Update missing
	missing := WorkflowRecord{ID: "missing"}
	if err := s.Update(ctx, missing); err != ErrWorkflowNotFound {
		t.Fatalf("Update missing: got %v, want ErrWorkflowNotFound", err)
	}

	// Delete
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

	// Delete missing
	if err := s.Delete(ctx, "wf-1"); err != ErrWorkflowNotFound {
		t.Fatalf("Delete missing: got %v, want ErrWorkflowNotFound", err)
	}
}

func TestMemoryStore_ListOrder(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	for _, id := range []string{"c", "a", "b"} {
		_ = s.Create(ctx, WorkflowRecord{
			ID:     id,
			Source: json.RawMessage(`{}`),
		})
	}

	list, _ := s.List(ctx)
	if len(list) != 3 {
		t.Fatalf("got %d items, want 3", len(list))
	}
	// Insertion order: c, a, b
	want := []string{"c", "a", "b"}
	for i, rec := range list {
		if rec.ID != want[i] {
			t.Errorf("list[%d].ID = %q, want %q", i, rec.ID, want[i])
		}
	}
}
