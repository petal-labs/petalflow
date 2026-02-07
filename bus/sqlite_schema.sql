-- SQLite schema for PetalFlow event store.
-- Spec section 3.3: events table with UNIQUE(run_id, seq) and indexes.

CREATE TABLE IF NOT EXISTS events (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id    TEXT    NOT NULL,
    seq       INTEGER NOT NULL,
    kind      TEXT    NOT NULL,
    node_id   TEXT    NOT NULL DEFAULT '',
    node_kind TEXT    NOT NULL DEFAULT '',
    time      TEXT    NOT NULL,  -- RFC3339Nano
    attempt   INTEGER NOT NULL DEFAULT 1,
    elapsed   INTEGER NOT NULL DEFAULT 0,  -- nanoseconds
    payload   TEXT    NOT NULL DEFAULT '{}', -- JSON
    trace_id  TEXT    NOT NULL DEFAULT '',
    span_id   TEXT    NOT NULL DEFAULT '',
    UNIQUE(run_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_events_run_id ON events (run_id);
CREATE INDEX IF NOT EXISTS idx_events_run_id_seq ON events (run_id, seq);
CREATE INDEX IF NOT EXISTS idx_events_time ON events (time);
