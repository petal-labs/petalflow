-- PostgreSQL schema for PetalFlow event store. Mirrors bus/sqlite_schema.sql.
CREATE TABLE IF NOT EXISTS events (
    id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id    TEXT    NOT NULL,
    seq       BIGINT  NOT NULL,
    kind      TEXT    NOT NULL,
    node_id   TEXT    NOT NULL DEFAULT '',
    node_kind TEXT    NOT NULL DEFAULT '',
    time      TEXT    NOT NULL,
    attempt   BIGINT  NOT NULL DEFAULT 1,
    elapsed   BIGINT  NOT NULL DEFAULT 0,
    payload   TEXT    NOT NULL DEFAULT '{}',
    trace_id  TEXT    NOT NULL DEFAULT '',
    span_id   TEXT    NOT NULL DEFAULT '',
    UNIQUE(run_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_events_run_id ON events (run_id);
CREATE INDEX IF NOT EXISTS idx_events_run_id_seq ON events (run_id, seq);
CREATE INDEX IF NOT EXISTS idx_events_time ON events (time);
