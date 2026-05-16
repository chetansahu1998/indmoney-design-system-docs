-- 0029_figma_owner_and_section_overrides — Phase C of the autosync bridge
-- (2026-05-14). Plan: docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md
--
-- Three changes:
--   1. figma_file last-editor columns — populated by GetFileVersions during
--      deep-sync. Used to filter eligible files to the 6-person allowlist.
--   2. figma_section override columns — Claude-driven classification writes
--      sub_product / sub_flow directly; planner prefers overrides over the
--      "Wallet/Main Flow" section-name parser when set.
--   3. figma_owner_allowlist — per-tenant list of designer full names whose
--      last-edited files are eligible for auto-sync. Empty list = allow all
--      (back-compat for fresh installs).

-- ─── figma_file last-editor ──────────────────────────────────────────────────
ALTER TABLE figma_file ADD COLUMN last_editor_user_id TEXT;
ALTER TABLE figma_file ADD COLUMN last_editor_handle  TEXT;
ALTER TABLE figma_file ADD COLUMN last_editor_name    TEXT;
ALTER TABLE figma_file ADD COLUMN last_editor_at      TEXT;

CREATE INDEX IF NOT EXISTS idx_figma_file_last_editor
    ON figma_file (tenant_id, last_editor_name);

-- ─── figma_section override columns ──────────────────────────────────────────
-- When set, the planner uses these in preference to ParseSectionName().
-- classified_source distinguishes hand-entered designer naming
-- ('section_name'), Claude/heuristic pass ('claude_heuristic'), or
-- admin override ('admin_override').
ALTER TABLE figma_section ADD COLUMN sub_product_override TEXT;
ALTER TABLE figma_section ADD COLUMN sub_flow_override    TEXT;
ALTER TABLE figma_section ADD COLUMN classified_source    TEXT
    CHECK (classified_source IN ('section_name','claude_heuristic','admin_override'));
ALTER TABLE figma_section ADD COLUMN classified_at        TEXT;

-- ─── figma_owner_allowlist ───────────────────────────────────────────────────
-- Per-tenant list of allowed designer full names. The planner filters
-- ListFigmaFilesForAutoSync by joining on this table — when the table is
-- empty for a tenant, the filter is bypassed (back-compat).
CREATE TABLE IF NOT EXISTS figma_owner_allowlist (
    tenant_id  TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    full_name  TEXT NOT NULL,
    added_at   TEXT NOT NULL,
    PRIMARY KEY (tenant_id, full_name)
) STRICT;
