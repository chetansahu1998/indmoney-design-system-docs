-- 0034_figma_node_metadata — structured per-section descendant store.
--
-- Replaces the JSON `figma_section.subtree_json_zstd` blob (added 0030,
-- never consumed at row-level granularity) with a flat row-per-node table
-- that downstream queries can join, filter, and aggregate without
-- decompressing + parsing JSON.
--
-- One row per Figma node descendant under a SECTION (the section itself
-- is NOT a row in this table; its bbox lives on figma_section). The poller
-- walks `/v1/files/<key>/nodes?ids=<section_ids>&depth=N` per section and
-- writes one row per descendant the response returns. Same data the
-- extraction pipeline already fetches at depth=14 for per-frame canonical
-- trees, but here scoped to the section-subtree axis.
--
-- Coordinate convention:
--   * abs_x / abs_y / width / height come straight from Figma's
--     absoluteBoundingBox — file-space coords, the same frame the audit
--     pipeline renders against.
--   * rel_to_frame_id / rel_x / rel_y are computed at insert time: the
--     nearest FRAME ancestor's id (within the same section) and the
--     offset from that frame's top-left. NULL when the node IS a frame
--     directly under the section (no FRAME parent inside the subtree).
--
-- Layout signal:
--   * layout_mode is Figma's autolayout signal — NONE / HORIZONTAL /
--     VERTICAL. NULL for nodes that don't have one (TEXT, VECTOR, etc.).
--     Designers reading the report distinguish "items in an autolayout
--     row" from "items manually placed on a frame" via this column.
--
-- has_bbox: Figma returns no absoluteBoundingBox on a small set of node
-- types (BOOLEAN_OPERATION children sometimes; INSTANCE swap stubs).
-- We store them with has_bbox=0 and NULL coords so the row stays a
-- complete record of the subtree even when bbox is unknowable.

CREATE TABLE IF NOT EXISTS figma_node_metadata (
    tenant_id       TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    file_key        TEXT NOT NULL,
    page_id         TEXT NOT NULL,
    section_id      TEXT NOT NULL,
    node_id         TEXT NOT NULL,            -- e.g. "12345:6789"
    parent_id       TEXT NOT NULL,            -- node_id of parent; equals section_id for top-level children
    depth           INTEGER NOT NULL,         -- 1 = direct child of section, increases as we descend
    order_index     INTEGER NOT NULL,         -- position in parent.children
    node_type       TEXT NOT NULL,            -- FRAME, GROUP, INSTANCE, COMPONENT, TEXT, VECTOR, RECTANGLE, ELLIPSE, LINE, BOOLEAN_OPERATION, ...
    name            TEXT NOT NULL,
    -- absoluteBoundingBox in file-space coords. NULL when Figma omits it.
    has_bbox        INTEGER NOT NULL DEFAULT 1 CHECK (has_bbox IN (0,1)),
    abs_x           REAL,
    abs_y           REAL,
    width           REAL,
    height          REAL,
    -- Coords relative to the nearest enclosing FRAME (within the section
    -- subtree). NULL when this node IS a top-level FRAME under the section.
    rel_to_frame_id TEXT,
    rel_x           REAL,
    rel_y           REAL,
    -- Figma's autolayout signal. NULL on non-container node types.
    -- GRID arrived with autolayout v2 (2025); future-proof by listing it.
    -- Loaders normalize unknown values to NULL to keep this constraint stable.
    layout_mode     TEXT CHECK (layout_mode IS NULL OR layout_mode IN ('NONE','HORIZONTAL','VERTICAL','GRID')),
    -- Component refs for INSTANCE / COMPONENT nodes. NULL on others.
    component_id    TEXT,
    component_key   TEXT,
    first_seen_at   TEXT NOT NULL,
    last_seen_at    TEXT NOT NULL,
    PRIMARY KEY (tenant_id, file_key, node_id)
) STRICT;

-- Section-scoped walk in depth/order. Drives the "render the section's
-- frame tree" admin view.
CREATE INDEX IF NOT EXISTS idx_figma_node_metadata_section
    ON figma_node_metadata (tenant_id, file_key, section_id, depth, order_index);

-- Parent-scoped lookup for "give me this node's children, in order".
-- Drives the tree-rendering recursion without a full table scan.
CREATE INDEX IF NOT EXISTS idx_figma_node_metadata_parent
    ON figma_node_metadata (tenant_id, file_key, parent_id, order_index);

-- Type-scoped filter ("all INSTANCE nodes in this file") for component
-- usage queries. Small index; node_type cardinality is bounded.
CREATE INDEX IF NOT EXISTS idx_figma_node_metadata_type
    ON figma_node_metadata (tenant_id, file_key, node_type);

-- Component-id index for "where is this component used" cross-file
-- queries. Partial so we don't pay for rows without a component_id.
CREATE INDEX IF NOT EXISTS idx_figma_node_metadata_component
    ON figma_node_metadata (tenant_id, component_id)
    WHERE component_id IS NOT NULL;
