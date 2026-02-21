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
);

CREATE TABLE IF NOT EXISTS workflow_schedules (
	id TEXT PRIMARY KEY,
	workflow_id TEXT NOT NULL,
	cron_expr TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	input_json BLOB NOT NULL,
	options_json BLOB NOT NULL,
	next_run_at TEXT NOT NULL,
	last_run_at TEXT,
	last_run_id TEXT,
	last_status TEXT,
	last_error TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY(workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_workflow
ON workflow_schedules(workflow_id);

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_due
ON workflow_schedules(enabled, next_run_at);`

var workflowInsertQueries = [8]string{
	"INSERT INTO workflows (id, schema_kind, name, source, compiled, created_at, updated_at)\nVALUES (?, ?, ?, ?, ?, ?, ?)",
	"INSERT INTO workflows (id, schema_kind, kind, name, source, compiled, created_at, updated_at)\nVALUES (?, ?, ?, ?, ?, ?, ?, ?)",
	"INSERT INTO workflows (id, schema_kind, name, source, source_json, compiled, created_at, updated_at)\nVALUES (?, ?, ?, ?, ?, ?, ?, ?)",
	"INSERT INTO workflows (id, schema_kind, kind, name, source, source_json, compiled, created_at, updated_at)\nVALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
	"INSERT INTO workflows (id, schema_kind, name, source, compiled, compiled_json, created_at, updated_at)\nVALUES (?, ?, ?, ?, ?, ?, ?, ?)",
	"INSERT INTO workflows (id, schema_kind, kind, name, source, compiled, compiled_json, created_at, updated_at)\nVALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
	"INSERT INTO workflows (id, schema_kind, name, source, source_json, compiled, compiled_json, created_at, updated_at)\nVALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
	"INSERT INTO workflows (id, schema_kind, kind, name, source, source_json, compiled, compiled_json, created_at, updated_at)\nVALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
}

var workflowUpdateQueries = [8]string{
	"UPDATE workflows\nSET schema_kind = ?, name = ?, source = ?, compiled = ?, created_at = ?, updated_at = ?\nWHERE id = ?",
	"UPDATE workflows\nSET schema_kind = ?, kind = ?, name = ?, source = ?, compiled = ?, created_at = ?, updated_at = ?\nWHERE id = ?",
	"UPDATE workflows\nSET schema_kind = ?, name = ?, source = ?, source_json = ?, compiled = ?, created_at = ?, updated_at = ?\nWHERE id = ?",
	"UPDATE workflows\nSET schema_kind = ?, kind = ?, name = ?, source = ?, source_json = ?, compiled = ?, created_at = ?, updated_at = ?\nWHERE id = ?",
	"UPDATE workflows\nSET schema_kind = ?, name = ?, source = ?, compiled = ?, compiled_json = ?, created_at = ?, updated_at = ?\nWHERE id = ?",
	"UPDATE workflows\nSET schema_kind = ?, kind = ?, name = ?, source = ?, compiled = ?, compiled_json = ?, created_at = ?, updated_at = ?\nWHERE id = ?",
	"UPDATE workflows\nSET schema_kind = ?, name = ?, source = ?, source_json = ?, compiled = ?, compiled_json = ?, created_at = ?, updated_at = ?\nWHERE id = ?",
	"UPDATE workflows\nSET schema_kind = ?, kind = ?, name = ?, source = ?, source_json = ?, compiled = ?, compiled_json = ?, created_at = ?, updated_at = ?\nWHERE id = ?",
}

// SQLiteStoreConfig configures the SQLite workflow store.
type SQLiteStoreConfig struct {
	DSN string
}

// SQLiteStore persists workflow records in SQLite.
type SQLiteStore struct {
	db                            *sql.DB
	workflowHasLegacyKind         bool
	workflowHasLegacySourceJSON   bool
	workflowHasLegacyCompiledJSON bool
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
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("workflow sqlite store enable foreign keys: %w", err)
	}

	if _, err := db.Exec(workflowSQLiteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("workflow sqlite store create schema: %w", err)
	}
	if err := migrateLegacyWorkflowSQLiteSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	workflowColumns, err := sqliteTableColumns(db, "workflows")
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLiteStore{
		db:                            db,
		workflowHasLegacyKind:         workflowColumns["kind"],
		workflowHasLegacySourceJSON:   workflowColumns["source_json"],
		workflowHasLegacyCompiledJSON: workflowColumns["compiled_json"],
	}, nil
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

	sourceBytes := normalizeWorkflowSource(rec.Source)
	compiled, err := marshalCompiledGraph(rec.Compiled)
	if err != nil {
		return err
	}
	legacyCompiled := normalizeLegacyWorkflowCompiled(compiled)

	args := []any{rec.ID, string(rec.SchemaKind)}
	if s.workflowHasLegacyKind {
		args = append(args, string(rec.SchemaKind))
	}
	args = append(args, rec.Name, sourceBytes)
	if s.workflowHasLegacySourceJSON {
		args = append(args, sourceBytes)
	}
	args = append(args, compiled)
	if s.workflowHasLegacyCompiledJSON {
		args = append(args, legacyCompiled)
	}
	args = append(args,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	query := workflowInsertQueries[s.workflowLegacyColumnMask()]

	_, err = s.db.ExecContext(ctx, query, args...)
	if err != nil {
		if isWorkflowSQLiteUniqueViolation(err) {
			return ErrWorkflowExists
		}
		return fmt.Errorf("workflow sqlite store create: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Update(ctx context.Context, rec WorkflowRecord) error {
	sourceBytes := normalizeWorkflowSource(rec.Source)
	compiled, err := marshalCompiledGraph(rec.Compiled)
	if err != nil {
		return err
	}
	legacyCompiled := normalizeLegacyWorkflowCompiled(compiled)

	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}

	args := []any{string(rec.SchemaKind)}
	if s.workflowHasLegacyKind {
		args = append(args, string(rec.SchemaKind))
	}
	args = append(args, rec.Name, sourceBytes)
	if s.workflowHasLegacySourceJSON {
		args = append(args, sourceBytes)
	}
	args = append(args, compiled)
	if s.workflowHasLegacyCompiledJSON {
		args = append(args, legacyCompiled)
	}
	args = append(args,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	args = append(args, rec.ID)
	query := workflowUpdateQueries[s.workflowLegacyColumnMask()]

	res, err := s.db.ExecContext(ctx, query, args...)
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

func (s *SQLiteStore) ListSchedules(ctx context.Context, workflowID string) ([]WorkflowSchedule, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workflow_id, cron_expr, enabled, input_json, options_json, next_run_at, last_run_at, last_run_id, last_status, last_error, created_at, updated_at
FROM workflow_schedules
WHERE workflow_id = ?
ORDER BY created_at ASC`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store list schedules: %w", err)
	}
	defer rows.Close()

	var schedules []WorkflowSchedule
	for rows.Next() {
		schedule, err := scanWorkflowSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow sqlite store list schedules rows: %w", err)
	}
	return schedules, nil
}

func (s *SQLiteStore) GetSchedule(ctx context.Context, workflowID, scheduleID string) (WorkflowSchedule, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, workflow_id, cron_expr, enabled, input_json, options_json, next_run_at, last_run_at, last_run_id, last_status, last_error, created_at, updated_at
FROM workflow_schedules
WHERE workflow_id = ? AND id = ?`, workflowID, scheduleID)

	schedule, err := scanWorkflowSchedule(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowSchedule{}, false, nil
		}
		return WorkflowSchedule{}, false, err
	}
	return schedule, true, nil
}

func (s *SQLiteStore) CreateSchedule(ctx context.Context, schedule WorkflowSchedule) error {
	now := time.Now().UTC()
	if schedule.CreatedAt.IsZero() {
		schedule.CreatedAt = now
	}
	if schedule.UpdatedAt.IsZero() {
		schedule.UpdatedAt = schedule.CreatedAt
	}

	inputJSON, err := marshalScheduleInput(schedule.Input)
	if err != nil {
		return err
	}
	optionsJSON, err := marshalScheduleOptions(schedule.Options)
	if err != nil {
		return err
	}

	enabled := 0
	if schedule.Enabled {
		enabled = 1
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO workflow_schedules
	(id, workflow_id, cron_expr, enabled, input_json, options_json, next_run_at, last_run_at, last_run_id, last_status, last_error, created_at, updated_at)
VALUES
	(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		schedule.ID,
		schedule.WorkflowID,
		schedule.Cron,
		enabled,
		inputJSON,
		optionsJSON,
		schedule.NextRunAt.UTC().Format(time.RFC3339Nano),
		formatNullableTime(schedule.LastRunAt),
		nullIfEmpty(schedule.LastRunID),
		nullIfEmpty(schedule.LastStatus),
		nullIfEmpty(schedule.LastError),
		schedule.CreatedAt.UTC().Format(time.RFC3339Nano),
		schedule.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if isWorkflowScheduleSQLiteUniqueViolation(err) {
			return ErrWorkflowScheduleExists
		}
		return fmt.Errorf("workflow sqlite store create schedule: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateSchedule(ctx context.Context, schedule WorkflowSchedule) error {
	if schedule.UpdatedAt.IsZero() {
		schedule.UpdatedAt = time.Now().UTC()
	}

	inputJSON, err := marshalScheduleInput(schedule.Input)
	if err != nil {
		return err
	}
	optionsJSON, err := marshalScheduleOptions(schedule.Options)
	if err != nil {
		return err
	}

	enabled := 0
	if schedule.Enabled {
		enabled = 1
	}

	res, err := s.db.ExecContext(ctx, `
UPDATE workflow_schedules
SET
	cron_expr = ?,
	enabled = ?,
	input_json = ?,
	options_json = ?,
	next_run_at = ?,
	last_run_at = ?,
	last_run_id = ?,
	last_status = ?,
	last_error = ?,
	updated_at = ?
WHERE workflow_id = ? AND id = ?`,
		schedule.Cron,
		enabled,
		inputJSON,
		optionsJSON,
		schedule.NextRunAt.UTC().Format(time.RFC3339Nano),
		formatNullableTime(schedule.LastRunAt),
		nullIfEmpty(schedule.LastRunID),
		nullIfEmpty(schedule.LastStatus),
		nullIfEmpty(schedule.LastError),
		schedule.UpdatedAt.UTC().Format(time.RFC3339Nano),
		schedule.WorkflowID,
		schedule.ID,
	)
	if err != nil {
		return fmt.Errorf("workflow sqlite store update schedule: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow sqlite store update schedule affected rows: %w", err)
	}
	if affected == 0 {
		return ErrWorkflowScheduleNotFound
	}
	return nil
}

func (s *SQLiteStore) DeleteSchedule(ctx context.Context, workflowID, scheduleID string) error {
	res, err := s.db.ExecContext(ctx, `
DELETE FROM workflow_schedules
WHERE workflow_id = ? AND id = ?`, workflowID, scheduleID)
	if err != nil {
		return fmt.Errorf("workflow sqlite store delete schedule: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow sqlite store delete schedule affected rows: %w", err)
	}
	if affected == 0 {
		return ErrWorkflowScheduleNotFound
	}
	return nil
}

func (s *SQLiteStore) DeleteSchedulesByWorkflow(ctx context.Context, workflowID string) error {
	if _, err := s.db.ExecContext(ctx, `
DELETE FROM workflow_schedules
WHERE workflow_id = ?`, workflowID); err != nil {
		return fmt.Errorf("workflow sqlite store delete schedules by workflow: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]WorkflowSchedule, error) {
	query := `
SELECT id, workflow_id, cron_expr, enabled, input_json, options_json, next_run_at, last_run_at, last_run_id, last_status, last_error, created_at, updated_at
FROM workflow_schedules
WHERE enabled = 1 AND next_run_at <= ?
ORDER BY next_run_at ASC`
	args := []any{now.UTC().Format(time.RFC3339Nano)}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store list due schedules: %w", err)
	}
	defer rows.Close()

	var schedules []WorkflowSchedule
	for rows.Next() {
		schedule, err := scanWorkflowSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow sqlite store list due schedules rows: %w", err)
	}
	return schedules, nil
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB returns the underlying database connection for sharing with other stores.
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
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

type scheduleScanner interface {
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

func scanWorkflowSchedule(scanner scheduleScanner) (WorkflowSchedule, error) {
	var (
		id         string
		workflowID string
		cronExpr   string
		enabledRaw int
		inputRaw   []byte
		optionsRaw []byte
		nextRunAt  string
		lastRunAt  sql.NullString
		lastRunID  sql.NullString
		lastStatus sql.NullString
		lastError  sql.NullString
		createdAt  string
		updatedAt  string
	)
	if err := scanner.Scan(
		&id,
		&workflowID,
		&cronExpr,
		&enabledRaw,
		&inputRaw,
		&optionsRaw,
		&nextRunAt,
		&lastRunAt,
		&lastRunID,
		&lastStatus,
		&lastError,
		&createdAt,
		&updatedAt,
	); err != nil {
		return WorkflowSchedule{}, err
	}

	next, err := time.Parse(time.RFC3339Nano, nextRunAt)
	if err != nil {
		return WorkflowSchedule{}, fmt.Errorf("workflow sqlite store parse schedule next_run_at: %w", err)
	}
	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return WorkflowSchedule{}, fmt.Errorf("workflow sqlite store parse schedule created_at: %w", err)
	}
	updated, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return WorkflowSchedule{}, fmt.Errorf("workflow sqlite store parse schedule updated_at: %w", err)
	}

	input, err := unmarshalScheduleInput(inputRaw)
	if err != nil {
		return WorkflowSchedule{}, err
	}
	options, err := unmarshalScheduleOptions(optionsRaw)
	if err != nil {
		return WorkflowSchedule{}, err
	}

	var lastRunPtr *time.Time
	if lastRunAt.Valid && strings.TrimSpace(lastRunAt.String) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, lastRunAt.String)
		if err != nil {
			return WorkflowSchedule{}, fmt.Errorf("workflow sqlite store parse schedule last_run_at: %w", err)
		}
		lastRunPtr = &parsed
	}

	return WorkflowSchedule{
		ID:         id,
		WorkflowID: workflowID,
		Cron:       cronExpr,
		Enabled:    enabledRaw == 1,
		Input:      input,
		Options:    options,
		NextRunAt:  next,
		LastRunAt:  lastRunPtr,
		LastRunID:  lastRunID.String,
		LastStatus: lastStatus.String,
		LastError:  lastError.String,
		CreatedAt:  created,
		UpdatedAt:  updated,
	}, nil
}

func isWorkflowSQLiteUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed: workflows.id")
}

func isWorkflowScheduleSQLiteUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed: workflow_schedules.id")
}

func marshalScheduleInput(input map[string]any) ([]byte, error) {
	if input == nil {
		return []byte(`{}`), nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store marshal schedule input: %w", err)
	}
	return data, nil
}

func unmarshalScheduleInput(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("workflow sqlite store unmarshal schedule input: %w", err)
	}
	if input == nil {
		return map[string]any{}, nil
	}
	return input, nil
}

func marshalScheduleOptions(options RunReqOptions) ([]byte, error) {
	data, err := json.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store marshal schedule options: %w", err)
	}
	if len(data) == 0 {
		return []byte(`{}`), nil
	}
	return data, nil
}

func unmarshalScheduleOptions(raw []byte) (RunReqOptions, error) {
	if len(raw) == 0 {
		return RunReqOptions{}, nil
	}
	var options RunReqOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return RunReqOptions{}, fmt.Errorf("workflow sqlite store unmarshal schedule options: %w", err)
	}
	return options, nil
}

func formatNullableTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func (s *SQLiteStore) workflowLegacyColumnMask() int {
	mask := 0
	if s.workflowHasLegacyKind {
		mask |= 1
	}
	if s.workflowHasLegacySourceJSON {
		mask |= 2
	}
	if s.workflowHasLegacyCompiledJSON {
		mask |= 4
	}
	return mask
}

func normalizeWorkflowSource(raw json.RawMessage) []byte {
	data := []byte(raw)
	if len(data) == 0 {
		return []byte(`{}`)
	}
	return data
}

func normalizeLegacyWorkflowCompiled(compiled []byte) []byte {
	if len(compiled) == 0 {
		return []byte(`{}`)
	}
	return compiled
}

func migrateLegacyWorkflowSQLiteSchema(db *sql.DB) error {
	if db == nil {
		return errors.New("workflow sqlite store db is nil")
	}

	columns, err := sqliteTableColumns(db, "workflows")
	if err != nil {
		return err
	}
	if len(columns) == 0 {
		return nil
	}
	if !columns["id"] {
		return errors.New("workflow sqlite store workflows table missing id column")
	}

	if !columns["seq"] {
		if _, err := db.Exec(`ALTER TABLE workflows ADD COLUMN seq INTEGER`); err != nil {
			return fmt.Errorf("workflow sqlite store add workflows.seq: %w", err)
		}
	}
	if !columns["schema_kind"] {
		if _, err := db.Exec(`ALTER TABLE workflows ADD COLUMN schema_kind TEXT`); err != nil {
			return fmt.Errorf("workflow sqlite store add workflows.schema_kind: %w", err)
		}
	}
	if !columns["name"] {
		if _, err := db.Exec(`ALTER TABLE workflows ADD COLUMN name TEXT`); err != nil {
			return fmt.Errorf("workflow sqlite store add workflows.name: %w", err)
		}
	}
	if !columns["source"] {
		if _, err := db.Exec(`ALTER TABLE workflows ADD COLUMN source BLOB`); err != nil {
			return fmt.Errorf("workflow sqlite store add workflows.source: %w", err)
		}
	}
	if !columns["compiled"] {
		if _, err := db.Exec(`ALTER TABLE workflows ADD COLUMN compiled BLOB`); err != nil {
			return fmt.Errorf("workflow sqlite store add workflows.compiled: %w", err)
		}
	}
	if !columns["created_at"] {
		if _, err := db.Exec(`ALTER TABLE workflows ADD COLUMN created_at TEXT`); err != nil {
			return fmt.Errorf("workflow sqlite store add workflows.created_at: %w", err)
		}
	}
	if !columns["updated_at"] {
		if _, err := db.Exec(`ALTER TABLE workflows ADD COLUMN updated_at TEXT`); err != nil {
			return fmt.Errorf("workflow sqlite store add workflows.updated_at: %w", err)
		}
	}

	// Ensure seq is always populated for older schemas where seq was added later.
	if _, err := db.Exec(`
CREATE TRIGGER IF NOT EXISTS workflows_set_seq_after_insert
AFTER INSERT ON workflows
FOR EACH ROW
WHEN NEW.seq IS NULL
BEGIN
	UPDATE workflows SET seq = NEW.rowid WHERE rowid = NEW.rowid;
END;
`); err != nil {
		return fmt.Errorf("workflow sqlite store create seq trigger: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE workflows SET seq = rowid WHERE seq IS NULL`); err != nil {
		return fmt.Errorf("workflow sqlite store backfill workflows.seq: %w", err)
	}
	if columns["kind"] {
		if _, err := db.Exec(`
UPDATE workflows
SET schema_kind = kind
WHERE (schema_kind IS NULL OR TRIM(schema_kind) = '')
  AND kind IS NOT NULL
  AND TRIM(kind) <> ''`); err != nil {
			return fmt.Errorf("workflow sqlite store backfill workflows.schema_kind from kind: %w", err)
		}
	}
	if columns["source_json"] {
		if _, err := db.Exec(`
UPDATE workflows
SET source = source_json
WHERE source IS NULL
  AND source_json IS NOT NULL`); err != nil {
			return fmt.Errorf("workflow sqlite store backfill workflows.source from source_json: %w", err)
		}
	}
	if columns["compiled_json"] {
		if _, err := db.Exec(`
UPDATE workflows
SET compiled = compiled_json
WHERE compiled IS NULL
  AND compiled_json IS NOT NULL`); err != nil {
			return fmt.Errorf("workflow sqlite store backfill workflows.compiled from compiled_json: %w", err)
		}
	}
	if _, err := db.Exec(`UPDATE workflows SET schema_kind = ? WHERE schema_kind IS NULL OR TRIM(schema_kind) = ''`, string(loader.SchemaKindGraph)); err != nil {
		return fmt.Errorf("workflow sqlite store backfill workflows.schema_kind: %w", err)
	}
	if _, err := db.Exec(`UPDATE workflows SET source = '{}' WHERE source IS NULL`); err != nil {
		return fmt.Errorf("workflow sqlite store backfill workflows.source: %w", err)
	}
	if _, err := db.Exec(`UPDATE workflows SET created_at = ? WHERE created_at IS NULL OR TRIM(created_at) = ''`, now); err != nil {
		return fmt.Errorf("workflow sqlite store backfill workflows.created_at: %w", err)
	}
	if _, err := db.Exec(`UPDATE workflows SET updated_at = created_at WHERE updated_at IS NULL OR TRIM(updated_at) = ''`); err != nil {
		return fmt.Errorf("workflow sqlite store backfill workflows.updated_at: %w", err)
	}

	return nil
}

func sqliteTableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store inspect schema for %s: %w", table, err)
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notNull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("workflow sqlite store scan schema for %s: %w", table, err)
		}
		columns[strings.ToLower(strings.TrimSpace(name))] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow sqlite store schema rows for %s: %w", table, err)
	}

	return columns, nil
}

var _ WorkflowStore = (*SQLiteStore)(nil)
var _ WorkflowScheduleStore = (*SQLiteStore)(nil)
