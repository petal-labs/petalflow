-- PostgreSQL schema for PetalFlow tool registration store.
CREATE TABLE IF NOT EXISTS tool_registrations (
    name       TEXT PRIMARY KEY,
    payload    BYTEA NOT NULL,
    updated_at TEXT NOT NULL
);

-- Audit records for the tool lifecycle (tenant-scoped; oldest-first paging).
CREATE TABLE IF NOT EXISTS tool_audit_records (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL DEFAULT '',
    workflow_id  TEXT,
    run_id       TEXT,
    node_id      TEXT,
    actor        TEXT,
    tool         TEXT NOT NULL,
    action_name  TEXT,
    kind         TEXT NOT NULL,
    decision     TEXT,
    reason       TEXT,
    payload_json TEXT,
    created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tool_audit_created
ON tool_audit_records(created_at);

CREATE INDEX IF NOT EXISTS idx_tool_audit_tenant_scoped
ON tool_audit_records(tenant_id, created_at);
