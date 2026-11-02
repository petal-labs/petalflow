package tool

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/internal/sqldialect"

	_ "modernc.org/sqlite"
)

const toolAuditSQLiteSchema = `
CREATE TABLE IF NOT EXISTS tool_audit_records (
	id            TEXT PRIMARY KEY,
	tenant_id     TEXT NOT NULL DEFAULT '',
	workflow_id   TEXT,
	run_id        TEXT,
	node_id       TEXT,
	actor         TEXT,
	tool          TEXT NOT NULL,
	action_name   TEXT,
	kind          TEXT NOT NULL,
	decision      TEXT,
	reason        TEXT,
	payload_json  TEXT,
	created_at    TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tool_audit_created
ON tool_audit_records(created_at);

CREATE INDEX IF NOT EXISTS idx_tool_audit_tenant_scoped
ON tool_audit_records(tenant_id, created_at);
`

// SQLiteAuditStore persists tool audit records in SQLite. Records stay
// tenant-scoped: List never returns another tenant's rows.
type SQLiteAuditStore struct {
	db *sql.DB
}

// NewSQLiteAuditStore opens (or creates) a SQLite-backed audit store.
// It shares the typical PetalFlow database file with registration and
// run stores.
func NewSQLiteAuditStore(cfg SQLiteStoreConfig) (*SQLiteAuditStore, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("tool audit sqlite store dsn is required")
	}
	db, err := sql.Open("sqlite", sqldialect.SQLiteBusyTimeout(cfg.DSN))
	if err != nil {
		return nil, fmt.Errorf("tool audit sqlite store open: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("tool audit sqlite store set WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("tool audit sqlite store set busy timeout: %w", err)
	}
	if _, err := db.Exec(toolAuditSQLiteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("tool audit sqlite store create schema: %w", err)
	}
	return &SQLiteAuditStore{db: db}, nil
}

func (s *SQLiteAuditStore) Append(ctx context.Context, record ToolAuditRecord) error {
	if record.ID == "" {
		return fmt.Errorf("tool audit sqlite store: record id is required")
	}
	payloadJSON := ""
	if len(record.PayloadSummary) > 0 {
		data, err := json.Marshal(record.PayloadSummary)
		if err == nil {
			payloadJSON = string(data)
		}
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO tool_audit_records
	(id, tenant_id, workflow_id, run_id, node_id, actor, tool, action_name, kind, decision, reason, payload_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		return fmt.Errorf("tool audit sqlite store append: %w", err)
	}
	return nil
}

func (s *SQLiteAuditStore) List(ctx context.Context, filter ToolAuditFilter) ([]ToolAuditRecord, string, error) {
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

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("tool audit sqlite store list: %w", err)
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
		return nil, "", fmt.Errorf("tool audit sqlite store list rows: %w", err)
	}

	next := ""
	if filter.Limit > 0 && len(records) > filter.Limit {
		records = records[:filter.Limit]
		next = toolAuditCursor(records[len(records)-1].CreatedAt, records[len(records)-1].ID)
	}
	return records, next, nil
}

func (s *SQLiteAuditStore) Prune(ctx context.Context, filter ToolAuditFilter, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, fmt.Errorf("tool audit sqlite store prune: refusing unbounded delete without a cutoff")
	}
	filter.Before = &before
	filter.Cursor = ""
	// #nosec G201 -- expressions come from fixed toolAuditFilterConditions
	// column placeholders only; values are always parameterized.
	conditions, args := toolAuditFilterConditions(filter)
	res, err := s.db.ExecContext(ctx, "DELETE FROM tool_audit_records WHERE "+strings.Join(conditions, " AND "), args...)
	if err != nil {
		return 0, fmt.Errorf("tool audit sqlite store prune: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("tool audit sqlite store prune affected: %w", err)
	}
	return affected, nil
}

var _ ToolAuditStore = (*SQLiteAuditStore)(nil)

type toolAuditScanner interface {
	Scan(dest ...any) error
}

func scanToolAuditRecord(scanner toolAuditScanner) (ToolAuditRecord, error) {
	var (
		id, tenantID, tool, kind string
		workflow, run, node      sql.NullString
		actor                    sql.NullString
		actionName               sql.NullString
		decision                 sql.NullString
		reason                   sql.NullString
		payloadJSON              sql.NullString
		createdAt                string
	)
	if err := scanner.Scan(&id, &tenantID, &workflow, &run, &node, &actor, &tool, &actionName, &kind, &decision, &reason, &payloadJSON, &createdAt); err != nil {
		return ToolAuditRecord{}, fmt.Errorf("tool audit record scan: %w", err)
	}
	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return ToolAuditRecord{}, fmt.Errorf("tool audit record parse created_at: %w", err)
	}
	record := ToolAuditRecord{
		ID:         id,
		TenantID:   tenantID,
		WorkflowID: workflow.String,
		RunID:      run.String,
		NodeID:     node.String,
		Actor:      actor.String,
		Tool:       tool,
		Action:     actionName.String,
		Kind:       ToolAuditKind(kind),
		Decision:   decision.String,
		Reason:     reason.String,
		CreatedAt:  created,
	}
	if payloadJSON.Valid && strings.TrimSpace(payloadJSON.String) != "" {
		var summary map[string]string
		if err := json.Unmarshal([]byte(payloadJSON.String), &summary); err == nil && len(summary) > 0 {
			record.PayloadSummary = summary
		}
	}
	return record, nil
}

// toolAuditFilterConditions builds the shared selector clauses with the
// tenant scope as unconditional security boundary. Cursors encode
// created_at|id so pages stay deterministic under concurrent appends.
func toolAuditFilterConditions(filter ToolAuditFilter) ([]string, []any) {
	var conditions []string
	var args []any

	if filter.TenantID != "" {
		conditions = append(conditions, "tenant_id = ?")
		args = append(args, filter.TenantID)
	}
	if filter.WorkflowID != "" {
		conditions = append(conditions, "workflow_id = ?")
		args = append(args, filter.WorkflowID)
	}
	if filter.RunID != "" {
		conditions = append(conditions, "run_id = ?")
		args = append(args, filter.RunID)
	}
	if filter.Tool != "" {
		conditions = append(conditions, "tool = ?")
		args = append(args, filter.Tool)
	}
	if filter.Kind != "" {
		conditions = append(conditions, "kind = ?")
		args = append(args, string(filter.Kind))
	}
	if filter.Before != nil {
		conditions = append(conditions, "created_at < ?")
		args = append(args, filter.Before.UTC().Format(time.RFC3339Nano))
	}
	if filter.Cursor != "" {
		if createdAt, id, ok := decodeToolAuditCursor(filter.Cursor); ok {
			conditions = append(conditions, "(created_at > ? OR (created_at = ? AND id > ?))")
			args = append(args,
				createdAt.UTC().Format(time.RFC3339Nano),
				createdAt.UTC().Format(time.RFC3339Nano),
				id)
		}
	}
	return conditions, args
}

// toolAuditCursor encodes the pagination position.
func toolAuditCursor(createdAt time.Time, id string) string {
	return base64.StdEncoding.EncodeToString([]byte(
		fmt.Sprintf("%s|%s", createdAt.UTC().Format(time.RFC3339Nano), id)))
}

func decodeToolAuditCursor(cursor string) (time.Time, string, bool) {
	decorated, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cursor))
	if err != nil {
		return time.Time{}, "", false
	}
	parts := strings.SplitN(string(decorated), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", false
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", false
	}
	return createdAt, parts[1], true
}
