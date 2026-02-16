package server

import (
	"path/filepath"
	"testing"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/tool"
)

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()

	path := filepath.Join(t.TempDir(), "workflows.sqlite")
	store, err := NewSQLiteStore(SQLiteStoreConfig{DSN: path})
	if err != nil {
		t.Fatalf("NewSQLiteStore(workflows): %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newTestWorkflowStore(t *testing.T) WorkflowStore {
	t.Helper()
	return newTestSQLiteStore(t)
}

func newTestEventStore(t *testing.T) bus.EventStore {
	t.Helper()

	path := filepath.Join(t.TempDir(), "events.sqlite")
	store, err := bus.NewSQLiteEventStore(bus.SQLiteStoreConfig{DSN: path})
	if err != nil {
		t.Fatalf("NewSQLiteEventStore(events): %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newTestToolStore(t *testing.T) tool.Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tools.sqlite")
	store, err := tool.NewSQLiteStore(tool.SQLiteStoreConfig{DSN: path, Scope: path})
	if err != nil {
		t.Fatalf("NewSQLiteStore(tools): %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
