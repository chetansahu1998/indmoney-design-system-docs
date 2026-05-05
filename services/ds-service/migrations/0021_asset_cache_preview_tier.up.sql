-- 0021_asset_cache_preview_tier — widen the format CHECK on asset_cache
-- so the preview-pyramid (U1 of plan 2026-05-06-001) can store its four
-- size tiers alongside existing 'png' / 'svg' / 'image-fill' rows.
--
-- Tier ladder mirrors DesignBrain-AI's
--   ImageTileManager.ts:111-130 (tier selection)
--   image_tile_generator.go      (per-tier render)
-- Per-tier longest-edge target: 128 / 512 / 1024 / 2048 px.
-- Selected client-side by `previewMaxDim ≥ frameWidth × zoom × dpr`.
--
-- Rationale for new format values (vs. extending the `scale` column):
--   - `scale` is constrained BETWEEN 1 AND 4 by the 0019 migration; widening it
--     for preview tiers would conflate render quality (the existing scale
--     dimension) with size tier (this new dimension) and break the cache PK.
--   - New format strings keep the PK clean: an explicit `preview-128` row
--     never collides with a future `scale=3` re-render.
--   - The GC sweeper can prefer evicting `preview-128` rows first on disk
--     pressure (cheapest to re-render), without per-format conditional logic.
--
-- SQLite still can't ALTER a CHECK constraint in-place; same recreate-and-
-- copy dance as 0020. PRAGMA foreign_keys is toggled around the swap so the
-- temporary FK gap during DROP/RENAME doesn't trip the FK to tenants.

PRAGMA foreign_keys = OFF;

CREATE TABLE asset_cache_new (
    tenant_id        TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    file_id          TEXT NOT NULL,
    node_id          TEXT NOT NULL,
    -- 'png' / 'svg'        — node renders from /v1/images at scale
    -- 'image-fill'         — raster fill blobs from /v1/files/<key>/images
    -- 'preview-128' .. 'preview-2048'
    --                      — tier rows of the preview pyramid (this migration)
    format           TEXT NOT NULL CHECK (format IN (
                         'png',
                         'svg',
                         'image-fill',
                         'preview-128',
                         'preview-512',
                         'preview-1024',
                         'preview-2048'
                     )),
    scale            INTEGER NOT NULL CHECK (scale BETWEEN 1 AND 4),
    version_index    INTEGER NOT NULL,

    storage_key      TEXT NOT NULL,
    bytes            INTEGER NOT NULL,
    mime             TEXT NOT NULL,
    created_at       TEXT NOT NULL,

    PRIMARY KEY (tenant_id, file_id, node_id, format, scale, version_index)
) STRICT;

INSERT INTO asset_cache_new
SELECT * FROM asset_cache;

DROP TABLE asset_cache;
ALTER TABLE asset_cache_new RENAME TO asset_cache;

CREATE INDEX IF NOT EXISTS idx_asset_cache_tenant
    ON asset_cache (tenant_id);
CREATE INDEX IF NOT EXISTS idx_asset_cache_file_node
    ON asset_cache (file_id, node_id);

PRAGMA foreign_keys = ON;
