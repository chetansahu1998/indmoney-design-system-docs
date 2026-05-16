-- 0033_figma_autosync_lease — per-tenant advisory lease for the
-- autosync cycle so multi-replica deployments don't double-export.
--
-- #10 from the post-merge audit: the previous serialisation primitive
-- was a process-local sync.Mutex (cmd/server/main.go:autosyncMu),
-- which works for single-replica but breaks the moment a second
-- ds-service replica spins up (rolling deploy overlap, manual scale).
-- Two replicas would both pull state.last_attempt_status='failed',
-- both call RunExport, both write project_versions rows, both
-- increment retry_count — doubling Figma quota burn and racing F4's
-- quarantine math.
--
-- Lease semantics:
--   - PRIMARY KEY (tenant_id) — at most one holder per tenant
--   - holder_id = "<hostname>:<pid>:<nanoid>" so we can tell which
--     replica acquired and detect a misbehaving holder
--   - expires_at = acquired_at + TTL (typically the per-cycle budget
--     + slack) — a crashed replica's lease auto-recovers when the
--     next TryAcquire sees expires_at < now()
--
-- TryAcquire shape (executed in repository_figma_autosync.go):
--   INSERT INTO figma_autosync_lease (...)
--   VALUES (?, ?, ?, ?)
--   ON CONFLICT(tenant_id) DO UPDATE
--     SET holder_id   = excluded.holder_id,
--         acquired_at = excluded.acquired_at,
--         expires_at  = excluded.expires_at
--     WHERE figma_autosync_lease.expires_at < excluded.acquired_at;
--
-- If RowsAffected = 1 the caller has the lease; if 0 someone else
-- holds an unexpired one and the caller should bail (same shape as
-- the old TryLock contract).
--
-- Release:
--   DELETE FROM figma_autosync_lease
--    WHERE tenant_id = ? AND holder_id = ?;
--
-- Holder-id scoping prevents a replica from accidentally releasing
-- another replica's lease after its own expired and got reclaimed.

CREATE TABLE figma_autosync_lease (
    tenant_id   TEXT NOT NULL PRIMARY KEY,
    holder_id   TEXT NOT NULL,
    acquired_at TEXT NOT NULL,
    expires_at  TEXT NOT NULL
);

CREATE INDEX idx_figma_autosync_lease_expires
    ON figma_autosync_lease (expires_at);

-- #31 audit fix — index the ORDER BY clause of ListAutoSyncState
-- (repository_figma_autosync.go). Existing indices cover
-- (tenant_id, status) and (tenant_id, file_key, status); the admin
-- inspection endpoint's "most-recent-activity first" ordering was
-- doing a full table scan + sort. Trivial at the current ~2.5k row
-- count, but the endpoint now exists (#12) and grows with usage.
CREATE INDEX IF NOT EXISTS idx_figma_auto_sync_state_last_attempt_at
    ON figma_auto_sync_state (tenant_id, last_attempt_at DESC);
