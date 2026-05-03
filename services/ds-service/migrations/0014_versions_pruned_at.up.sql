-- 0014_versions_pruned_at — T6 (audit follow-up plan 2026-05-03-001).
--
-- Track which version_ids have had their on-disk PNG cache pruned by the
-- retention sweeper so the sweeper can skip already-pruned versions on
-- subsequent passes (idempotent + cheap to re-run).
--
-- Pre-T6 every export wrote a fresh `data/screens/<tenant>/<version>/`
-- directory and nothing ever cleaned them up. Re-export the same project
-- 10 times → 10 directories × ~250 MB each. Fly's volume fills up
-- silently. T6 retains the N most-recent view_ready versions per project
-- (default 3, configurable via VERSION_RETENTION env) and removes the
-- on-disk dir for everything older. SQLite rows stay — the screens table
-- still holds metadata; only the rendered PNGs (regenerable from Figma)
-- are reclaimed.
--
-- pruned_at is NULL for versions whose PNG dir is still on disk (current
-- state for every existing row) and set to the RFC3339 timestamp once the
-- sweeper has reclaimed it.

ALTER TABLE project_versions ADD COLUMN pruned_at TEXT;

-- Speed up the sweeper's "find pruneable rows" scan. Only rows with
-- a fixed status and NULL pruned_at qualify; the partial index keeps
-- the index small even after most rows transition to pruned.
CREATE INDEX IF NOT EXISTS idx_project_versions_prunable
    ON project_versions(project_id, version_index)
    WHERE pruned_at IS NULL AND status IN ('view_ready', 'failed');
