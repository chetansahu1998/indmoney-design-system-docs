---
title: "fix: F4 quarantine 4-bug cluster — hybrid recovery + live_content_hash separation"
status: active
created: 2026-05-17
type: fix
---

# fix: F4 quarantine 4-bug cluster

## Problem Frame

Four interlocking bugs in the autosync F4 quarantine mechanism (shipped via migration 0032 + `repository_figma_autosync.go::UpsertAutoSyncState`) combine into a one-way-door trap:

1. **adv-2** — `AutoSyncMaxRetries=5` doc-comment claims "~3hr Figma window at 15min cadence" (actual: 75min). With the post-#8 1-min `FIGMA_AUTOSYNC_INTERVAL` floor, 5 retries = 5 minutes. A real 30-minute Figma outage permanently quarantines every actively-syncing section. The "auto-reset on next ok" the UPSERT promises is unreachable because the planner short-circuits on `LastAttemptStatus='quarantined'` before the executor can ever run an export that writes 'ok'.

2. **correctness-#4** — transient `hash_not_ready` is persisted as terminal quarantine. When `sec.ContentHash==""` the planner emits `ActionSkipQuarantined` + `SkipReason=SkipHashNotReady`. The executor UPSERTs `LastAttemptStatus='quarantined'`. Next cycle the planner skips on quarantine status BEFORE the hash check, locking the section into manual-clear-only state without ever exhausting MaxRetries.

3. **correctness-#11 / api-contract-3** — `ClearAutoSyncQuarantine` has no `last_attempt_status='quarantined'` guard. An operator misclick on a healthy 'ok' row wipes `last_attempt_status` to NULL, zeros `retry_count`, drops `error_message`. Next cycle re-exports the section unnecessarily. The doc-comment promises "idempotent 200 on non-quarantined" but the implementation returns 404 — doc and impl disagree.

4. **correctness-#13** — executor's UPSERT for `ActionSkipQuarantined` writes `ps.LiveContentHash` into the state row's `content_hash` column every cycle. After a designer fixes the section, `content_hash` drifts to match the new live hash even though the section is still quarantined. Operator clears, planner sees `prior.ContentHash == sec.ContentHash` → emits skip_unchanged. The fix is never exported.

**Root cause:** the F4 state model conflates "synced content hash" with "live content hash at last attempt." Quarantine status is terminal in the planner but not in the executor. The clear endpoint operates on the wrong unit (any matching row instead of any quarantined row).

## Scope

**In scope:**
- New schema: separate `live_content_hash` column for "what the source contained at last attempt" — distinct from `content_hash` ("what the synced version contains"). Migration 0035.
- New schema: `recovery_window_seconds` column on `figma_auto_sync_state` (default 6h = 21600) — time-based auto-release from quarantine.
- Hybrid recovery: planner treats `quarantined` as non-terminal if (a) `now() > quarantined_at + recovery_window` (time-based), OR (b) `live_content_hash` has changed since quarantine (content-change-based).
- `hash_not_ready` no longer flows through quarantine. Executor skips the UPSERT for `ActionSkipQuarantined + SkipHashNotReady`, OR planner emits a separate `ActionSkipNotReady` that the executor no-ops on. (See U3 design.)
- Executor's UPSERT separated into "syncing write" (sets content_hash + last_synced_*) vs "bookkeeping write" (sets only retry_count, last_attempt_*, live_content_hash). Quarantine writes are bookkeeping only.
- `ClearAutoSyncQuarantine` guarded — only updates rows where `last_attempt_status='quarantined'`. Returns 200+cleared when guard fires, 404 when no quarantined row matches, doc-comments aligned with implementation.
- Existing tests updated to the new semantics; new tests added for the hybrid recovery + clear-guard paths.

**Out of scope (deferred to follow-up):**
- UI surface for quarantined sections (Cluster E).
- Per-tenant `recovery_window` config (single global default for now; cluster-level operator override possible later).
- Adjustable `AutoSyncMaxRetries` per tenant.
- Webhook trigger for immediate quarantine clear (operators use the existing endpoint).

**Outside this fix's identity:**
- Replacing F4's quarantine mechanism entirely (e.g., switching to a separate `figma_autosync_failures` table).
- Multi-tenant write coordination.

---

## Requirements

**R1 — Time-based auto-recovery.** When `now() > quarantined_at + recovery_window_seconds`, the planner considers the section eligible for retry on the next cycle, regardless of `last_attempt_status='quarantined'`. The retry uses content-change detection to decide what action to emit.

**R2 — Content-change auto-recovery.** When `live_content_hash != content_hash` AND the section is quarantined, the planner treats this as a designer fix and emits `ActionFullExport` (not skip_quarantined). The fix gets exported.

**R3 — `hash_not_ready` is never terminal.** A section with empty `sec.ContentHash` is skipped (planner action `ActionSkipNotReady` or equivalent) but does NOT result in a quarantine write. The state row's `last_attempt_status` stays at its prior value (or 'skipped' if first encounter), retry_count unchanged, quarantined_at NULL.

**R4 — Hash separation.** `content_hash` is written ONLY when the executor successfully syncs (status='ok'). `live_content_hash` is updated on every executor pass (sync OR quarantine bookkeeping) so the planner can detect designer fixes via `live != content`. Migration 0035 adds the column with backfill: `UPDATE figma_auto_sync_state SET live_content_hash = content_hash` so existing rows behave consistently on first post-deploy planner run.

**R5 — Clear-quarantine guard.** `ClearAutoSyncQuarantine`'s UPDATE includes `WHERE last_attempt_status='quarantined'`. Returns `(cleared=true, nil)` on rowsAffected=1. Returns `(cleared=false, nil)` on rowsAffected=0 — caller maps to 404. Doc-comments on the handler + the planner's reference comments updated to match.

**R6 — Single-writer invariant preserved.** The write pool stays MaxOpenConns=1; all schema and code changes respect this. Migration 0035 follows the same UP-only pattern as 0032/0033.

**R7 — Test compatibility.** The existing autosync test suite (autosync_planner_test.go, autosync_executor_test.go, repository_figma_autosync_test.go, server_figma_autosync_test.go) is updated to match the new semantics. New tests added for: time-based auto-recovery, content-change auto-recovery, hash_not_ready non-terminal, clear-quarantine guard.

**R8 — Existing data coexists.** Migration 0035 backfills `live_content_hash` from `content_hash` and sets `recovery_window_seconds` to the default 21600 for all existing rows. Any tenant running F4 today continues to function on first deploy without manual intervention; quarantined sections become eligible for time-based recovery from 6 hours after `quarantined_at`.

---

## Key Technical Decisions

### Decision 1: Hybrid recovery (time + content change)

Picked over time-only or content-only because each handles a different failure class:
- Time-based handles Figma outages (planner re-attempts after 6h regardless of code state).
- Content-based handles designer fixes (live hash diverges from synced hash → retry now).

Together they eliminate both halves of the one-way-door trap. The `recovery_window_seconds` column lets the default move per-deploy without a migration.

### Decision 2: Separate `live_content_hash` column

`content_hash` becomes "what the synced version contains" (write only on status='ok').
`live_content_hash` becomes "what the live source contained at the last attempt" (write on every executor pass).

The planner's existing skip-unchanged comparison switches from `prior.ContentHash == sec.ContentHash` to `prior.LiveContentHash == sec.ContentHash`. For unquarantined sections this is functionally identical to today (both columns track the same value). For quarantined-then-cleared sections, the separation is the fix: post-quarantine designer edits update `live_content_hash` but leave `content_hash` at the last-synced value, so when the operator clears (or time-based recovery fires) the planner detects `live != synced` and emits FullExport.

### Decision 3: `ActionSkipNotReady` planner action

New PlanAction value. The executor's case branch for it is a no-op: the planner detected "section's hash isn't computed yet," nothing to sync, nothing to record. The state row stays at its prior values (or is never created if no row existed). Removes hash_not_ready from the quarantine path entirely.

Alternative considered: keep emitting `ActionSkipQuarantined + SkipHashNotReady` but special-case the executor to skip the UPSERT. Rejected because the action name lies about intent. Better to have a dedicated action that says what it means.

### Decision 4: Clear-quarantine reframed as "force immediate recovery"

DELETE `/quarantine` now does:
```sql
UPDATE figma_auto_sync_state
   SET last_attempt_status = 'cleared',
       quarantined_at = NULL,
       retry_count = 0,
       error_message = ''
 WHERE tenant_id = ? AND file_key = ? AND page_id = ? AND section_id = ?
   AND last_attempt_status = 'quarantined'
```

- `last_attempt_status='cleared'` instead of NULL — gives operator dashboards a "recently cleared" surface and lets the planner emit FullExport on the next cycle (cleared status flows through the not-equal-to-ok branch).
- `WHERE last_attempt_status='quarantined'` is the guard.
- Returns rowsAffected so handler can map to 200/404 cleanly.

### Decision 5: Migration 0035 backfill strategy

```sql
ALTER TABLE figma_auto_sync_state
    ADD COLUMN live_content_hash TEXT;

ALTER TABLE figma_auto_sync_state
    ADD COLUMN recovery_window_seconds INTEGER NOT NULL DEFAULT 21600;

-- Backfill existing rows so the planner's new skip-unchanged comparison
-- works on first post-deploy cycle:
UPDATE figma_auto_sync_state
   SET live_content_hash = content_hash
 WHERE live_content_hash IS NULL;
```

Single transaction, sub-second on any realistic row count. Default of 21600 (6h) applies to every row including newly-inserted ones via the column default.

---

## Implementation Units

### U1. Migration 0035 — live_content_hash + recovery_window_seconds

**Goal:** Add the two new columns to `figma_auto_sync_state` with a backfill that lets existing rows behave consistently under the new semantics.

**Requirements:** R4, R6, R8.

**Files:**
- `services/ds-service/migrations/0035_figma_autosync_recovery.up.sql` (new)
- `services/ds-service/internal/projects/repository_figma_autosync.go` (modify — add fields to `AutoSyncState` struct, no logic changes yet)

**Approach:** mirror migration 0032's shape. Two `ALTER TABLE ADD COLUMN`s plus a UPDATE backfill. `live_content_hash` defaults to NULL (existing data backfilled in same migration); `recovery_window_seconds` defaults to 21600 (6h).

**Test scenarios:**
- Migration applies cleanly on a fresh DB; `live_content_hash` + `recovery_window_seconds` columns exist with expected types.
- Migration applied on a DB with existing 0032 data backfills `live_content_hash` from `content_hash` row-for-row.
- `recovery_window_seconds` defaults to 21600 on new inserts.

**Verification:** `go test ./internal/db/...` passes (schema bring-up test sees the new columns), `go test ./internal/projects/...` passes (struct compiles with new fields).

---

### U2. UpsertAutoSyncState — separate sync vs bookkeeping writes

**Goal:** Refactor the UPSERT so a quarantine/error/skip write updates only bookkeeping fields (`retry_count`, `last_attempt_*`, `live_content_hash`, `quarantined_at`, `error_message`, `skip_reason`). A success write (`last_attempt_status='ok'`) updates the sync fields (`content_hash`, `position_hash`, `last_synced_*`) AND mirrors `live_content_hash` to the same value.

**Requirements:** R4, R6.

**Files:**
- `services/ds-service/internal/projects/repository_figma_autosync.go` (modify — UPSERT statement + AutoSyncState input handling)
- `services/ds-service/internal/projects/repository_figma_autosync_test.go` (modify — existing tests assert content_hash is written on quarantine; flip them)

**Approach:** the UPSERT's CASE expressions for `content_hash` and `position_hash` gain a guard: only update when `excluded.last_attempt_status = 'ok'`. New CASE for `live_content_hash`: always update from `excluded.live_content_hash`. Caller-facing change: `Executor` now passes `LiveContentHash` separately from `ContentHash` on quarantine paths.

**Test scenarios:**
- UpsertAutoSyncState with status='ok' writes content_hash + live_content_hash from the same input value.
- UpsertAutoSyncState with status='error' writes live_content_hash only; content_hash stays at prior value.
- UpsertAutoSyncState with status='quarantined' (caller-driven) writes live_content_hash only.
- After 3 error cycles on a section that designer then fixes: live_content_hash reflects each cycle's live hash; content_hash stays at the last-synced value.

**Verification:** repository tests pass; the existing TestUpsertAutoSyncState_AutoQuarantines test is updated to assert content_hash is NOT overwritten on the quarantine transition.

---

### U3. Planner — hybrid recovery + ActionSkipNotReady

**Goal:** Replace the "quarantined → terminal skip" branch with hybrid recovery logic. Add `ActionSkipNotReady` for the hash_not_ready case (no executor side-effects).

**Requirements:** R1, R2, R3.

**Files:**
- `services/ds-service/internal/figma/inventory/autosync_planner.go` (modify — Plan + the quarantine branch + the SkipHashNotReady case)
- `services/ds-service/internal/figma/inventory/autosync_planner_test.go` (modify — TestPlanner_QuarantineShortCircuit semantics flip; add new tests)

**Approach:**

When the planner sees `prior.LastAttemptStatus == 'quarantined'`:
- If `time.Since(prior.QuarantinedAt) >= prior.RecoveryWindow`: ignore quarantine, fall through to the normal content-comparison branch. Effectively a "time has passed, try again" reset.
- Else if `prior.LiveContentHash != "" && prior.LiveContentHash != prior.ContentHash`: same fall-through (designer fix detected).
- Else: emit `ActionSkipQuarantined` as today.

When the planner sees `sec.ContentHash == ""`:
- Emit `ActionSkipNotReady` (new). Executor's case branch for this is a no-op (no UPSERT, no write).

Doc-comments at the quarantine-short-circuit + the SkipHashNotReady case updated to reference R1/R2/R3.

**Test scenarios:**
- TestPlanner_QuarantineExpired_TimeRecovery: prior status='quarantined', quarantined_at 7h ago, content unchanged → planner emits content-comparison action (skip_unchanged or full_export depending on hash).
- TestPlanner_QuarantineExpired_ContentRecovery: prior status='quarantined', quarantined_at 1h ago, live_content_hash != content_hash → planner emits ActionFullExport.
- TestPlanner_QuarantineActive: prior status='quarantined', quarantined_at 1h ago, live_content_hash == content_hash → planner still emits ActionSkipQuarantined.
- TestPlanner_HashNotReady_NoQuarantine: sec.ContentHash=="" → planner emits ActionSkipNotReady (not ActionSkipQuarantined). Subsequent UpsertAutoSyncState NOT called.
- TestPlanner_HashNotReadyToReady: section with ContentHash="" gets ContentHash populated on next cycle → planner emits normal action; no terminal lock.

**Verification:** all new tests pass; the existing TestPlanner_SkipQuarantine test is rewritten or replaced to match the new semantics.

---

### U4. Executor — no-op on ActionSkipNotReady; bookkeeping-only on ActionSkipQuarantined

**Goal:** Stop the executor from persisting `content_hash` on quarantine paths. Add a case branch for `ActionSkipNotReady` that no-ops (no UPSERT).

**Requirements:** R3, R4.

**Files:**
- `services/ds-service/internal/figma/inventory/autosync_executor.go` (modify — case branches for ActionSkipNotReady + ActionSkipQuarantined)
- `services/ds-service/internal/figma/inventory/autosync_executor_test.go` (modify — assert no UPSERT for ActionSkipNotReady; assert content_hash unchanged after ActionSkipQuarantined cycle)

**Approach:**

`switch ps.Action` gains a new case:
```
case ActionSkipNotReady:
    return nil // no state write; planner re-evaluates on next cycle
```

The existing `case ActionSkipQuarantined` UPSERT switches the input to set only `LiveContentHash`, not `ContentHash`. The bookkeeping fields (`retry_count` already handled in the UPSERT, `last_attempt_status='quarantined'`, `quarantined_at`) work via U2's UPSERT changes.

**Test scenarios:**
- TestExecutor_SkipNotReady_NoWrite: planner emits ActionSkipNotReady → executor returns nil, no UPSERT call (verified via mock or row-count assertion).
- TestExecutor_SkipQuarantined_PreservesContentHash: planner emits ActionSkipQuarantined → executor UPSERTs but content_hash stays at prior value, live_content_hash updates.
- TestExecutor_FullExport_BothHashesUpdate: planner emits ActionFullExport that succeeds → executor's UpsertAutoSyncState writes both content_hash and live_content_hash to the new synced value.

**Verification:** executor tests pass; the autosync end-to-end flow (planner → executor → state) preserves content_hash across error/quarantine cycles.

---

### U5. ClearAutoSyncQuarantine guarded + reframed as force-recovery

**Goal:** Add `last_attempt_status='quarantined'` guard. Set `last_attempt_status='cleared'` instead of NULL. Align doc-comments with implementation.

**Requirements:** R5.

**Files:**
- `services/ds-service/internal/projects/repository_figma_autosync.go` (modify — ClearAutoSyncQuarantine SQL)
- `services/ds-service/internal/projects/server.go` (no change — handler already returns 404 when cleared=false; just verify the doc-comment reads correctly with the new semantics)
- `services/ds-service/internal/projects/server_figma_autosync_test.go` (modify — TestClearAutoSyncQuarantine_NonQuarantined now asserts 404, not 200)
- `services/ds-service/internal/figma/inventory/autosync_planner.go` (modify — doc-comments at lines 62 + 344 referencing the endpoint behaviour)

**Approach:** straightforward SQL change. Existing tests need expectation flips on the non-quarantined cases.

**Test scenarios:**
- TestClearAutoSyncQuarantine_OnQuarantinedRow_Cleared: row with status='quarantined' → UPDATE returns rowsAffected=1, handler returns 200+cleared=true. State row now has status='cleared', retry_count=0, quarantined_at NULL.
- TestClearAutoSyncQuarantine_OnHealthyRow_404: row with status='ok' → UPDATE returns rowsAffected=0, handler returns 404. Row untouched.
- TestClearAutoSyncQuarantine_OnErrorRow_404: row with status='error' but not yet quarantined → 404. Row untouched.
- TestClearAutoSyncQuarantine_NotFound: no row matches → 404.

**Verification:** server_figma_autosync_test.go passes with the flipped expectations; manual `curl DELETE` on a quarantined row returns 200, on a healthy row returns 404.

---

### U6. AutoSyncMaxRetries — re-document + recovery_window-aware semantics

**Goal:** Update AutoSyncMaxRetries doc-comment + the constant's value (if needed) so the threshold makes sense alongside time-based auto-recovery. With hybrid recovery, MaxRetries becomes "how many consecutive errors before we enter quarantine for a recovery_window," not "how many retries before we give up forever."

**Requirements:** R1 (calibration), R7 (doc coherence).

**Files:**
- `services/ds-service/internal/projects/repository_figma_autosync.go` (modify — AutoSyncMaxRetries doc-comment lines 22-28)

**Approach:** prose-only change. The constant stays at 5 — the meaning changes from "permanently quarantine after 5 retries" to "back off into quarantine for `recovery_window_seconds` after 5 consecutive errors, then auto-resume." Doc-comment names the change.

**Test expectation: none** — pure doc change. Tested implicitly by U3's auto-recovery tests.

**Verification:** code review.

---

## Risks

- **Existing 'quarantined' rows from F4's earlier semantics.** Migration 0035 backfills `live_content_hash`, but the existing rows still have status='quarantined' and `quarantined_at` set per F4's earlier writes. The new planner's time-based check uses `now() - quarantined_at >= recovery_window`. For rows quarantined more than 6 hours before deploy, the next planner cycle immediately recovers them. Net effect: post-deploy, the autosync retry loop will attempt re-exports of historically-quarantined sections. This is the **intended** behaviour (the quarantine was meant to be transient) but it's a non-zero re-export burst on first deploy. Mitigation: the existing `figma_autosync_lease` (migration 0033) serializes the cycle so the burst doesn't overlap, and the per-section UpsertAutoSyncState will re-quarantine the genuinely-broken ones after 5 fresh failures.
- **Tests are coupled to current semantics.** Several existing tests (`TestUpsertAutoSyncState_AutoQuarantines`, `TestPlanner_SkipQuarantine`, `TestClearAutoSyncQuarantine_*`) explicitly assert the old behaviour. They need careful updates, not deletion — they're still valuable as regression checks for the *new* semantics. Mitigation: walk them one at a time in U2/U3/U5, flipping expectations rather than removing tests.
- **`live_content_hash` migration backfill on huge tables.** A single UPDATE on a table with millions of rows could lock the write pool for tens of seconds. Current scale is well under that (per repo-research-analyst: ~thousands of sections), so it's fine. If the table grows past ~1M rows in the future, the migration should switch to batched UPDATE.

---

## Verification Strategy

End-to-end after all six units land:
1. `go build ./...` clean.
2. `go test ./internal/db/... ./internal/projects/... ./internal/figma/inventory/... -race -count=1` passes.
3. Manual: create a section, force it through 5 failures to enter quarantine, observe `live_content_hash` writes on each cycle and `content_hash` preserved at the original value. Change `sec.ContentHash` to simulate a designer fix, observe the next planner cycle emits ActionFullExport without operator intervention.
4. Manual: wait 6h (or set `recovery_window_seconds=10` in a test row), confirm the planner auto-recovers without operator action.
5. Manual: `curl DELETE /v1/admin/figma-autosync/state/.../quarantine` on a quarantined row returns 200; same call on a healthy row returns 404.
