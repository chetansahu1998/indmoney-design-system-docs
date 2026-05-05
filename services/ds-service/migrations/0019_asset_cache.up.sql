-- 0019_asset_cache — U1 of plan 2026-05-05-002 (Zeplin-grade leaf canvas).
--
-- Server-side cache for Figma /v1/images?ids=… renders consumed by the
-- new asset-export endpoints (U4/U5). Without caching, every per-icon
-- export hits Figma's rate limit (~5 req/sec/PAT) and the bulk-zip flow
-- would saturate within seconds on a real designer's selection.
--
-- Cache key is the full identity of a render: tenant + Figma file +
-- node + format + scale + version_index. The version_index suffix
-- ensures a re-export under a new project version naturally invalidates
-- prior cached blobs without a separate eviction job — old version_index
-- rows are simply never consulted again and can be GC'd by a periodic
-- TTL sweeper later.
--
-- Storage layout mirrors the existing screens.png_storage_key convention
-- under data/assets/<tenant>/<version>/<node_id>.<format> on the Fly volume.
-- The blob bytes are NOT stored in SQLite — `storage_key` is the relative
-- path; the file is read off disk on serve. This keeps the DB small and
-- lets us swap in S3-signed-URL serving in a follow-up without changing
-- this schema.

CREATE TABLE IF NOT EXISTS asset_cache (
    tenant_id        TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    file_id          TEXT NOT NULL,
    node_id          TEXT NOT NULL,
    format           TEXT NOT NULL CHECK (format IN ('png', 'svg')),
    scale            INTEGER NOT NULL CHECK (scale BETWEEN 1 AND 4),
    version_index    INTEGER NOT NULL,

    storage_key      TEXT NOT NULL,    -- relative path under data/assets/...
    bytes            INTEGER NOT NULL, -- size for content-length + budget tracking
    mime             TEXT NOT NULL,    -- "image/png" | "image/svg+xml"
    created_at       TEXT NOT NULL,

    PRIMARY KEY (tenant_id, file_id, node_id, format, scale, version_index)
) STRICT;

-- Tenant-scoped sweep: GC job + per-tenant size accounting.
CREATE INDEX IF NOT EXISTS idx_asset_cache_tenant
    ON asset_cache (tenant_id);

-- Cross-tenant lookups by Figma identity — useful for de-dup across
-- shared design systems where multiple tenants reference the same icon.
CREATE INDEX IF NOT EXISTS idx_asset_cache_file_node
    ON asset_cache (file_id, node_id);
