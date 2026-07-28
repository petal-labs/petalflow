-- PostgreSQL schema for PetalFlow workflow store. Mirrors server SQLite schema.
CREATE TABLE IF NOT EXISTS workflows (
    seq         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id          TEXT NOT NULL UNIQUE,
    schema_kind TEXT NOT NULL,
    name        TEXT,
    source      BYTEA NOT NULL,
    compiled    BYTEA,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workflow_schedules (
    id           TEXT PRIMARY KEY,
    workflow_id  TEXT NOT NULL,
    cron_expr    TEXT NOT NULL,
    enabled      SMALLINT NOT NULL DEFAULT 1,
    input_json   BYTEA NOT NULL,
    options_json BYTEA NOT NULL,
    next_run_at  TEXT NOT NULL,
    last_run_at  TEXT,
    last_run_id  TEXT,
    last_status  TEXT,
    last_error   TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    FOREIGN KEY(workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_workflow
ON workflow_schedules(workflow_id);

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_due
ON workflow_schedules(enabled, next_run_at);
