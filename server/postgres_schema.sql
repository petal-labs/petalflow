-- PostgreSQL schema for PetalFlow workflow store. Mirrors server SQLite schema.
CREATE TABLE IF NOT EXISTS workflows (
    seq         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id          TEXT NOT NULL UNIQUE,
    tenant_id   TEXT NOT NULL DEFAULT '',
    schema_kind TEXT NOT NULL,
    name        TEXT,
    source      BYTEA NOT NULL,
    compiled    BYTEA,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workflow_schedules (
    id                   TEXT PRIMARY KEY,
    workflow_id          TEXT NOT NULL,
    tenant_id            TEXT NOT NULL DEFAULT '',
    cron_expr            TEXT NOT NULL,
    enabled              SMALLINT NOT NULL DEFAULT 1,
    input_json           BYTEA NOT NULL,
    options_json         BYTEA NOT NULL,
    next_run_at          TEXT NOT NULL,
    last_run_at          TEXT,
    last_run_id          TEXT,
    last_status          TEXT,
    last_error           TEXT,
    lease_owner          TEXT,
    lease_expires_at     TEXT,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    FOREIGN KEY(workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_workflow
ON workflow_schedules(workflow_id);

CREATE INDEX IF NOT EXISTS idx_workflow_schedules_due
ON workflow_schedules(enabled, next_run_at);

CREATE TABLE IF NOT EXISTS run_jobs (
    id               TEXT PRIMARY KEY,
    workflow_id      TEXT NOT NULL,
    tenant_id        TEXT NOT NULL DEFAULT '',
    trigger_type     TEXT NOT NULL DEFAULT 'api',
    schedule_id      TEXT,
    input_json       BYTEA NOT NULL,
    options_json     BYTEA NOT NULL,
    status           TEXT NOT NULL,
    attempt          INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 3,
    last_error       TEXT,
    next_attempt_at  TEXT NOT NULL,
    lease_owner      TEXT,
    lease_expires_at TEXT,
    run_id           TEXT,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_run_jobs_claim
ON run_jobs(status, next_attempt_at);

CREATE INDEX IF NOT EXISTS idx_run_jobs_updated
ON run_jobs(updated_at);
