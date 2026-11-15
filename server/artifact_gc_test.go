package server

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/tool"
)

type recordingArtifactGCObserver struct{ observations []ArtifactGCObservation }

func (o *recordingArtifactGCObserver) ObserveArtifactGC(_ context.Context, observation ArtifactGCObservation) {
	o.observations = append(o.observations, observation)
}

type channelArtifactGCObserver struct{ observed chan ArtifactGCObservation }

func (o *channelArtifactGCObserver) ObserveArtifactGC(_ context.Context, observation ArtifactGCObservation) {
	o.observed <- observation
}

type artifactGCErrorStore struct {
	artifact.LifecycleStore
	listErr, deleteErr error
}

func (s artifactGCErrorStore) ListExpired(ctx context.Context, before time.Time, tenant string, offset, limit int) ([]artifact.Metadata, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.LifecycleStore.ListExpired(ctx, before, tenant, offset, limit)
}

func (s artifactGCErrorStore) DeleteExpired(ctx context.Context, metadata artifact.Metadata, before time.Time) (bool, error) {
	if s.deleteErr != nil {
		return false, s.deleteErr
	}
	return s.LifecycleStore.DeleteExpired(ctx, metadata, before)
}

type artifactGCErrorRunStore struct{ runtime.RunStore }

func (s artifactGCErrorRunStore) Get(context.Context, string) (*runtime.RunRecord, error) {
	return nil, errors.New("run store failed")
}

type artifactGCErrorEventStore struct{ bus.EventStore }

func (s artifactGCErrorEventStore) List(context.Context, string, uint64, int) ([]runtime.Event, error) {
	return nil, errors.New("event store failed")
}

func TestArtifactCollectorProtectsLiveAndReferencedArtifacts(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store, err := artifact.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	runs := runtime.NewMemoryRunStore()
	events := newTestEventStore(t)
	audit := tool.NewMemoryAuditStore()
	observer := &recordingArtifactGCObserver{}

	put := func(runID, content string) artifact.Metadata {
		t.Helper()
		metadata, putErr := store.Put(ctx, artifact.PutRequest{
			Scope:      artifact.Scope{TenantID: "tenant-a", RunID: runID},
			WorkflowID: "workflow-a", ContentType: "text/plain", ExpiresAt: now.Add(-time.Hour),
		}, bytes.NewBufferString(content))
		if putErr != nil {
			t.Fatalf("Put(%s): %v", runID, putErr)
		}
		return metadata
	}
	live := put("run-live", "live")
	paused := put("run-paused", "paused")
	referenced := put("run-referenced", "referenced")
	eventReferenced := put("run-event-referenced", "event-referenced")
	unreferenced := put("run-unreferenced", "unreferenced")

	createRun := func(record *runtime.RunRecord) {
		t.Helper()
		if createErr := runs.Create(ctx, record); createErr != nil {
			t.Fatalf("Create(%s): %v", record.ID, createErr)
		}
	}
	createRun(&runtime.RunRecord{ID: live.Scope.RunID, TenantID: "tenant-a", Status: runtime.RunStatusRunning})
	createRun(&runtime.RunRecord{
		ID: paused.Scope.RunID, TenantID: "tenant-a", Status: runtime.RunStatusPaused,
		PendingAction: &runtime.PendingAction{ID: "pending", RunID: paused.Scope.RunID},
	})
	createRun(&runtime.RunRecord{
		ID: referenced.Scope.RunID, TenantID: "tenant-a", Status: runtime.RunStatusCompleted,
		Checkpoint: &runtime.Checkpoint{Envelope: &core.Envelope{Artifacts: []core.Artifact{{
			ID: "output", URI: artifact.ReferenceURI(referenced.Reference),
		}}}},
	})
	createRun(&runtime.RunRecord{ID: eventReferenced.Scope.RunID, TenantID: "tenant-a", Status: runtime.RunStatusCompleted})
	if err := events.Append(ctx, runtime.Event{
		RunID: eventReferenced.Scope.RunID, Seq: 1, Kind: runtime.EventNodeFinished,
		Payload: map[string]any{"capture": artifact.ReferenceURI(eventReferenced.Reference)},
	}); err != nil {
		t.Fatalf("Append event: %v", err)
	}
	createRun(&runtime.RunRecord{ID: unreferenced.Scope.RunID, TenantID: "tenant-a", Status: runtime.RunStatusCompleted})

	collector, err := NewArtifactCollector(ArtifactCollectorConfig{
		Store: store, RunStore: runs, EventStore: events, AuditStore: audit, Observer: observer,
		Now: func() time.Time { return now }, BatchSize: 10, Interval: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewArtifactCollector: %v", err)
	}
	stats, err := collector.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Candidates != 5 || stats.Deleted != 1 || stats.Retained != 4 || stats.BytesDeleted != unreferenced.Reference.Size {
		t.Fatalf("stats = %+v", stats)
	}
	for _, metadata := range []artifact.Metadata{live, paused, referenced, eventReferenced} {
		if _, statErr := store.Stat(ctx, metadata.Scope, metadata.Reference); statErr != nil {
			t.Fatalf("protected artifact %s: %v", metadata.Scope.RunID, statErr)
		}
	}
	if _, statErr := store.Stat(ctx, unreferenced.Scope, unreferenced.Reference); !errors.Is(statErr, artifact.ErrNotFound) {
		t.Fatalf("unreferenced Stat error = %v, want ErrNotFound", statErr)
	}
	records, _, err := audit.List(ctx, tool.ToolAuditFilter{TenantID: "tenant-a", Limit: 10})
	if err != nil || len(records) != 1 || records[0].Decision != ArtifactAuditDeleted {
		t.Fatalf("audit records = %+v, %v", records, err)
	}
	if len(observer.observations) != 1 || observer.observations[0].Deleted != 1 {
		t.Fatalf("observer = %+v", observer.observations)
	}
}

func TestArtifactCollectorAdvancesPastRetainedCandidates(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store, err := artifact.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	put := func(runID string) artifact.Metadata {
		metadata, putErr := store.Put(ctx, artifact.PutRequest{
			Scope:       artifact.Scope{TenantID: "tenant-a", RunID: runID},
			ContentType: "text/plain", ExpiresAt: now.Add(-time.Hour),
		}, bytes.NewBufferString(runID))
		if putErr != nil {
			t.Fatal(putErr)
		}
		return metadata
	}
	protected := put("run-a")
	deletable := put("run-z")
	runs := runtime.NewMemoryRunStore()
	if err := runs.Create(ctx, &runtime.RunRecord{ID: protected.Scope.RunID, TenantID: "tenant-a", Status: runtime.RunStatusRunning}); err != nil {
		t.Fatal(err)
	}
	collector, err := NewArtifactCollector(ArtifactCollectorConfig{
		Store: store, RunStore: runs, EventStore: newTestEventStore(t),
		BatchSize: 1, Interval: time.Hour, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats, err := collector.RunOnce(ctx); err != nil || stats.Retained != 1 {
		t.Fatalf("first pass = %+v, %v", stats, err)
	}
	if stats, err := collector.RunOnce(ctx); err != nil || stats.Deleted != 1 {
		t.Fatalf("second pass = %+v, %v", stats, err)
	}
	if _, err := store.Stat(ctx, deletable.Scope, deletable.Reference); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("deletable artifact error = %v", err)
	}
}

func TestArtifactCollectorBackgroundLifecycle(t *testing.T) {
	store, err := artifact.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	observer := &channelArtifactGCObserver{observed: make(chan ArtifactGCObservation, 1)}
	collector, err := NewArtifactCollector(ArtifactCollectorConfig{
		Store: store, RunStore: runtime.NewMemoryRunStore(), EventStore: newTestEventStore(t),
		Observer: observer, Interval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	collector.Start(ctx)
	collector.Start(ctx)
	select {
	case <-observer.observed:
	case <-time.After(5 * time.Second):
		t.Fatal("collector did not run initial pass")
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := collector.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := collector.Stop(stopCtx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestArtifactCollectorValidatesConfiguration(t *testing.T) {
	store, _ := artifact.NewInMemoryStore()
	events := newTestEventStore(t)
	runs := runtime.NewMemoryRunStore()
	tests := []ArtifactCollectorConfig{
		{},
		{Store: store, RunStore: runs, EventStore: events, BatchSize: -1},
		{Store: store, RunStore: runs, EventStore: events, BatchSize: 10_001},
		{Store: store, RunStore: runs, EventStore: events, Interval: -time.Second},
	}
	for _, config := range tests {
		if _, err := NewArtifactCollector(config); err == nil {
			t.Fatalf("NewArtifactCollector(%+v) error = nil", config)
		}
	}
}

func TestArtifactCollectorReportsStoreAndReferenceErrors(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	base, _ := artifact.NewInMemoryStore()
	metadata, err := base.Put(ctx, artifact.PutRequest{
		Scope:       artifact.Scope{TenantID: "tenant-a", RunID: "run-error"},
		ContentType: "text/plain", ExpiresAt: now.Add(-time.Hour),
	}, bytes.NewBufferString("error candidate"))
	if err != nil {
		t.Fatal(err)
	}
	events := newTestEventStore(t)
	runs := runtime.NewMemoryRunStore()
	if err := runs.Create(ctx, &runtime.RunRecord{ID: metadata.Scope.RunID, TenantID: "tenant-a", Status: runtime.RunStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	tests := []ArtifactCollectorConfig{
		{Store: artifactGCErrorStore{LifecycleStore: base, listErr: errors.New("list failed")}, RunStore: runs, EventStore: events},
		{Store: base, RunStore: artifactGCErrorRunStore{RunStore: runs}, EventStore: events},
		{Store: base, RunStore: runs, EventStore: artifactGCErrorEventStore{EventStore: events}},
		{Store: artifactGCErrorStore{LifecycleStore: base, deleteErr: errors.New("delete failed")}, RunStore: runtime.NewMemoryRunStore(), EventStore: events},
	}
	for index, config := range tests {
		config.Now = func() time.Time { return now }
		collector, err := NewArtifactCollector(config)
		if err != nil {
			t.Fatalf("case %d NewArtifactCollector: %v", index, err)
		}
		stats, err := collector.RunOnce(ctx)
		if err == nil || stats.Errors != 1 {
			t.Fatalf("case %d stats=%+v err=%v", index, stats, err)
		}
	}
}
