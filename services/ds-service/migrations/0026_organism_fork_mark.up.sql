-- 0026_organism_fork_mark — designer-asserted intentional forks (2026-05-13).
--
-- U9 of the organism-pattern-detection plan
-- (docs/plans/2026-05-13-001-feat-organism-pattern-detection-plan.md).
-- When a designer runs the plugin's "Check selection against DS" command
-- and clicks "Mark as intentional fork", we record their assertion here
-- so subsequent verdict lookups (and the admin dashboard) can surface
-- "this is an intentional design decision, not drift."
--
-- Primary key (tenant_id, frame_id). Subsequent fork marks on the same
-- frame are UPSERTs — the most recent reason wins. The frame_id matches
-- the figma node id (canonical_tree's frame id), same shape used by
-- detected_organism_match.frame_id.
--
-- Why not a column on detected_organism_match: detection rows are
-- version-scoped (re-imports create new rows); the fork assertion is
-- a property of the designer's intent on the frame node, which should
-- survive re-imports. Keeping them in a sibling table avoids the
-- complication of copying the flag forward on every re-import.

CREATE TABLE IF NOT EXISTS organism_fork_mark (
    tenant_id           TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    frame_id            TEXT NOT NULL,
    marked_by_user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    reason              TEXT NOT NULL DEFAULT '',
    marked_at           TEXT NOT NULL,
    PRIMARY KEY (tenant_id, frame_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_organism_fork_mark_tenant
    ON organism_fork_mark (tenant_id, marked_at DESC);
