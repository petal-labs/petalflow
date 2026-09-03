package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
)

func TestMemoryRunStore_PersistsRunAcrossInstances(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryRunStore()
	record := &RunRecord{
		ID:     "run-1",
		Status: RunStatusPaused,
		Checkpoint: &Checkpoint{
			ID:       "checkpoint-1",
			RunID:    "run-1",
			Queue:    []string{"approval"},
			Envelope: core.NewEnvelope(),
		},
	}

	if err := store.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	got, err := store.Get(ctx, "run-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	got.Checkpoint.Queue[0] = "changed-locally"

	again, err := store.Get(ctx, "run-1")
	if err != nil {
		t.Fatalf("Get() second call error = %v", err)
	}
	if again.Checkpoint.Queue[0] != "approval" {
		t.Fatalf("store returned aliased checkpoint queue: %v", again.Checkpoint.Queue)
	}
}

func TestSQLiteRunStore_PersistsCheckpointAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.sqlite")
	ctx := context.Background()
	store, err := NewSQLiteRunStore(SQLiteRunStoreConfig{DSN: path})
	if err != nil {
		t.Fatal(err)
	}
	record := &RunRecord{ID: "run-1", Status: RunStatusPaused, Checkpoint: &Checkpoint{
		ID: "checkpoint-1", RunID: "run-1", Queue: []string{"approval"}, Envelope: core.NewEnvelope(),
	}}
	if err := store.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteRunStore(SQLiteRunStoreConfig{DSN: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.Get(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != RunStatusPaused || got.Checkpoint.Queue[0] != "approval" {
		t.Fatalf("reopened record = %#v", got)
	}
}

func TestMemoryRunStore_CancelIsTerminal(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryRunStore()
	if err := store.Create(ctx, &RunRecord{ID: "run-1", Status: RunStatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(ctx, "run-1"); err != nil {
		t.Fatal(err)
	}
	record, err := store.Get(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != RunStatusCanceled || !record.CancelRequested {
		t.Fatalf("record after cancel = %#v", record)
	}

	record.Status = RunStatusCompleted
	if err := store.Update(ctx, record); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != RunStatusCanceled {
		t.Fatalf("late update overwrote cancellation: %s", got.Status)
	}
}

func TestRuntime_ResumeFromDurableCheckpoint(t *testing.T) {
	store := NewMemoryRunStore()
	firstRuns := 0
	secondRuns := 0
	g := graph.NewGraph("recoverable")
	if err := g.AddNode(core.NewFuncNode("first", func(_ context.Context, env *core.Envelope) (*core.Envelope, error) {
		firstRuns++
		out := env.Clone()
		out.SetVar("first", true)
		return out, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode(core.NewFuncNode("second", func(_ context.Context, env *core.Envelope) (*core.Envelope, error) {
		secondRuns++
		out := env.Clone()
		out.SetVar("second", true)
		return out, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("first", "second"); err != nil {
		t.Fatal(err)
	}
	if err := g.SetEntry("first"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var once bool
	opts := DefaultRunOptions()
	opts.RunStore = store
	opts.EventHandler = func(e Event) {
		if e.Kind == EventNodeStarted && e.NodeID == "second" && !once {
			once = true
			close(started)
			cancel()
		}
	}
	_, err := NewRuntime().Run(ctx, g, core.NewEnvelope(), opts)
	if err == nil || !errors.Is(err, ErrRunCanceled) {
		t.Fatalf("first run error = %v, want cancellation", err)
	}
	<-started
	record, err := store.Get(context.Background(), findRunID(t, store))
	if err != nil {
		t.Fatal(err)
	}
	if record.Checkpoint == nil || len(record.Checkpoint.Queue) == 0 || record.Checkpoint.Queue[0] != "second" {
		t.Fatalf("checkpoint = %#v", record.Checkpoint)
	}

	result, err := NewRuntime().Resume(context.Background(), g, record.ID, RunOptions{RunStore: store})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if value, ok := result.GetVar("first"); !ok || value != true {
		t.Fatalf("first output = %v, want true", value)
	}
	if value, ok := result.GetVar("second"); !ok || value != true {
		t.Fatalf("second output = %v, want true", value)
	}
	if firstRuns != 1 || secondRuns != 2 {
		t.Fatalf("node runs = first:%d second:%d, want 1:2 (replayed from pre-node checkpoint)", firstRuns, secondRuns)
	}
}

func findRunID(t *testing.T, store *MemoryRunStore) string {
	t.Helper()
	for _, id := range store.IDs() {
		return id
	}
	t.Fatal("no run persisted")
	return ""
}
