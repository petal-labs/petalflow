//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/petal-labs/petalflow/artifact/artifacttest"
	artifactpostgres "github.com/petal-labs/petalflow/artifact/postgres"
)

func TestStoreConformance(t *testing.T) {
	dsn := isolatedDSN(t)
	store, err := artifactpostgres.NewStore(artifactpostgres.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	artifacttest.RunConformance(t, store)
}

func isolatedDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("PETALFLOW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set PETALFLOW_TEST_POSTGRES_DSN to run PostgreSQL integration tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	name := postgresSchemaName(t.Name())
	if _, err := db.ExecContext(context.Background(), fmt.Sprintf(`CREATE SCHEMA %s`, name)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), fmt.Sprintf(`DROP SCHEMA %s CASCADE`, name)) })
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "search_path=" + name
}

var nonSchemaCharacter = regexp.MustCompile(`[^a-z0-9_]+`)

func postgresSchemaName(testName string) string {
	prefix := nonSchemaCharacter.ReplaceAllString(strings.ToLower(testName), "_")
	prefix = strings.Trim(prefix, "_")
	return fmt.Sprintf("artifact_%s_%d", prefix, time.Now().UnixNano())
}
