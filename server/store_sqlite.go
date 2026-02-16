package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/loader"

	_ "modernc.org/sqlite"
)

const workflowSQLiteSchema = `
CREATE TABLE IF NOT EXISTS workflows (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL UNIQUE,
	schema_kind TEXT NOT NULL,
	name TEXT,
	source BLOB NOT NULL,
	compiled BLOB,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`

// SQLiteStoreConfig configures the SQLite workflow store.
type SQLiteStoreConfig struct {
	DSN string
}

// SQLiteStore persists workflow records in SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens (or creates) a SQLite-backed workflow store.
func NewSQLiteStore(cfg SQLiteStoreConfig) (*SQLiteStore, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("workflow store sqlite dsn is required")
	}

	db, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store open: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("workflow sqlite store set WAL mode: %w", err)
	}

	if _, err := db.Exec(workflowSQLiteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("workflow sqlite store create schema: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) List(ctx context.Context) ([]WorkflowRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, schema_kind, name, source, compiled, created_at, updated_at
FROM workflows
ORDER BY seq ASC`)
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store list: %w", err)
	}
	defer rows.Close()

	var records []WorkflowRecord
	for rows.Next() {
		rec, err := scanWorkflowRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow sqlite store list rows: %w", err)
	}

	return records, nil
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (WorkflowRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, schema_kind, name, source, compiled, created_at, updated_at
FROM workflows
WHERE id = ?`, id)

	rec, err := scanWorkflowRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowRecord{}, false, nil
		}
		return WorkflowRecord{}, false, err
	}
	return rec, true, nil
}

func (s *SQLiteStore) Create(ctx context.Context, rec WorkflowRecord) error {
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = rec.CreatedAt
	}

	compiled, err := marshalCompiledGraph(rec.Compiled)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO workflows (id, schema_kind, name, source, compiled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rec.ID,
		string(rec.SchemaKind),
		rec.Name,
		[]byte(rec.Source),
		compiled,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if isWorkflowSQLiteUniqueViolation(err) {
			return ErrWorkflowExists
		}
		return fmt.Errorf("workflow sqlite store create: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Update(ctx context.Context, rec WorkflowRecord) error {
	compiled, err := marshalCompiledGraph(rec.Compiled)
	if err != nil {
		return err
	}

	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}

	res, err := s.db.ExecContext(ctx, `
UPDATE workflows
SET schema_kind = ?, name = ?, source = ?, compiled = ?, created_at = ?, updated_at = ?
WHERE id = ?`,
		string(rec.SchemaKind),
		rec.Name,
		[]byte(rec.Source),
		compiled,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
		rec.ID,
	)
	if err != nil {
		return fmt.Errorf("workflow sqlite store update: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow sqlite store update affected rows: %w", err)
	}
	if affected == 0 {
		return ErrWorkflowNotFound
	}
	return nil
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM workflows WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("workflow sqlite store delete: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow sqlite store delete affected rows: %w", err)
	}
	if affected == 0 {
		return ErrWorkflowNotFound
	}
	return nil
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func marshalCompiledGraph(compiled *graph.GraphDefinition) ([]byte, error) {
	if compiled == nil {
		return nil, nil
	}
	data, err := json.Marshal(compiled)
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store marshal compiled graph: %w", err)
	}
	return data, nil
}

type workflowScanner interface {
	Scan(dest ...any) error
}

func scanWorkflowRecord(scanner workflowScanner) (WorkflowRecord, error) {
	var (
		id        string
		kind      string
		name      sql.NullString
		sourceRaw []byte
		compRaw   []byte
		createdAt string
		updatedAt string
	)
	if err := scanner.Scan(&id, &kind, &name, &sourceRaw, &compRaw, &createdAt, &updatedAt); err != nil {
		return WorkflowRecord{}, err
	}

	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return WorkflowRecord{}, fmt.Errorf("workflow sqlite store parse created_at: %w", err)
	}
	updated, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return WorkflowRecord{}, fmt.Errorf("workflow sqlite store parse updated_at: %w", err)
	}

	rec := WorkflowRecord{
		ID:         id,
		SchemaKind: loader.SchemaKind(kind),
		Name:       name.String,
		Source:     json.RawMessage(append([]byte(nil), sourceRaw...)),
		CreatedAt:  created,
		UpdatedAt:  updated,
	}

	if len(compRaw) > 0 {
		var compiled graph.GraphDefinition
		if err := json.Unmarshal(compRaw, &compiled); err != nil {
			return WorkflowRecord{}, fmt.Errorf("workflow sqlite store unmarshal compiled graph: %w", err)
		}
		rec.Compiled = &compiled
	}

	return rec, nil
}

func isWorkflowSQLiteUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed: workflows.id")
}

var _ WorkflowStore = (*SQLiteStore)(nil)
