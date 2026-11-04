package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/daemon"
)

func TestDetectBackend(t *testing.T) {
	cases := []struct {
		dsn  string
		want databaseBackend
	}{
		{"postgres://u:p@h:5432/db", backendPostgres},
		{"postgresql://u:p@h:5432/db", backendPostgres},
		{"/var/lib/petalflow/petalflow.db", backendSQLite},
		{"file:/tmp/x.db?cache=shared", backendSQLite},
		{"petalflow.db", backendSQLite},
	}
	for _, c := range cases {
		if got := detectBackend(c.dsn); got != c.want {
			t.Errorf("detectBackend(%q) = %q, want %q", c.dsn, got, c.want)
		}
	}
}

func TestNormalizePostgresScope(t *testing.T) {
	t.Run("strips credentials and query params", func(t *testing.T) {
		dsn := "postgres://user:pass@host:5432/db?sslmode=disable"
		want := "host:5432/db"
		if got := normalizePostgresScope(dsn); got != want {
			t.Errorf("normalizePostgresScope(%q) = %q, want %q", dsn, got, want)
		}
	})

	t.Run("stable across benign DSN edits", func(t *testing.T) {
		original := "postgres://user:pass@host:5432/db?sslmode=disable"
		rotated := "postgres://user:newpass@host:5432/db?sslmode=require&connect_timeout=5"

		gotOriginal := normalizePostgresScope(original)
		gotRotated := normalizePostgresScope(rotated)
		if gotOriginal != gotRotated {
			t.Errorf("scope changed across benign DSN edit: %q != %q", gotOriginal, gotRotated)
		}
	})

	t.Run("unparseable DSN returned unchanged", func(t *testing.T) {
		dsn := "postgres://[::not-valid"
		if got := normalizePostgresScope(dsn); got != dsn {
			t.Errorf("normalizePostgresScope(%q) = %q, want unchanged %q", dsn, got, dsn)
		}
	})
}

func TestOpenAuditStore(t *testing.T) {
	// The SQLite backend opens (and creates) the shared audit database.
	path := filepath.Join(t.TempDir(), "audit.db")
	sqliteStore, err := openAuditStore(path, backendSQLite, path)
	if err != nil || sqliteStore == nil {
		t.Fatalf("sqlite audit store = %v err %v", sqliteStore, err)
	}

	// Empty DSNs fail cleanly.
	if _, err := openAuditStore("", backendSQLite, ""); err == nil {
		t.Fatal("empty sqlite DSN must be rejected")
	}

	// The Postgres branch requires a reachable server; construction
	// fails without one.
	if _, err := openAuditStore("postgres://127.0.0.1:1/none", backendPostgres, ""); err == nil {
		t.Fatal("postgres audit store without a server must fail")
	}
}

func TestOpenEventStoreAppliesRetention(t *testing.T) {
	retention := daemon.ParsedEventsRetention{
		Age:       720 * time.Hour,
		MaxPerRun: 1000,
		Interval:  15 * time.Minute,
	}

	// SQLite: a pruner-capable store with retention configured.
	store, err := openEventStore(t.TempDir()+"/events.db", backendSQLite, retention)
	if err != nil {
		t.Fatalf("openEventStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	if _, ok := store.(bus.EventPruner); !ok {
		t.Error("sqlite event store should implement bus.EventPruner")
	}

	// Postgres branch is exercised (unreachable DSN fails loudly at
	// schema creation).
	if _, err := openEventStore("postgres://127.0.0.1:1/unreachable", backendPostgres, retention); err == nil {
		t.Error("unreachable postgres DSN should fail loudly")
	}

	// Invalid retention never reaches the store: serve validates first.
	if _, err := (&daemon.EventsRetentionConfig{Retention: "nope"}).Parse(); err == nil {
		t.Error("invalid retention should be rejected")
	}
}
