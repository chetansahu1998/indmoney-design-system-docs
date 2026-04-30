-- 0002_audit_rules_and_categories — Projects · Flow Atlas Phase 2 schema
--
-- Adds the rule catalog, violation categorization, audit-job priority + trigger
-- attribution, and the Figma prototype-link cache that flow-graph (U5) reads.
-- Mirrors decisions from
-- docs/plans/2026-04-30-001-feat-projects-flow-atlas-phase-2-plan.md U1.
--
-- Forward-only column-add discipline (carried from 0001):
--   - ADD COLUMN ... NULL or ADD COLUMN ... NOT NULL DEFAULT <const> only.
--   - No DROP COLUMN here.
--   - Renames / removals are deferred to a future 3-release dual-write window.

-- ─── audit_rules ──────────────────────────────────────────────────────────────
-- Org-wide rule catalog. Phase 2 seeds with default severities (0003 migration).
-- Per-tenant overrides land in Phase 7 via a tenant_audit_rule_overrides table;
-- this row is the global default and stays that way.
--
-- expression is NULL for all Phase 2 rules (compiled Go). Phase 7's CEL DSL
-- writes into this column when DS leads author custom rules.
--
-- target_node_types is a CSV string (e.g., "TEXT" or "INSTANCE,COMPONENT") —
-- Phase 2 reads it for runtime filtering; Phase 7 admin UI edits it.
CREATE TABLE IF NOT EXISTS audit_rules (
    rule_id            TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    description        TEXT NOT NULL,
    category           TEXT NOT NULL,           -- see violations.category enum below
    default_severity   TEXT NOT NULL,           -- critical | high | medium | low | info
    enabled            INTEGER NOT NULL DEFAULT 1,
    target_node_types  TEXT,                    -- CSV; NULL = applies to all
    expression         TEXT,                    -- CEL source (Phase 7); NULL in Phase 2
    created_at         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_rules_category_enabled
    ON audit_rules(category, enabled);

-- ─── violations: category + auto_fixable ─────────────────────────────────────
-- category is the new axis the Violations tab filters on. We default to
-- 'token_drift' so the column is NOT NULL on existing rows; the trailing
-- backfill UPDATE re-classifies rows whose rule_id maps to a different category.
--
-- auto_fixable is the Phase 4 plugin handoff signal — Phase 2 sets it on rules
-- that are token-binding or style-class fixes. Defaults to 0; Phase 4 wires
-- the "Fix in Figma" CTA off this flag.
ALTER TABLE violations
    ADD COLUMN category TEXT NOT NULL DEFAULT 'token_drift';
ALTER TABLE violations
    ADD COLUMN auto_fixable INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_violations_version_category
    ON violations(version_id, category);
CREATE INDEX IF NOT EXISTS idx_violations_screen_category
    ON violations(screen_id, category);

-- ─── audit_jobs: priority + triggered_by + metadata ───────────────────────────
-- priority drives the dequeue order — recently-edited flow exports = 100,
-- routine exports = 50, fan-out re-audits = 10. Workers ORDER BY priority DESC,
-- created_at ASC LIMIT 1 with channel-notification on insert.
--
-- triggered_by tags the source so dashboards can split fan-out from organic
-- exports; metadata carries fanout_id (UUID) for AE-7 progress aggregation.
ALTER TABLE audit_jobs
    ADD COLUMN priority INTEGER NOT NULL DEFAULT 50;
ALTER TABLE audit_jobs
    ADD COLUMN triggered_by TEXT NOT NULL DEFAULT 'export'; -- export | rule_change | tokens_published
ALTER TABLE audit_jobs
    ADD COLUMN metadata TEXT;                                -- JSON; nullable

-- New compound index for the priority queue. Phase 1's idx_audit_jobs_status_created
-- stays — the planner picks. We don't drop it (no DROP within same-release that
-- stops writing it; the new index simply gives the planner a better path for
-- ORDER BY priority DESC, created_at ASC).
CREATE INDEX IF NOT EXISTS idx_audit_jobs_status_priority_created
    ON audit_jobs(status, priority DESC, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_jobs_triggered_by
    ON audit_jobs(triggered_by, created_at);

-- ─── screen_prototype_links ──────────────────────────────────────────────────
-- Cache of Figma prototype connections per screen. Populated by U5's flow-graph
-- runner on first audit of a version; reused on re-audit. Cascades on screen
-- deletion (CASCADE on FK already covers version-level deletion).
--
-- destination_screen_id is NULL when the destination is OUT_OF_FLOW (close /
-- back / external link / scroll-to). We persist the raw destination_node_id so
-- Phase 5+ DRD `/figma-link` blocks can render even when the destination isn't
-- in our screens table.
CREATE TABLE IF NOT EXISTS screen_prototype_links (
    id                      TEXT PRIMARY KEY,
    screen_id               TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
    tenant_id               TEXT NOT NULL,
    source_node_id          TEXT NOT NULL,
    destination_screen_id   TEXT REFERENCES screens(id) ON DELETE SET NULL,
    destination_node_id     TEXT,
    trigger                 TEXT NOT NULL,         -- ON_CLICK | ON_HOVER | AFTER_TIMEOUT | …
    action                  TEXT NOT NULL,         -- NAVIGATE | OVERLAY | SWAP | CLOSE | BACK | URL
    metadata                TEXT,                  -- JSON; raw transition data for forensics
    created_at              TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_proto_links_screen
    ON screen_prototype_links(screen_id);
CREATE INDEX IF NOT EXISTS idx_proto_links_destination
    ON screen_prototype_links(tenant_id, destination_screen_id);
CREATE INDEX IF NOT EXISTS idx_proto_links_source
    ON screen_prototype_links(tenant_id, source_node_id);

-- ─── Backfill: violations.category from rule_id ──────────────────────────────
-- Phase 1 ruleIDs (services/ds-service/internal/projects/runner.go:ruleIDFor)
-- are synthesized as "<reason>.<property>". Map each known prefix to the
-- category enum the Phase 2 frontend filters on. Unknown rule_ids stay at the
-- default 'token_drift'.
UPDATE violations SET category = 'theme_parity'
    WHERE rule_id LIKE 'theme_break.%';
UPDATE violations SET category = 'text_style_drift'
    WHERE rule_id LIKE 'drift.text';
UPDATE violations SET category = 'spacing_drift'
    WHERE rule_id IN ('drift.padding', 'drift.gap', 'drift.spacing');
UPDATE violations SET category = 'radius_drift'
    WHERE rule_id = 'drift.radius';
UPDATE violations SET category = 'token_drift'
    WHERE rule_id IN (
        'drift.fill', 'drift.stroke',
        'deprecated.fill', 'deprecated.stroke', 'deprecated.text',
        'unbound.fill', 'unbound.stroke', 'unbound.text'
    );
UPDATE violations SET category = 'component_match'
    WHERE rule_id IN ('unbound.component', 'drift.component');
UPDATE violations SET category = 'component_governance'
    WHERE rule_id = 'custom.component';

-- auto_fixable: Phase 4 wires "Fix in Figma" off this flag. The token-binding
-- and text-style fixes are auto-fixable; raw-color drifts that have a token_path
-- suggestion are auto-fixable; rationale-only / sprawl / structural rules are not.
-- For Phase 2 we mark these classes as auto_fixable=1 across existing data.
UPDATE violations SET auto_fixable = 1
    WHERE rule_id IN (
        'drift.fill', 'drift.stroke', 'drift.text',
        'unbound.fill', 'unbound.stroke', 'unbound.text',
        'deprecated.fill', 'deprecated.stroke', 'deprecated.text'
    )
      AND suggestion IS NOT NULL
      AND suggestion <> '';
