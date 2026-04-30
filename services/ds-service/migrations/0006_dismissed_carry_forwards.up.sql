-- 0006_dismissed_carry_forwards — Phase 4 U3 — carry-forward of Dismissed
-- violations across re-exports.
--
-- When a designer dismisses a violation with rationale ("logged-out persona
-- doesn't trigger network errors"), the next re-audit of the same flow will
-- re-emit the same violation against the same logical screen / rule /
-- property triple. Without this table, the designer would have to re-dismiss
-- it on every re-export — fan-out hell.
--
-- The stable identity is (tenant_id, screen_logical_id, rule_id, property).
-- screen_logical_id is Phase 1's stable per-frame UUID; it survives re-export
-- because InsertScreens reuses the existing logical_id when the same Figma
-- frame is re-uploaded.
--
-- The worker's PersistRunIdempotent hooks this table inside its INSERT
-- transaction: any new Active violation matching a row here is marked
-- 'dismissed' before commit. The reason lives on this row, not on the
-- violations table — surfaces in the Violations tab via a JOIN at read time.
--
-- Override path: when a DS-lead reactivates a Dismissed violation
-- (acknowledged|dismissed → active in lifecycle.go), the carry-forward row
-- is deleted in the same transaction so subsequent re-audits leave the
-- violation Active until re-fixed or re-dismissed.

CREATE TABLE IF NOT EXISTS dismissed_carry_forwards (
    tenant_id            TEXT NOT NULL,
    screen_logical_id    TEXT NOT NULL,
    rule_id              TEXT NOT NULL,
    property             TEXT NOT NULL,
    reason               TEXT NOT NULL,
    dismissed_by_user_id TEXT NOT NULL,
    dismissed_at         TEXT NOT NULL,
    original_violation_id TEXT NOT NULL,
    PRIMARY KEY (tenant_id, screen_logical_id, rule_id, property)
);

-- Lookup happens per (tenant_id, screen_logical_id) batch during re-audit;
-- the PRIMARY KEY already covers it. No additional index needed.
