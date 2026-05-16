-- 0027_figma_node — deep node-tree metadata for FIGMA DB (2026-05-13).
--
-- Phase 1 stored pages + top-level SECTION nodes only (depth=2).
-- This migration extends the inventory to mirror the FULL document
-- tree down to a configurable depth (default 14), capturing only
-- positional + structural metadata. No fills, no text content, no
-- styles, no fontSize — just enough to reason about where things are
-- and which components are instanced where.
--
-- Storage shape per node:
--   id (Figma "X:Y") + parent_id (tree parent) → standard one-parent tree
--   component_id  — populated on INSTANCE nodes; the node_id of the master
--                   COMPONENT they reference (same file)
--   component_key — populated on COMPONENT + COMPONENT_SET nodes; durable
--                   library-side key, stable across publish cycles. Lets us
--                   answer "every INSTANCE of button-primary across all files"
--                   by joining INSTANCE.componentId → master's component_key
--                   via this column.
--
-- Soft-delete via deleted_at on the same crawl-timestamp sweep pattern as
-- figma_page / figma_section. Re-sync of a file marks vanished nodes as
-- deleted_at without losing their history.

CREATE TABLE IF NOT EXISTS figma_node (
    tenant_id       TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    file_key        TEXT NOT NULL,
    node_id         TEXT NOT NULL,             -- Figma node id, "X:Y"
    parent_id       TEXT,                      -- tree parent; NULL for the document node itself
    node_type       TEXT NOT NULL,             -- FRAME / INSTANCE / TEXT / COMPONENT / COMPONENT_SET / SECTION / RECTANGLE / VECTOR / etc.
    name            TEXT NOT NULL,
    -- bbox in absolute canvas coords (Figma's `absoluteBoundingBox`). Nullable
    -- because BOOLEAN-OP and slot-only nodes don't carry geometry.
    x               REAL,
    y               REAL,
    width           REAL,
    height          REAL,
    -- depth from the document root: document=0, page=1, top-level frame=2, ...
    depth           INTEGER NOT NULL CHECK (depth >= 0),
    -- sibling order within parent (preserves layer-panel order in Figma)
    order_index     INTEGER NOT NULL,
    -- INSTANCE → master COMPONENT reference (same-file node_id). Empty
    -- on non-INSTANCE nodes.
    component_id    TEXT,
    -- COMPONENT and COMPONENT_SET → durable library key. Same key shows up
    -- on every INSTANCE that references this master across every file once
    -- we cross-link via component_id → master row → component_key.
    component_key   TEXT,
    first_seen_at   TEXT NOT NULL,
    last_seen_at    TEXT NOT NULL,
    deleted_at      TEXT,
    PRIMARY KEY (tenant_id, file_key, node_id)
) STRICT;

-- Tree-walk queries: "all children of X" or "subtree of X".
CREATE INDEX IF NOT EXISTS idx_figma_node_parent
    ON figma_node (tenant_id, file_key, parent_id, deleted_at);

-- Per-file sweep + ordered tree-render queries (depth-first via depth,
-- then order_index).
CREATE INDEX IF NOT EXISTS idx_figma_node_file_depth
    ON figma_node (tenant_id, file_key, depth, order_index);

-- "Which files instance this component?" — filter by component_key.
-- Partial index keeps it lean since most nodes don't carry a component_key.
CREATE INDEX IF NOT EXISTS idx_figma_node_component_key
    ON figma_node (tenant_id, component_key)
    WHERE component_key IS NOT NULL;

-- Cross-file name search ("find every node named 'Pricing'").
CREATE INDEX IF NOT EXISTS idx_figma_node_name
    ON figma_node (tenant_id, name);

-- Cross-file type aggregations ("count all INSTANCE nodes").
CREATE INDEX IF NOT EXISTS idx_figma_node_type
    ON figma_node (tenant_id, node_type);

-- ─── figma_file: track deep-sync state independently of pages-sync ───────────
--
-- Pages-sync (depth=2) and deep-sync (depth=14) refresh on the same trigger
-- (last_modified moved), but they're written in separate transactions and
-- can advance independently. Phase 2D's webhook receiver may also force a
-- deep-resync without a pages-resync.

ALTER TABLE figma_file ADD COLUMN deep_synced_at TEXT;
ALTER TABLE figma_file ADD COLUMN deep_sync_version TEXT;
ALTER TABLE figma_file ADD COLUMN node_count INTEGER;

-- ─── figma_inventory_run: per-cycle counter ──────────────────────────────────

ALTER TABLE figma_inventory_run ADD COLUMN nodes_upserted INTEGER NOT NULL DEFAULT 0;
