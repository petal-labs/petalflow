package bus

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

// testDSN returns a unique shared-memory DSN for test isolation.
func testDSN(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
}

func newTestStore(t *testing.T, cfg ...SQLiteStoreConfig) *SQLiteEventStore {
	t.Helper()
	var c SQLiteStoreConfig
	if len(cfg) > 0 {
		c = cfg[0]
	}
	if c.DSN == "" {
		c.DSN = testDSN(t)
	}
	store, err := NewSQLiteEventStore(c)
	if err != nil {
		t.Fatalf("NewSQLiteEventStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func makeEvent(runID string, seq uint64, kind runtime.EventKind) runtime.Event {
	e := runtime.NewEvent(kind, runID)
	e.Seq = seq
	return e
}

// --- CRUD operations ---

func TestSQLiteEventStore_Append_List(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := uint64(1); i <= 5; i++ {
		e := makeEvent("run-1", i, runtime.EventNodeStarted)
		e.NodeID = fmt.Sprintf("node-%d", i)
		e.NodeKind = core.NodeKindLLM
		e.Attempt = 1
		e.Elapsed = time.Duration(i) * time.Millisecond
		e.TraceID = "trace-abc"
		e.SpanID = "span-def"
		e.Payload = map[string]any{"index": float64(i)}
		if err := store.Append(ctx, e); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}

	events, err := store.List(ctx, "run-1", 0, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("got %d events, want 5", len(events))
	}

	// Verify round-trip fidelity.
	e := events[0]
	if e.RunID != "run-1" {
		t.Errorf("RunID = %q, want %q", e.RunID, "run-1")
	}
	if e.Seq != 1 {
		t.Errorf("Seq = %d, want 1", e.Seq)
	}
	if e.Kind != runtime.EventNodeStarted {
		t.Errorf("Kind = %q, want %q", e.Kind, runtime.EventNodeStarted)
	}
	if e.NodeID != "node-1" {
		t.Errorf("NodeID = %q, want %q", e.NodeID, "node-1")
	}
	if e.NodeKind != core.NodeKindLLM {
		t.Errorf("NodeKind = %q, want %q", e.NodeKind, core.NodeKindLLM)
	}
	if e.Attempt != 1 {
		t.Errorf("Attempt = %d, want 1", e.Attempt)
	}
	if e.Elapsed != time.Millisecond {
		t.Errorf("Elapsed = %v, want %v", e.Elapsed, time.Millisecond)
	}
	if e.TraceID != "trace-abc" {
		t.Errorf("TraceID = %q, want %q", e.TraceID, "trace-abc")
	}
	if e.SpanID != "span-def" {
		t.Errorf("SpanID = %q, want %q", e.SpanID, "span-def")
	}
	if v, ok := e.Payload["index"]; !ok || v != float64(1) {
		t.Errorf("Payload[index] = %v (%T), want 1 (float64)", v, v)
	}
}

func TestSQLiteEventStore_Append_DuplicateSeq(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	e := makeEvent("run-1", 1, runtime.EventNodeStarted)
	if err := store.Append(ctx, e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Second insert with same (run_id, seq) must fail due to UNIQUE constraint.
	err := store.Append(ctx, e)
	if err == nil {
		t.Fatal("expected error on duplicate (run_id, seq), got nil")
	}
}

// --- Replay with afterSeq cursor ---

func TestSQLiteEventStore_List_AfterSeq(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := uint64(1); i <= 10; i++ {
		if err := store.Append(ctx, makeEvent("run-1", i, runtime.EventNodeStarted)); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}

	events, err := store.List(ctx, "run-1", 7, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (seq 8,9,10)", len(events))
	}
	if events[0].Seq != 8 {
		t.Errorf("first event Seq = %d, want 8", events[0].Seq)
	}
	if events[2].Seq != 10 {
		t.Errorf("last event Seq = %d, want 10", events[2].Seq)
	}
}

func TestSQLiteEventStore_List_WithLimit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := uint64(1); i <= 10; i++ {
		store.Append(ctx, makeEvent("run-1", i, runtime.EventNodeStarted))
	}

	events, err := store.List(ctx, "run-1", 0, 3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("got %d events, want 3", len(events))
	}
}

func TestSQLiteEventStore_List_AfterSeqWithLimit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := uint64(1); i <= 10; i++ {
		store.Append(ctx, makeEvent("run-1", i, runtime.EventNodeStarted))
	}

	events, err := store.List(ctx, "run-1", 5, 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Seq != 6 {
		t.Errorf("first event Seq = %d, want 6", events[0].Seq)
	}
	if events[1].Seq != 7 {
		t.Errorf("second event Seq = %d, want 7", events[1].Seq)
	}
}

func TestSQLiteEventStore_List_EmptyStore(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	events, err := store.List(ctx, "run-1", 0, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}

// --- LatestSeq ---

func TestSQLiteEventStore_LatestSeq(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	seq, err := store.LatestSeq(ctx, "run-1")
	if err != nil {
		t.Fatalf("LatestSeq: %v", err)
	}
	if seq != 0 {
		t.Errorf("empty store LatestSeq = %d, want 0", seq)
	}

	for i := uint64(1); i <= 5; i++ {
		store.Append(ctx, makeEvent("run-1", i, runtime.EventNodeStarted))
	}

	seq, err = store.LatestSeq(ctx, "run-1")
	if err != nil {
		t.Fatalf("LatestSeq: %v", err)
	}
	if seq != 5 {
		t.Errorf("LatestSeq = %d, want 5", seq)
	}
}

// --- Run isolation ---

func TestSQLiteEventStore_RunIsolation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.Append(ctx, makeEvent("run-1", 1, runtime.EventRunStarted))
	store.Append(ctx, makeEvent("run-1", 2, runtime.EventRunFinished))
	store.Append(ctx, makeEvent("run-2", 1, runtime.EventRunStarted))

	events, _ := store.List(ctx, "run-1", 0, 0)
	if len(events) != 2 {
		t.Errorf("run-1 events = %d, want 2", len(events))
	}

	events, _ = store.List(ctx, "run-2", 0, 0)
	if len(events) != 1 {
		t.Errorf("run-2 events = %d, want 1", len(events))
	}

	seq, _ := store.LatestSeq(ctx, "run-1")
	if seq != 2 {
		t.Errorf("run-1 LatestSeq = %d, want 2", seq)
	}
	seq, _ = store.LatestSeq(ctx, "run-2")
	if seq != 1 {
		t.Errorf("run-2 LatestSeq = %d, want 1", seq)
	}
}

// --- Retention pruning: age-based ---

func TestSQLiteEventStore_PruneByAge(t *testing.T) {
	dsn := testDSN(t)
	store, err := NewSQLiteEventStore(SQLiteStoreConfig{
		DSN:          dsn,
		RetentionAge: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewSQLiteEventStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Insert an event with a time far in the past.
	old := makeEvent("run-1", 1, runtime.EventNodeStarted)
	old.Time = time.Now().Add(-1 * time.Hour)
	store.Append(ctx, old)

	// Insert a recent event.
	recent := makeEvent("run-1", 2, runtime.EventNodeFinished)
	recent.Time = time.Now()
	store.Append(ctx, recent)

	// Run prune.
	if err := store.Prune(ctx); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	events, _ := store.List(ctx, "run-1", 0, 0)
	if len(events) != 1 {
		t.Fatalf("after prune got %d events, want 1", len(events))
	}
	if events[0].Seq != 2 {
		t.Errorf("remaining event Seq = %d, want 2", events[0].Seq)
	}
}

// --- Retention pruning: count-based ---

func TestSQLiteEventStore_PruneByCount(t *testing.T) {
	dsn := testDSN(t)
	store, err := NewSQLiteEventStore(SQLiteStoreConfig{
		DSN:            dsn,
		RetentionCount: 3,
	})
	if err != nil {
		t.Fatalf("NewSQLiteEventStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	for i := uint64(1); i <= 7; i++ {
		store.Append(ctx, makeEvent("run-1", i, runtime.EventNodeStarted))
	}

	if err := store.Prune(ctx); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	events, _ := store.List(ctx, "run-1", 0, 0)
	if len(events) != 3 {
		t.Fatalf("after prune got %d events, want 3", len(events))
	}
	// The kept events should be the highest seq: 5, 6, 7.
	if events[0].Seq != 5 {
		t.Errorf("first remaining event Seq = %d, want 5", events[0].Seq)
	}
	if events[2].Seq != 7 {
		t.Errorf("last remaining event Seq = %d, want 7", events[2].Seq)
	}
}

func TestSQLiteEventStore_PruneByCount_MultipleRuns(t *testing.T) {
	dsn := testDSN(t)
	store, err := NewSQLiteEventStore(SQLiteStoreConfig{
		DSN:            dsn,
		RetentionCount: 2,
	})
	if err != nil {
		t.Fatalf("NewSQLiteEventStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	for i := uint64(1); i <= 5; i++ {
		store.Append(ctx, makeEvent("run-1", i, runtime.EventNodeStarted))
		store.Append(ctx, makeEvent("run-2", i, runtime.EventNodeStarted))
	}

	if err := store.Prune(ctx); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	events1, _ := store.List(ctx, "run-1", 0, 0)
	events2, _ := store.List(ctx, "run-2", 0, 0)
	if len(events1) != 2 {
		t.Errorf("run-1 after prune got %d events, want 2", len(events1))
	}
	if len(events2) != 2 {
		t.Errorf("run-2 after prune got %d events, want 2", len(events2))
	}
}

// --- WAL concurrent reads ---

func TestSQLiteEventStore_WALConcurrentReads(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Pre-populate data.
	for i := uint64(1); i <= 20; i++ {
		store.Append(ctx, makeEvent("run-1", i, runtime.EventNodeStarted))
	}

	// Concurrently read from multiple goroutines.
	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			events, err := store.List(ctx, "run-1", 0, 0)
			if err != nil {
				errs <- fmt.Errorf("List: %w", err)
				return
			}
			if len(events) != 20 {
				errs <- fmt.Errorf("got %d events, want 20", len(events))
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func TestSQLiteEventStore_WALConcurrentReadWrite(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Writer goroutine.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := uint64(1); i <= 50; i++ {
			store.Append(ctx, makeEvent("run-1", i, runtime.EventNodeStarted))
		}
	}()

	// Reader goroutines running concurrently with the writer.
	errs := make(chan error, 5)
	for g := 0; g < 5; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, err := store.List(ctx, "run-1", 0, 0)
				if err != nil {
					errs <- err
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent read error: %v", err)
	}

	// Verify all writes landed.
	events, err := store.List(ctx, "run-1", 0, 0)
	if err != nil {
		t.Fatalf("final List: %v", err)
	}
	if len(events) != 50 {
		t.Errorf("got %d events, want 50", len(events))
	}
}

// --- Persistence across close/reopen ---

func TestSQLiteEventStore_PersistenceAcrossReopen(t *testing.T) {
	// Use a file-based temp DB (not memory) so data persists.
	dir := t.TempDir()
	dsn := dir + "/test.db"

	store1, err := NewSQLiteEventStore(SQLiteStoreConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open store1: %v", err)
	}

	ctx := context.Background()
	for i := uint64(1); i <= 3; i++ {
		e := makeEvent("run-1", i, runtime.EventNodeStarted)
		e.NodeID = fmt.Sprintf("node-%d", i)
		e.Payload = map[string]any{"val": float64(i)}
		store1.Append(ctx, e)
	}

	if err := store1.Close(); err != nil {
		t.Fatalf("close store1: %v", err)
	}

	// Reopen.
	store2, err := NewSQLiteEventStore(SQLiteStoreConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open store2: %v", err)
	}
	defer store2.Close()

	events, err := store2.List(ctx, "run-1", 0, 0)
	if err != nil {
		t.Fatalf("List after reopen: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("after reopen got %d events, want 3", len(events))
	}
	if events[0].NodeID != "node-1" {
		t.Errorf("NodeID = %q, want %q", events[0].NodeID, "node-1")
	}

	// Verify payload survived.
	if v, ok := events[1].Payload["val"]; !ok || v != float64(2) {
		t.Errorf("Payload[val] = %v, want 2", v)
	}

	seq, _ := store2.LatestSeq(ctx, "run-1")
	if seq != 3 {
		t.Errorf("LatestSeq after reopen = %d, want 3", seq)
	}
}

// --- RunIDs query ---

func TestSQLiteEventStore_RunIDs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Empty store should return nil.
	ids, err := store.RunIDs(ctx)
	if err != nil {
		t.Fatalf("RunIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("empty store RunIDs = %v, want empty", ids)
	}

	store.Append(ctx, makeEvent("run-b", 1, runtime.EventRunStarted))
	store.Append(ctx, makeEvent("run-a", 1, runtime.EventRunStarted))
	store.Append(ctx, makeEvent("run-b", 2, runtime.EventRunFinished))
	store.Append(ctx, makeEvent("run-c", 1, runtime.EventRunStarted))

	ids, err = store.RunIDs(ctx)
	if err != nil {
		t.Fatalf("RunIDs: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("got %d run IDs, want 3", len(ids))
	}
	// Sorted alphabetically.
	if ids[0] != "run-a" || ids[1] != "run-b" || ids[2] != "run-c" {
		t.Errorf("RunIDs = %v, want [run-a run-b run-c]", ids)
	}
}

// --- Payload with complex data ---

func TestSQLiteEventStore_ComplexPayload(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	e := makeEvent("run-1", 1, runtime.EventNodeFinished)
	e.Payload = map[string]any{
		"text":   "hello world",
		"count":  float64(42),
		"nested": map[string]any{"key": "value"},
		"list":   []any{float64(1), float64(2), float64(3)},
		"flag":   true,
	}
	if err := store.Append(ctx, e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	events, _ := store.List(ctx, "run-1", 0, 0)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}

	p := events[0].Payload
	if p["text"] != "hello world" {
		t.Errorf("Payload[text] = %v", p["text"])
	}
	if p["count"] != float64(42) {
		t.Errorf("Payload[count] = %v", p["count"])
	}
	if p["flag"] != true {
		t.Errorf("Payload[flag] = %v", p["flag"])
	}
	nested, ok := p["nested"].(map[string]any)
	if !ok || nested["key"] != "value" {
		t.Errorf("Payload[nested] = %v", p["nested"])
	}
}

// --- Nil payload ---

func TestSQLiteEventStore_NilPayload(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	e := makeEvent("run-1", 1, runtime.EventNodeStarted)
	e.Payload = nil
	if err := store.Append(ctx, e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	events, _ := store.List(ctx, "run-1", 0, 0)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	// Should get back an empty map, not nil.
	if events[0].Payload == nil {
		t.Error("Payload is nil, want empty map")
	}
}

// --- Interface compliance ---

func TestSQLiteEventStore_InterfaceCompliance(t *testing.T) {
	var _ EventStore = (*SQLiteEventStore)(nil)
}
