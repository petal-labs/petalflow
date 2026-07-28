package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/server"
	"github.com/petal-labs/petalflow/tool"
)

type databaseBackend string

const (
	backendSQLite   databaseBackend = "sqlite"
	backendPostgres databaseBackend = "postgres"
)

func detectBackend(dsn string) databaseBackend {
	l := strings.ToLower(strings.TrimSpace(dsn))
	if strings.HasPrefix(l, "postgres://") || strings.HasPrefix(l, "postgresql://") {
		return backendPostgres
	}
	return backendSQLite
}

// flagString reads a string flag's value, returning "" if the flag is not
// registered on cmd. Cobra returns an error for GetString on an unregistered
// flag; that case is treated as "no value" rather than a hard error, since
// different commands register different subsets of these flags.
func flagString(cmd *cobra.Command, name string) string {
	if cmd == nil {
		return ""
	}
	if cmd.Flags().Lookup(name) == nil {
		return ""
	}
	v, _ := cmd.Flags().GetString(name)
	return strings.TrimSpace(v)
}

// resolveDatabaseDSN resolves the database DSN and backend from flags/env.
//
// Precedence (highest first):
//  1. --database-dsn flag
//  2. PETALFLOW_DATABASE_DSN env
//  3. --sqlite-path flag
//  4. --store-path flag
//  5. PETALFLOW_SQLITE_PATH env
//  6. PETALFLOW_TOOLS_STORE_PATH env
//  7. default SQLite path (tool.DefaultSQLitePath())
func resolveDatabaseDSN(cmd *cobra.Command) (string, databaseBackend, string, error) {
	dsn := flagString(cmd, "database-dsn")
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("PETALFLOW_DATABASE_DSN"))
	}
	if dsn == "" {
		dsn = flagString(cmd, "sqlite-path")
	}
	if dsn == "" {
		dsn = flagString(cmd, "store-path")
	}
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("PETALFLOW_SQLITE_PATH"))
	}
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("PETALFLOW_TOOLS_STORE_PATH"))
	}
	if dsn == "" {
		def, err := tool.DefaultSQLitePath()
		if err != nil {
			return "", "", "", fmt.Errorf("resolving default sqlite path: %w", err)
		}
		dsn = def
	}

	backend := detectBackend(dsn)
	scope := dsn
	if backend == backendSQLite && !strings.HasPrefix(strings.ToLower(dsn), "file:") {
		clean := filepath.Clean(dsn)
		dsn, scope = clean, clean
	}
	return dsn, backend, scope, nil
}

// eventStoreCloser is a bus.EventStore that can be closed.
type eventStoreCloser interface {
	bus.EventStore
	io.Closer
}

func openEventStore(dsn string, backend databaseBackend) (eventStoreCloser, error) {
	switch backend {
	case backendPostgres:
		return bus.NewPostgresEventStore(bus.PostgresStoreConfig{DSN: dsn})
	default:
		return bus.NewSQLiteEventStore(bus.SQLiteStoreConfig{DSN: dsn})
	}
}

// workflowStore is the combined workflow + schedule store surface used by serve.
type workflowStore interface {
	server.WorkflowStore
	server.WorkflowScheduleStore
	io.Closer
}

func openWorkflowStore(dsn string, backend databaseBackend) (workflowStore, error) {
	switch backend {
	case backendPostgres:
		return server.NewPostgresStore(server.PostgresStoreConfig{DSN: dsn})
	default:
		return server.NewSQLiteStore(server.SQLiteStoreConfig{DSN: dsn})
	}
}

func openToolStore(dsn string, backend databaseBackend, scope string) (tool.Store, error) {
	switch backend {
	case backendPostgres:
		return tool.NewPostgresStore(tool.PostgresStoreConfig{DSN: dsn, Scope: scope})
	default:
		return tool.NewSQLiteStore(tool.SQLiteStoreConfig{DSN: dsn, Scope: scope})
	}
}

// closeIfCloser closes v if it implements io.Closer, otherwise it is a no-op.
func closeIfCloser(v any) error {
	if c, ok := v.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
