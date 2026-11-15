package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/artifact/artifacttest"
	artifactsqlite "github.com/petal-labs/petalflow/artifact/sqlite"
)

func TestStoreConformance(t *testing.T) {
	store := newStore(t, artifactsqlite.Config{DSN: t.TempDir() + "/artifacts.db"})
	artifacttest.RunConformance(t, store)
}

func TestStorePersistsAcrossRestart(t *testing.T) {
	dsn := t.TempDir() + "/artifacts.db"
	store := newStore(t, artifactsqlite.Config{DSN: dsn})
	scope := artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}
	metadata, err := store.Put(context.Background(), artifact.PutRequest{
		Scope: scope, ContentType: "application/octet-stream",
	}, bytes.NewReader([]byte{0, 1, 2, 3}))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened := newStore(t, artifactsqlite.Config{DSN: dsn})
	reader, err := reopened.Get(context.Background(), scope, metadata.Reference)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	_ = reader.Close()
	if !bytes.Equal(got, []byte{0, 1, 2, 3}) {
		t.Fatalf("content = %v, want persisted bytes", got)
	}
}

func TestStoreAppliesPolicyAndDefaultRetention(t *testing.T) {
	store := newStore(t, artifactsqlite.Config{
		DSN:                 t.TempDir() + "/artifacts.db",
		MaxSizeBytes:        4,
		AllowedContentTypes: []string{"text/plain"},
		DefaultRetention:    time.Hour,
	})
	scope := artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}
	metadata, err := store.Put(context.Background(), artifact.PutRequest{
		Scope: scope, ContentType: "text/plain",
	}, bytes.NewReader([]byte("four")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if metadata.ExpiresAt.IsZero() || time.Until(metadata.ExpiresAt) > time.Hour {
		t.Fatalf("ExpiresAt = %v, want default retention within one hour", metadata.ExpiresAt)
	}

	_, err = store.Put(context.Background(), artifact.PutRequest{
		Scope: scope, ContentType: "application/json",
	}, bytes.NewReader([]byte(`{}`)))
	if !errors.Is(err, artifact.ErrPolicyRejected) {
		t.Fatalf("disallowed Put error = %v, want ErrPolicyRejected", err)
	}
	_, err = store.Put(context.Background(), artifact.PutRequest{
		Scope: scope, ContentType: "text/plain",
	}, bytes.NewReader([]byte("oversized")))
	if !errors.Is(err, artifact.ErrPolicyRejected) {
		t.Fatalf("oversized Put error = %v, want ErrPolicyRejected", err)
	}
}

func TestStoreConcurrentIdempotentPut(t *testing.T) {
	store := newStore(t, artifactsqlite.Config{DSN: t.TempDir() + "/artifacts.db"})
	request := artifact.PutRequest{
		Scope: artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}, ContentType: "text/plain",
	}
	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Put(context.Background(), request, bytes.NewReader([]byte("shared")))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Put: %v", err)
		}
	}
	items, err := store.ListByRun(context.Background(), request.Scope)
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ListByRun count = %d, want 1", len(items))
	}
}

func TestStoreLifecycleDeleteExpired(t *testing.T) {
	store := newStore(t, artifactsqlite.Config{DSN: t.TempDir() + "/artifacts.db"})
	ctx := context.Background()
	scope := artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}
	metadata, err := store.Put(ctx, artifact.PutRequest{
		Scope: scope, ContentType: "text/plain", ExpiresAt: time.Now().Add(-time.Minute),
	}, bytes.NewReader([]byte("expired")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	candidates, err := store.ListExpired(ctx, time.Now(), scope.TenantID, 0, 10)
	if err != nil {
		t.Fatalf("ListExpired: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("ListExpired count = %d, want 1", len(candidates))
	}
	removed, err := store.DeleteExpired(ctx, candidates[0], time.Now())
	if err != nil || !removed {
		t.Fatalf("DeleteExpired = %v, %v; want true, nil", removed, err)
	}
	if _, err := store.Stat(ctx, scope, metadata.Reference); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("Stat pruned artifact error = %v, want ErrNotFound", err)
	}
}

func TestNewStoreValidationAndLegacyMigration(t *testing.T) {
	if _, err := artifactsqlite.NewStore(artifactsqlite.Config{}); err == nil {
		t.Fatal("NewStore without DSN error = nil")
	}
	if _, err := artifactsqlite.NewStore(artifactsqlite.Config{DSN: t.TempDir() + "/bad.db", MaxSizeBytes: -1}); err == nil {
		t.Fatal("NewStore with invalid policy error = nil")
	}

	dsn := t.TempDir() + "/legacy.db"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("Open legacy database: %v", err)
	}
	legacySchema := `
CREATE TABLE artifact_blobs (
 digest TEXT NOT NULL, size INTEGER NOT NULL, content_type TEXT NOT NULL,
 content BLOB NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY (digest, size, content_type)
);
CREATE TABLE artifact_entries (
 tenant_id TEXT NOT NULL, run_id TEXT NOT NULL, digest TEXT NOT NULL,
 size INTEGER NOT NULL, content_type TEXT NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY (tenant_id, run_id, digest, size, content_type)
);`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	_ = db.Close()
	store := newStore(t, artifactsqlite.Config{DSN: dsn})
	if _, err := store.Put(context.Background(), artifact.PutRequest{
		Scope:      artifact.Scope{TenantID: "tenant-1", RunID: "run-1"},
		WorkflowID: "workflow-1", NodeID: "node-1", ContentType: "text/plain",
	}, bytes.NewReader([]byte("migrated"))); err != nil {
		t.Fatalf("Put after migration: %v", err)
	}
}

func newStore(t *testing.T, config artifactsqlite.Config) *artifactsqlite.Store {
	t.Helper()
	store, err := artifactsqlite.NewStore(config)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
