package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/memory"
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
	}, Scope: &memory.Scope{TenantID: "acme", Namespace: "support", SessionID: "sess-1", RunID: "run-1"}}
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
	if got.Scope == nil || *got.Scope != *record.Scope {
		t.Fatalf("reopened scope = %+v, want %+v", got.Scope, record.Scope)
	}
}

func TestSQLiteRunStore_PersistsPendingCompletionAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.sqlite")
	store, err := NewSQLiteRunStore(SQLiteRunStoreConfig{DSN: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(context.Background(), &RunRecord{
		ID: "run-1", Status: RunStatusPaused,
		PendingAction: &PendingAction{ID: "action-1", RunID: "run-1", NodeID: "review"},
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.CompletePendingAction(context.Background(), "run-1", "action-1", nil); err != nil {
		_ = store.Close()
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
	if _, err := reopened.CompletePendingAction(context.Background(), "run-1", "action-1", map[string]any{"approved": true}); !errors.Is(err, ErrPendingCompleted) {
		t.Fatalf("second completion error = %v, want ErrPendingCompleted", err)
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

func TestMemoryRunStore_CompletePendingActionIsExactlyOnce(t *testing.T) {
	store := NewMemoryRunStore()
	pending := &PendingAction{ID: "action-1", RunID: "run-1", NodeID: "review"}
	if err := store.Create(context.Background(), &RunRecord{
		ID: "run-1", Status: RunStatusPaused, PendingAction: pending,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompletePendingAction(context.Background(), "run-1", "action-1", nil); err != nil {
		t.Fatalf("first completion error = %v", err)
	}
	if _, err := store.CompletePendingAction(context.Background(), "run-1", "action-1", map[string]any{"approved": true}); !errors.Is(err, ErrPendingCompleted) {
		t.Fatalf("second completion error = %v, want ErrPendingCompleted", err)
	}
	record, err := store.Get(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.PendingAction == nil || !record.PendingAction.Completed {
		t.Fatalf("pending action = %#v, want completed", record.PendingAction)
	}
}

func TestRuntime_PersistsAndValidatesResumeToken(t *testing.T) {
	store := NewMemoryRunStore()
	g := graph.NewGraph("resume-token")
	if err := g.AddNode(core.NewFuncNode("done", func(_ context.Context, env *core.Envelope) (*core.Envelope, error) {
		return env.Clone(), nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := g.SetEntry("done"); err != nil {
		t.Fatal(err)
	}

	if _, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), RunOptions{
		RunStore: store,
	}); err != nil {
		t.Fatal(err)
	}
	runID := findRunID(t, store)
	record, err := store.Get(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if record.ResumeToken == "" {
		t.Fatal("resume token is empty")
	}

	paused := &RunRecord{ID: "paused", Status: RunStatusFailed, ResumeToken: "token-1", Checkpoint: &Checkpoint{
		ID: "checkpoint", RunID: "paused", Queue: []string{"done"}, Envelope: core.NewEnvelope(),
	}}
	if err := store.Create(context.Background(), paused); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntime().Resume(context.Background(), g, "paused", RunOptions{
		RunStore:    store,
		ResumeToken: "wrong-token",
	}); !errors.Is(err, ErrInvalidResumeToken) {
		t.Fatalf("Resume() error = %v, want ErrInvalidResumeToken", err)
	}
}

func TestMemoryRunStore_ClonesPendingActionPayloads(t *testing.T) {
	store := NewMemoryRunStore()
	data := map[string]any{"nested": map[string]any{"value": "original"}}
	if err := store.Create(context.Background(), &RunRecord{
		ID: "run-1", Status: RunStatusPaused,
		PendingAction: &PendingAction{ID: "action-1", RunID: "run-1", Data: data},
	}); err != nil {
		t.Fatal(err)
	}
	data["nested"].(map[string]any)["value"] = "changed"

	record, err := store.Get(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	nested := record.PendingAction.Data.(map[string]any)["nested"].(map[string]any)
	if nested["value"] != "original" {
		t.Fatalf("pending action data was aliased: %#v", nested)
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

func TestRuntime_ResumeFailedRunAfterPanic(t *testing.T) {
	store := NewMemoryRunStore()
	var attempts atomic.Int32
	g := graph.NewGraph("panic-recovery")
	if err := g.AddNode(core.NewFuncNode("panic-node", func(_ context.Context, env *core.Envelope) (*core.Envelope, error) {
		if attempts.Add(1) == 1 {
			panic("transient worker failure")
		}
		return env.Clone(), nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := g.SetEntry("panic-node"); err != nil {
		t.Fatal(err)
	}

	_, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), RunOptions{RunStore: store})
	if !errors.Is(err, ErrNodePanic) {
		t.Fatalf("first run error = %v, want node panic", err)
	}
	runID := findRunID(t, store)
	record, err := store.Get(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != RunStatusFailed || len(record.Checkpoint.Queue) != 1 {
		t.Fatalf("failed record = %#v", record)
	}
	if record.Checkpoint.NodeStatuses["panic-node"] != NodeStatusFailed {
		t.Fatalf("node status = %q, want failed", record.Checkpoint.NodeStatuses["panic-node"])
	}

	if _, err := NewRuntime().Resume(context.Background(), g, runID, RunOptions{RunStore: store}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

func TestRuntime_ResumeFailedRunAfterTimeout(t *testing.T) {
	store := NewMemoryRunStore()
	var attempts atomic.Int32
	g := graph.NewGraph("timeout-recovery")
	if err := g.AddNode(core.NewFuncNode("timeout-node", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		if attempts.Add(1) == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return env.Clone(), nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := g.SetEntry("timeout-node"); err != nil {
		t.Fatal(err)
	}

	_, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), RunOptions{
		RunStore:    store,
		NodeTimeout: 10 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first run error = %v, want deadline exceeded", err)
	}
	runID := findRunID(t, store)
	if _, err := NewRuntime().Resume(context.Background(), g, runID, RunOptions{RunStore: store}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

func TestRuntime_NodeReceivesStableIdempotencyKeyOnResume(t *testing.T) {
	store := NewMemoryRunStore()
	var seen atomic.Value
	g := graph.NewGraph("idempotency-context")
	if err := g.AddNode(core.NewFuncNode("side-effect", func(ctx context.Context, env *core.Envelope) (*core.Envelope, error) {
		seen.Store(IdempotencyKeyFromContext(ctx))
		return env.Clone(), nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := g.SetEntry("side-effect"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntime().Run(context.Background(), g, core.NewEnvelope(), RunOptions{
		RunStore:       store,
		IdempotencyKey: "request-123",
	}); err != nil {
		t.Fatal(err)
	}
	if got := seen.Load(); got != "request-123" {
		t.Fatalf("idempotency key = %v, want request-123", got)
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
