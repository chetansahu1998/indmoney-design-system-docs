-- 0010_admin_acl_search — Phase 7 + 8 schema.
--
-- Phase 7 adds per-flow ACL grants, taxonomy curation tables, and DRD link
-- aliasing. Phase 8 adds the FTS5 virtual table that serves global search.
-- Both phases share this migration because they're shipped together; the
-- combined plan is at:
--   docs/plans/2026-05-01-003-feat-projects-flow-atlas-phase-7-and-8-plan.md
--
-- Forward-only column-add discipline (per 0001 conventions): never NOT NULL
-- without DEFAULT, never DROP COLUMN within the release that stops writing.

-- ─── flow_grants — Phase 7 U1 (R21) ─────────────────────────────────────────
-- Per-flow ACL override on top of the product-default-role model. Resolution
-- rule (services/ds-service/internal/projects/acl.go::ResolveFlowRole):
--
--   effective_role = MAX(product_default_role, flow_grants.role)
--
-- where MAX honours the precedence ladder
--   viewer < commenter < editor < owner < admin.
--
-- Soft-revoke via revoked_at preserves the audit trail; an active grant is
-- one with revoked_at IS NULL.
CREATE TABLE IF NOT EXISTS flow_grants (
    flow_id        TEXT NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id      TEXT NOT NULL,
    role           TEXT NOT NULL,                 -- viewer | commenter | editor | owner
    granted_by     TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    granted_at     TEXT NOT NULL,
    revoked_at     TEXT,
    PRIMARY KEY (flow_id, user_id)
);
-- Lookup-by-user is the dominant read path (search ACL filter, project-list
-- gating) — covered by the partial index that excludes revoked rows.
CREATE INDEX IF NOT EXISTS idx_flow_grants_user
    ON flow_grants(user_id, flow_id) WHERE revoked_at IS NULL;
-- Lookup-by-flow is the access-panel UI — list every grant on a flow.
CREATE INDEX IF NOT EXISTS idx_flow_grants_flow
    ON flow_grants(flow_id, user_id) WHERE revoked_at IS NULL;

-- ─── canonical_taxonomy — Phase 7 U3 (R4) ───────────────────────────────────
-- DS-lead-curated authoritative tree of Product → folder paths. Designer-
-- extended sub-folders sit "below" this (i.e., projects.path strings that
-- aren't in canonical_taxonomy are treated as proposals until promoted).
CREATE TABLE IF NOT EXISTS canonical_taxonomy (
    tenant_id      TEXT NOT NULL,
    product        TEXT NOT NULL,
    path           TEXT NOT NULL,                 -- empty string for the product root
    archived_at    TEXT,
    promoted_by    TEXT REFERENCES users(id) ON DELETE SET NULL,
    promoted_at    TEXT NOT NULL,
    PRIMARY KEY (tenant_id, product, path)
);
CREATE INDEX IF NOT EXISTS idx_canonical_taxonomy_active
    ON canonical_taxonomy(tenant_id, product) WHERE archived_at IS NULL;

-- ─── taxonomy_proposals — Phase 7 U3 + U4 (R4 + R26) ────────────────────────
-- Designer-suggested folder + persona moves awaiting DS-lead approval.
-- payload_json shape varies by kind; see admin_taxonomy.go for the schemas.
CREATE TABLE IF NOT EXISTS taxonomy_proposals (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL,
    kind           TEXT NOT NULL,                 -- folder | persona
    proposed_by    TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    proposed_at    TEXT NOT NULL,
    payload_json   TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending', -- pending | approved | rejected
    reviewed_by    TEXT REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at    TEXT,
    review_note    TEXT
);
-- Pending queue is the hot read path for the admin's inbox.
CREATE INDEX IF NOT EXISTS idx_taxonomy_proposals_pending
    ON taxonomy_proposals(tenant_id, kind, proposed_at)
    WHERE status = 'pending';

-- ─── flow_aliases — Phase 7 U5 (Origin Q3) ──────────────────────────────────
-- When a flow's slug changes (path moved by a designer), prior URLs 301 →
-- the new slug. flow_aliases is append-only; the live slug is always on
-- projects.slug. HandleProjectGet checks here when the requested slug
-- doesn't match an active project row.
CREATE TABLE IF NOT EXISTS flow_aliases (
    slug              TEXT NOT NULL,
    tenant_id         TEXT NOT NULL,
    flow_id           TEXT NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    redirected_to     TEXT NOT NULL,              -- the live slug at write time
    created_at        TEXT NOT NULL,
    PRIMARY KEY (tenant_id, slug)
);
CREATE INDEX IF NOT EXISTS idx_flow_aliases_flow
    ON flow_aliases(flow_id, created_at DESC);

-- ─── search_index_fts — Phase 8 U8 (R23) ────────────────────────────────────
-- FTS5 virtual table backing the global search. Populated by the same
-- RebuildGraphIndex worker (Phase 6) — one write transaction covers both
-- graph_index and search_index_fts. Tokenizer: porter unicode61 for
-- stemming + diacritic folding.
--
-- ACL filtering happens at query time via JOIN against flow_grants in
-- search.go. UNINDEXED columns aren't tokenised but ARE retrievable.
CREATE VIRTUAL TABLE IF NOT EXISTS search_index_fts USING fts5(
    tenant_id UNINDEXED,
    entity_kind UNINDEXED,                       -- flow | drd | decision | persona | component
    entity_id UNINDEXED,
    open_url UNINDEXED,
    title,
    body,
    tokenize = 'porter unicode61'
);
