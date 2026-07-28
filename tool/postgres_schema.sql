-- PostgreSQL schema for PetalFlow tool registration store.
CREATE TABLE IF NOT EXISTS tool_registrations (
    name       TEXT PRIMARY KEY,
    payload    BYTEA NOT NULL,
    updated_at TEXT NOT NULL
);
