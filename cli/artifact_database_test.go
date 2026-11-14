package cli

import (
	"context"
	"testing"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/daemon"
)

func TestOpenArtifactStoreSQLite(t *testing.T) {
	store, err := openArtifactStore(t.TempDir()+"/artifacts.db", backendSQLite, daemon.ParsedArtifactsConfig{
		MaxSizeBytes: 1024,
	})
	if err != nil {
		t.Fatalf("openArtifactStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var _ artifact.Store = store
	items, err := store.ListByRun(context.Background(), artifact.Scope{TenantID: "tenant-1", RunID: "run-1"})
	if err != nil || len(items) != 0 {
		t.Fatalf("ListByRun = %v, %v; want empty", items, err)
	}
}

func TestOpenArtifactStorePostgresFailure(t *testing.T) {
	if _, err := openArtifactStore("postgres://127.0.0.1:1/unreachable", backendPostgres, daemon.ParsedArtifactsConfig{}); err == nil {
		t.Fatal("openArtifactStore unreachable PostgreSQL error = nil")
	}
}
