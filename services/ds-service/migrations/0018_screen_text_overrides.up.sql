-- 0018_screen_text_overrides — U1 of plan 2026-05-05-002 (Zeplin-grade leaf canvas).
--
-- Provisions the screen_text_overrides table that backs in-place text editing
-- on the leaf canvas (R4). One row per (screen_id, figma_node_id) pair:
-- designers/PMs override the original Figma copy and we serve the override on
-- subsequent renders.
--
-- Optimistic concurrency uses an integer revision counter (DRD's pattern from
-- 0001_projects_schema):
--   - Client GETs current revision, includes it as expected_revision on PUT.
--   - PUT increments revision atomically; mismatched expected_revision → 409.
--   - SQLite's CURRENT_TIMESTAMP has 1-second resolution, so we MUST use a
--     counter, never updated_at, as the ETag.
--
-- status enum:
--   active   — override is currently anchored to a node in canonical_tree.
--   orphaned — re-import couldn't re-attach via figma_node_id / canonical_path
--              / last_seen_original_text. Surfaced in the Copy Overrides tab
--              for manual reattachment (U11).
--
-- Tenant denormalisation matches every other table created since 0001 — keeps
-- TenantRepo's "every query carries tenant_id" discipline cheap.

CREATE TABLE IF NOT EXISTS screen_text_overrides (
    id                       TEXT PRIMARY KEY,
    tenant_id                TEXT NOT NULL,
    screen_id                TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
    figma_node_id            TEXT NOT NULL,
    canonical_path           TEXT NOT NULL,                    -- path through canonical_tree (re-anchor tier 2)
    last_seen_original_text  TEXT NOT NULL DEFAULT '',         -- pre-edit copy (re-anchor tier 3)
    value                    TEXT NOT NULL,                    -- the override
    revision                 INTEGER NOT NULL DEFAULT 1,
    status                   TEXT NOT NULL DEFAULT 'active'
                              CHECK (status IN ('active', 'orphaned')),
    updated_by_user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    updated_at               TEXT NOT NULL
);

-- One override per (screen, figma_node_id). Upserts collide on this index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_screen_text_overrides_unique
    ON screen_text_overrides(screen_id, figma_node_id);

-- Tenant-scoped status filter (Copy Overrides tab lists active + orphaned).
CREATE INDEX IF NOT EXISTS idx_screen_text_overrides_tenant_status
    ON screen_text_overrides(tenant_id, status);

-- Per-screen list (HandleListOverrides hot path).
CREATE INDEX IF NOT EXISTS idx_screen_text_overrides_tenant_screen
    ON screen_text_overrides(tenant_id, screen_id);
