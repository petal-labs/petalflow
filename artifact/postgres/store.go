// Package postgres provides a durable PostgreSQL artifact store backend.
package postgres

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/artifact/internal/sqlstore"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schema string

//go:embed indexes.sql
var indexSchema string

var (
	openDatabase       = sql.Open
	initializeDatabase = initialize
)

type Config struct {
	DSN                 string
	MaxSizeBytes        int64
	AllowedContentTypes []string
	DefaultRetention    time.Duration
	Retention           artifact.RetentionPolicy
	PruneInterval       time.Duration
	Logger              *slog.Logger
}

type Store struct {
	*sqlstore.Store
}

func NewStore(config Config) (*Store, error) {
	if config.DSN == "" {
		return nil, errors.New("artifact postgres store: DSN is required")
	}
	db, err := openDatabase("pgx", config.DSN)
	if err != nil {
		return nil, fmt.Errorf("artifact postgres store: open: %w", err)
	}
	db.SetMaxOpenConns(10)
	if err := initializeDatabase(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	store, err := sqlstore.New(db, sqlstore.DialectPostgres, sqlstore.Config{
		MaxSizeBytes: config.MaxSizeBytes, AllowedContentTypes: config.AllowedContentTypes,
		DefaultRetention: config.DefaultRetention, Retention: config.Retention,
		PruneInterval: config.PruneInterval, Logger: config.Logger,
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{Store: store}, nil
}

type executor interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func initialize(db executor) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("artifact postgres store: create schema: %w", err)
	}
	migrations := []string{
		`ALTER TABLE artifact_entries ADD COLUMN IF NOT EXISTS workflow_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE artifact_entries ADD COLUMN IF NOT EXISTS node_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE artifact_entries ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`,
	}
	for _, migration := range migrations {
		if _, err := db.Exec(migration); err != nil {
			return fmt.Errorf("artifact postgres store: migrate schema: %w", err)
		}
	}
	if _, err := db.Exec(indexSchema); err != nil {
		return fmt.Errorf("artifact postgres store: create indexes: %w", err)
	}
	return nil
}
