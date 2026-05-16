-- 0035_figma_autosync_recovery — hybrid quarantine recovery model.
--
-- Plan: docs/plans/2026-05-17-001-fix-f4-quarantine-cluster-plan.md
--
-- Resolves four interlocking F4 bugs from the post-merge audit:
--   adv-2          permanent quarantine, no auto-recovery
--   correctness-#4 transient hash_not_ready becomes terminal
--   correctness-#11 clear-quarantine has no guard
--   correctness-#13 executor overwrites content_hash on non-syncs
--
-- Two new columns:
--
--   live_content_hash TEXT
--     What the live source contained at the last attempt — distinct
--     from content_hash (which now means "what the synced version
--     contains"). The executor updates this on every cycle, including
--     quarantine bookkeeping passes. The planner uses
--     `live_content_hash != content_hash` to detect designer-fixed
--     sections and auto-recover them from quarantine even before the
--     time-based window expires.
--
--   recovery_window_seconds INTEGER NOT NULL DEFAULT 21600
--     6 hours. When `now() > quarantined_at + recovery_window_seconds`
--     the planner treats the quarantine as expired and re-evaluates the
--     section on the next cycle. Eliminates the one-way-door trap where
--     a Figma outage permanently quarantines actively-syncing sections.
--     Default per-row so an operator can adjust individual sections
--     later (escape hatch for chronically-broken sections that need a
--     longer cooldown).
--
-- Backfill: existing rows running F4 today have content_hash populated
-- and live_content_hash NULL after the ADD COLUMN. The planner's new
-- skip-unchanged check compares `prior.live_content_hash` to
-- `sec.content_hash`, so a row with live_content_hash=NULL would
-- mis-classify every first-post-deploy cycle as content_changed and
-- trigger a full re-export. Backfill mirrors content_hash → live so
-- the first cycle's comparison is consistent with the pre-deploy state.

ALTER TABLE figma_auto_sync_state
    ADD COLUMN live_content_hash TEXT;

ALTER TABLE figma_auto_sync_state
    ADD COLUMN recovery_window_seconds INTEGER NOT NULL DEFAULT 21600;

UPDATE figma_auto_sync_state
   SET live_content_hash = content_hash
 WHERE live_content_hash IS NULL;
