// Package sqlite provides a durable SQLite artifact store backend.
package sqlite

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/petal-labs/petalflow/artifact/internal/sqlstore"
	"github.com/petal-labs/petalflow/internal/sqldialect"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

//go:embed indexes.sql
var indexSchema string

type Config struct {
	DSN                 string
	MaxSizeBytes        int64
	AllowedContentTypes []string
	DefaultRetention    time.Duration
	PruneInterval       time.Duration
	Logger              *slog.Logger
}

type Store struct {
	*sqlstore.Store
}

func NewStore(config Config) (*Store, error) {
	if config.DSN == "" {
		return nil, errors.New("artifact sqlite store: DSN is required")
	}
	db, err := sql.Open("sqlite", sqldialect.SQLiteBusyTimeout(config.DSN))
	if err != nil {
		return nil, fmt.Errorf("artifact sqlite store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := initialize(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	store, err := sqlstore.New(db, sqlstore.DialectSQLite, sqlstore.Config{
		MaxSizeBytes: config.MaxSizeBytes, AllowedContentTypes: config.AllowedContentTypes,
		DefaultRetention: config.DefaultRetention, PruneInterval: config.PruneInterval, Logger: config.Logger,
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{Store: store}, nil
}

func initialize(db *sql.DB) error {
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", schema} {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("artifact sqlite store: initialize: %w", err)
		}
	}
	columns, err := tableColumns(db, "artifact_entries")
	if err != nil {
		return err
	}
	additions := []struct{ name, ddl string }{
		{"workflow_id", `ALTER TABLE artifact_entries ADD COLUMN workflow_id TEXT NOT NULL DEFAULT ''`},
		{"node_id", `ALTER TABLE artifact_entries ADD COLUMN node_id TEXT NOT NULL DEFAULT ''`},
		{"expires_at", `ALTER TABLE artifact_entries ADD COLUMN expires_at TEXT`},
	}
	for _, addition := range additions {
		if columns[addition.name] {
			continue
		}
		if _, err := db.Exec(addition.ddl); err != nil {
			return fmt.Errorf("artifact sqlite store: add %s: %w", addition.name, err)
		}
	}
	if _, err := db.Exec(indexSchema); err != nil {
		return fmt.Errorf("artifact sqlite store: create indexes: %w", err)
	}
	return nil
}

func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, fmt.Errorf("artifact sqlite store: inspect schema: %w", err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("artifact sqlite store: scan schema: %w", err)
		}
		columns[name] = true
	}
	return columns, rows.Err()
}
