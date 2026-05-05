-- 0020_asset_cache_image_fill — widen the format CHECK on asset_cache so
-- raster image fills (Figma `imageRef` blobs proxied from /v1/files/<key>/images)
-- can share the same cache table as node renders.
--
-- The original CHECK was too narrow: `format IN ('png', 'svg')`. Image
-- fills are content-addressed by Figma's imageRef hash and arrive with
-- whatever MIME the underlying upload had — usually image/png, image/jpeg,
-- image/webp, or image/gif. Keeping all of them in one cache table lets
-- the GC sweeper, per-tenant size budget, and serve handler share one
-- code path; the only thing that differs across formats is the file
-- extension on disk.
--
-- SQLite can't ALTER a CHECK constraint in-place (until 3.35 added support
-- for DROP COLUMN, table-level CHECKs still need the rebuild dance), so
-- we create a new table, copy rows, drop the old, rename, and re-add the
-- two indexes from migration 0019.

PRAGMA foreign_keys = OFF;

CREATE TABLE asset_cache_new (
    tenant_id        TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    file_id          TEXT NOT NULL,
    node_id          TEXT NOT NULL,
    -- 'png' / 'svg' — node renders from /v1/images
    -- 'image-fill'   — raster fill blobs from /v1/files/<key>/images,
    --                  keyed by Figma imageRef. node_id holds the
    --                  imageRef hash (32 hex chars) for these rows.
    format           TEXT NOT NULL CHECK (format IN ('png', 'svg', 'image-fill')),
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
