//go:build integration

package bus

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/runtime"

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
	name := schemaName(t.Name())
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

// schemaNamePrefixMaxLen bounds the readable portion of a generated schema
// name so the full identifier (prefix + separator + hash) always stays
// well under PostgreSQL's 63-byte NAMEDATALEN identifier limit.
const schemaNamePrefixMaxLen = 40

// schemaName builds a PostgreSQL schema identifier from a test name. It
// pairs a truncated, sanitized, human-readable prefix with an 8-hex-digit
// FNV-32a hash of the FULL test name, so uniqueness never depends on the
// name staying under PostgreSQL's 63-byte identifier limit: two test names
// that share a long common prefix (and would otherwise truncate to the
// same schema and collide) still hash to different suffixes.
func schemaName(testName string) string {
	prefix := sanitize(testName)
	if len(prefix) > schemaNamePrefixMaxLen {
		prefix = prefix[:schemaNamePrefixMaxLen]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(testName))
	return fmt.Sprintf("pf_test_%s_%08x", prefix, h.Sum32())
}

// TestPostgresEventStorePruneBeforeTenantScoping covers tenant-scoped
// retention pruning against a real PostgreSQL store.
func TestPostgresEventStorePruneBeforeTenantScoping(t *testing.T) {
	dsn := os.Getenv("PETALFLOW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set PETALFLOW_TEST_POSTGRES_DSN to run postgres integration tests")
	}

	schema := uniqueSchema(t, dsn)
	store, err := NewPostgresEventStore(PostgresStoreConfig{DSN: schema})
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	old := time.Now().Add(-2 * time.Hour)
	appendEvent := func(runID string, seq uint64, tenant string, at time.Time) {
		t.Helper()
		event := makeEvent(runID, seq, runtime.EventNodeStarted)
		event.TenantID = tenant
		event.Time = at
		if err := store.Append(ctx, event); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	appendEvent("run-a", 1, "tenant-a", old)
	appendEvent("run-a", 2, "tenant-a", time.Now())
	appendEvent("run-b", 1, "tenant-b", old)

	removed, err := store.PruneBefore(ctx, time.Now().Add(-time.Hour), "tenant-a")
	if err != nil {
		t.Fatalf("PruneBefore: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	remainingB, _ := store.List(ctx, "run-b", 0, 0)
	if len(remainingB) != 1 {
		t.Errorf("tenant-b events = %d, want 1 (untouched)", len(remainingB))
	}
}
