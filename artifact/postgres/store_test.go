package postgres

import (
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func TestNewStoreRejectsMissingAndUnavailableDSN(t *testing.T) {
	if _, err := NewStore(Config{}); err == nil {
		t.Fatal("NewStore without DSN error = nil")
	}
	if _, err := NewStore(Config{DSN: "postgres://127.0.0.1:1/unreachable"}); err == nil {
		t.Fatal("NewStore unreachable error = nil")
	}
}

func TestInitialize(t *testing.T) {
	if err := initialize(&fakeExecutor{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := initialize(&fakeExecutor{failAt: 1}); err == nil {
		t.Fatal("initialize schema error = nil")
	}
	if err := initialize(&fakeExecutor{failAt: 2}); err == nil {
		t.Fatal("initialize migration error = nil")
	}
	if err := initialize(&fakeExecutor{failAt: 5}); err == nil {
		t.Fatal("initialize index error = nil")
	}
}

func TestNewStoreSuccessAndPolicyValidation(t *testing.T) {
	originalOpen := openDatabase
	originalInitialize := initializeDatabase
	t.Cleanup(func() {
		openDatabase = originalOpen
		initializeDatabase = originalInitialize
	})
	openDatabase = func(string, string) (*sql.DB, error) {
		return sql.Open("sqlite", t.TempDir()+"/postgres-constructor.db")
	}
	initializeDatabase = func(executor) error { return nil }

	store, err := NewStore(Config{DSN: "test"})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := NewStore(Config{DSN: "test", MaxSizeBytes: -1}); err == nil {
		t.Fatal("NewStore invalid policy error = nil")
	}
}

type fakeExecutor struct {
	calls  int
	failAt int
}

func (f *fakeExecutor) Exec(string, ...any) (sql.Result, error) {
	f.calls++
	if f.calls == f.failAt {
		return nil, errors.New("exec failed")
	}
	return fakeResult(0), nil
}

type fakeResult int64

func (r fakeResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r fakeResult) RowsAffected() (int64, error) { return int64(r), nil }
