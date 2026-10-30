package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/internal/sqldialect"
	"github.com/petal-labs/petalflow/loader"
	"github.com/petal-labs/petalflow/tool"

	_ "modernc.org/sqlite"
)

const workflowSQLiteSchema = `
CREATE TABLE IF NOT EXISTS workflows (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL UNIQUE,
	tenant_id TEXT NOT NULL DEFAULT '',
	schema_kind TEXT NOT NULL,
	name TEXT,
	source BLOB NOT NULL,
	compiled BLOB,
	tool_policy BLOB,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workflow_schedules (
	id TEXT PRIMARY KEY,
	workflow_id TEXT NOT NULL,
	tenant_id TEXT NOT NULL DEFAULT '',
	cron_expr TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	input_json BLOB NOT NULL,
	options_json BLOB NOT NULL,
	next_run_at TEXT NOT NULL,
	last_run_at TEXT,
	last_run_id TEXT,
	last_status TEXT,
	last_error TEXT,
	lease_owner TEXT,
	lease_expires_at TEXT,
	consecutive_failures INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY(workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_workflow
ON workflow_schedules(workflow_id);

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_due
ON workflow_schedules(enabled, next_run_at);

CREATE TABLE IF NOT EXISTS tenant_tool_policies (
	tenant_id TEXT PRIMARY KEY,
	policy BLOB NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

` + runJobsSQLiteSchema

// scheduleSelectColumns is the canonical workflow_schedules column list,
// including the distributed lease columns.
const scheduleSelectColumns = `id, workflow_id, cron_expr, enabled, input_json, options_json, next_run_at, last_run_at, last_run_id, last_status, last_error, created_at, updated_at, tenant_id, lease_owner, lease_expires_at, consecutive_failures`

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

	db, err := sql.Open("sqlite", sqldialect.SQLiteBusyTimeout(cfg.DSN))
	if err != nil {
		return nil, fmt.Errorf("workflow sqlite store open: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("workflow sqlite store set WAL mode: %w", err)
	}
	// Concurrent writers (worker claims, scheduler leases) must queue
	// for the write lock instead of failing fast with SQLITE_BUSY.
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("workflow sqlite store set busy timeout: %w", err)
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
	for table := range map[string]bool{"workflows": true, "workflow_schedules": true} {
		columns, err := sqliteTableColumns(db, table)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		if !columns["tenant_id"] {
			if _, err := db.Exec("ALTER TABLE " + table + " ADD COLUMN tenant_id TEXT NOT NULL DEFAULT ''"); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("workflow sqlite store add tenant column: %w", err)
			}
		}
	}
	scheduleColumns, err := sqliteTableColumns(db, "workflow_schedules")
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	for _, addition := range []struct{ column, ddl string }{
		{column: "lease_owner", ddl: "ALTER TABLE workflow_schedules ADD COLUMN lease_owner TEXT"},
		{column: "lease_expires_at", ddl: "ALTER TABLE workflow_schedules ADD COLUMN lease_expires_at TEXT"},
		{column: "consecutive_failures", ddl: "ALTER TABLE workflow_schedules ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0"},
	} {
		if scheduleColumns[addition.column] {
			continue
		}
		if _, err := db.Exec(addition.ddl); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("workflow sqlite store add schedule column %s: %w", addition.column, err)
		}
	}
	workflowColumns, err := sqliteTableColumns(db, "workflows")
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if !workflowColumns["tool_policy"] {
		if _, err := db.Exec("ALTER TABLE workflows ADD COLUMN tool_policy BLOB"); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("workflow sqlite store add tool_policy column: %w", err)
		}
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
SELECT id, schema_kind, name, source, compiled, tool_policy, created_at, updated_at, tenant_id
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
SELECT id, schema_kind, name, source, compiled, tool_policy, created_at, updated_at, tenant_id
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
	if _, err := s.db.ExecContext(ctx, "UPDATE workflows SET tenant_id = ?, tool_policy = ? WHERE id = ?", rec.TenantID, toolPolicyJSON(rec.Policy), rec.ID); err != nil {
		return fmt.Errorf("workflow sqlite store set tenant and policy: %w", err)
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
	if _, err := s.db.ExecContext(ctx, "UPDATE workflows SET tenant_id = ?, tool_policy = ? WHERE id = ?", rec.TenantID, toolPolicyJSON(rec.Policy), rec.ID); err != nil {
		return fmt.Errorf("workflow sqlite store set tenant and policy: %w", err)
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
SELECT `+scheduleSelectColumns+`
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
SELECT `+scheduleSelectColumns+`
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
	(id, workflow_id, tenant_id, cron_expr, enabled, input_json, options_json, next_run_at, last_run_at, last_run_id, last_status, last_error, lease_owner, lease_expires_at, consecutive_failures, created_at, updated_at)
VALUES
	(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		schedule.ID,
		schedule.WorkflowID,
		schedule.TenantID,
		schedule.Cron,
		enabled,
		inputJSON,
		optionsJSON,
		schedule.NextRunAt.UTC().Format(time.RFC3339Nano),
		formatNullableTime(schedule.LastRunAt),
		nullIfEmpty(schedule.LastRunID),
		nullIfEmpty(schedule.LastStatus),
		nullIfEmpty(schedule.LastError),
		nullIfEmpty(schedule.LeaseOwner),
		formatNullableTime(schedule.LeaseExpiresAt),
		schedule.ConsecutiveFailures,
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
	lease_owner = ?,
	lease_expires_at = ?,
	consecutive_failures = ?,
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
		nullIfEmpty(schedule.LeaseOwner),
		formatNullableTime(schedule.LeaseExpiresAt),
		schedule.ConsecutiveFailures,
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
SELECT ` + scheduleSelectColumns + `
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
		policyRaw []byte
		createdAt string
		updatedAt string
		tenantID  string
	)
	if err := scanner.Scan(&id, &kind, &name, &sourceRaw, &compRaw, &policyRaw, &createdAt, &updatedAt, &tenantID); err != nil {
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
		TenantID:   tenantID,
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

	if len(policyRaw) > 0 {
		var policy tool.ToolPolicy
		if err := json.Unmarshal(policyRaw, &policy); err != nil {
			return WorkflowRecord{}, fmt.Errorf("workflow sqlite store unmarshal tool policy: %w", err)
		}
		rec.Policy = &policy
	}

	return rec, nil
}

func toolPolicyJSON(policy *tool.ToolPolicy) []byte {
	if policy == nil {
		return nil
	}
	data, err := json.Marshal(*policy)
	if err != nil {
		// ToolPolicy contains only JSON-safe fields, so marshalling
		// cannot fail; treat any error as unset to keep writes flowing.
		return nil
	}
	return data
}

func (s *SQLiteStore) GetTenantPolicy(ctx context.Context, tenantID string) (TenantPolicyRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, policy, created_at, updated_at
FROM tenant_tool_policies
WHERE tenant_id = ?`, tenantID)
	return scanTenantPolicyRecord(row)
}

func (s *SQLiteStore) UpsertTenantPolicy(ctx context.Context, rec TenantPolicyRecord) (TenantPolicyRecord, error) {
	if err := rec.Policy.Validate(); err != nil {
		return TenantPolicyRecord{}, fmt.Errorf("workflow store tenant policy validation: %w", err)
	}

	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	rec.Policy = rec.Policy.Normalized()

	stored, found, err := s.GetTenantPolicy(ctx, rec.TenantID)
	if err != nil {
		return TenantPolicyRecord{}, err
	}
	if found {
		prev := stored.Policy
		if !tenantPolicyContentChanged(prev, rec.Policy) {
			rec.Policy.Version = prev.Version
		} else {
			rec.Policy.Version = prev.Version + 1
		}
		rec.CreatedAt = stored.CreatedAt
	} else {
		rec.Policy.Version = 1
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO tenant_tool_policies (tenant_id, policy, created_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(tenant_id) DO UPDATE
SET policy = excluded.policy,
    updated_at = excluded.updated_at`,
		rec.TenantID, toolPolicyJSON(&rec.Policy), rec.CreatedAt.UTC().Format(time.RFC3339Nano), rec.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return TenantPolicyRecord{}, fmt.Errorf("workflow sqlite store upsert tenant policy: %w", err)
	}
	return rec, nil
}

func (s *SQLiteStore) DeleteTenantPolicy(ctx context.Context, tenantID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tenant_tool_policies WHERE tenant_id = ?`, tenantID)
	if err != nil {
		return fmt.Errorf("workflow sqlite store delete tenant policy: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow sqlite store delete tenant policy affected rows: %w", err)
	}
	if affected == 0 {
		return ErrTenantPolicyNotFound
	}
	return nil
}

func scanTenantPolicyRecord(scanner workflowScanner) (TenantPolicyRecord, bool, error) {
	var (
		tenantID  string
		policyRaw []byte
		createdAt string
		updatedAt string
	)
	if err := scanner.Scan(&tenantID, &policyRaw, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TenantPolicyRecord{}, false, nil
		}
		return TenantPolicyRecord{}, false, err
	}

	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return TenantPolicyRecord{}, false, fmt.Errorf("workflow sqlite store parse tenant policy created_at: %w", err)
	}
	updated, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return TenantPolicyRecord{}, false, fmt.Errorf("workflow sqlite store parse tenant policy updated_at: %w", err)
	}

	policy, err := unmarshalTenantPolicy(policyRaw)
	if err != nil {
		return TenantPolicyRecord{}, false, err
	}
	return TenantPolicyRecord{TenantID: tenantID, Policy: policy, CreatedAt: created, UpdatedAt: updated}, true, nil
}

func unmarshalTenantPolicy(raw []byte) (tool.ToolPolicy, error) {
	var policy tool.ToolPolicy
	if len(raw) == 0 {
		return policy, nil
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		return tool.ToolPolicy{}, fmt.Errorf("unmarshal tenant policy: %w", err)
	}
	return policy, nil
}

// tenantPolicyContentChanged compares policies ignoring Version and
// UpdatedAt so a byte-equal re-upsert does not churn the version.
func tenantPolicyContentChanged(a, b tool.ToolPolicy) bool {
	a.Version, b.Version = 0, 0
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	return !reflect.DeepEqual(a.Normalized(), b.Normalized())
}

func scanWorkflowSchedule(scanner scheduleScanner) (WorkflowSchedule, error) {
	var (
		id                  string
		workflowID          string
		cronExpr            string
		enabledRaw          int
		inputRaw            []byte
		optionsRaw          []byte
		nextRunAt           string
		lastRunAt           sql.NullString
		lastRunID           sql.NullString
		lastStatus          sql.NullString
		lastError           sql.NullString
		createdAt           string
		updatedAt           string
		tenantID            string
		leaseOwner          sql.NullString
		leaseExpiresAt      sql.NullString
		consecutiveFailures int
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
		&tenantID,
		&leaseOwner,
		&leaseExpiresAt,
		&consecutiveFailures,
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

	var leaseExpiresPtr *time.Time
	if leaseExpiresAt.Valid && strings.TrimSpace(leaseExpiresAt.String) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, leaseExpiresAt.String)
		if err != nil {
			return WorkflowSchedule{}, fmt.Errorf("workflow sqlite store parse schedule lease_expires_at: %w", err)
		}
		leaseExpiresPtr = &parsed
	}

	return WorkflowSchedule{
		ID:                  id,
		TenantID:            tenantID,
		WorkflowID:          workflowID,
		Cron:                cronExpr,
		Enabled:             enabledRaw == 1,
		Input:               input,
		Options:             options,
		NextRunAt:           next,
		LastRunAt:           lastRunPtr,
		LastRunID:           lastRunID.String,
		LastStatus:          lastStatus.String,
		LastError:           lastError.String,
		LeaseOwner:          leaseOwner.String,
		LeaseExpiresAt:      leaseExpiresPtr,
		ConsecutiveFailures: consecutiveFailures,
		CreatedAt:           created,
		UpdatedAt:           updated,
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
var _ TenantPolicyStore = (*SQLiteStore)(nil)
var _ WorkflowScheduleStore = (*SQLiteStore)(nil)
