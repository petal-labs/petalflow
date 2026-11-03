package tool

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/petal-labs/petalflow/internal/sqldialect"
)

// PostgresAuditStore persists tool audit records in PostgreSQL.
// Records stay tenant-scoped: List never returns another tenant's rows.
type PostgresAuditStore struct {
	db *sql.DB
}

// NewPostgresAuditStore opens (or creates) a PostgreSQL-backed audit
// store.
func NewPostgresAuditStore(cfg PostgresAuditStoreConfig) (*PostgresAuditStore, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("tool audit postgres store dsn is required")
	}
	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("tool audit postgres store: open: %w", err)
	}
	db.SetMaxOpenConns(10)

	if _, err := db.Exec(toolAuditPostgresSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("tool audit postgres store: create schema: %w", err)
	}
	return &PostgresAuditStore{db: db}, nil
}

//go:embed audit_postgres_schema.sql
var toolAuditPostgresSchema string

// PostgresAuditStoreConfig configures the audit store.
type PostgresAuditStoreConfig struct {
	DSN string
}

func (s *PostgresAuditStore) Append(ctx context.Context, record ToolAuditRecord) error {
	if record.ID == "" {
		return fmt.Errorf("tool audit postgres store: record id is required")
	}
	payloadJSON := ""
	if len(record.PayloadSummary) > 0 {
		if data, err := json.Marshal(record.PayloadSummary); err == nil {
			payloadJSON = string(data)
		}
	}
	_, err := s.db.ExecContext(ctx, sqldialect.Rebind(`
INSERT INTO tool_audit_records
	(id, tenant_id, workflow_id, run_id, node_id, actor, tool, action_name, kind, decision, reason, payload_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		record.ID,
		record.TenantID,
		record.WorkflowID,
		record.RunID,
		record.NodeID,
		record.Actor,
		record.Tool,
		record.Action,
		string(record.Kind),
		record.Decision,
		record.Reason,
		payloadJSON,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if isAuditPostgresDuplicate(err) {
			// Audit records are at-most-once per id: a duplicated append
			// (redelivery) is not an error.
			return nil
		}
		return fmt.Errorf("tool audit postgres store: append: %w", err)
	}
	return nil
}

func (s *PostgresAuditStore) List(ctx context.Context, filter ToolAuditFilter) ([]ToolAuditRecord, string, error) {
	query := `
SELECT id, tenant_id, workflow_id, run_id, node_id, actor, tool, action_name, kind, decision, reason, payload_json, created_at
FROM tool_audit_records`
	conditions, args := toolAuditFilterConditions(filter)
	if len(conditions) > 0 {
		query += "\nWHERE " + strings.Join(conditions, " AND ")
	}
	query += "\nORDER BY created_at ASC, id ASC"
	if filter.Limit > 0 {
		query += "\nLIMIT ?"
		args = append(args, filter.Limit+1)
	}

	rows, err := s.db.QueryContext(ctx, sqldialect.Rebind(query), args...)
	if err != nil {
		return nil, "", fmt.Errorf("tool audit postgres store: list: %w", err)
	}
	defer rows.Close()

	var records []ToolAuditRecord
	for rows.Next() {
		record, err := scanToolAuditRecord(rows)
		if err != nil {
			return nil, "", err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("tool audit postgres store: list rows: %w", err)
	}

	next := ""
	if filter.Limit > 0 && len(records) > filter.Limit {
		records = records[:filter.Limit]
		next = toolAuditCursor(records[len(records)-1].CreatedAt, records[len(records)-1].ID)
	}
	return records, next, nil
}

func (s *PostgresAuditStore) Prune(ctx context.Context, filter ToolAuditFilter, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, fmt.Errorf("tool audit postgres store: prune: refusing unbounded delete without a cutoff")
	}
	filter.Before = &before
	filter.Cursor = ""
	conditions, args := toolAuditFilterConditions(filter)
	// #nosec G202 -- the WHERE template is built from fixed column-name
	// placeholders in toolAuditFilterConditions; values are parameterized.
	res, err := s.db.ExecContext(ctx, sqldialect.Rebind("DELETE FROM tool_audit_records WHERE "+strings.Join(conditions, " AND ")), args...)
	if err != nil {
		return 0, fmt.Errorf("tool audit postgres store: prune: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("tool audit postgres store: prune affected: %w", err)
	}
	return affected, nil
}

// isAuditPostgresDuplicate reports duplicate-key violations: audit
// appends are at-most-once per record id, so redelivery is not an
// error.
func isAuditPostgresDuplicate(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

var _ ToolAuditStore = (*PostgresAuditStore)(nil)
