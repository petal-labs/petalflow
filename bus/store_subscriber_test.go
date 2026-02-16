package bus

import (
	"context"
	"log/slog"
	"testing"

	"github.com/petal-labs/petalflow/runtime"
)

func TestStoreSubscriber_PersistsEvents(t *testing.T) {
	store := newTestStore(t)
	sub := NewStoreSubscriber(store, slog.Default())

	for i := 1; i <= 3; i++ {
		e := runtime.NewEvent(runtime.EventNodeStarted, "run-1")
		e.Seq = uint64(i)
		sub.Handle(e)
	}

	events, err := store.List(context.Background(), "run-1", 0, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("got %d events, want 3", len(events))
	}
}

func TestStoreSubscriber_HandleContinuesOnError(t *testing.T) {
	store := newTestStore(t)
	sub := NewStoreSubscriber(store, slog.Default())

	// Handle should not panic
	e := runtime.NewEvent(runtime.EventRunStarted, "run-1")
	e.Seq = 1
	sub.Handle(e)

	events, _ := store.List(context.Background(), "run-1", 0, 0)
	if len(events) != 1 {
		t.Errorf("got %d events, want 1", len(events))
	}
}

func TestStoreSubscriber_NilLogger(t *testing.T) {
	store := newTestStore(t)
	sub := NewStoreSubscriber(store, nil)

	e := runtime.NewEvent(runtime.EventRunStarted, "run-1")
	e.Seq = 1
	sub.Handle(e) // should not panic with nil logger
}
