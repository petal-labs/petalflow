package sqlstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/artifact/artifacttest"
	"github.com/petal-labs/petalflow/artifact/internal/sqlstore"
)

const testSchema = `
CREATE TABLE artifact_blobs (
 digest TEXT NOT NULL, size INTEGER NOT NULL, content_type TEXT NOT NULL,
 content BLOB NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY (digest, size, content_type)
);
CREATE TABLE artifact_entries (
 tenant_id TEXT NOT NULL, run_id TEXT NOT NULL, digest TEXT NOT NULL,
 size INTEGER NOT NULL, content_type TEXT NOT NULL,
 workflow_id TEXT NOT NULL DEFAULT '', node_id TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, expires_at TEXT,
 PRIMARY KEY (tenant_id, run_id, digest, size, content_type),
 FOREIGN KEY (digest, size, content_type) REFERENCES artifact_blobs (digest, size, content_type)
);`

func TestStoreConformance(t *testing.T) {
	store, _ := newSQLStore(t, sqlstore.Config{})
	artifacttest.RunConformance(t, store)
}

func TestStorePolicyPruneAndUnavailable(t *testing.T) {
	store, db := newSQLStore(t, sqlstore.Config{
		MaxSizeBytes: 4, AllowedContentTypes: []string{"text/plain"}, DefaultRetention: time.Hour,
	})
	ctx := context.Background()
	scope := artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}
	metadata, err := store.Put(ctx, artifact.PutRequest{
		Scope: scope, ContentType: "text/plain", ExpiresAt: time.Now().Add(-time.Minute),
	}, bytes.NewReader([]byte("four")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := store.Put(ctx, artifact.PutRequest{Scope: scope, ContentType: "text/plain"}, bytes.NewReader([]byte("oversized"))); !errors.Is(err, artifact.ErrPolicyRejected) {
		t.Fatalf("oversized Put error = %v, want ErrPolicyRejected", err)
	}
	removed, err := store.DeleteExpired(ctx, metadata, time.Now())
	if err != nil || !removed {
		t.Fatalf("DeleteExpired = %v, %v; want true, nil", removed, err)
	}
	if _, err := store.Stat(ctx, scope, metadata.Reference); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("Stat pruned error = %v, want ErrNotFound", err)
	}

	metadata, err = store.Put(ctx, artifact.PutRequest{Scope: scope, ContentType: "text/plain"}, bytes.NewReader([]byte("safe")))
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if _, err := db.Exec(`UPDATE artifact_blobs SET content = X'00' WHERE digest = ?`, metadata.Reference.Digest); err != nil {
		t.Fatalf("corrupt content: %v", err)
	}
	if _, err := store.Get(ctx, scope, metadata.Reference); !errors.Is(err, artifact.ErrUnavailable) {
		t.Fatalf("Get corrupt error = %v, want ErrUnavailable", err)
	}
}

func TestLifecyclePruneIsTenantScopedAndRaceSafe(t *testing.T) {
	store, db := newSQLStore(t, sqlstore.Config{})
	ctx := context.Background()
	expired := time.Now().UTC().Add(-time.Hour)
	request := artifact.PutRequest{
		Scope:       artifact.Scope{TenantID: "tenant-a", RunID: "run-a"},
		ContentType: "text/plain", ExpiresAt: expired,
	}
	metadata, err := store.Put(ctx, request, bytes.NewReader([]byte("candidate")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := store.Put(ctx, artifact.PutRequest{
		Scope:       artifact.Scope{TenantID: "tenant-b", RunID: "run-b"},
		ContentType: "text/plain", ExpiresAt: expired,
	}, bytes.NewReader([]byte("other"))); err != nil {
		t.Fatalf("Put other tenant: %v", err)
	}

	candidates, err := store.ListExpired(ctx, time.Now().UTC(), "tenant-a", 0, 10)
	if err != nil || len(candidates) != 1 || candidates[0].Scope.TenantID != "tenant-a" {
		t.Fatalf("ListExpired = %+v, %v", candidates, err)
	}

	// Simulate a concurrent retention extension after candidate selection.
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE artifact_entries SET expires_at = ? WHERE tenant_id = ? AND run_id = ?`,
		future, metadata.Scope.TenantID, metadata.Scope.RunID); err != nil {
		t.Fatalf("extend expiry: %v", err)
	}
	deleted, err := store.DeleteExpired(ctx, metadata, time.Now().UTC())
	if err != nil || deleted {
		t.Fatalf("DeleteExpired after extension = %v, %v; want false, nil", deleted, err)
	}
}

func TestStoreResolvesWorkflowRetentionBeforeTenantAndDefault(t *testing.T) {
	store, _ := newSQLStore(t, sqlstore.Config{Retention: artifact.RetentionPolicy{
		Default: 3 * time.Hour, Tenants: map[string]time.Duration{"tenant-a": 2 * time.Hour},
		Workflows: map[string]time.Duration{"workflow-a": time.Hour},
	}})
	metadata, err := store.Put(context.Background(), artifact.PutRequest{
		Scope:      artifact.Scope{TenantID: "tenant-a", RunID: "run-a"},
		WorkflowID: "workflow-a", ContentType: "text/plain",
	}, strings.NewReader("retained"))
	if err != nil {
		t.Fatal(err)
	}
	remaining := time.Until(metadata.ExpiresAt)
	if remaining <= 59*time.Minute || remaining > time.Hour {
		t.Fatalf("workflow retention remaining = %s, want about 1h", remaining)
	}
}

func TestNewValidatesInputs(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/validation.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tests := []struct {
		name    string
		db      *sql.DB
		dialect string
		config  sqlstore.Config
	}{
		{name: "nil database", dialect: sqlstore.DialectSQLite},
		{name: "bad dialect", db: db, dialect: "bad"},
		{name: "negative size", db: db, dialect: sqlstore.DialectSQLite, config: sqlstore.Config{MaxSizeBytes: -1}},
		{name: "negative retention", db: db, dialect: sqlstore.DialectSQLite, config: sqlstore.Config{DefaultRetention: -time.Second}},
		{name: "negative prune", db: db, dialect: sqlstore.DialectSQLite, config: sqlstore.Config{PruneInterval: -time.Second}},
		{name: "bad content type", db: db, dialect: sqlstore.DialectSQLite, config: sqlstore.Config{AllowedContentTypes: []string{"bad"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := sqlstore.New(tt.db, tt.dialect, tt.config); err == nil {
				t.Fatal("New error = nil, want validation error")
			}
		})
	}
}

func TestOperationsValidateInputs(t *testing.T) {
	store, _ := newSQLStore(t, sqlstore.Config{})
	ctx := context.Background()
	scope := artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}
	ref := artifact.Reference{Digest: "invalid", ContentType: "text/plain"}

	if _, err := store.Put(ctx, artifact.PutRequest{Scope: scope, ContentType: "text/plain"}, nil); err == nil {
		t.Fatal("Put nil content error = nil")
	}
	if _, err := store.Get(ctx, scope, ref); err == nil {
		t.Fatal("Get invalid reference error = nil")
	}
	if err := store.Delete(ctx, scope, ref); err == nil {
		t.Fatal("Delete invalid reference error = nil")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.ListByRun(canceled, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListByRun error = %v, want context.Canceled", err)
	}
}

func newSQLStore(t *testing.T, config sqlstore.Config) (*sqlstore.Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", t.TempDir()+"/artifacts.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("foreign keys: %v", err)
	}
	if _, err := db.Exec(testSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	store, err := sqlstore.New(db, sqlstore.DialectSQLite, config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, db
}
