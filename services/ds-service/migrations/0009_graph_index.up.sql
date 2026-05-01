-- 0009_graph_index — Phase 6 single materialised graph index.
--
-- The mind-graph (`/atlas`) needs a unified read surface across products /
-- folders / flows / personas / components / tokens / decisions plus four edge
-- classes (hierarchy, uses, binds-to, supersedes). Phases 1–5 store these
-- across multiple tables (projects, flows, personas, decisions) plus two
-- file-system sources (public/icons/glyph/manifest.json,
-- lib/tokens/indmoney/{base,semantic,semantic-dark}.json) plus per-screen
-- canonical_tree BLOBs. Aggregating on every request would be expensive
-- (≥800ms cold) so Phase 6 inverts the cost: a `RebuildGraphIndex` worker
-- materialises the full graph into this single table, and the read path is
-- one indexed SELECT.
--
-- See docs/plans/2026-05-02-002-feat-projects-flow-atlas-phase-6-plan.md for
-- the full design rationale (Data Model section). Forward-only column-add
-- discipline applies (per 0001 conventions): never NOT NULL without DEFAULT,
-- never DROP COLUMN within the release that stops writing it.

-- ─── graph_index ────────────────────────────────────────────────────────────
-- One row per node in the mind graph. Hierarchy edge stored as parent_id
-- (indexed). Three satellite edge classes stored as JSON arrays on the source
-- node row. Severity counts + persona count + last-edit metadata denormalised
-- inline so the hover card is zero-round-trip.
--
-- Composite PK (id, tenant_id, platform):
--   - `id` is a typed string key like "product:indian-stocks", "flow:flow_abc",
--     "component:button-cta", "token:colour.surface.button-cta",
--     "decision:dec_xyz". The type prefix matches the `type` column.
--   - `tenant_id` partitions the index per tenant; populated by the rebuild
--     worker from the source row's tenant_id (or, for org-wide personas,
--     duplicated per tenant by the worker).
--   - `platform` (R25) — every node is mobile- or web-tagged. Component and
--     token nodes are platform-agnostic in source; the worker writes one row
--     per (tenant, platform) for PK alignment with the rest of the index.
CREATE TABLE IF NOT EXISTS graph_index (
    -- Identity
    id              TEXT NOT NULL,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    platform        TEXT NOT NULL,                        -- mobile | web

    -- Type + classification
    type            TEXT NOT NULL,                        -- product | folder | flow | persona | component | token | decision
    label           TEXT NOT NULL,

    -- Hierarchy edge — denormalised, indexed; the most common edge class
    parent_id       TEXT,                                 -- NULL for top-level (products); composite-FK by convention not constraint

    -- Satellite edges — out-only JSON arrays of node ids. NULL or '[]' when none.
    edges_uses_json         TEXT,                         -- flow → components, persona → flows
    edges_binds_to_json     TEXT,                         -- component → tokens (manifest variants[].fills[].bound_variable_id)
    edges_supersedes_json   TEXT,                         -- decision → decision (mirrors decisions.supersedes_id)

    -- Signal payload — denormalised for hover card; zero round-trips at render time
    severity_critical       INTEGER NOT NULL DEFAULT 0,
    severity_high           INTEGER NOT NULL DEFAULT 0,
    severity_medium         INTEGER NOT NULL DEFAULT 0,
    severity_low            INTEGER NOT NULL DEFAULT 0,
    severity_info           INTEGER NOT NULL DEFAULT 0,
    persona_count           INTEGER NOT NULL DEFAULT 0,
    last_updated_at         TEXT NOT NULL,
    last_editor             TEXT,
    open_url                TEXT,                         -- CTA destination ("Open project →" link)

    -- Source pointer — rebuild worker uses these for incremental updates + cache-bust
    source_kind     TEXT NOT NULL,                        -- projects | flows | personas | decisions | manifest | tokens | derived
    source_ref      TEXT NOT NULL,                        -- canonical PK from source: project.id | flow.id | decision.id | manifest slug | token name

    -- Refresh tracking — the latest materialized_at across a tenant slice is
    -- the SSE cache_key the channel emits to the frontend.
    materialized_at TEXT NOT NULL,

    PRIMARY KEY (id, tenant_id, platform)
);

-- Read path — one indexed SELECT per request: WHERE tenant_id=? AND platform=?
-- The compound index covers the predicate; type lets the planner short-cut
-- per-type filters in the rare cases the handler wants to slice (e.g. for
-- diagnostics or admin).
CREATE INDEX IF NOT EXISTS idx_graph_index_tenant_platform_type
    ON graph_index(tenant_id, platform, type);

-- Hierarchy traversal — "all children of this parent" is a common query in
-- the LOD culler (U13). Partial index excludes top-level rows (parent_id IS
-- NULL) so the index stays compact.
CREATE INDEX IF NOT EXISTS idx_graph_index_parent
    ON graph_index(tenant_id, parent_id) WHERE parent_id IS NOT NULL;

-- Worker incremental update — "find the row(s) backed by this source_ref" is
-- the hot path during SSE-driven re-derivation.
CREATE INDEX IF NOT EXISTS idx_graph_index_source
    ON graph_index(source_kind, source_ref);
