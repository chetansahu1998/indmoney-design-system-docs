---
title: Remediate code review findings — Stage 6.5 prerender + canvas race fixes
type: fix
status: active
date: 2026-05-06
origin: /tmp/compound-engineering/ce-code-review/20260506-230117-37f47c06/
---

# Remediate code review findings — Stage 6.5 prerender + canvas race fixes

## Overview

Land the 33 in-scope findings from the 13-reviewer pass on commit `f597e98..HEAD` + uncommitted `pipeline_cluster_prerender.go`. Two findings deferred per user decision:
- `@ts-nocheck` removal in `app/atlas/_lib/leafcanvas.tsx` — separate plan (200-400 line typing refactor).
- Server-side classification refactor — keep duplicate `isCluster` (Go) / `shouldRasterize` (TS) predicates and ship a fixture-based parity test that fails CI on drift.

Sequencing rule: P0 panic-recovery + P1 type-assert / safety-net / shutdown / dedup fixes ship first (U1). Operational hardening follows on the same file (U2) so a single re-review can validate the whole prerender hardening pass. API contract, frontend, performance, and tests follow.

The new file `pipeline_cluster_prerender.go` is currently untracked. U1 commits it as part of the hardened version — there is no value in landing the un-hardened version first.

---

## Problem Frame

The just-completed code review (`/tmp/compound-engineering/ce-code-review/20260506-230117-37f47c06/`) surfaced 35 findings against the three-pronged Atlas leaf-canvas fix:
1. Frontend bug fixes (committed b9b4377): bulk-fetch slice removal, openLeaf idempotency, IO transform recompute, lazy-img removal.
2. `AssetExportTokenTTL` 60s→1h (committed 07a2a33).
3. NEW Stage 6.5 background goroutine that pre-renders icon/illustration/shape clusters during pipeline import (uncommitted `pipeline_cluster_prerender.go`).

Verdict was **"Not ready" for the cluster-prerender (uncommitted) work** because the new goroutine has no panic recovery (one bad cluster crash-loops the process), silent type-assertion fallthrough (potential cross-tenant write under future refactor), and an inverted `LookupAsset` gate that defeats the documented safety net under DB error. The two committed canvas-v2 fixes were **"Ready with fixes"** — the openLeaf fix has a concurrent-invocation race bypass and the IO-recompute path leaks observation against detached DOM.

This plan converts the findings into a sequenced, dependency-ordered fix backlog. The bug shapes have already been verified against source during synthesis; this plan does not re-derive them.

---

## Requirements Trace

- R1. Stage 6.5 goroutine cannot crash the server process under any panic path inside the prerender body.
- R2. Stage 6.5 does not silently degrade tenant scoping; type-assertion failures return an error rather than fall through.
- R3. Stage 6.5's documented Figma-429 safety net holds across all skip paths including transient DB error in `LookupAsset`.
- R4. Stage 6.5 drains cleanly under SIGTERM; in-flight writes either complete or are abandoned with bounded loss.
- R5. Stage 6.5 does not double-fire for the same `version_id` under `HandleVersionRetry` or N concurrent imports.
- R6. Stage 6.5 survives Figma 429 bursts without losing whole 80-ID chunks; transient errors are retried with backoff.
- R7. Stage 6.5 produces bounded log volume regardless of cluster count or Figma error rate.
- R8. Stage 6.5 walk is bounded against adversarial `canonical_tree` inputs (depth and accumulator size).
- R9. Asset cache writes are idempotent on duplicate composite keys; second write does not increment the failure counter.
- R10. `AssetExportTokenTTL` change is reflected in the public `expires_in` contract; the cluster-class TTL is split from the default 60s mint endpoint constant.
- R11. Frontend `openLeaf(X)` is idempotent across serial AND concurrent invocations.
- R12. `IntersectionObserver` re-observe on gesture-settle never accumulates observation against detached DOM elements.
- R13. Module-load gesture-tracker subscription is HMR-safe and test-safe.
- R14. Wheel listener cleanup symmetry is preserved under future `onWheel` identity changes.
- R15. Stage 6.5 prerender path has unit-test coverage (`PrerenderClusters` happy/sad paths) and a fixture-based parity test for `isCluster` (Go) ↔ `shouldRasterize` (TS).
- R16. Headline frontend fixes have regression coverage (openLeaf, leaf-zoom-signal, data-adapters).
- R17. Stage 6.5 outcome is observable to operators and agents via a `prerender_runs` row OR a structured aggregate log line, not per-cluster Warns.

---

## Scope Boundaries

- Not removing `// @ts-nocheck` from `app/atlas/_lib/leafcanvas.tsx` (review finding #24, pre-existing). Separate plan; needs typing of `camRef`/`worldRef`/RAF helpers and `LeafCanvas`/`LeafTopBar` prop shapes — 200-400 line refactor.
- Not moving cluster classification entirely server-side (review alternative for #20). Keeping duplicate `isCluster` (Go) / `shouldRasterize` (TS) predicates and shipping a fixture-based parity test that fails CI on drift.
- Not changing the `AssetExporter` struct shape to be copy-safe (review finding #16, P2 reliability/learnings) beyond adding a `WithRepo(*TenantRepo)` constructor — the underlying `expCopy := *exp` pattern stays in U1's hardening because today the struct is verified copy-safe and a `WithRepo` constructor encapsulates the operation. A future `AssetExporter` mutex addition would surface in U6's tests via `go vet -copylocks`.
- Not refactoring the `pipelineFactory` forward-declared closure pattern in `cmd/server/main.go` (review finding #27, P3 advisory). Verified working; refactor is fragile-to-change but not broken.
- Not pruning the `useLeafZoom` backwards-compat alias call sites (review finding #32). This plan adds a deprecation TODO comment; the rename is a follow-up.
- Not implementing the Phase 2 LookupAsset → in-memory map optimization independently of U5; U5 ships it as a single change.

### Deferred to Follow-Up Work

- `// @ts-nocheck` removal from `app/atlas/_lib/leafcanvas.tsx`: separate plan after this remediation lands.
- Server-side cluster classification (kill TS↔Go duplication): future plan if the parity test starts catching drift in practice.
- `useLeafZoom` alias call-site rename: separate small refactor.

---

## Context & Research

### Relevant Code and Patterns

- `services/ds-service/internal/projects/asset_export.go` — `AssetExporter` struct, `RenderAssetsForLeaf`, `TenantBoundExporter` helper at line 1541, `AssetExportTokenTTL` constant at line 579, `HandleMintAssetExportToken` at line ~793, godoc at line ~713.
- `services/ds-service/internal/projects/pipeline.go` — `Pipeline` struct, `runStages`, Stage 6 commit point at line ~440, `ClusterPrerenderTotalBudget` at line ~282, Stage 6.5 goroutine spawn at line ~458.
- `services/ds-service/internal/projects/repository.go` — `TenantRepo`, existing `Get*` method patterns to mirror, new `AnyLeafIDForVersion`/`LookupVersionIndex`/`LookupAsset`/`StoreAsset` methods at lines 703-742.
- `services/ds-service/cmd/server/main.go` — `pipelineFactory` closure at lines 119-156 (forward-declared `previewPyramid`/`assetExporter` captured by reference); HTTP server setup at line ~398.
- `services/ds-service/internal/projects/audit_jobs.go` — existing pattern for in-process dedup that U1 mirrors for `prerender_runs`.
- `app/atlas/_lib/leafcanvas-v2/node-classifier.ts` — `shouldRasterize` predicate that the Go `isCluster` mirrors. Source of truth for the parity fixture.
- `lib/atlas/live-store.ts` — `openLeaf` at line ~377-496; idempotency early-return at line ~402; awaited HTTP fetch sequence at lines ~471-495 before `set()`.
- `lib/atlas/data-adapters.ts` — bulk fetch at line ~376-377 (slice removal); per-screen `getJSON` parallel fan-out.
- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` — IO `useEffect` lines ~290-385; gesture-tracker subscriptions; `unsubReobserve` at line ~373.
- `app/atlas/_lib/leafcanvas-v2/gesture-tracker.ts` — singleton at module scope; `now` field at line 58 (dead); `reset()` at line ~120 clears listeners.
- `app/atlas/_lib/leafcanvas-v2/leaf-zoom-signal.ts` — module-load subscribe at line ~129; `useLeafZoom` alias at line ~71.
- `app/atlas/_lib/leafcanvas-v2/canvas-log.ts` — `clog`, `cwarn`, `__CANVAS_LOG` window flag pattern.

### Institutional Learnings

- `docs/solutions/2026-04-30-001-projects-phase-1-learnings.md` — tenant-scoping discipline, `audit_jobs` idempotency pattern (mirror in U2 for per-version dedup), SQLite NULL-as-distinct quirk, "ship env-var override with default of 1" anti-pattern reminder.
- `docs/solutions/2026-05-01-002-phase-4-lifecycle-learnings.md` — worker pool shutdown patterns, lifecycle ownership.
- `docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md` — asset_cache key shape, Stage 6 read-before-write tx pattern, FRAME/GROUP cluster boundary semantics.
- `docs/plans/2026-05-06-002-fix-canvas-refresh-evidence-driven-debug-plan.md` — canvas-v2 architecture context, the diagnostic plan that drove the b9b4377 commit.

### External References

None — fixes are codebase-internal, no new technology layer.

---

## Key Technical Decisions

- **Commit the new `pipeline_cluster_prerender.go` as the hardened version, not the un-hardened version followed by patches.** Avoids landing a known-crashy goroutine on `main` even briefly. U1 stages the file fresh with the P0/P1 fixes already applied.
- **Use `sync.WaitGroup` instead of the `done` channel for Phase 2 goroutine drain.** Survives early-break and panic-without-recover paths that the fixed-size `for range clusterIDs { <-done }` does not.
- **Server-shutdown context** is plumbed via a new `Server.shutdownCtx` field on the `Pipeline` struct, derived from a `signal.NotifyContext(SIGTERM, SIGINT)` at the top of `cmd/server/main.go`. The Stage 6.5 goroutine's `bgCtx` is `context.WithTimeout(shutdownCtx, ClusterPrerenderTotalBudget)` so SIGTERM cancels in-flight prerenders. Prefer this over `http.Server.Shutdown()` plumbing because the existing handlers already use the request-scoped ctx pattern; only background work needs the new signal.
- **Per-version dedup is in-process only**, mirroring `audit_jobs`. A `sync.Map[versionID]struct{}` on the `Pipeline` (or a process-global if Pipeline is per-tenant). Acquired before goroutine spawn, released in `defer`. Server crash drops the entry; recovery happens via the on-demand path. Avoids the schema migration a `prerender_runs` table would require.
- **Split `AssetExportTokenTTL` into two constants**: `AssetExportTokenTTLDefault = 60 * time.Second` (used by `HandleMintAssetExportToken` and the public `expires_in` contract) and `AssetExportTokenTTLCluster = 1 * time.Hour` (used internally by cluster-class signed URLs that survive the queue). Restores the public-API contract without reverting the b9b4377 / 07a2a33 fix benefit.
- **`isCluster` ↔ `shouldRasterize` parity test uses a checked-in fixture**, not a JS execution from Go tests. The fixture is `testdata/cluster_classifier_fixture.json` containing canonical_tree blobs + expected cluster IDs; the same fixture is consumed by both the Go test and a new `node-classifier.test.ts`. Drift becomes a Jest+Go test failure simultaneously.
- **`LookupAsset` gate fix** is the one-character flip `lerr == nil && !cached` → `lerr != nil || !cached`. Restores documented intent ("skip if not cached, including on lookup error") with no other behavior change.
- **Type-assert** at line 236 changes from silent fallthrough to `errors.New(...)`. The error returns `(0, err)` from `PrerenderClusters`; the caller in `pipeline.go` already logs prerender errors and proceeds with the on-demand fallback.
- **`StoreAsset` idempotency**: confirm the existing `INSERT ... ON CONFLICT` semantics in U2's repo audit; if missing, add `ON CONFLICT DO UPDATE SET storage_key=excluded.storage_key, bytes=excluded.bytes, mime=excluded.mime, created_at=excluded.created_at`. Re-runs become safe; the failed counter no longer increments on benign duplicates.
- **Per-cluster `Warn` log replaced with sampled-Warn + aggregate-Info**. First 5 failures + every 100th get full Warn; final summary log at end of Phase 2 has `rendered`/`failed`/`first_5_errors[]`. Bounds log volume at ~50 lines per pipeline regardless of failure rate.
- **`walkClusters` depth bound** = 256 (covers any realistic Figma file; Figma's UI caps nesting well below this); accumulator cap = 50000 IDs (early-return + Warn if exceeded).
- **Frontend `openLeaf` concurrent guard** uses an in-flight `Set<string>` field on the store, set before `await fetchLeafCanvas`, deleted in `finally`. Second concurrent call short-circuits at the same guard.
- **IO `unsubReobserve` captures the observed element at observe-time**, not at callback-time. Closure captures `el` from the same scope as the original `observe()` call.
- **`leaf-zoom-signal` HMR guard** uses `(globalThis as any).__lcZoomSignalWired ||= true` pattern; idempotent across module re-evaluation.

---

## Open Questions

### Resolved During Planning

- *Should pre-existing findings ship in this plan?* — No. `@ts-nocheck` (#24) defers; wheel-listener cleanup (#25) ships in U4 because it's a single-line `useCallback` wrap reachable from the same area being touched.
- *Should we move classification server-side?* — No. Parity test in U6 protects against drift.
- *Commit the un-hardened `pipeline_cluster_prerender.go` first or hardened?* — Hardened. U1 commits the file with all P0/P1 fixes already applied.
- *`prerender_runs` schema row vs in-process dedup?* — In-process dedup. Schema migration is bigger than the problem; on-demand path is the recovery mechanism.

### Deferred to Implementation

- Exact verb for renamed repo methods (`GetAnyLeafIDForVersion` vs `GetVersionLeafID` etc.) — pick what reads best in U3 against the call sites; convention is `Get*` returns `ErrNotFound`, `Lookup*` returns `(zero, false, nil)`.
- Backoff strategy specifics (jitter window, max attempts) for U2's Phase 1 429 retry — pick from `services/ds-service/internal/projects/figma_proxy.go` if it already has a backoff helper; else hand-roll exponential 2/4/8s + 0-1s jitter, max 3 attempts.
- `prerender_runs` heartbeat (U8) data model — could be a single in-memory ring buffer surfaced via a status endpoint, or a SQLite table. Pick during U8 based on what monitoring hooks already exist.
- Whether to split U5 (perf polish) into one commit or three. Default to one commit; split if individual changes need independent rollback.

---

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

**Stage 6.5 prerender goroutine — hardened control flow (after U1 + U2):**

```text
Pipeline.runStages → Stage 6 commit
                  ↓
       acquire prerenderInFlight[versionID] (sync.Map)
                  ↓
       if already in flight: log "skip — already running"; return
                  ↓
       go func() {
           defer release prerenderInFlight[versionID]
           defer recover() → log + metric, never propagates

           bgCtx := context.WithTimeout(server.shutdownCtx, 30min)
           defer cancel

           clusterIDs := ExtractClusterIDs(canonicalTree)  // depth + size bounded
           PrerenderClusters(bgCtx, log, deps, in, clusterIDs, cfg)
       }()

PrerenderClusters:
    Phase 1 — batched Figma /v1/images
       for each chunk of 80 in clusterIDs:
           if bgCtx.Err(): break
           retry-with-backoff (max 3, exponential + jitter):
               results = expCopy.RenderAssetsForLeaf(...)
           accumulate sampled-Warn on failure (first 5 + every 100th)

    Phase 2 — per-node downsample + persist
       sync.WaitGroup wg
       for each nodeID in clusterIDs:
           if bgCtx.Err(): break
           sem.Acquire(); wg.Add(1)
           go func() {
               defer wg.Done(); defer sem.Release()
               defer recover() → counter, never propagates

               if !phase1Cached[nodeID]: return  // in-memory map, not LookupAsset
               pyramid := PreviewPyramid.Render(...)
               for each tier: StoreAsset (UPSERT)
           }()
       wg.Wait()

       log.Info("prerender complete",
           rendered=N, failed=M, sampled_errors=[...])
```

**Frontend `openLeaf` concurrent-guard pattern (after U4):**

```text
openLeaf(leafID):
    if !leafID:                                  // selection clear path
        set selection=null; return

    if loadingLeafIDs.has(leafID):               // concurrent-call guard
        update selection only; return

    if existingSlot:                              // idempotency guard (existing fix)
        update selection only; return

    loadingLeafIDs.add(leafID)
    try:
        canvas, overlays = await Promise.all([...])
        slot = { ... loadedAt: Date.now() }
        set { leafSlots: { ...prev, [leafID]: slot }, selection }
    finally:
        loadingLeafIDs.delete(leafID)
```

---

## Implementation Units

- U1. **Backend prerender — P0/P1 hardening**

**Goal:** Land `pipeline_cluster_prerender.go` as a hardened first commit with panic-recovery, fail-loud type-assert, fixed `LookupAsset` gate, server-shutdown context, and `WaitGroup` drain. After this unit, the goroutine cannot crash the process and respects the documented Figma-429 safety net.

**Requirements:** R1, R2, R3, R4

**Dependencies:** None (this is the foundation; the file is currently untracked).

**Files:**
- Create / track: `services/ds-service/internal/projects/pipeline_cluster_prerender.go` (the existing untracked file, modified inline before commit)
- Modify: `services/ds-service/internal/projects/pipeline.go`
- Modify: `services/ds-service/cmd/server/main.go`

**Approach:**
- Inside the goroutine in `pipeline.go:458`, add `defer func() { if r := recover(); r != nil { p.Log.Error("stage 6.5 panic", "version_id", in.VersionID, "panic", r) } }()` as the FIRST defer.
- Inside each Phase 2 per-node `go func()` in `pipeline_cluster_prerender.go:306`, add the same `defer recover()` as the FIRST defer (before the `defer { <-sem; done <- ... }`). On panic, increment `failed.Add(1)` so the aggregate count is honest.
- Replace `done` channel + `for range clusterIDs { <-done }` drain pattern with `sync.WaitGroup`. Per-goroutine `defer wg.Done()`. Outer `wg.Wait()`.
- Flip `LookupAsset` gate at line 320: `lerr == nil && !cached` → `lerr != nil || !cached`. Update the comment block above to read "Skip nodes whose source PNG isn't cached *or* whose cache lookup failed (transient SQLite contention)".
- Replace silent type-assert at line 236 with `tr, ok := deps.Repo.(*TenantRepo); if !ok { return 0, errors.New("cluster prerender: deps.Repo must be *TenantRepo (got incompatible type)") }`. Move the assignment into the success branch.
- In `cmd/server/main.go`, add `signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)` at the top of `main()`. Pass the resulting `shutdownCtx` into `Pipeline` construction (new field `Server.ShutdownCtx context.Context`). The Stage 6.5 spawn in `pipeline.go:458` derives `bgCtx` via `context.WithTimeout(p.ShutdownCtx, ClusterPrerenderTotalBudget)`.
- Phase 2 dispatch loop at `pipeline_cluster_prerender.go:303`: add `if ctx.Err() != nil { break }` at the top of the loop (mirror Phase 1's existing pattern). Cleans up the cancellation path now that `WaitGroup` allows partial spawns.

**Execution note:** Test-first not required — these are surgical fixes with verifiable outcomes. U6 ships the regression coverage.

**Patterns to follow:**
- `defer recover()` shape from `services/ds-service/internal/projects/audit_jobs.go` worker goroutines (mirror the same Error-log + counter-increment shape).
- `signal.NotifyContext` pattern from any existing `cmd/` binary that already has graceful shutdown — if none exist, this is a greenfield wiring; keep it minimal.

**Test scenarios:** *U6 owns the test file. This unit's verification is via U6's tests.*

**Verification:**
- `go build ./...` passes.
- File `pipeline_cluster_prerender.go` is staged and the diff in `pipeline.go`/`main.go` reads as a coherent set.
- Manual smoke: run a pipeline import; trigger a forced panic in `RenderPreviewPyramid` (e.g., via a temporary `panic("test")`); confirm the process stays up and the goroutine logs the panic.
- Manual smoke: send SIGTERM mid-prerender; confirm `bgCtx.Done()` fires and the spawn loop exits within ~1s.

---

- U2. **Backend prerender — operational hardening**

**Goal:** Concurrent-invocation dedup, Phase 1 retry-with-backoff, sampled logging, `walkClusters` depth bound, `StoreAsset` UPSERT confirmation, `AnyLeafIDForVersion` ORDER BY.

**Requirements:** R5, R6, R7, R8, R9, plus the SQLite stable-row contract for `AnyLeafIDForVersion`.

**Dependencies:** U1 (panic recovery must exist before retry-with-backoff, otherwise an inner panic during retry crashes the process).

**Files:**
- Modify: `services/ds-service/internal/projects/pipeline_cluster_prerender.go`
- Modify: `services/ds-service/internal/projects/pipeline.go` (add `prerenderInFlight sync.Map` field on Pipeline, OR a process-global if Pipeline is per-tenant — verify in U1 audit)
- Modify: `services/ds-service/internal/projects/repository.go` (`AnyLeafIDForVersion` ORDER BY, `StoreAsset` UPSERT confirmation)

**Approach:**
- Per-version dedup: at Stage 6.5 goroutine spawn site in `pipeline.go:458`, `if _, loaded := prerenderInFlight.LoadOrStore(in.VersionID, struct{}{}); loaded { p.Log.Info("stage 6.5 skip — already running", "version_id", in.VersionID); return }`. Defer `prerenderInFlight.Delete(in.VersionID)`.
- Phase 1 chunk retry: wrap `expCopy.RenderAssetsForLeaf(...)` in 3-attempt loop with exponential backoff (2s, 4s, 8s) + 0-1s jitter. Detect HTTP 429 / 5xx via the error type `figmaRateLimitErr` (introduce if not exposed). Skip retry if `ctx.Err() != nil`. Reuse a backoff helper from `figma_proxy.go` if one exists; else hand-roll inline.
- Sampled logging: replace per-cluster `Warn` with a `sampledWarn` helper that logs first 5 + every 100th + final aggregate. End-of-Phase-2 log line includes `rendered`, `failed`, `sampled_errors` (last 5 distinct error messages).
- `walkClusters` depth bound: thread a `depth int` parameter; return early if `depth > 256`. Accumulator cap: if `len(*acc) >= 50000`, log Warn and return.
- `StoreAsset` UPSERT: read `repository.go` definition; confirm `INSERT ... ON CONFLICT DO UPDATE SET ...` semantics. If not present, add it. Composite key is `(tenant_id, file_id, node_id, format, scale, version_index)`.
- `AnyLeafIDForVersion` SQL: add `ORDER BY f.id ASC LIMIT 1` (currently `LIMIT 1` only).
- Move `ClusterPrerenderTotalBudget` from `pipeline.go:282` into `ClusterPrerenderConfig` as a new `TotalBudget time.Duration` field; defaulted in `DefaultClusterPrerenderConfig`. (Resolves finding #31.)

**Patterns to follow:**
- `audit_jobs.go` enqueue/dedup pattern — mirror the `LoadOrStore`+`defer Delete` shape.
- Existing `figma_proxy.go` rate-limit / backoff helpers if any.
- Existing `INSERT ... ON CONFLICT` patterns elsewhere in `repository.go` for the UPSERT shape.

**Test scenarios:** *U6 owns coverage; this unit's regression risk is fully captured by U6's "Phase 1 chunk failure → retry then continue" and "concurrent invocations on same version_id → second is no-op" scenarios.*

**Verification:**
- `go build ./...` passes; `go vet ./...` passes.
- Manual smoke: `curl` the import endpoint twice in quick succession for the same version; confirm only one prerender goroutine starts and the second logs "skip — already running".
- Manual smoke: temporarily inject a 429 from `RenderAssetsForLeaf`; confirm 3 retry attempts visible in logs before the chunk Warn fires.
- Inspect logs from a real import: confirm at most ~50 Warn lines regardless of cluster count.

---

- U3. **Backend API contract — `expires_in` + repo naming + interface seam**

**Goal:** Restore the public `expires_in: 60` contract for the mint endpoint by splitting the TTL constant. Tighten the `PrerenderRepo` seam (compile-time satisfaction assertion + remove redundant runtime type-assert that U1 already neutralized). Document repo method naming convention.

**Requirements:** R10

**Dependencies:** U1 (the type-assert at line 236 is replaced with fail-loud in U1; this unit removes the now-unreachable interface seam if practical).

**Files:**
- Modify: `services/ds-service/internal/projects/asset_export.go` (split TTL constants; restore `expires_in` godoc)
- Modify: `services/ds-service/internal/projects/pipeline_cluster_prerender.go` (use `AssetExportTokenTTLCluster` if Stage 6.5 ever mints; add `var _ PrerenderRepo = (*TenantRepo)(nil)`)
- Modify: `services/ds-service/internal/projects/repository.go` (rename `AnyLeafIDForVersion` → `GetAnyLeafIDForVersion` if call-site count is small AND consistent with surrounding `Get*` pattern; add a comment block above the new methods documenting the `Get*` returns ErrNotFound / `Lookup*` returns `(zero, false, nil)` convention)

**Approach:**
- Replace `const AssetExportTokenTTL = 1 * time.Hour` (line 579) with two constants:
  - `AssetExportTokenTTLDefault = 60 * time.Second` — used by `HandleMintAssetExportToken` (line ~793) and stamped into `expires_in` response.
  - `AssetExportTokenTTLCluster = 1 * time.Hour` — used internally by code paths that mint URLs that survive the cluster mint queue (audit call sites; the b9b4377 + 07a2a33 fix benefit was for cluster-class URLs, so the internal callers are what need the longer TTL).
- Identify which call sites need the long TTL — the answer comes from `git log -p f597e98..HEAD -- services/ds-service/internal/projects/asset_export.go` showing what the 60s→1h change was meant to fix. Probable: `RenderAssetsForLeaf` token mint; `HandleMintAssetExportToken` should NOT.
- Restore the `expires_in: 60` godoc at line ~713; update to reference the new constant name.
- Add `var _ PrerenderRepo = (*TenantRepo)(nil)` immediately after the `PrerenderRepo` interface declaration in `pipeline_cluster_prerender.go`. Compile-time guarantees `TenantRepo` satisfies the interface.
- Decide on repo method naming: `AnyLeafIDForVersion` is unique-verb. Either rename to `GetAnyLeafIDForVersion` (matches `Get*` neighbors) or leave and add a doc comment justifying. Default: rename, since call sites are limited to U1's wiring.

**Test scenarios:**
- Happy path: `HandleMintAssetExportToken` response includes `expires_in: 60` (matches public contract).
- Happy path: cluster-internal mint uses 3600s and the URL validates after 30 minutes.
- Edge case: `var _ PrerenderRepo = (*TenantRepo)(nil)` causes a compile error if `TenantRepo` ever drops a method.

**Verification:**
- `go build ./...` passes (catches `var _` assertion).
- `go test ./...` for asset_export passes.
- `grep -r "AssetExportTokenTTL\b" services/` shows no references to the old constant name.
- Manual smoke: mint a token via the public endpoint; verify response JSON has `expires_in: 60`.

---

- U4. **Frontend race fixes**

**Goal:** Close the `openLeaf` concurrent-invocation race, fix IO `unsubReobserve` target drift, consolidate or order the dual gesture-tracker subscribers, guard the `leaf-zoom-signal` HMR leak, fix the wheel-listener cleanup symmetry, drop the dead `now` field on `GestureTracker`, route `gesture-tracker` logging through `clog`, add deprecation comments to the `useLeafZoom` alias.

**Requirements:** R11, R12, R13, R14

**Dependencies:** None on backend. Can land in parallel with U1-U3.

**Files:**
- Modify: `lib/atlas/live-store.ts`
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/gesture-tracker.ts`
- Modify: `app/atlas/_lib/leafcanvas-v2/leaf-zoom-signal.ts`
- Modify: `app/atlas/_lib/leafcanvas.tsx` (wheel listener `useCallback` wrap only — `@ts-nocheck` removal is deferred per Scope Boundaries)

**Approach:**
- **`openLeaf` concurrent guard** (`lib/atlas/live-store.ts`): add private `loadingLeafIDs: Set<string>` field on the store. Inside `openLeaf`, between the `existingSlot` early-return and the `await fetchLeafCanvas` call, add:
  ```text
  if loadingLeafIDs.has(leafID): update selection only; return
  loadingLeafIDs.add(leafID)
  try { ...fetch + set... } finally { loadingLeafIDs.delete(leafID) }
  ```
- **IO target capture** (`LeafFrameRenderer.tsx:373`): inside the `useEffect` that calls `observer.observe(el)`, capture `el` in a closure variable; `unsubReobserve` reads the captured `el`, not `wrapperRef.current`. Re-observe only when `wrapperRef.current === el` (the original target).
- **Gesture subscriber order** (`LeafFrameRenderer.tsx:290`): collapse the two `canvasGestureTracker.subscribe` calls (`ensureGestureSubscriber` drain + `unsubReobserve`) into one settle handler that performs drain-then-reobserve in deterministic order. Remove the second subscribe.
- **`leaf-zoom-signal` HMR guard** (`leaf-zoom-signal.ts:129`): wrap the module-load `canvasGestureTracker.subscribe(...)` block in `if (!(globalThis as any).__lcZoomSignalWired) { (globalThis as any).__lcZoomSignalWired = true; ... }`. Idempotent across HMR + Vitest module-reset.
- **Wheel listener cleanup** (`leafcanvas.tsx:247`, pre-existing per finding #25): wrap `onWheel` in `useCallback(... , [deps])` and add to the `useEffect` deps. `addEventListener`/`removeEventListener` reference the same identity. **Note:** This is the only `leafcanvas.tsx` change in this unit; full `@ts-nocheck` removal is out of scope per Scope Boundaries. Reading the file with `@ts-nocheck` still in place is fine.
- **Dead `now` field** (`gesture-tracker.ts:58`): remove `private now: () => number` declaration, remove the constructor-args destructuring of `now`, drop the corresponding `now: clk.now` line from the test harness in `__tests__/gesture-tracker.test.ts`.
- **`gesture-tracker` clog** (`gesture-tracker.ts:96-128`): replace inline `if (typeof window !== 'undefined' && window.__CANVAS_LOG) console.log(...)` with `clog('gesture', 'begin' | 'settle', { ... })`. Import from `./canvas-log`.
- **`useLeafZoom` deprecation comment** (`leaf-zoom-signal.ts:71`): add a `// DEPRECATED: alias for useLeafZoomSettled — remove call sites and delete in follow-up plan` comment block above the alias. No call-site rename in this unit.

**Patterns to follow:**
- The `loadingLeafIDs` shape mirrors any existing in-flight tracking pattern in the store; if there isn't one, this becomes the convention.
- `clog` usage from existing call sites in `LeafFrameRenderer.tsx` and `leafcanvas.tsx`.

**Test scenarios:** *U7 owns the regression coverage. Verification is via that test file.*

**Verification:**
- TypeScript builds cleanly (`npm run build` or `tsc --noEmit` if available).
- Manual smoke: dev server with HMR; edit `leaf-zoom-signal.ts`; confirm only one gesture-tracker listener exists (instrument temporarily or trust U7's test).
- Manual smoke: rapid leaf-switch (URL change + click within 100ms); confirm only one canonical_tree fetch in DevTools Network panel.
- Manual smoke: pan-zoom session; confirm IO observation count stays bounded across many settles.

---

- U5. **Performance polish**

**Goal:** Replace Phase 2's per-node `LookupAsset` SQLite reads with an in-memory map of Phase 1's success set. Guard `clog` allocation overhead. Bound IO `unobserve+observe` work to changed bands only.

**Requirements:** Performance-tier findings #17, #34, P3 #35 (heartbeat is in U8).

**Dependencies:** U1 (Phase 1 retry / partial-success counting must be stable before relying on Phase 1 result set as the gate).

**Files:**
- Modify: `services/ds-service/internal/projects/pipeline_cluster_prerender.go`
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/canvas-log.ts` (only if changing `clog` signature to accept a thunk)

**Approach:**
- **Phase 2 LookupAsset → Phase-1 success-set map**: capture each chunk's `RenderAssetsForLeaf` results into `phase1Cached := map[string]struct{}{}` keyed by `result.NodeID`. In Phase 2's per-node `go func`, gate on `if _, ok := phase1Cached[nodeID]; !ok { return }` instead of `LookupAsset`. Removes ~2000 SQLite reads per pipeline. Keep the existing `LookupAsset` import in case future regression needs it.
- **`clog` allocation guard**: change `clog` signature in `canvas-log.ts` to accept either an object literal OR a thunk: `clog(ns, msg, dataOrThunk?: object | (() => object))`. If thunk, only invoke when `__CANVAS_LOG` is true. Update hot-path call sites in `LeafFrameRenderer.tsx:122`, `:376` to pass thunks. Cold-path call sites can stay as object literals.
- **Bound per-frame IO re-observe**: in the consolidated settle handler from U4, walk only bands whose visibility-bucket has changed since the previous settle (track via a small `Map<bandID, bucket>`). Skip re-observe for bands whose bucket is unchanged. Reduces ~316 IO mutations per gesture-end (79 frames × 4 ops) to O(visible-changed). Acceptable to land this as a follow-up if U4's consolidated handler is already shipped.

**Patterns to follow:**
- `phase1Cached` is local to `PrerenderClusters`; no exported type needed.
- `clog` thunk pattern is the standard "lazy evaluation for debug logging" trick; common in Rails `Rails.logger.debug { "..." }`.

**Test scenarios:**
- Performance regression check: time `PrerenderClusters` against a fixture with 2000 cluster IDs before vs after the LookupAsset removal. Expect ~2s reduction.
- Edge case: Phase 1 chunk fails for one batch; Phase 2 correctly skips those nodes (verified via U6's test).

**Verification:**
- `go test ./...` passes (existing + U6 tests).
- TypeScript builds cleanly.
- Manual smoke: open a 79-frame leaf with `__CANVAS_LOG = false`; confirm no perceptible jank during pan/zoom; confirm DevTools shows zero `console.log` from `clog` thunks.

---

- U6. **Backend tests — Stage 6.5 + classifier parity + token TTL**

**Goal:** Test coverage for `PrerenderClusters` (happy + sad paths), `ExtractClusterIDs`, `isCluster`-↔-`shouldRasterize` parity (shared fixture), `AssetExportTokenTTL` clock-skew boundaries.

**Requirements:** R15

**Dependencies:** U1, U2, U3 (tests cover the post-hardening behavior).

**Files:**
- Create: `services/ds-service/internal/projects/pipeline_cluster_prerender_test.go`
- Create: `services/ds-service/internal/projects/cluster_classifier_parity_test.go`
- Create: `services/ds-service/internal/projects/testdata/cluster_classifier_fixture.json` (shared between Go + TS tests)
- Modify: `services/ds-service/internal/projects/asset_export_test.go` (add clock-skew boundary tests)
- Create: `app/atlas/_lib/leafcanvas-v2/__tests__/node-classifier.test.ts` (consumes the same fixture; lives next to the TS classifier)

**Approach:**
- Build a `fakePrerenderRepo` struct implementing `PrerenderRepo` for table-driven testing. Inject into `ClusterPrerenderDeps` alongside a `fakePreviewPyramid`.
- `pipeline_cluster_prerender_test.go` covers:
  - Happy path: 3 cluster IDs, all render, all persist.
  - `ExtractClusterIDs` with depth-bounded fixture.
  - `walkClusters` depth-bound fires at 256.
  - `walkClusters` accumulator-cap fires at 50000.
  - Phase 1 chunk failure → retry-with-backoff → eventual success.
  - Phase 1 chunk failure → all retries exhausted → log + skip.
  - Phase 2 cache-hit miss (`phase1Cached[nodeID]` false) → skip without calling `RenderPreviewPyramid`.
  - Concurrent invocations on same `version_id` → second is no-op (covers U2's dedup).
  - `ctx.Done()` mid-Phase-2 → spawn loop breaks; `wg.Wait()` returns within 1s.
  - Panic in Phase 2 per-node `RenderPreviewPyramid` → counter increments, process stays up (covers U1's recover).
  - `StoreAsset` returns conflict-error → counter does NOT increment (covers U2's UPSERT).
  - `nil` `PreviewPyramid`/`Repo` deps → returns error.
  - Type-assert failure (deps.Repo not `*TenantRepo`) → returns error (covers U1's fail-loud).
- `cluster_classifier_parity_test.go` and `node-classifier.test.ts` both load `testdata/cluster_classifier_fixture.json`. Each fixture entry: `{ name, canonical_tree (JSON), expected_cluster_ids: [...] }`. Both test runners assert their classifier produces the same set. Drift on either side fails CI.
- `asset_export_test.go` clock-skew test: mint a token with `AssetExportTokenTTLCluster`; advance the test clock 30 minutes; validate token; assert `200`. Then advance to 65 minutes; assert rejection. Same shape for the default 60s TTL.

**Test scenarios:** *(self-referential — this unit IS the test scenarios for U1+U2+U3)*

**Verification:**
- `go test ./services/ds-service/internal/projects/...` passes with new tests included.
- `npm test -- --testPathPattern node-classifier` passes.
- Intentionally edit `isCluster` (Go) or `shouldRasterize` (TS) to introduce drift; confirm one test fails on each side.

---

- U7. **Frontend tests — openLeaf + leaf-zoom-signal + data-adapters + IO recompute**

**Goal:** Regression coverage for the headline frontend fixes shipped in b9b4377 and the U4 consolidations.

**Requirements:** R16

**Dependencies:** U4 (tests cover post-fix behavior).

**Files:**
- Create: `lib/atlas/__tests__/live-store.test.ts`
- Create: `lib/atlas/__tests__/data-adapters.test.ts`
- Create: `app/atlas/_lib/leafcanvas-v2/__tests__/leaf-zoom-signal.test.ts`
- Create (optional): `app/atlas/_lib/leafcanvas-v2/__tests__/LeafFrameRenderer.test.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/__tests__/gesture-tracker.test.ts` (drop `now: clk.now` line per U4)

**Approach:**
- `live-store.test.ts`:
  - `openLeaf(X)` then `openLeaf(X)` → `leafSlots[X].loadedAt` unchanged across calls; `fetchLeafCanvas` mock called exactly once.
  - `openLeaf(X)` (in flight) + concurrent `openLeaf(X)` → second call short-circuits via `loadingLeafIDs`; mock called exactly once.
  - `openLeaf(X)` then `openLeaf(Y)` → both slots populated; selection points to Y.
  - `openLeaf(null)` → selection cleared; `leafSlots` untouched.
- `data-adapters.test.ts`:
  - Mock 3 screens with canonical_tree fetches; assert all 3 returned in `canonicalTreeByScreenID` (covers slice removal).
  - One rejected fetch in `Promise.allSettled` does not drop the others.
  - Probe-first pattern still silences 404 spam for unbuilt projects.
- `leaf-zoom-signal.test.ts`:
  - `setLeafZoom(z)` updates both `liveZoom` and `settledZoom` when not gesturing (or after settle).
  - During gesture, only `liveZoom` advances; `settledZoom` snaps to last `liveZoom` after debounce.
  - HMR guard: re-evaluating the module does not add a second listener (use `GestureTracker.listeners.size` assertion or expose a count via the singleton).
  - Throwing settled-listener does not block another listener.
- `LeafFrameRenderer.test.tsx` (optional, may be hard with no JSDOM IO):
  - Mount with stub slot; simulate gesture-end via `canvasGestureTracker.tick`; assert no preview-tier change during burst; change after debounce.
  - Or: extract the recompute decision into a pure function and unit-test that.

**Patterns to follow:**
- Existing test patterns under `app/atlas/_lib/leafcanvas-v2/__tests__/gesture-tracker.test.ts`: FakeClock, module-singleton reset hooks.
- React Testing Library + jsdom — verify the project's existing TS test runner before assuming Vitest.

**Test scenarios:** *(self-referential — this unit IS test scenarios for U4)*

**Verification:**
- All new tests pass.
- Intentionally regress `openLeaf` (remove the `loadingLeafIDs` guard) → concurrent test fails.
- Intentionally regress `leaf-zoom-signal` HMR guard → listener-count test fails.

---

- U8. **Operational observability — Stage 6.5 status**

**Goal:** Make Stage 6.5 outcome programmatically observable to operators and agents without per-cluster log noise. Status should answer "did Stage 6.5 succeed for version X" without grepping logs.

**Requirements:** R17

**Dependencies:** U2 (sampled logging is the prerequisite — without it, this unit is just adding more noise).

**Files:**
- Modify: `services/ds-service/internal/projects/pipeline_cluster_prerender.go` (emit status via the chosen mechanism)
- Modify: `services/ds-service/internal/projects/pipeline.go` (or add a new `prerender_status.go`) — a process-wide ring buffer of the last N runs with `version_id`, `started_at`, `finished_at`, `rendered`, `failed`, `last_error`. In-memory only; survives restart loss is fine because the on-demand path is the recovery mechanism.
- Modify: `services/ds-service/cmd/server/main.go` — register `GET /v1/prerender/status` (or `/admin/prerender/status` if admin-gated) returning the ring buffer as JSON.

**Approach:**
- Choose between two designs (default to ring buffer; adjust during implementation if the team prefers a SQLite table):
  - **Ring buffer (default)**: simple `*ringBuffer[PrerenderRun]` on the `Pipeline` struct or process-global. Capacity 256. Each Stage 6.5 entry adds a record on completion. JSON endpoint dumps the buffer.
  - **SQLite table** (alternative): new migration `prerender_runs(version_id PK, started_at, finished_at, rendered, failed, last_error)`. Schema migration overhead.
- Endpoint shape:
  ```text
  GET /v1/prerender/status?version_id=... → { runs: [...] }
  GET /v1/prerender/status (no filter)     → { runs: [last 256] }
  ```
- This unit is **optional / advisory** — it resolves the agent-native + reliability-tier concern that no programmatic status hook exists. Skip if the team wants to defer.

**Patterns to follow:**
- Existing `/v1/health` endpoint shape in `main.go`.
- Any existing in-memory status structs (e.g., `IdempotencyCache`, `RateLimiter`).

**Test scenarios:**
- Happy path: trigger a Stage 6.5 run; GET endpoint returns one row with `rendered > 0`.
- Edge case: trigger 300 runs; ring buffer truncates to last 256.
- Filter: GET with `version_id` query returns only matching rows.

**Verification:**
- `curl http://localhost:8080/v1/prerender/status` returns a non-empty `runs` array after one import.
- Endpoint authentication matches existing admin endpoints (or is wide-open if `/v1/health` is wide-open).

---

## System-Wide Impact

- **Interaction graph:** Stage 6.5 goroutine now derives ctx from server shutdown context (U1) — every callsite that reaches `ClusterPrerenderTotalBudget` gets cancelled on SIGTERM. The `prerenderInFlight sync.Map` (U2) is shared across all per-tenant Pipelines if Pipeline is per-tenant (verify in U1 audit). Frontend `loadingLeafIDs` (U4) lives on the Atlas store and may be observable to other consumers reading store state.
- **Error propagation:** Stage 6.5 panics no longer propagate to the process (U1). Phase 1 retry-exhaustion errors are logged but never returned. Phase 2 per-node panics are counted in `failed` but never returned. The function-level error return reflects only setup errors (nil deps, type-assert failure, version-leaf lookup failure).
- **State lifecycle risks:** Server SIGTERM during Stage 6.5 leaves partial pyramid rows (some tiers persisted, others not). Acceptable because the on-demand path fills gaps at user-open. `prerenderInFlight` map entry leaks if process crashes between `LoadOrStore` and `defer Delete` — acceptable because process restart clears the map. `loadingLeafIDs` (frontend) leaks if a fetch promise never resolves AND `finally` is dropped — defensive, but `Promise.finally` always runs.
- **API surface parity:** `expires_in` HTTP response is split between `AssetExportTokenTTLDefault` (60s, public) and `AssetExportTokenTTLCluster` (1h, internal). Frontend callers consuming the public mint endpoint see the contract restored. New `/v1/prerender/status` endpoint is additive.
- **Integration coverage:** U6 + U7 cover the backend goroutine + classifier-parity + token-TTL boundary + frontend race fixes end-to-end. The `cluster_classifier_fixture.json` is the contract between Go and TS.
- **Unchanged invariants:** `AssetExporter.Repo` runtime composition, `Pipeline` per-tenant scoping model, on-demand `HandleAssetDownload` path (this remains the authoritative recovery mechanism for any Stage 6.5 miss). Frontend `LeafFrameRenderer`'s deferred-mount queue (separate from the gesture-tracker fixes). `useLeafZoomSettled` semantics (only the alias deprecation comment is added).

---

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| `signal.NotifyContext` wiring in `main.go` is a greenfield change to the boot sequence; could regress existing handler shutdown paths. | Keep the change minimal — only the Stage 6.5 goroutine derives from `shutdownCtx`. HTTP handlers continue to use request-scoped ctx. Manual smoke: SIGTERM during a regular asset request should still return cleanly. |
| `expires_in` split could break a hidden frontend caller that hardcoded a refresh-before-expiry timer based on the 60s contract. | U3 audits frontend call sites via `grep "expires_in" app/ lib/` before splitting. If any caller relies on the bug-introduced 3600s value, document and route through a follow-up. |
| `prerenderInFlight sync.Map` placement — Pipeline may be per-tenant (one map per tenant, no cross-tenant dedup) or per-process. U2 needs to verify. | U1's audit step explicitly locates the Pipeline construction site and decides. If per-tenant, escalate to a process-global map keyed on `tenantID + versionID`. |
| Phase 1 retry-with-backoff could amplify total Stage 6.5 time under sustained Figma 429s — 3 attempts × 8s backoff × ~12 chunks = ~5 min worst case. | Total wall-time stays under `ClusterPrerenderTotalBudget` (30 min). The on-demand fallback handles whatever times out. |
| `walkClusters` depth bound at 256 may reject a future legitimate Figma file with deeper nesting. | 256 is well above any current Figma file (UI surfaces ~10 levels typical). If hit in practice, raise the bound; the early-return path is benign (under-counts clusters; on-demand path renders them). |
| `loadingLeafIDs` introduces shared mutable state on the frontend store — race with other store mutations? | The store uses Zustand-style atomic updates; `Set.add`/`Set.delete` are synchronous. Race surface is bounded to the fetch sequence between `add` and the awaited `Promise.all`. Test in U7 explicitly covers concurrent invocation. |
| `cluster_classifier_fixture.json` becomes a coordination point between Go and TS test runners. Adding new fixture cases requires updating both. | Document the fixture format in a `README.md` next to the testdata file. CI failure on either side is the forcing function — drift cannot ship. |
| U8 (status endpoint) adds a new API surface that needs auth review. | Default to admin-gated (mirror `/admin/*` routes if they exist) or skip the unit if auth review is bottleneck. |

---

## Documentation / Operational Notes

- **Logging volume**: U2 reduces per-import log volume from up to ~17,600 Warn lines (under degraded Figma) to ~50 lines (sampled + aggregate). No log-collector configuration change should be needed.
- **Monitoring**: `/v1/prerender/status` (if U8 ships) gives ops a programmatic check. Otherwise, the aggregate `Info` log line at end of Stage 6.5 is the per-import signal.
- **Rollout**: All changes are backward-compatible. No schema migration. Deploy in any order; the Pipeline factory still works with the old goroutine if the new file is reverted (the goroutine just doesn't hard-fail then).
- **Rollback**: `git revert` of any single unit is safe. Reverting U1 alone reverts to the un-hardened goroutine, which is the same risk profile as today's uncommitted state. Reverting U3 reverts the TTL split; cluster URLs go back to 60s and the bug returns.
- **Operator runbook update**: add a "Stage 6.5 cluster prerender" section to whatever ds-service operations doc exists. Include the new aggregate log shape and the status endpoint URL (if U8 ships).

---

## Sources & References

- **Origin document:** `/tmp/compound-engineering/ce-code-review/20260506-230117-37f47c06/` (run artifact directory; review report is in this session's prior assistant message)
- Per-reviewer compact JSON: `/tmp/compound-engineering/ce-code-review/20260506-230117-37f47c06/{reviewer}.json` (only `performance.json` was successfully written by the agent — others are in the in-session review output)
- Synthesis notes: `/tmp/compound-engineering/ce-code-review/20260506-230117-37f47c06/SYNTHESIS.md`
- Related code:
  - `services/ds-service/internal/projects/pipeline_cluster_prerender.go`
  - `services/ds-service/internal/projects/pipeline.go`
  - `services/ds-service/internal/projects/repository.go`
  - `services/ds-service/internal/projects/asset_export.go`
  - `services/ds-service/cmd/server/main.go`
  - `lib/atlas/live-store.ts`
  - `lib/atlas/data-adapters.ts`
  - `app/atlas/_lib/leafcanvas-v2/{LeafFrameRenderer.tsx,gesture-tracker.ts,leaf-zoom-signal.ts,canvas-log.ts}`
  - `app/atlas/_lib/leafcanvas.tsx`
  - `app/atlas/_lib/real-data-bridge.ts`
- Related plans:
  - `docs/plans/2026-05-06-001-perf-canvas-v2-design-brain-borrow-plan.md`
  - `docs/plans/2026-05-06-002-fix-canvas-refresh-evidence-driven-debug-plan.md`
- Related commits:
  - `b9b4377 fix(canvas-v2): four real bugs causing canvas refresh + stuck-on-shimmer`
  - `07a2a33 fix(asset-export): bump AssetExportTokenTTL 60s → 1h to survive concurrent mint queue`
- Institutional learnings:
  - `docs/solutions/2026-04-30-001-projects-phase-1-learnings.md`
  - `docs/solutions/2026-05-01-002-phase-4-lifecycle-learnings.md`
  - `docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md`
