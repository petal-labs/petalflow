package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/loader"
	storagesqlite "github.com/petal-labs/petalflow/storage/sqlite"
)

const workflowSQLiteMigrationV1 = `
CREATE TABLE IF NOT EXISTS workflows (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  tags_json TEXT NOT NULL DEFAULT '[]',
  source_json TEXT NOT NULL,
  compiled_json TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_workflows_created_at ON workflows (created_at);
`

// SQLiteWorkflowStore persists workflow records in SQLite.
type SQLiteWorkflowStore struct {
	path string
	db   *sql.DB
}

// NewSQLiteWorkflowStore creates a workflow store at the given DB path.
func NewSQLiteWorkflowStore(path string) (*SQLiteWorkflowStore, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" || clean == "." {
		return nil, fmt.Errorf("server: sqlite workflow store path is required")
	}

	db, err := storagesqlite.Open(clean)
	if err != nil {
		return nil, err
	}
	if err := storagesqlite.ApplyMigrations(db, []storagesqlite.Migration{
		{
			Name: "workflows_v1",
			SQL:  workflowSQLiteMigrationV1,
		},
	}); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLiteWorkflowStore{
		path: clean,
		db:   db,
	}, nil
}

// NewDefaultSQLiteWorkflowStore creates a workflow store at the default DB path.
func NewDefaultSQLiteWorkflowStore() (*SQLiteWorkflowStore, error) {
	path, err := storagesqlite.DefaultPath()
	if err != nil {
		return nil, err
	}
	return NewSQLiteWorkflowStore(path)
}

// Path returns the backing DB path.
func (s *SQLiteWorkflowStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Close releases the SQLite handle.
func (s *SQLiteWorkflowStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteWorkflowStore) List(ctx context.Context) ([]WorkflowRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, kind, name, description, tags_json, source_json, compiled_json, created_at, updated_at
FROM workflows
ORDER BY rowid ASC`)
	if err != nil {
		return nil, fmt.Errorf("server: list workflows: %w", err)
	}
	defer rows.Close()

	records := make([]WorkflowRecord, 0)
	for rows.Next() {
		record, err := scanWorkflowRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("server: iterate workflows: %w", err)
	}
	return records, nil
}

func (s *SQLiteWorkflowStore) Get(ctx context.Context, id string) (WorkflowRecord, bool, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT id, kind, name, description, tags_json, source_json, compiled_json, created_at, updated_at
		 FROM workflows WHERE id = ?`,
		id,
	)
	record, err := scanWorkflowRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowRecord{}, false, nil
		}
		return WorkflowRecord{}, false, err
	}
	return record, true, nil
}

func (s *SQLiteWorkflowStore) Create(ctx context.Context, rec WorkflowRecord) error {
	encoded, err := encodeWorkflowRecord(rec)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO workflows
		   (id, kind, name, description, tags_json, source_json, compiled_json, created_at, updated_at)
		 VALUES
		   (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID,
		string(rec.SchemaKind),
		rec.Name,
		rec.Description,
		encoded.tagsJSON,
		encoded.sourceJSON,
		encoded.compiledJSON,
		formatSQLiteTime(rec.CreatedAt),
		formatSQLiteTime(rec.UpdatedAt),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrWorkflowExists
		}
		return fmt.Errorf("server: create workflow %q: %w", rec.ID, err)
	}
	return nil
}

func (s *SQLiteWorkflowStore) Update(ctx context.Context, rec WorkflowRecord) error {
	encoded, err := encodeWorkflowRecord(rec)
	if err != nil {
		return err
	}

	result, err := s.db.ExecContext(
		ctx,
		`UPDATE workflows
		    SET kind = ?,
		        name = ?,
		        description = ?,
		        tags_json = ?,
		        source_json = ?,
		        compiled_json = ?,
		        created_at = ?,
		        updated_at = ?
		  WHERE id = ?`,
		string(rec.SchemaKind),
		rec.Name,
		rec.Description,
		encoded.tagsJSON,
		encoded.sourceJSON,
		encoded.compiledJSON,
		formatSQLiteTime(rec.CreatedAt),
		formatSQLiteTime(rec.UpdatedAt),
		rec.ID,
	)
	if err != nil {
		return fmt.Errorf("server: update workflow %q: %w", rec.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("server: update workflow %q rows affected: %w", rec.ID, err)
	}
	if affected == 0 {
		return ErrWorkflowNotFound
	}
	return nil
}

func (s *SQLiteWorkflowStore) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM workflows WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("server: delete workflow %q: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("server: delete workflow %q rows affected: %w", id, err)
	}
	if affected == 0 {
		return ErrWorkflowNotFound
	}
	return nil
}

type workflowRowScanner interface {
	Scan(dest ...any) error
}

func scanWorkflowRecord(scanner workflowRowScanner) (WorkflowRecord, error) {
	var (
		id          string
		kind        string
		name        string
		description string
		tagsJSON    string
		sourceJSON  string
		compiledRaw sql.NullString
		createdRaw  string
		updatedRaw  string
	)
	if err := scanner.Scan(
		&id,
		&kind,
		&name,
		&description,
		&tagsJSON,
		&sourceJSON,
		&compiledRaw,
		&createdRaw,
		&updatedRaw,
	); err != nil {
		return WorkflowRecord{}, err
	}

	tags := []string{}
	if strings.TrimSpace(tagsJSON) != "" {
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			return WorkflowRecord{}, fmt.Errorf("server: decode workflow tags for %q: %w", id, err)
		}
	}

	var compiled *graph.GraphDefinition
	if compiledRaw.Valid && strings.TrimSpace(compiledRaw.String) != "" {
		var gd graph.GraphDefinition
		if err := json.Unmarshal([]byte(compiledRaw.String), &gd); err != nil {
			return WorkflowRecord{}, fmt.Errorf("server: decode workflow compiled graph for %q: %w", id, err)
		}
		compiled = &gd
	}

	createdAt, err := parseSQLiteTime(createdRaw)
	if err != nil {
		return WorkflowRecord{}, fmt.Errorf("server: parse workflow created_at for %q: %w", id, err)
	}
	updatedAt, err := parseSQLiteTime(updatedRaw)
	if err != nil {
		return WorkflowRecord{}, fmt.Errorf("server: parse workflow updated_at for %q: %w", id, err)
	}

	return WorkflowRecord{
		ID:          id,
		SchemaKind:  loader.SchemaKind(kind),
		Name:        name,
		Description: description,
		Tags:        tags,
		Source:      json.RawMessage(sourceJSON),
		Compiled:    compiled,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}, nil
}

type encodedWorkflowRecord struct {
	tagsJSON     string
	sourceJSON   string
	compiledJSON any
}

func encodeWorkflowRecord(rec WorkflowRecord) (encodedWorkflowRecord, error) {
	tags := rec.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return encodedWorkflowRecord{}, fmt.Errorf("server: encode workflow tags for %q: %w", rec.ID, err)
	}

	sourceJSON := strings.TrimSpace(string(rec.Source))
	if sourceJSON == "" {
		sourceJSON = "{}"
	}

	var compiledJSON any
	if rec.Compiled != nil {
		data, err := json.Marshal(rec.Compiled)
		if err != nil {
			return encodedWorkflowRecord{}, fmt.Errorf("server: encode workflow compiled graph for %q: %w", rec.ID, err)
		}
		compiledJSON = string(data)
	}

	return encodedWorkflowRecord{
		tagsJSON:     string(tagsJSON),
		sourceJSON:   sourceJSON,
		compiledJSON: compiledJSON,
	}, nil
}

var _ WorkflowStore = (*SQLiteWorkflowStore)(nil)
