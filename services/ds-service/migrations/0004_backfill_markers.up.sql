-- 0004_backfill_markers — Phase 2 U9 — sidecar backfill idempotency markers.
--
-- One row per synthetic project the backfill creates. Stores the source
-- sidecar's mtime so re-running the CLI skips unchanged sidecars.
--
-- Empty in fresh installs. Populated by `cmd/migrate-sidecars` only.

CREATE TABLE IF NOT EXISTS backfill_markers (
    project_id      TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    source_path     TEXT NOT NULL,             -- relative path of the sidecar (e.g. "lib/audit/foo.json")
    sidecar_mtime   INTEGER NOT NULL,          -- unix seconds
    last_run_at     TEXT NOT NULL              -- RFC3339
);

CREATE INDEX IF NOT EXISTS idx_backfill_markers_source
    ON backfill_markers(source_path);
