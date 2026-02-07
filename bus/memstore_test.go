package bus

import (
	"context"
	"testing"

	"github.com/petal-labs/petalflow/runtime"
)

func TestMemEventStore_Append_List(t *testing.T) {
	store := NewMemEventStore()

	for i := 1; i <= 5; i++ {
		e := runtime.NewEvent(runtime.EventNodeStarted, "run-1")
		e.Seq = uint64(i)
		if err := store.Append(context.Background(), e); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}

	events, err := store.List(context.Background(), "run-1", 0, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 5 {
		t.Errorf("got %d events, want 5", len(events))
	}
}

func TestMemEventStore_List_AfterSeq(t *testing.T) {
	store := NewMemEventStore()

	for i := 1; i <= 10; i++ {
		e := runtime.NewEvent(runtime.EventNodeStarted, "run-1")
		e.Seq = uint64(i)
		store.Append(context.Background(), e)
	}

	events, err := store.List(context.Background(), "run-1", 7, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("got %d events, want 3 (seq 8,9,10)", len(events))
	}
	if events[0].Seq != 8 {
		t.Errorf("first event Seq = %d, want 8", events[0].Seq)
	}
}

func TestMemEventStore_List_WithLimit(t *testing.T) {
	store := NewMemEventStore()

	for i := 1; i <= 10; i++ {
		e := runtime.NewEvent(runtime.EventNodeStarted, "run-1")
		e.Seq = uint64(i)
		store.Append(context.Background(), e)
	}

	events, err := store.List(context.Background(), "run-1", 0, 3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("got %d events, want 3", len(events))
	}
}

func TestMemEventStore_LatestSeq(t *testing.T) {
	store := NewMemEventStore()

	seq, err := store.LatestSeq(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("LatestSeq: %v", err)
	}
	if seq != 0 {
		t.Errorf("empty store LatestSeq = %d, want 0", seq)
	}

	for i := 1; i <= 5; i++ {
		e := runtime.NewEvent(runtime.EventNodeStarted, "run-1")
		e.Seq = uint64(i)
		store.Append(context.Background(), e)
	}

	seq, err = store.LatestSeq(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("LatestSeq: %v", err)
	}
	if seq != 5 {
		t.Errorf("LatestSeq = %d, want 5", seq)
	}
}

func TestMemEventStore_RunIsolation(t *testing.T) {
	store := NewMemEventStore()

	e1 := runtime.NewEvent(runtime.EventRunStarted, "run-1")
	e1.Seq = 1
	store.Append(context.Background(), e1)

	e2 := runtime.NewEvent(runtime.EventRunStarted, "run-2")
	e2.Seq = 1
	store.Append(context.Background(), e2)

	events, _ := store.List(context.Background(), "run-1", 0, 0)
	if len(events) != 1 {
		t.Errorf("run-1 events = %d, want 1", len(events))
	}
}
