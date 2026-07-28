//go:build integration

package bus

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresEventStoreConformance(t *testing.T) {
	dsn := os.Getenv("PETALFLOW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set PETALFLOW_TEST_POSTGRES_DSN to run postgres integration tests")
	}
	runEventStoreConformance(t, func(t *testing.T) EventStore {
		schema := uniqueSchema(t, dsn) // creates + sets search_path to an isolated schema
		s, err := NewPostgresEventStore(PostgresStoreConfig{DSN: schema})
		if err != nil {
			t.Fatalf("new postgres store: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

// uniqueSchema creates a throwaway schema named from the test and returns a DSN
// whose search_path points at it, dropping it on cleanup.
func uniqueSchema(t *testing.T, baseDSN string) string {
	t.Helper()
	admin, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	name := "pf_test_" + sanitize(t.Name())
	if _, err := admin.Exec("DROP SCHEMA IF EXISTS " + name + " CASCADE"); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := admin.Exec("CREATE SCHEMA " + name); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP SCHEMA IF EXISTS " + name + " CASCADE")
		_ = admin.Close()
	})
	sep := "?"
	if strings.Contains(baseDSN, "?") {
		sep = "&"
	}
	return baseDSN + sep + "search_path=" + name
}

// sanitize lower-cases name and replaces every non-[a-z0-9_] rune with '_'
// to produce a safe PostgreSQL identifier.
func sanitize(name string) string {
	lower := strings.ToLower(name)
	var b strings.Builder
	b.Grow(len(lower))
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
