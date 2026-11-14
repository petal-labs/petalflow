CREATE TABLE IF NOT EXISTS artifact_blobs (
    digest       TEXT        NOT NULL,
    size         BIGINT      NOT NULL CHECK (size >= 0),
    content_type TEXT        NOT NULL,
    content      BYTEA       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (digest, size, content_type)
);
CREATE TABLE IF NOT EXISTS artifact_entries (
    tenant_id    TEXT        NOT NULL,
    run_id       TEXT        NOT NULL,
    digest       TEXT        NOT NULL,
    size         BIGINT      NOT NULL CHECK (size >= 0),
    content_type TEXT        NOT NULL,
    workflow_id  TEXT        NOT NULL DEFAULT '',
    node_id      TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, run_id, digest, size, content_type),
    FOREIGN KEY (digest, size, content_type)
        REFERENCES artifact_blobs (digest, size, content_type)
        ON DELETE CASCADE
);
