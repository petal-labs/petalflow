package bus

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/runtime"
)

// runEventStoreConformance asserts the EventStore contract. Both backends run it.
func runEventStoreConformance(t *testing.T, newStore func(t *testing.T) EventStore) {
	t.Helper()

	t.Run("append_list_latestseq", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		ev := runtime.Event{
			RunID: "run-1", Seq: 1, Kind: runtime.EventKind("node.start"),
			Time: time.Now().UTC(), Attempt: 1, Payload: map[string]any{"k": "v"},
		}
		if err := s.Append(ctx, ev); err != nil {
			t.Fatalf("append: %v", err)
		}
		got, err := s.List(ctx, "run-1", 0, 0)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 1 || got[0].Seq != 1 {
			t.Fatalf("list = %+v, want 1 event seq=1", got)
		}
		latest, err := s.LatestSeq(ctx, "run-1")
		if err != nil {
			t.Fatalf("latestseq: %v", err)
		}
		if latest != 1 {
			t.Fatalf("latestseq = %d, want 1", latest)
		}
	})

	t.Run("list_after_seq_and_limit", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		for i := uint64(1); i <= 3; i++ {
			if err := s.Append(ctx, runtime.Event{RunID: "r", Seq: i, Kind: "k", Time: time.Now().UTC(), Attempt: 1}); err != nil {
				t.Fatalf("append %d: %v", i, err)
			}
		}
		got, err := s.List(ctx, "r", 1, 1)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 1 || got[0].Seq != 2 {
			t.Fatalf("list afterSeq=1 limit=1 = %+v, want seq=2", got)
		}
	})

	t.Run("latestseq_unknown_run_is_zero", func(t *testing.T) {
		s := newStore(t)
		latest, err := s.LatestSeq(context.Background(), "nope")
		if err != nil {
			t.Fatalf("latestseq: %v", err)
		}
		if latest != 0 {
			t.Fatalf("latestseq unknown = %d, want 0", latest)
		}
	})
}

func TestSQLiteEventStoreConformance(t *testing.T) {
	runEventStoreConformance(t, func(t *testing.T) EventStore {
		dir := t.TempDir()
		s, err := NewSQLiteEventStore(SQLiteStoreConfig{DSN: filepath.Join(dir, "events.db")})
		if err != nil {
			t.Fatalf("new sqlite store: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}
