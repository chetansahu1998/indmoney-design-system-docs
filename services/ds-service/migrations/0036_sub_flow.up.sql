-- 0036_sub_flow — first-class storage for the {sub_product}/{sub_flow}
-- taxonomy that already exists in Figma section names but lived only as
-- parsed strings (see internal/projects/figma_section_parser.go:41).
--
-- Plan: docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md
--   U1 — Add sub_product and sub_flow tables + repo.
--
-- Why first-class:
--   - DRD authoring (U3) keys by sub_flow_id, not by frame-name string.
--   - PRD typed-stems (U4) hang off sub_flow_id.
--   - Autosync (U2) upserts a sub_flow row when a Figma section first
--     appears, then back-fills figma_section_id when the parser binds.
--   - {sub_product.slug}/{sub_flow.slug} becomes the org-wide universal
--     join key (KTD-6) — Mixpanel event prefix, Storybook story path,
--     Sentry tag, JIRA component.
--
-- Lifecycle ordering note:
--   A DRD can be created BEFORE the Figma section exists. The sub_flow
--   row is born when the PM creates the DRD; figma_section_id stays NULL
--   until autosync sees a matching section name. drd_id stays NULL until
--   U3 wires the back-link. Both nullable columns are sparse-unique
--   (partial unique index on figma_section_id) so we can have many
--   "design pending" sub_flows but at most one sub_flow per figma section.
--
-- Naming rules (mirrored by the repo's slugify + LOWER(TRIM(name))):
--   - Name matching is case-insensitive and whitespace-trimmed
--     (the LOWER(TRIM(name)) unique index enforces this at the DB).
--   - Slug is the lowercased, hyphenated form of the name.
--   - sub_flow names are unique per (tenant_id, sub_product_id) — two
--     sub-flows can share the same name across different sub-products
--     (e.g. "Cold State" under "Wallet" vs "INDstocks").

CREATE TABLE IF NOT EXISTS sub_product (
    id           TEXT NOT NULL,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    slug         TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (tenant_id, id)
) STRICT;

-- Slug is the org-wide identifier (KTD-6). Unique per tenant.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sub_product_tenant_slug
    ON sub_product(tenant_id, slug);

-- Case-insensitive uniqueness on the human-readable name. Matches the
-- repo's lookup key: WHERE tenant_id = ? AND LOWER(TRIM(name)) = ?.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sub_product_tenant_name_lower
    ON sub_product(tenant_id, LOWER(TRIM(name)));

CREATE TABLE IF NOT EXISTS sub_flow (
    id                  TEXT NOT NULL,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    sub_product_id      TEXT NOT NULL,
    name                TEXT NOT NULL,
    slug                TEXT NOT NULL,
    -- Nullable back-link set by U3 when the PM creates / opens a DRD
    -- for this sub_flow. flow_drd is keyed by sub_flow_id going forward;
    -- this column is the inverse pointer for fast lookup.
    drd_id              TEXT,
    -- Nullable until autosync (U2) detects the matching Figma section
    -- name and binds it. Sparse-unique via the partial index below.
    figma_section_id    TEXT,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, sub_product_id) REFERENCES sub_product(tenant_id, id) ON DELETE CASCADE
) STRICT;

-- Unique per (tenant, sub_product). Two sub-products can each have a
-- "Cold State" sub_flow without colliding.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sub_flow_tenant_subproduct_slug
    ON sub_flow(tenant_id, sub_product_id, slug);

-- Case-insensitive name uniqueness, scoped by sub_product. Matches the
-- repo's UpsertSubFlow lookup key.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sub_flow_tenant_subproduct_name_lower
    ON sub_flow(tenant_id, sub_product_id, LOWER(TRIM(name)));

-- Partial unique index: at most one sub_flow per (tenant, figma_section_id).
-- NULLs are excluded so many "design pending" sub_flows coexist.
-- Autosync (U2) relies on this to enforce a 1:1 mapping when binding.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sub_flow_figma_section
    ON sub_flow(tenant_id, figma_section_id) WHERE figma_section_id IS NOT NULL;
