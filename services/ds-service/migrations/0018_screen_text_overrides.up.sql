-- 0018_screen_text_overrides — U1 of plan 2026-05-05-002 (Zeplin-grade leaf canvas).
--
-- Designers edit text content directly on the reconstructed leaf canvas.
-- Each edit lands here as an override on a specific TEXT atomic node inside
-- a screen, anchored to the Figma node-id (primary) with canonical_path and
-- last_seen_original_text as fallback fingerprints used during 5-min
-- sheet-sync re-imports (see U3 for the re-attachment logic).
--
-- Storage philosophy: dedicated side-table (not a JSON column on `screens`),
-- mirroring the `flow_drd` precedent. Read paths are independent of screens —
-- search reindex, activity feed, CSV export, and the per-leaf "Copy
-- overrides" inspector tab all query this table directly.
--
-- Optimistic concurrency: callers PUT with `expected_revision`; mismatch
-- returns 409 with the current revision. Last-write-wins for v1.
--
-- Status enum:
--   'active'   — the override applies; renderer + search + CSV all use `value`.
--   'orphaned' — re-import couldn't re-anchor by node-id, path, or fingerprint.
--                Surfaced in the Copy Overrides inspector tab for manual
--                re-attachment or deletion. The override stays in the table
--                so the value isn't lost when a designer fixes their Figma
--                file and re-imports.

CREATE TABLE IF NOT EXISTS screen_text_overrides (
    id                          TEXT PRIMARY KEY,
    tenant_id                   TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    screen_id                   TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,

    -- Primary anchor — Figma's stable node id (e.g. "2882:56635"). Survives
    -- reorders, padding tweaks, and most structural edits.
    figma_node_id               TEXT NOT NULL,

    -- Secondary anchor — dot-separated path through canonical_tree
    -- (e.g. "0.children.2.children.4"). Used when figma_node_id changes
    -- (delete + recreate) but the structural slot is the same.
    canonical_path              TEXT NOT NULL,

    -- Tertiary anchor — fingerprint of the pre-override `characters` value
    -- on the source node. Used when both node-id and path miss but a unique
    -- TEXT node in the new tree carries the same original text.
    last_seen_original_text     TEXT NOT NULL,

    -- The override value the renderer + search + CSV all consume.
    value                       TEXT NOT NULL,

    -- Optimistic-concurrency token. PUT increments by 1 per successful write.
    revision                    INTEGER NOT NULL DEFAULT 1,

    status                      TEXT NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active', 'orphaned')),

    updated_by_user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    updated_at                  TEXT NOT NULL
) STRICT;

-- One override per (screen, node). Re-edits update the existing row.
CREATE UNIQUE INDEX IF NOT EXISTS idx_screen_text_overrides_unique
    ON screen_text_overrides (screen_id, figma_node_id);

-- Per-tenant scans: orphan dashboard, active-override search reindex driver.
CREATE INDEX IF NOT EXISTS idx_screen_text_overrides_tenant_status
    ON screen_text_overrides (tenant_id, status);

-- Per-screen scans: re-attachment loop in pipeline Stage 6 + per-screen
-- inspector list in the Copy Overrides tab.
CREATE INDEX IF NOT EXISTS idx_screen_text_overrides_tenant_screen
    ON screen_text_overrides (tenant_id, screen_id);
