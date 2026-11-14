CREATE INDEX IF NOT EXISTS idx_artifact_entries_run
    ON artifact_entries (tenant_id, run_id, created_at, digest);
CREATE INDEX IF NOT EXISTS idx_artifact_entries_expiry
    ON artifact_entries (expires_at) WHERE expires_at IS NOT NULL;
