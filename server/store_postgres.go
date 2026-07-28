package server

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/petal-labs/petalflow/internal/sqldialect"
)

//go:embed postgres_schema.sql
var workflowPostgresSchema string

// PostgresStoreConfig configures the PostgreSQL workflow store.
type PostgresStoreConfig struct {
	DSN string
}

// PostgresStore persists workflow records and schedules in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore opens (or creates) a PostgreSQL-backed workflow store.
func NewPostgresStore(cfg PostgresStoreConfig) (*PostgresStore, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("workflow postgres store: dsn is required")
	}

	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("workflow postgres store: open: %w", err)
	}
	db.SetMaxOpenConns(10)

	if _, err := db.Exec(workflowPostgresSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("workflow postgres store: create schema: %w", err)
	}

	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) List(ctx context.Context) ([]WorkflowRecord, error) {
	rows, err := s.db.QueryContext(ctx, sqldialect.Rebind(`
SELECT id, schema_kind, name, source, compiled, created_at, updated_at
FROM workflows
ORDER BY seq ASC`))
	if err != nil {
		return nil, fmt.Errorf("workflow postgres store: list: %w", err)
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
		return nil, fmt.Errorf("workflow postgres store: list rows: %w", err)
	}

	return records, nil
}

func (s *PostgresStore) Get(ctx context.Context, id string) (WorkflowRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, sqldialect.Rebind(`
SELECT id, schema_kind, name, source, compiled, created_at, updated_at
FROM workflows
WHERE id = ?`), id)

	rec, err := scanWorkflowRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowRecord{}, false, nil
		}
		return WorkflowRecord{}, false, err
	}
	return rec, true, nil
}

func (s *PostgresStore) Create(ctx context.Context, rec WorkflowRecord) error {
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

	_, err = s.db.ExecContext(ctx, sqldialect.Rebind(
		"INSERT INTO workflows (id, schema_kind, name, source, compiled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)"),
		rec.ID,
		string(rec.SchemaKind),
		rec.Name,
		sourceBytes,
		compiled,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if isPostgresUniqueViolation(err) {
			return ErrWorkflowExists
		}
		return fmt.Errorf("workflow postgres store: create: %w", err)
	}
	return nil
}

func (s *PostgresStore) Update(ctx context.Context, rec WorkflowRecord) error {
	sourceBytes := normalizeWorkflowSource(rec.Source)
	compiled, err := marshalCompiledGraph(rec.Compiled)
	if err != nil {
		return err
	}

	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}

	res, err := s.db.ExecContext(ctx, sqldialect.Rebind(
		"UPDATE workflows SET schema_kind = ?, name = ?, source = ?, compiled = ?, created_at = ?, updated_at = ? WHERE id = ?"),
		string(rec.SchemaKind),
		rec.Name,
		sourceBytes,
		compiled,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
		rec.ID,
	)
	if err != nil {
		return fmt.Errorf("workflow postgres store: update: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow postgres store: update affected rows: %w", err)
	}
	if affected == 0 {
		return ErrWorkflowNotFound
	}
	return nil
}

func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, sqldialect.Rebind(`DELETE FROM workflows WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("workflow postgres store: delete: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow postgres store: delete affected rows: %w", err)
	}
	if affected == 0 {
		return ErrWorkflowNotFound
	}
	return nil
}

func (s *PostgresStore) ListSchedules(ctx context.Context, workflowID string) ([]WorkflowSchedule, error) {
	rows, err := s.db.QueryContext(ctx, sqldialect.Rebind(`
SELECT id, workflow_id, cron_expr, enabled, input_json, options_json, next_run_at, last_run_at, last_run_id, last_status, last_error, created_at, updated_at
FROM workflow_schedules
WHERE workflow_id = ?
ORDER BY created_at ASC`), workflowID)
	if err != nil {
		return nil, fmt.Errorf("workflow postgres store: list schedules: %w", err)
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
		return nil, fmt.Errorf("workflow postgres store: list schedules rows: %w", err)
	}
	return schedules, nil
}

func (s *PostgresStore) GetSchedule(ctx context.Context, workflowID, scheduleID string) (WorkflowSchedule, bool, error) {
	row := s.db.QueryRowContext(ctx, sqldialect.Rebind(`
SELECT id, workflow_id, cron_expr, enabled, input_json, options_json, next_run_at, last_run_at, last_run_id, last_status, last_error, created_at, updated_at
FROM workflow_schedules
WHERE workflow_id = ? AND id = ?`), workflowID, scheduleID)

	schedule, err := scanWorkflowSchedule(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowSchedule{}, false, nil
		}
		return WorkflowSchedule{}, false, err
	}
	return schedule, true, nil
}

// CreateSchedule inserts a new workflow schedule. Unlike SQLite (where
// PRAGMA foreign_keys=ON is not reliably enforced across the pool),
// Postgres always enforces the workflow_schedules.workflow_id foreign
// key, so callers must ensure the workflow exists first (as the HTTP
// handler does via workflowExists); a resulting 23503 violation is not
// mapped to a sentinel error here.
func (s *PostgresStore) CreateSchedule(ctx context.Context, schedule WorkflowSchedule) error {
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

	_, err = s.db.ExecContext(ctx, sqldialect.Rebind(`
INSERT INTO workflow_schedules
	(id, workflow_id, cron_expr, enabled, input_json, options_json, next_run_at, last_run_at, last_run_id, last_status, last_error, created_at, updated_at)
VALUES
	(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
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
		if isPostgresUniqueViolation(err) {
			return ErrWorkflowScheduleExists
		}
		return fmt.Errorf("workflow postgres store: create schedule: %w", err)
	}
	return nil
}

func (s *PostgresStore) UpdateSchedule(ctx context.Context, schedule WorkflowSchedule) error {
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

	res, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
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
WHERE workflow_id = ? AND id = ?`),
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
		return fmt.Errorf("workflow postgres store: update schedule: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow postgres store: update schedule affected rows: %w", err)
	}
	if affected == 0 {
		return ErrWorkflowScheduleNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteSchedule(ctx context.Context, workflowID, scheduleID string) error {
	res, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
DELETE FROM workflow_schedules
WHERE workflow_id = ? AND id = ?`), workflowID, scheduleID)
	if err != nil {
		return fmt.Errorf("workflow postgres store: delete schedule: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow postgres store: delete schedule affected rows: %w", err)
	}
	if affected == 0 {
		return ErrWorkflowScheduleNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteSchedulesByWorkflow(ctx context.Context, workflowID string) error {
	if _, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
DELETE FROM workflow_schedules
WHERE workflow_id = ?`), workflowID); err != nil {
		return fmt.Errorf("workflow postgres store: delete schedules by workflow: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]WorkflowSchedule, error) {
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

	rows, err := s.db.QueryContext(ctx, sqldialect.Rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("workflow postgres store: list due schedules: %w", err)
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
		return nil, fmt.Errorf("workflow postgres store: list due schedules rows: %w", err)
	}
	return schedules, nil
}

// Close closes the underlying database connection.
func (s *PostgresStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func isPostgresUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

var _ WorkflowStore = (*PostgresStore)(nil)
var _ WorkflowScheduleStore = (*PostgresStore)(nil)
