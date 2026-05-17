-- 0040_prd_audit — U6b of docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md.
--
-- Append-only audit log of writes to prd_state and its typed stems. Every
-- prd.* MCP write tool emits a row keyed to the affected prd_state_id so
-- the coverage wall (section.outline_states) can answer
-- "who last touched this state, and when?" without scanning the entire
-- write history of the system.
--
-- Shape mirrors auditlog.go (events with id, tenant_id, user_id, op, at).
-- We keep the table local to the PRD subsystem rather than reusing the
-- shared audit_log table because:
--   1. The query pattern is "latest row per prd_state_id" — a tight
--      composite index on (tenant_id, prd_state_id, at DESC) gives the
--      wall a sub-ms lookup. The shared audit_log carries every project
--      event and would force a sequential scan with json_extract.
--   2. Cascading on prd_state DELETE keeps the log self-cleaning when a
--      state is hard-deleted (the soft-delete path leaves the row + audit
--      trail intact).
--
-- Auto-skeleton writes (U2b) do NOT record audits — those are system-
-- driven, not PM-authored. Only the MCP prd.* path threads
-- RecordPRDAudit, so last_touched_by represents actual human authorship.
--
-- Op vocabulary (lives in internal/projects/prd_audit.go as constants):
--   upsert_state | add_acceptance_criterion | add_edge_case |
--   upsert_copy_string | add_event | add_a11y_note | attach_frame |
--   detach_frame | upsert_tab
--
-- Convention check-list (matches recent migrations 0034 / 0036 / 0037):
--   - STRICT mode.
--   - Composite PK (tenant_id, id).
--   - ON DELETE CASCADE on tenant FK + prd_state FK.
--   - at column carries the same RFC3339-ish strftime default used by
--     prd_state.created_at / updated_at so a single parser handles both.
--   - DESC on the index is intentional — the wall reads "latest first".
CREATE TABLE IF NOT EXISTS prd_audit (
    id              TEXT NOT NULL,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    prd_state_id    TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    op              TEXT NOT NULL,
    at              TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, prd_state_id) REFERENCES prd_state(tenant_id, id) ON DELETE CASCADE
) STRICT;

CREATE INDEX IF NOT EXISTS idx_prd_audit_tenant_state_at
    ON prd_audit(tenant_id, prd_state_id, at DESC);
