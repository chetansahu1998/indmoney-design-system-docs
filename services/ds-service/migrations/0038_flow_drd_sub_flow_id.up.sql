-- 0038_flow_drd_sub_flow_id — extend flow_drd so a DRD can be addressed by
-- sub_flow_id in addition to the legacy flow_id PK.
--
-- Plan: docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md
--   U3 — Extend `drd_collab` to key DRDs by sub_flow_id.
--
-- Why a column, not a new table:
--   - The DRD's binary y_doc_state, revision counter, content_json snapshot
--     and audit metadata are already on flow_drd. Adding a parallel
--     drd-by-sub-flow table would split the YDoc state in two and force
--     the autosync + viewer paths to merge writes. Instead, sub_flow_id
--     becomes a secondary key on the existing row.
--   - flow_id stays the primary key. Every existing row keeps its identity;
--     legacy slug-based access (resolveDRDFlowID at drd_collab.go:61)
--     continues to work unchanged.
--   - The column is nullable so the migration adds zero work to existing
--     rows. New DRDs that the PM creates via the sub_flow workflow get
--     sub_flow_id populated at insert time; legacy rows stay NULL until
--     manually relinked (rare).
--
-- Why a partial unique index:
--   - We want at-most-one flow_drd per (tenant, sub_flow_id) when the
--     column is set, but every existing row currently has sub_flow_id =
--     NULL and they must all coexist. SQLite UNIQUE allows multiple NULLs,
--     and the partial filter ensures we only enforce uniqueness on the
--     populated values — matching mig 0036's idx_sub_flow_figma_section
--     pattern.
--
-- No backfill: existing flow_drd rows have no canonical sub_flow mapping
-- (the sub_flow table itself is U1, post-dates these rows). They stay
-- NULL; the legacy slug path resolves them as before.

ALTER TABLE flow_drd ADD COLUMN sub_flow_id TEXT;

-- Sparse-unique mapping. NULLs excluded so legacy rows coexist; populated
-- values are exactly one per (tenant, sub_flow_id).
CREATE UNIQUE INDEX IF NOT EXISTS idx_flow_drd_tenant_sub_flow
    ON flow_drd(tenant_id, sub_flow_id) WHERE sub_flow_id IS NOT NULL;

-- Lookup index for the "load DRD by sub_flow_id within tenant" hot path
-- used by LoadYDocStateBySubFlow / CreateDRDForSubFlow. The unique index
-- above also serves this query, but a non-unique covering index here
-- documents the access pattern explicitly and matches the unique index
-- column order.
CREATE INDEX IF NOT EXISTS idx_flow_drd_sub_flow_lookup
    ON flow_drd(sub_flow_id) WHERE sub_flow_id IS NOT NULL;
