-- 0030_figma_autosync_retry_count — bound the auto-retry loop.
--
-- F4 from the post-merge audit: the planner's retry_failed_pipeline
-- branch + the 15-min ticker form an unbounded retry cascade. A
-- permanently broken section (corrupt file, oversized depth=14
-- response, designer-removed FRAME) gets re-queued every cycle
-- forever — one new project_versions row per cycle, Figma fan-out
-- compounded.
--
-- Add a retry_count + max_retries policy. Once a section has
-- accumulated MaxRetries consecutive non-ok attempts, the executor
-- writes last_attempt_status='quarantined' with a synthetic skip
-- reason 'max_retries_exceeded'; the planner treats quarantined the
-- same as already_synced (skip) until an admin clears the row via
-- POST /v1/admin/figma-autosync/state/{section_id}/clear-quarantine
-- (separate handler — schema only here).

ALTER TABLE figma_auto_sync_state
    ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0
        CHECK (retry_count >= 0);

ALTER TABLE figma_auto_sync_state
    ADD COLUMN quarantined_at TEXT;

-- Backfill: existing 'error' rows count their current attempts as 1
-- so we don't immediately quarantine prior failures. The retry loop
-- will increment from here.
UPDATE figma_auto_sync_state
   SET retry_count = 1
 WHERE last_attempt_status = 'error';
