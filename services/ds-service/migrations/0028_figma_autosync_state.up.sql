-- 0028_figma_autosync_state — auto-sync bridge between FIGMA DB and audit
-- pipeline (2026-05-14).
--
-- Plan: docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md
--
-- Adds two new tables and enriches three existing ones (figma_page,
-- figma_section, figma_file) with the metadata the AutoSyncPlanner
-- needs to:
--   1. Decide which file is in the 6-month window and has a usable
--      "Final Design" page.
--   2. Hash each section's subtree to detect content change vs. position
--      change (cheap-update vs. full-export).
--   3. Track per-section sync history independent of figma_section so
--      re-crawling Figma doesn't wipe history.
--
-- A planned third table (figma_page_picker_rule, per-tenant classifier
-- overrides) is deliberately deferred — empty rule slice covers v1.

-- ─── figma_auto_sync_state ───────────────────────────────────────────────────
--
-- Per-(tenant, file_key, page_id, section_id) sync history. Lives next to
-- figma_section rather than as columns ON figma_section because a re-crawl
-- rewrites figma_section but the planner's history (last_synced_flow_id,
-- last_synced_version_id, content_hash at sync time) must survive across
-- crawls.

CREATE TABLE IF NOT EXISTS figma_auto_sync_state (
    tenant_id              TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    file_key               TEXT NOT NULL,
    page_id                TEXT NOT NULL,
    section_id             TEXT NOT NULL,
    -- Content hash AT THE LAST SUCCESSFUL SYNC. May lag the live
    -- figma_section.content_hash if a sync is pending or failed.
    content_hash           TEXT,
    -- Position hash at the last successful sync (name + bbox + parent
    -- order_index). Used to detect cheap-update opportunities.
    position_hash          TEXT,
    -- Output of the last successful runExport call (full_export path).
    -- Both NULL on a brand-new section or after permanent error.
    last_synced_flow_id    TEXT,
    last_synced_version_id TEXT,
    last_synced_at         TEXT,
    -- Most-recent ATTEMPT timestamp (success OR failure) — distinct from
    -- last_synced_at which only moves on success.
    last_attempt_at        TEXT,
    -- 'ok'         — runExport (or cheap_update) succeeded
    -- 'skipped'    — planner decided no action (e.g. content matches state)
    -- 'error'      — runExport returned an error
    -- 'quarantined'— blocked upstream (e.g. project_unmapped, out_of_window)
    last_attempt_status    TEXT CHECK (last_attempt_status IN ('ok','skipped','error','quarantined')),
    skip_reason            TEXT,
    error_message          TEXT,
    first_seen_at          TEXT NOT NULL,
    PRIMARY KEY (tenant_id, file_key, page_id, section_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_figma_auto_sync_state_status
    ON figma_auto_sync_state (tenant_id, last_attempt_status);

CREATE INDEX IF NOT EXISTS idx_figma_auto_sync_state_file
    ON figma_auto_sync_state (tenant_id, file_key, last_attempt_status);

-- ─── figma_project_mapping ───────────────────────────────────────────────────
--
-- Per-tenant per-Figma-project: which Domain + Product does this Figma
-- project's files belong to? Admin-managed; planner refuses to ingest
-- unmapped projects (quarantine with skip_reason='project_unmapped').
-- Mapping by figma_project.project_id (Figma's id), not by name, so a
-- rename in Figma doesn't break the link.

CREATE TABLE IF NOT EXISTS figma_project_mapping (
    tenant_id            TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id           TEXT NOT NULL,
    -- Domain at the top of the user's taxonomy (Markets / Money matters /
    -- etc). Free-text in v1; future iterations may add an enum + a
    -- separate domains table.
    domain               TEXT NOT NULL,
    -- Product under the domain (Indian Stocks / US Stocks / Mutual Funds
    -- / etc). The Sub-product comes from the section name parser; the
    -- domain + product live here at the project level.
    product              TEXT NOT NULL,
    -- Platform default for files in this project. ExportRequest.platform
    -- in the audit pipeline accepts 'mobile' or 'web'; 'unspecified' lets
    -- the planner skip the field and let the pipeline infer.
    platform_default     TEXT NOT NULL DEFAULT 'unspecified'
                         CHECK (platform_default IN ('mobile','web','unspecified')),
    -- Toggle to disable auto-sync for an individual project without
    -- deleting the mapping. Default ON so a mapped project is eligible.
    enabled_for_autosync INTEGER NOT NULL DEFAULT 1 CHECK (enabled_for_autosync IN (0,1)),
    mapped_by_user_id    TEXT NOT NULL,
    mapped_at            TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    PRIMARY KEY (tenant_id, project_id)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_figma_project_mapping_enabled
    ON figma_project_mapping (tenant_id, enabled_for_autosync);

-- ─── figma_page additions ────────────────────────────────────────────────────
--
-- Hash + classification metadata. Populated by the poller during deep
-- sync (U4). The classifier (U2) reads page_classification + version_base
-- + version_n + persona_hint to pick the source page for the planner.

ALTER TABLE figma_page ADD COLUMN content_hash TEXT;
ALTER TABLE figma_page ADD COLUMN position_hash TEXT;
-- Bumps to crawl timestamp ONLY when content_hash changes; preserves the
-- prior value across no-op cycles. Lets the planner pick "most recently
-- content-changed Final page" when a file has multiple Final pages.
ALTER TABLE figma_page ADD COLUMN derived_last_modified TEXT;
-- Output of ClassifyPages (U2). Persisted so admin UI can render the
-- classifier's decision without re-running it.
ALTER TABLE figma_page ADD COLUMN page_classification TEXT
    CHECK (page_classification IN ('final','version','noise','unknown'));
-- Base name with the trailing version suffix stripped (e.g. "Onboarding"
-- from "Onboarding v2"). Empty for non-versioned pages.
ALTER TABLE figma_page ADD COLUMN version_base TEXT;
-- Numeric version when classification='version'. Highest version_n per
-- version_base wins.
ALTER TABLE figma_page ADD COLUMN version_n INTEGER;
-- Persona derived from a Final page's name ('trader' from 'Trader
-- FINAL DESIGN'). 'default' when nothing remains after stripping.
ALTER TABLE figma_page ADD COLUMN persona_hint TEXT;

-- ─── figma_section additions ─────────────────────────────────────────────────
--
-- Subtree hash + own-position hash. Populated by the poller during deep
-- sync (U4). The planner reads these to decide full_export vs.
-- cheap_update vs. skip.

ALTER TABLE figma_section ADD COLUMN content_hash TEXT;
ALTER TABLE figma_section ADD COLUMN position_hash TEXT;
