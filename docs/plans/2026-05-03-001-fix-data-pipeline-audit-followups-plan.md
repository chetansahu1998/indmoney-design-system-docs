---
title: "fix: Data pipeline + auth audit follow-ups — 9 sequenced tickets to close out the May-3 deep-dive findings"
type: fix
status: active
date: 2026-05-03
origin: docs/plans/2026-05-03-001-fix-data-pipeline-audit-followups-plan.md (this doc — the audit findings live in the conversation transcript that produced it)
---

# Data pipeline + auth audit follow-ups

## Overview

The May-3 deep-dive (transcript) traced the full read/write topology — Figma plugin → audit-server → ds-service → SQLite + filesystem → frontend — and surfaced nine concrete bugs / gaps. Two are P0 (data-integrity), three P1 (user-visible drift), three P2 (defense-in-depth + storage hygiene), and one Hygiene (post-token-mint security baseline).

Already shipped this session (commit `72c8938` and earlier):

- `cmd/mint-tokens` — 9 designer JWTs, 365-day lifetime
- `UpsertFlow` rename propagation
- KTX2-attempt gate via `NEXT_PUBLIC_ENABLE_KTX2`
- Atlas zoom-floor fix (frames now actually render)
- CORS PUT/PATCH/DELETE in ds-service
- Components page filter widening (147 components instead of 2)
- Variants extractor run (1132 variants)
- Foundations mock-data deletion
- Empty-string `NEXT_PUBLIC_DS_SERVICE_URL` defensive fallback
- AtlasFrame LOD-tier-not-found fallback
- Backfill-LOD one-shot + 657 sidecars on the volume
- Audit-server hardened with JWT auth + deployed to Fly
- Plugin retargeted to Fly URLs

This plan covers everything the deep-dive flagged but **didn't** ship in that session.

---

## Problem Frame

The system **works** but is **fragile** in five ways the audit identified:

1. **Half-writes**: `HandleExport` does ~6 DB writes outside any transaction. A mid-handler error leaves the DB partially-populated; the idempotency key dedupes on retry → broken state preserved.
2. **Stale derived views**: `graph_index` (the atlas mesh) only rebuilds on audit-completion or every 60 minutes. Flow renames, project deletions, decision additions stay invisible to the atlas for up to an hour.
3. **Silent pipeline failures**: When the Figma render API times out, screens get inserted with `png_storage_key=NULL`, the PNG handler 404s those forever, and the frontend has no signal to surface the failure.
4. **Tenant isolation is application-only**: 9 tables hold `tenant_id` columns with no FK to `tenants(id)`. A bug in any query that forgets the WHERE clause leaks across tenants with no DB-level guard.
5. **Storage drift**: Old version directories accumulate forever (~250 MB per export, no GC). `screen_canonical_trees` is 95 MB of uncompressed JSON nobody reads at runtime.

Plus one new concern: we just minted 9 long-lived tokens. If any leaks, the only mitigation today is rotating `JWT_SIGNING_KEY` — which invalidates **all** tokens, including yours. We need a per-token revocation path before more designers come online.

---

## Requirements

- **R1 — No partial writes**: An `/v1/projects/export` request that fails partway through leaves the DB exactly as it was before the request. (T2)
- **R2 — Live atlas**: Renaming a flow, creating a decision, deleting a project, or adding a persona reflects in `/atlas` within 5 seconds, not 60 minutes. (T3)
- **R3 — Visible failures**: When pipeline render fails, the project shell shows an actionable retry CTA, not a permanent 404 storm. (T4)
- **R4 — File identity**: Two different Figma files with the same `(product, platform, path)` produce two separate project rows, not one collapsed row. (T5)
- **R5 — Bounded storage**: The Fly volume's `data/screens/` doesn't grow unboundedly per re-export. (T6)
- **R6 — Tenant safety net**: A query bug that forgets to filter by `tenant_id` cannot expose cross-tenant rows; the DB schema enforces isolation. (T7)
- **R7 — Cheap canonical trees**: `screen_canonical_trees` no longer dominates the SQLite size; gzipped at write time, transparent to read paths. (T8)
- **R8 — Honest atlas empty-state**: `/atlas?platform=web` with zero web flows shows an explicit "no flows on this platform yet" message, not a blank canvas. (T9)
- **R9 — Per-token revocation**: A leaked token can be invalidated within minutes via a CLI without rotating the signing key. (T1)

---

## Scope Boundaries

- **Schema changes are forward-only**: no DOWN migrations. Every UP commits idempotent `IF NOT EXISTS` / data backfill where applicable.
- **No breaking changes to existing routes**: the response shapes of `/v1/projects`, `/v1/projects/:slug`, `/v1/projects/graph`, `/v1/projects/export` stay backwards-compatible. Add fields, never remove.
- **No changes to the JWT signing key**: T1 (revocation list) sits below the existing JWT verifier as a denylist; existing JWTs keep working until explicitly revoked.
- **Hocuspocus / DRD live collab stays deferred**: not in this plan.
- **No frontend rewrites**: T4 adds a single empty-state component; T9 adds an `EmptyState` mount. No tab/page restructure.
- **Migrations test on the local SQLite first** before running on Fly's volume.

---

## Execution Sequence (Critical Path + Parallel Opportunities)

```
                    Critical path (must be serial)
                    ─────────────────────────────────

  T1                T2                T3                T4
  Revoke ───────►   TxWrap ────►      Enqueue ────►     FailedUX
  (1 hr)            (1.5 hr)          (2 hr)            (1 hr)


                    Independent (any order, can interleave)
                    ───────────────────────────────────────

  T5  file_id key            (30 min)
  T6  Version dir GC         (1.5 hr)
  T7  Tenant FK migration    (2 hr)
  T8  CT compression         (1 hr)
  T9  Web platform UX        (30 min)
```

**Why this order:**
- **T1 first** because we just minted 9 long-lived tokens. Closing the leak vector before more designers come online is a security baseline.
- **T2 before T3** because once incremental graph rebuilds fire on every CRUD action, half-completed `HandleExport` calls would trigger graph rebuilds against a half-populated DB, producing inconsistent atlas state. Locking down writes first keeps the derived view honest.
- **T3 before T4** because failed pipelines need to also bust the graph cache so the atlas reflects the failure (e.g., dim the failed flow's node).
- **T5/T6/T7/T8/T9 in any order** — they touch different files, different layers. Best run in pairs where ROI per hour is highest: I'd pick T5 + T9 first (each ~30 min, both visible to users), then T7 (defense-in-depth migration), then T6 (storage hygiene), then T8 (cost optimization).

**Total wall-clock if serial: ~12 hours.** With T5–T9 parallelized into a 4-hour block: **~8 hours focused work.**

---

## Tickets

Each ticket is self-contained: goal, files, steps, verification, rollback, effort, deps. Pick one and execute; come back here for the next.

---

### T1 — Per-token revocation list

**Goal**: A leaked JWT can be invalidated within minutes without rotating `JWT_SIGNING_KEY`. Closes the security gap created by the May-3 mint of 9 long-lived tokens.

**Effort**: ~1 hr.
**Depends on**: nothing.
**Blocks**: T2–T4 (security baseline before further mutation work).

**Files to touch**
- `services/ds-service/migrations/<next>-revoked-jtis.up.sql` (new)
- `services/ds-service/internal/auth/auth.go` (add `IsJTIRevoked` check)
- `services/ds-service/cmd/server/main.go` (wire revocation lookup in `requireAuth`)
- `services/ds-service/cmd/revoke-token/main.go` (new CLI)

**Steps**
1. Migration: create `revoked_jtis (jti TEXT PRIMARY KEY, revoked_at TEXT NOT NULL, revoked_by TEXT, reason TEXT)`. No FK; jtis are opaque.
2. In `requireAuth` middleware, after `VerifyAccessToken` succeeds and we have `claims.ID` (the jti from `RegisteredClaims.ID`), do a single indexed lookup `SELECT 1 FROM revoked_jtis WHERE jti = ?`. If hit → 401 with `{"error":"token_revoked"}`. Cache the lookup in-memory with 60s TTL so happy-path requests don't pay an extra round-trip.
3. CLI `cmd/revoke-token` accepts `--jti <id> --reason <text>` or `--email <email>`. The `--email` form decodes JWTs we've minted (the email claim) and looks up by `sub` (deterministic from email). Inserts into `revoked_jtis`.
4. Bake the binary into the Fly Dockerfile so we can revoke via `fly ssh console -C "/usr/local/bin/revoke-token --email leaked@..."`.

**Verification**
- Mint a fresh token via `cmd/mint-tokens`, verify it works against `/v1/projects` → 200.
- Decode the JWT (jwt.io) to grab the `jti`. Run `revoke-token --jti <id>`. Wait 60s for the cache to expire.
- Same JWT should now return 401 with `token_revoked`.
- Other unrevoked tokens still work.

**Rollback**
- Drop the `revoked_jtis` table; remove the lookup from `requireAuth`. Existing JWTs unchanged.

---

### T2 — Wrap HandleExport in a DB transaction

**Goal**: An `/v1/projects/export` request either commits all writes (project + version + flows + screens) or none. Eliminates the "1 project + 1 version + 0 flows" half-write class of bug.

**Effort**: ~1.5 hr.
**Depends on**: T1 (security baseline).
**Blocks**: T3 (incremental graph rebuilds against half-populated DB are dangerous).

**Files to touch**
- `services/ds-service/internal/projects/repository.go` — accept `DBTX` interface instead of concrete `*sql.DB`
- `services/ds-service/internal/projects/server.go` HandleExport — open tx, defer rollback, commit on success
- `services/ds-service/internal/projects/repository_test.go` — add tx happy/sad path tests

**Steps**
1. Define `type DBTX interface { ExecContext(ctx, query, args...); QueryContext(ctx, query, args...); QueryRowContext(ctx, query, args...) }`. Both `*sql.DB` and `*sql.Tx` satisfy it.
2. Change `TenantRepo`'s field from `r *Repo` (which holds a `db *sql.DB`) to a `dbtx DBTX` accessor that returns whichever the caller set. Add a `WithTx(*sql.Tx) *TenantRepo` constructor that returns a clone bound to the transaction.
3. In `HandleExport`:
   ```go
   tx, err := s.deps.DB.BeginTx(ctx, nil)
   if err != nil { ... }
   defer func() { if !committed { _ = tx.Rollback() } }()
   txRepo := repo.WithTx(tx)
   project := txRepo.UpsertProject(...)
   version := txRepo.CreateVersion(...)
   for _, fp := range req.Flows {
       flow := txRepo.UpsertFlow(...)
       txRepo.InsertScreens(...)
   }
   if err := tx.Commit(); err != nil { ... }
   committed = true
   ```
4. Pipeline (Stage 6 transaction in `pipeline.go:296-321`) is already inside its own tx — no change needed there.
5. Tests: simulate `UpsertFlow` failure on flow #2 (mock or fault-injection), assert that `projects` and `project_versions` rollback (zero rows in those tables for the test tenant after the failed export).

**Verification**
- Run existing test suite: `cd services/ds-service && go test ./internal/projects/...` — must pass.
- Add a new test that asserts rollback on mid-export failure.
- Manual: re-export from the plugin against a freshly-cleared dev DB; verify the project list shows the project + all flows or shows nothing.

**Rollback**
- Revert the commit. All schema unchanged; only Go code touched.

---

### T3 — Wire `EnqueueIncremental` into CRUD handlers

**Goal**: Project / flow / decision / persona create-update-delete trigger an immediate `graph_index` rebuild for the affected (tenant, platform). Atlas freshness drops from 60 min worst-case to <5 sec.

**Effort**: ~2 hr.
**Depends on**: T2 (transactions wrap mutations so the post-commit rebuild sees consistent state).
**Blocks**: T4 (failed pipelines need to bust graph cache).

**Files to touch**
- `services/ds-service/internal/projects/server.go` — handlers for `HandleExport`, flow renames (none currently? verify), `HandleAddDecision`, `HandlePatchViolation`, persona create/update/delete (admin routes), project soft-delete (if exists)
- `services/ds-service/internal/projects/graph_rebuild.go` — confirm `EnqueueIncremental` is called *after* commit, not before

**Steps**
1. Inventory every handler that mutates a row whose data is denormalized into `graph_index`. The audit identified: project create/delete, flow create/rename/delete, decision create/update/delete, persona create/update/delete. Verify by grepping for `INSERT INTO`, `UPDATE`, `DELETE FROM` against those tables.
2. After each successful commit, call:
   ```go
   s.deps.GraphRebuildPool.EnqueueIncremental(graph.Source{
       Kind: graph.SourceFlows,  // or SourceProjects, SourceDecisions, SourcePersonas
       TenantID: tenantID,
       Platform: platform,
   })
   ```
   The pool's 200 ms debounce coalesces bursts; safe to call from every handler.
3. For `HandleExport` specifically, enqueue `SourceProjects` + `SourceFlows` (both touched) for both `mobile` and `web` platforms (the export's platform is one of them; the other still needs a no-op rebuild to stay consistent).
4. Confirm SSE channel `graph:<tenant>:<platform>` publishes `GraphIndexUpdated` after the rebuild completes (already wired per the audit; just verify).

**Verification**
- E2E: log in to docs site, open `/atlas`, run `cmd/admin --rename-flow <id> "New Name"` (or whatever path exists). Within 5 sec, atlas SSE should fire, page should re-fetch graph, label should update.
- If no admin command exists yet, write a minimal one in `cmd/admin` for this verification.

**Rollback**
- Revert the commit. The 60-min safety-net ticker still catches drift.

---

### T4 — Failed-version frontend retry UX

**Goal**: When the Figma render pipeline fails, the project shell shows a clear "Render failed — retry?" CTA instead of a 404 storm.

**Effort**: ~1 hr.
**Depends on**: T2, T3 (consistent state + live atlas).

**Files to touch**
- `services/ds-service/internal/projects/server.go` `HandleProjectGet` — include `latest_version.status` and `latest_version.error_summary` in the response
- `app/projects/[slug]/ProjectShellLoader.tsx` — branch on `version.status === "failed"` → render `<FailedVersionState />` instead of `<ProjectShell />`
- `components/empty-state/FailedVersionState.tsx` (new)
- `app/api/projects/retry-export/route.ts` (new) — proxies to ds-service `POST /v1/projects/:slug/versions/:vid/retry`
- `services/ds-service/internal/projects/server.go` — new handler `HandleVersionRetry` that re-runs the Figma render pipeline on a failed version

**Steps**
1. Repo: extend `GetProjectFull` to include `latest_version.status` and a `latest_version.error_summary` (last `audit_log` entry's details JSON for that version_id, summarized).
2. Frontend: in `ProjectShellLoader`, after fetch, if `state.versions[0].Status === "failed"`, render `<FailedVersionState project={...} version={...} onRetry={...} />` and skip the regular ProjectShell mount.
3. `<FailedVersionState />` shows: project name, version index, error summary, "Retry render" button, "Open in plugin" link. Button POSTs to `/api/projects/<slug>/retry-export` (Next.js API route; thin proxy with the user's JWT to ds-service).
4. Backend: `HandleVersionRetry` re-enqueues the pipeline goroutine on the existing `project_versions` row, flipping status from `failed` → `pending`. Same SSE flow notifies the frontend.

**Verification**
- Manually flip a version's status to `failed` in the dev DB. Refresh the project page; should show the empty-state. Click Retry → status flips to `pending`, then to `view_ready` (assuming Figma is reachable).

**Rollback**
- Revert the commit. Failed versions render the generic "Couldn't load project" empty-state we have today.

---

### T5 — Include `file_id` in `UpsertProject` unique key

**Goal**: Two different Figma files with the same `(product, platform, path)` triple produce two separate project rows, not one collapsed row.

**Effort**: ~30 min.
**Depends on**: nothing (independent of T1–T4).
**Blocks**: nothing.

**Files to touch**
- `services/ds-service/migrations/<next>-projects-file-id-unique.up.sql` (new)
- `services/ds-service/internal/projects/repository.go` `UpsertProject` — switch lookup key from `(tenant_id, slug)` to `(tenant_id, file_id)` with slug derived from path + file-id-suffix on collision

**Steps**
1. Migration:
   - Add `file_id TEXT` column to `projects` (nullable to backfill).
   - Backfill: `UPDATE projects SET file_id = (SELECT MIN(file_id) FROM flows WHERE flows.project_id = projects.id)` — uses the earliest flow's file_id.
   - For projects with no flows (welcome-project), leave NULL.
   - Add partial unique index `idx_projects_unique_file_id ON projects(tenant_id, file_id) WHERE file_id IS NOT NULL AND deleted_at IS NULL`.
   - Keep the existing `(tenant_id, slug)` unique constraint (slug stays meaningful for URLs).
2. `UpsertProject`: lookup by `(tenant_id, file_id)`; if no hit, generate slug from path, append `-<file_id_short>` if slug already exists for the tenant. Insert.
3. `req.FileID` (already present in the export request) gets propagated to the project row.

**Verification**
- Test 1: Export from File A (file_id `aaa`), product `Indian Stocks`, path `research`. Confirm project row created with `file_id=aaa`.
- Test 2: Export from File B (file_id `bbb`), product `Indian Stocks`, path `research`. Confirm a **second** project row created with `file_id=bbb`, slug `research-<bbb-short>`.
- Test 3: Re-export from File A. Confirm `file_id=aaa` row updated, no new row.

**Rollback**
- Drop the partial index. Drop the column. Existing data still queryable via slug.

---

### T6 — Old version directory garbage collection

**Goal**: Per-export PNG directories under `data/screens/<tenant>/<version>/` don't accumulate forever. Retain N most-recent `view_ready` versions per project; prune the rest.

**Effort**: ~1.5 hr.
**Depends on**: nothing.
**Blocks**: nothing.

**Files to touch**
- `services/ds-service/internal/projects/pipeline.go` — at end of Stage 6 (after `view_ready` flip), enqueue cleanup
- `services/ds-service/internal/projects/cleanup.go` (new) — implements `PruneOldVersionDirs(tenantID, projectID, retain int)`
- `services/ds-service/cmd/cleanup-versions/main.go` (new) — one-shot for backfill / manual runs
- `services/ds-service/migrations/<next>-versions-pruned-at.up.sql` (new) — adds `pruned_at` column to `project_versions` so we don't re-process

**Steps**
1. Migration: `ALTER TABLE project_versions ADD COLUMN pruned_at TEXT`.
2. `PruneOldVersionDirs`: list `project_versions` for the project ordered by `version_index DESC`. Keep the top N (default 3 — config via `VERSION_RETENTION` env). For each older version with `pruned_at IS NULL` and status in `(view_ready, failed)`:
   - `os.RemoveAll(filepath.Join(dataDir, "screens", tenantID, versionID))`
   - `UPDATE project_versions SET pruned_at = ? WHERE id = ?`
3. Pipeline: after `view_ready` commit, call `PruneOldVersionDirs(tenant, project, 3)` async.
4. `cmd/cleanup-versions`: backfill — iterate every project, run prune.

**Verification**
- Test on dev DB: have a project with 5 versions, run prune with retain=2. Confirm 3 oldest version dirs are gone, oldest 2 versions have `pruned_at` set, latest 2 versions untouched.
- Disk check: `du -sh data/screens/` before/after.

**Rollback**
- Drop the `pruned_at` column. Stop calling the prune function. Old data already pruned can't be recovered (PNGs are regenerable from Figma if needed).

---

### T7 — Tenant FK constraints migration

**Goal**: Every table holding a `tenant_id` column has a FK to `tenants(id)` ON DELETE CASCADE. A query bug that forgets `WHERE tenant_id = ?` cannot expose cross-tenant rows because the rows would also have to exist with consistent FK pointers.

**Effort**: ~2 hr.
**Depends on**: nothing.
**Blocks**: nothing.

**Files to touch**
- `services/ds-service/migrations/<next>-tenant-fks.up.sql` (new)
- `services/ds-service/internal/db/db.go` — verify `PRAGMA foreign_keys = ON` is set per connection (SQLite default is OFF)

**Steps**
1. Confirm `PRAGMA foreign_keys = ON` is applied via `_pragma` query string in the DSN. If not, add it.
2. Migration adds FKs to: `flows`, `screens`, `project_versions`, `violations`, `audit_jobs`, `decisions`, `drd_comments`, `screen_modes`, `flow_drd`, `screen_prototype_links`, `flow_grants`. SQLite doesn't support `ALTER TABLE ADD CONSTRAINT`, so each requires the rebuild dance:
   ```sql
   PRAGMA foreign_keys = OFF;
   BEGIN;
   CREATE TABLE flows_new (... same columns ..., FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE);
   INSERT INTO flows_new SELECT * FROM flows;
   DROP TABLE flows;
   ALTER TABLE flows_new RENAME TO flows;
   -- recreate indexes and triggers
   COMMIT;
   PRAGMA foreign_keys = ON;
   ```
   Repeat for each table. Concatenate into one transaction so it's atomic.
3. After migration, run `PRAGMA foreign_key_check;` — must return zero rows.

**Verification**
- Dev DB: run migration, then `PRAGMA foreign_key_check;` — empty output.
- Run the existing test suite — must pass without changes (FKs only fire on illegal references which the code never produces).
- Manual: try `INSERT INTO flows (tenant_id, ...) VALUES ('nonexistent-tenant', ...)` — should fail with constraint error.

**Rollback**
- Migration UP is destructive (rebuild tables). Recovery would require restoring the DB from a snapshot. **Take a `fly volumes snapshot create` before running on Fly.**

---

### T8 — Compress `screen_canonical_trees`

**Goal**: The `canonical_tree` column shrinks from ~95 MB to ~15 MB (5x typical for JSON). Read paths transparently decompress.

**Effort**: ~1 hr.
**Depends on**: nothing.
**Blocks**: nothing.

**Files to touch**
- `services/ds-service/migrations/<next>-canonical-trees-compressed.up.sql` (new)
- `services/ds-service/internal/projects/canonical_tree.go` (new) — `CompressTree`, `DecompressTree` helpers using `compress/gzip`
- `services/ds-service/internal/projects/repository.go` — write paths gzip; read paths gunzip
- `services/ds-service/cmd/compress-trees/main.go` (new) — backfill existing rows

**Steps**
1. Migration: add column `canonical_tree_gz BLOB` (nullable). Don't drop `canonical_tree` yet.
2. Helpers: `CompressTree([]byte) []byte`, `DecompressTree([]byte) []byte`. gzip default compression level.
3. Repo:
   - Writes: gzip the JSON, write to `canonical_tree_gz`. Leave `canonical_tree` empty for new rows.
   - Reads: prefer `canonical_tree_gz` (gunzip); fall back to `canonical_tree` (legacy uncompressed).
4. Backfill `cmd/compress-trees`: for each row where `canonical_tree_gz IS NULL` and `canonical_tree IS NOT NULL`: gzip → write → null out `canonical_tree`.
5. After backfill verifies clean, follow-up migration drops the legacy column. Out of scope for this ticket.

**Verification**
- Backfill on dev DB. `SELECT SUM(LENGTH(canonical_tree_gz)) FROM screen_canonical_trees;` — should be ~5–10 MB.
- Audit core (which reads canonical trees) runs without error after backfill.
- Re-export a project; confirm new rows write to `canonical_tree_gz` only.

**Rollback**
- Stop writing `canonical_tree_gz`. Delete the column. Existing data in `canonical_tree` still works.

---

### T9 — Web platform empty graph empty-state

**Goal**: When a tenant has no flows on `web` platform, `/atlas?platform=web` shows "No flows on web yet — switch to mobile" instead of a blank canvas.

**Effort**: ~30 min.
**Depends on**: nothing.
**Blocks**: nothing.

**Files to touch**
- `app/atlas/page.tsx` — branch on `aggregate.nodes.length === 0` → render `<NoPlatformFlows />` instead of `<BrainGraph />`
- `app/atlas/NoPlatformFlows.tsx` (new) — small empty-state with platform-toggle CTA

**Steps**
1. After fetching `aggregate`, check `aggregate.nodes.length`. If 0 and the user is on `?platform=web` (and `mobile` has rows per a quick separate fetch), render the empty state.
2. Empty state copy: "No flows extracted on web yet. Toggle to mobile to see the existing atlas, or run the plugin against a web Figma section." CTA buttons: "Switch to mobile" (changes URL param), "Open plugin" (deep-link).

**Verification**
- Hit `/atlas?platform=web` against the current data (zero web flows). Should show the new empty state.
- Toggle to mobile — atlas renders normally.

**Rollback**
- Revert. Atlas falls back to current blank-canvas behavior.

---

## Cross-ticket Risk Register

| Risk | Probability | Mitigation |
|------|------|-----|
| T2 transaction wrap breaks an unrelated tx-aware path I missed | Low | Run full test suite, scan for `t.r.db.` callers in repo.go and confirm each takes a `DBTX` |
| T3 EnqueueIncremental floods the rebuild pool on bulk operations | Low | Pool already has 200 ms debounce; confirm flooding isn't possible |
| T5 backfill assigns wrong file_id to multi-flow projects | Medium | Backfill takes earliest flow's file_id — wrong if project spans multiple files. Mitigation: log + manual review for projects with >1 distinct file_id across their flows |
| T6 prune deletes a version directory still in use by an open browser session | Low | Browsers cache 5 min; prune the *N+1*th oldest, not the latest. Worst case: a stale tab 404s on screen images for 5 min. |
| T7 migration fails partway, leaving DB in inconsistent state | Medium | Take Fly volume snapshot before running. Wrap each table's rebuild in its own savepoint so a partial failure rolls only the failing table |
| T8 backfill takes too long on large tenants | Low | Process in batches of 100 with explicit COMMIT; can resume on failure since the WHERE filter excludes already-compressed rows |

---

## Verification Strategy

For each ticket, the verification checklist must pass in this order:

1. **Unit tests**: `cd services/ds-service && go test ./...`
2. **Integration test against dev DB**: run the relevant CLI / handler manually, verify expected DB state
3. **Smoke test against staging Fly**: deploy to a staging app (we have one slot in the org), exercise the endpoint
4. **Production deploy**: only after #1–#3 green

Per-ticket-specific verification listed inside each ticket above.

---

## Out of Scope (Explicitly Deferred)

- **Hocuspocus deployment** — DRD live collab. No active demand.
- **Postgres migration** — SQLite + Fly volume meets current scale. Re-evaluate at 10× current data.
- **Audit-server publish endpoint wiring** — `POST /v1/publish` exists in audit-server but doesn't actually persist anywhere. Wire when product needs it.
- **Plugin sync push (incremental "this frame changed")** — current behavior is full-section re-extract. Acceptable for now.
- **Multi-tenant onboarding flow** — currently one tenant (indmoney). Add a self-serve signup flow when we onboard a second.
- **Token revocation list UI** — T1 ships the CLI + DB hook. A web UI to view/revoke tokens is a separate ticket.
- **Web platform Figma extraction** — T9 surfaces the empty case; actually populating web flows requires the plugin to support a web platform target, which is its own product decision.

---

## Single-sentence summary

Nine surgical tickets close out every issue the May-3 audit surfaced; T1 (revoke list), T2 (export tx wrap), T3 (live atlas), T4 (failed-version UX) form the critical path for data integrity + freshness; T5/T6/T7/T8/T9 harden schema, storage, and UX in parallel; total ~12 hours serial / ~8 hours with parallelization.
