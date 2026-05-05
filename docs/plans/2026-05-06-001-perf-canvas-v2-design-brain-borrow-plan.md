---
title: "perf: Canvas-v2 follow-ups (preview pyramid + adaptive IO + dedup + R-tree)"
type: perf
status: active
date: 2026-05-06
---

# Canvas-v2 perf — DesignBrain-AI pattern borrow

## Overview

Four canvas-v2 perf follow-ups borrowed from DesignBrain-AI's WebGL canvas engine. Patterns 2 (priority fetch queue) and 3 (HOT/WARM IO observers) and 5 (epoch discard) shipped in commit `5fdd25a`. This plan covers the remaining four, ordered by leverage:

| U-ID | Pattern | DesignBrain ref | Effort | Leverage |
|---|---|---|---|---|
| U1 | Preview pyramid (sized renders + zoom-aware selector) | `internal/canvas/image_tile_generator.go`, `web/src/engine/materials/ImageTileManager.ts:111-130,86-88,294-339` | ~1 day | **Highest** — 4–16× cut in Figma render budget at typical zoom |
| U7 | Adaptive IO sleep / detach-on-idle | `web/src/engine/render/RenderEngine.ts:646-651` | ~30 lines | Low — minor CPU/GC savings on static canvas |
| U6 | Content-hash subtree dedup (memoize JSX per cluster) | `internal/canonical/canonical_hash.go:9-28` | ~half day | Medium — depends on real subtree repeat rate; measure first |
| U4 | R-tree spatial index over frame AABBs | `web/src/engine/scene/ViewportCuller.ts` (rbush) | ~half day | **Skip unless metrics demand** — 80 frames is below the breakeven |

Each unit is independent and shippable on its own. U7 and U6 require no backend or migration. U1 is the biggest change — adds a backend endpoint + LRU + a small migration.

---

## Problem Frame

After the depth-10 + Basier Circle + image-fill + cluster-export work landed (commits through `b3584fa`) and HOT/WARM IO + priority queue (`5fdd25a`) layered on, the canvas-v2 LeafFrameRenderer reliably reconstructs Figma frames. Two perf shortcomings remain:

1. **Render-budget waste.** A frame at 1080×800 displayed at zoom 0.25 still requests a `scale=2` PNG (~2160×1600). Figma renders, ds-service caches, browser downloads, paints — at 1/16th of the data rate it could. Multiplied across 80 frames in a leaf this saturates Figma's 5-req/sec-per-PAT budget and leaks bandwidth.

2. **DOM walker re-work.** Every frame in a leaf re-walks identical icon-cluster subtrees (status bars, navigation bars, button instances). For a 79-screen leaf where every screen has the same status bar, the walker emits the same React subtree 79 times. Pure work for the same output.

Two smaller items piggyback: an idle-detach for the IntersectionObservers (cuts GC churn on static atlases), and a deferred R-tree index (only relevant if we add pan-driven prefetch beyond what IO already provides).

---

## Requirements Trace

- R1. **Preview pyramid (U1):** at zoom Z and DPR D, the renderer requests the smallest cached PNG tier where `tierPx ≥ frameWidth × Z × D`. A 4-tier ladder (128/512/1024/2048) covers zoom range 0.06–2.0 at common DPRs.
- R2. **No regressions in existing canvas-v2 behaviors** — text editing, atomic-child inspection, state picker, copy-overrides tab, bulk export panel must continue to work.
- R3. **Per-tenant disk budget:** preview-tier blobs bounded by the existing `asset_cache.bytes` budget infrastructure. GC sweeper evicts oldest tier-128 first (cheap to re-render) before tier-2048.
- R4. **Render-time hashing under 100µs per cluster (U6).** Anything slower offsets the savings.
- R5. **DEV_AUTH_BYPASS local dev** continues to work end-to-end.
- R6. **Each unit independently mergeable** — no cross-unit dependencies on un-merged work.

---

## Scope Boundaries

- **Not implementing:** WebGL renderer, MSDF text atlas, shader pipeline, GPU buffer managers, vector triangulation, web-worker compute graph, Yjs collab, sub-buffer dirty-range tracker. (See `docs/solutions/2026-05-01-001-designbrain-perf-findings.md` for why each is overkill for our DOM canvas.)
- **Not implementing:** spatial tiling within a frame. DesignBrain's image_tile_generator chops images >4096px into 512px tiles; our screens cap at ~2048×3000 so single-tile-per-tier is sufficient.
- **Not measuring or optimizing:** initial-paint Lighthouse scores, hydration time, bundle size. The patterns target steady-state canvas perf, not boot.
- **Out of session:** renaming or restructuring the existing `asset_cache` table beyond a CHECK widening.

### Deferred to Follow-Up Work

- **Pattern 4 (R-tree):** plan U4 is included for completeness but should not ship until metrics show IO callback time is a measurable fraction of a frame. At 80 frames the linear scan is well under 1ms.
- **Pattern 1 prefetch ladder:** U1 ships zoom-on-mount selection; "speculatively prefetch the next tier when zoom is changing" is a follow-up issue.

---

## Context & Research

### Relevant Code and Patterns

- `services/ds-service/internal/projects/asset_export.go` — existing render+cache pipeline. `RenderAssetsForLeaf` already takes `(format, scale)` params; preview tier extends this with a new format value rather than overloading scale.
- `services/ds-service/internal/projects/screen_image_fills_handler.go` — `HandleWarmAssetCache` exists but is currently unwired from frontend (mux 404 follow-up).
- `services/ds-service/migrations/0019_asset_cache.up.sql` and `0020_asset_cache_image_fill.up.sql` — `format` CHECK currently `('png', 'svg', 'image-fill')`; `scale` BETWEEN 1 AND 4. Migration 0021 widens for preview tiers.
- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx:228-260` — HOT/WARM IO observer setup (post-`5fdd25a`).
- `app/atlas/_lib/leafcanvas-v2/fetch-queue.ts` — module-level priority queue. New U1 backend calls plug into this same queue.
- `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts:131-180` — `renderClusterPlaceholder` function; the `ctx.clusterURLs` map is the integration point for preview-tier-aware URL resolution.

### Institutional Learnings

- `docs/solutions/2026-05-01-001-designbrain-perf-findings.md` — 4-week-old deep dive on DesignBrain's tile pyramid. Key takeaway carried forward: "**Level-0 is always pre-fetched as a fallback** … When a higher-level tile is pending, render falls back to a UV sub-region of the level-0 texture." Our DOM analog: render `<img>` at tier-128 immediately, swap `src` to higher tier when zoom warrants.
- `docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md` — pointer-events-on-wraps was the canvas-blocking gotcha. Any new `<img>` element must keep `pointer-events: none` so canvas pan/zoom isn't blocked.

### External References

- DesignBrain ADR-001 at `~/DesignBrain-AI/docs/decisions/adr-001-custom-webgl-engine.md` — explains why they rejected Konva/Fabric. Validates that we should *not* port their full engine.
- `golang.org/x/image/draw` (Catmull-Rom) — DesignBrain uses this for preview downsampling. Stdlib-adjacent, no new vendor.

---

## Key Technical Decisions

- **Preview tier as a new `format` value** (`preview-128`, `preview-512`, `preview-1024`, `preview-2048`) rather than abusing the `scale` column. Rationale: `scale` is already 1-4 (CHECK constraint); reusing it for preview tiers would conflate render-quality scale with preview-size tier and break existing per-node asset exports. New format values keep the cache PK clean and the GC sweeper simple.
- **Tier ladder fixed at 128 / 512 / 1024 / 2048** (matching DesignBrain). `previewMaxDim ≥ displayDim × zoom × DPR` selects the smallest sufficient tier. No dynamic tier set per file; hard-coded covers our zoom range.
- **Source for previews: existing PNG render at scale=2**, downsampled in Go via `golang.org/x/image/draw.CatmullRom.Scale`. Avoids a separate Figma render call per tier — Figma is hit once at scale=2, the four preview tiers are local downsamples.
- **No dynamic tier picker on backend** — frontend decides which tier to fetch based on zoom; backend serves whichever tier is requested. Keeps backend stateless w.r.t. viewport.
- **U6 hash is canonical_tree-only**, never includes resolved imageRefs or cluster URLs. Hashing post-resolution would invalidate the memo cache every time the imageRefs map updates, defeating the purpose.
- **U7 idle threshold: 500ms.** Below: too aggressive — single user keystroke detaches/reattaches. Above: callback churn savings diminish. 500ms matches DesignBrain's `RenderEngine.ts:646-651` debounce.

---

## Open Questions

### Resolved During Planning

- **Q: Should preview tier be a new column or a new `format` value?** A: New format value. Cleaner PK, no schema column add, GC sweeper unchanged.
- **Q: Cache the JPEG-90 or PNG variant?** A: PNG. Our content is UI screens (sharp edges, text, solid fills) — JPEG artifacts visible at any quality. ~2× bigger but disk is cheap; the LRU eviction handles it.
- **Q: Does U6 dedup work for clusters whose `imageRef` resolutions differ across frames?** A: U6 hashes the canonical_tree subtree (pre-resolution); the rendered output of a memoized cluster gets resolved imageRefs / cluster URLs at render time via the existing `ctx.clusterURLs` lookup. So a memo hit still produces the right `<img src>` per frame.

### Deferred to Implementation

- **Exact GC eviction threshold per tenant** for preview blobs. Phase 1 ships a hardcoded 200MB cap; ops will tune via env var if it bites.
- **Whether to ship U6's hash function as Go-side too** (for canonical_tree pre-hashing in the pipeline). For now JS-side is enough; Go side becomes useful if we add server-side dedup at canonical_tree storage time.
- **R-tree index dimension order in U4** (X-major or Y-major split). rbush defaults are fine; revisit if benchmarks suggest.

---

## Implementation Units

### U1. Preview pyramid — sized PNG tiers + zoom-aware frontend selector

**Goal:** Cut 4–16× off the Figma render budget by serving frame/cluster PNGs at the smallest tier that satisfies `tierPx ≥ displayPx × zoom × DPR`. Eliminates the "render a 2160px PNG to display at 270px" waste.

**Requirements:** R1, R3, R5, R6.

**Dependencies:** none (extends existing asset_export and asset_cache).

**Files:**
- Create: `services/ds-service/migrations/0021_asset_cache_preview_tier.up.sql`
- Create: `services/ds-service/internal/projects/asset_preview_pyramid.go`
- Modify: `services/ds-service/internal/projects/asset_export.go` (extend `RenderAssetsForLeaf` + `HandleAssetDownload` to recognize `preview-N` formats)
- Modify: `services/ds-service/cmd/server/main.go` (no new route; existing `/v1/projects/:slug/assets/:node_id?format=preview-128` plumbs through)
- Modify: `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts` (cluster `<img>` src incorporates current zoom×DPR via a new `pickPreviewTier(displayPx, zoom, dpr)` helper)
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` (pass current zoom from `useAtlas` selection state through `NodeToHTMLContext`)
- Modify: `app/atlas/_lib/leafcanvas-v2/types.ts` (add `zoom` and `dpr` to `NodeToHTMLContext`)
- Test: `services/ds-service/internal/projects/asset_preview_pyramid_test.go`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/nodeToHTML.test.ts` (add tier-selection test cases)

**Approach:**
1. Migration 0021 widens `asset_cache.format` CHECK to include `'preview-128' | 'preview-512' | 'preview-1024' | 'preview-2048'`.
2. New helper `RenderPreviewPyramid(ctx, tenantID, fileID, nodeID, versionIndex)` — looks up cache for each tier; for misses, renders the source PNG once at scale=2 (reusing existing `RenderAssetsForLeaf`), downsamples to each tier with `golang.org/x/image/draw.CatmullRom.Scale`, persists each as a separate `asset_cache` row keyed on the `preview-N` format.
3. Existing `HandleAssetDownload` accepts `format=preview-N`. When the tier is cached, serve directly. When missed, the handler triggers `RenderPreviewPyramid` synchronously and serves the requested tier.
4. Frontend `pickPreviewTier(displayPx, zoom, dpr)` returns the smallest of `[128, 512, 1024, 2048]` that's `>= displayPx × zoom × dpr`. Default zoom 1.0 if unknown; default DPR `window.devicePixelRatio` (fallback 2).
5. `nodeToHTML` cluster path adds `?format=preview-${tier}` to the `<img src>` URL.
6. Empty-state behavior: tier-128 is treated as a permanent fallback. The frontend always loads tier-128 first (`<img loading="eager">`); higher tiers are added via `srcset` so the browser auto-promotes when the element approaches the viewport.

**Patterns to follow:**
- DesignBrain `ImageTileManager.ts:111-130` (tier selection formula) and `:294-339` (level-0 fallback).
- `services/ds-service/internal/projects/asset_export.go:185-313` (RenderAssetsForLeaf cache lookup → fetch → persist pattern).
- Migration 0020 (CHECK widening pattern with row preservation).

**Test scenarios:**
- Happy path — frame requested at displayWidth=375, zoom=0.5, DPR=2 → expects `format=preview-512` (375×0.5×2 = 375 ≤ 512 < 1024).
- Happy path — same frame at zoom=2 → expects `format=preview-2048`.
- Edge case — zoom < 0.06 → tier-128 (smallest); never returns a non-existent smaller tier.
- Edge case — zoom > 2.0 → tier-2048 (largest); never overshoots into a non-existent larger tier.
- Edge case — first hit on uncached node renders all 4 tiers; subsequent calls hit cache for any tier.
- Error path — Figma returns 404 → all 4 tier writes are skipped, handler returns 404 (no partial cache).
- Error path — backend disk full mid-pyramid generation → tier rows persist for tiers that succeeded, missing tiers fall through to next request.
- Integration — frontend `pickPreviewTier` × backend rendering: a leaf with 80 frames at zoom 0.25 issues 80 × `preview-128` requests; total bytes downloaded ≤ 80 × (128² × 4 RGBA) = ~5MB vs. ~80MB at scale=2.
- Integration — DEV_AUTH_BYPASS path serves preview tiers identically to JWT path.

**Verification:**
- `go build ./services/ds-service/...` clean; `npx tsc --noEmit` clean.
- Migration 0021 applies idempotently (re-run is no-op).
- A real leaf with 79 frames at zoom 0.25 in browser DevTools Network panel: every cluster `<img>` request is `?format=preview-128`, response sizes ≤ 50KB each.
- At zoom 1.0 same leaf: requests upgrade to `preview-512` or `preview-1024` per zoom math.
- `du -sh services/ds-service/data/assets/<tenant>/` shows each frame having ≤ 4 cache files (one per tier); no orphans.

---

### U7. Adaptive IO sleep — detach observers when atlas idle

**Goal:** Cut IntersectionObserver callback churn (and resulting GC pressure) when the canvas is idle. Reattach on first user interaction.

**Requirements:** R2, R6.

**Dependencies:** none (modifies existing IO setup post-`5fdd25a`).

**Files:**
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` (~30 lines around the existing HOT/WARM observer setup)
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/leaf-frame-renderer.test.tsx` (new file or extend existing test)

**Approach:**
1. Track an `idleSince` timestamp at the leaf-shell level, reset on any of: wheel, keydown, pointermove (debounced), touch, focusin.
2. After 500ms idle, both HOT and WARM observers `disconnect()`. Leaf-state `state.status` of each frame is preserved; in-flight fetches continue.
3. On first interaction post-disconnect, re-construct the observers and re-observe every frame's wrapper. Frames that already transitioned to `ready` status during the active phase do not re-fire (the existing early-return `if (intersected) return;` guards this).
4. Cleanup on unmount: clear the timer + disconnect observers (existing).

**Execution note:** Test-first. Write a test that mounts 3 frames, asserts both observers exist after 100ms, simulates 600ms idle, asserts observers are disconnected, simulates a wheel event, asserts observers are re-attached.

**Patterns to follow:**
- DesignBrain `RenderEngine.ts:646-651` — RAF-loop self-pause pattern.

**Test scenarios:**
- Happy path — 3 frames mount, both observers attach. After 600ms with no interaction, observers disconnect (assert via spy on `IntersectionObserver.prototype.disconnect`).
- Happy path — after disconnect, dispatch a `wheel` event on the canvas root; observers re-attach within one frame.
- Edge case — frame that was already `intersected=true` before the idle period stays `intersected=true` after re-attach (no double-fetch).
- Edge case — unmounting the leaf during the idle phase clears the timer (no setTimeout leak).
- Edge case — rapid bursts of wheel/keydown events don't repeatedly attach/disconnect; debounce is single-trigger per idle cycle.
- Error path — IntersectionObserver constructor throws (extremely rare; SSR test stubs it as undefined) → the renderer falls through to "always intersected" path, atlas still functions.

**Verification:**
- Tests pass.
- DevTools Performance recording on a static atlas leaf (no interaction for 5s): zero IO callback entries after the 500ms idle mark.
- First wheel event after idle: IO callbacks fire in the next frame.

---

### U6. Content-hash subtree dedup — memoize JSX per cluster

**Goal:** When a leaf has 79 screens that all render the same status bar / nav bar / button instances, walk and emit those subtrees once and reuse the React element across frames.

**Requirements:** R2, R4, R5, R6.

**Dependencies:** none (pure frontend, no backend touch).

**Files:**
- Create: `app/atlas/_lib/leafcanvas-v2/subtree-cache.ts` (hash + memo cache)
- Modify: `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts` (`renderClusterPlaceholder` and `renderContainer` consult the memo before walking)
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` (provide a per-leaf cache instance via context to avoid cross-leaf bleed)
- Modify: `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts:NodeToHTMLContext` (add `subtreeCache?: SubtreeCache`)
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/subtree-cache.test.ts`
- Test: extend `app/atlas/_lib/leafcanvas-v2/__tests__/nodeToHTML.test.ts` with cache-hit assertions

**Approach:**
1. Hash function operates on `(type, name, sorted-children-hashes, fills, style, absoluteBoundingBox)` of a canonical_tree node. Output: 32-bit hash (`fnv-1a` or `xxhash-wasm` if already in the bundle; otherwise hand-rolled fnv).
2. Cache is a `Map<hash, ReactElement>` per leaf. Capacity 500 entries; LRU eviction.
3. Hashing is recursive and memoized by `node.id` so the same node hashes only once per render.
4. `renderClusterPlaceholder` and `renderContainer` consult the cache before walking. On miss, they walk + emit the element + insert. On hit, return the cached element directly.
5. Cache is invalidated on leaf change (the per-leaf instance is recreated).
6. **Critical:** `ctx.imageRefs` and `ctx.clusterURLs` must NOT be inputs to the hash — those resolve at render-time. The cached element is a React element tree that, when committed by React, resolves the resolved URLs through the latest context. (React already does this for memoized elements.)

**Execution note:** Benchmark the hash function on a real canonical_tree (~500 nodes) before integrating. Target: hash 100 clusters in <10ms total. If slower, fall back to a simpler structural hash that skips deep child traversal.

**Patterns to follow:**
- DesignBrain `internal/canonical/canonical_hash.go:9-28` — SHA-256 over semantic fields with volatile fields stripped. Our JS port is fnv (faster, sufficient for 32-bit collision probability at <500 entries).

**Test scenarios:**
- Happy path — two clusters with identical `(type, name, children, fills)` hash to the same value; second one is a cache hit.
- Happy path — two clusters identical except for `absoluteBoundingBox.x` (positioned differently) hash differently. (Bbox position matters because nodeToHTML emits absolute positioning.)
- Edge case — circular reference in canonical_tree (shouldn't happen but defensive) → hash function terminates via depth limit (max depth 12).
- Edge case — cache hits 500-entry limit; LRU eviction drops oldest, doesn't blow up.
- Edge case — leaf navigation: a cache populated on leaf A is not consulted for leaf B (per-leaf instance).
- Error path — hashing a malformed node (missing `type`) doesn't throw; returns a "unhashable" sentinel that always cache-misses.
- Integration — render a synthetic leaf with 10 frames each containing the same status-bar cluster; assert `nodeToHTML` is called for the status bar exactly once (spy on `renderClusterPlaceholder`).
- Performance — render a real 79-screen leaf; total `nodeToHTML` calls ≤ 50% of pre-cache baseline (measure via temporary instrumentation).

**Verification:**
- Tests pass.
- React DevTools Profiler shows cluster components rendered once per unique subtree, not per frame.
- Manual smoke test on the 79-screen NRI VKYC leaf: visual output identical to pre-cache version (no missing icons, no wrong positions).

---

### U4. R-tree spatial index over frame AABBs (deferred / measure-first)

**Goal:** Replace the linear-scan IO entry handling with O(log n) spatial queries when computing "frames within the WARM band." Only worth shipping when 80 frames stops being our ceiling.

**Requirements:** R6.

**Dependencies:** U1 ideally landed first (preview pyramid eliminates the dominant render-budget bottleneck; once that's gone, the next bottleneck might or might not be IO scan).

**Files:**
- Modify: `package.json` (add `rbush`, `@types/rbush`)
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` (use rbush instead of single-frame IO observers for canvas-wide pan-driven prefetch)
- Create: `app/atlas/_lib/leafcanvas-v2/spatial-index.ts`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/spatial-index.test.ts`

**Approach (sketch — defer detailed design until benchmarks demand):**
1. Build an `RBush<{frameId, x, y, w, h}>` at leaf load.
2. On `wheel` / drag end, compute the new visible AABB; query the rbush for all overlapping frames; promote those to HOT priority via the existing fetch queue.
3. Per-frame IntersectionObservers stay (they're free and correct); R-tree adds a faster pan-prefetch path on top.

**Execution note:** Skip implementation entirely until a Lighthouse-style measurement shows IO callback time > 5% of frame budget on a 80-frame leaf. The plan exists so we know the shape when the metric finally bites.

**Test scenarios:** TBD — defer until implementation starts; the structure is generic enough that we'd write the same tests rbush ships with.

**Test expectation: none for now** — feature-bearing but explicitly deferred. Only land tests when implementation lands.

**Verification:**
- A documented metric (IO callback time as % of frame budget) crossing the 5% threshold on a real leaf, captured via DevTools Performance → triggers this work.
- When implemented: pan a 200-frame synthetic leaf at 60fps without dropping frames.

---

## System-Wide Impact

- **Interaction graph:** U1 changes the shape of `<img src>` URLs across cluster + image-fill paths. The existing `useImageRefs` and `useIconClusterURLs` hooks already serve URLs through this codepath; U1 only changes the URL contents, not the call graph.
- **Error propagation:** U1 backend pyramid generation can partial-fail (3 of 4 tiers persist, one fails). The handler returns 404 only when the *requested* tier is absent; other tiers remain queryable. No cross-tier rollback. The existing `<img onError>` fallback (commit `b3584fa`) catches any transient miss.
- **State lifecycle risks:** U6 cache must clear on leaf change. Forgetting to clear → cross-leaf rendering bleed (cluster from leaf A appears in leaf B). The per-leaf instance pattern in U6 plus React's component tree teardown on unmount makes this near-impossible, but the test for "leaf navigation invalidates cache" enforces it.
- **API surface parity:** U1 introduces preview-tier formats only on `/v1/projects/:slug/assets/:node_id`. The bulk-export and warm-cache endpoints currently don't support preview tiers. Decision: leave them at `format=png|svg` for now (bulk export is for designer-initiated downloads, where full quality is wanted). If we later need bulk preview, that's a small extension to `RenderAssetsForLeaf`'s format param.
- **Integration coverage:** the U1 frontend tier selector × backend tier render path needs an integration test that wires real fetch through ds-service. Stubbing it with a fake fetcher would miss CHECK-constraint or migration-application bugs.
- **Unchanged invariants:** `screen_canonical_trees`, `screen_text_overrides`, `flows`, `projects`, `project_versions`, `screen_modes` schemas — all untouched. The atlas brain graph (`/v1/projects/atlas/brain-products`) — untouched. SSE event semantics — untouched.

---

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Pyramid generation under load saturates the Go process (4× downsample per cache miss). | Run downsampling in a goroutine pool with `MaxConcurrentPyramidGen=4`. Cache hits are unaffected. Existing `figmaProxyLimiter` already serializes the upstream Figma call. |
| Preview tier disk usage grows unboundedly. | Per-tenant LRU sweeper extension to `cmd/cleanup-versions` (or sibling). Hardcoded 200MB cap until ops tunes via env var. |
| U6 cache hit returning a stale element when canonical_tree changes mid-leaf. | Cache key includes content hash, not just node.id. A change to any content field changes the hash → cache miss → fresh walk. The remaining risk is "node tree changes between hash and emit" (atomic in JS — single-threaded; not possible). |
| Preview-128 tier downsampled from a 2048 source might render fuzzy text. | UI screens have sharp text; tier-128 is for thumbnail / overview state at zoom < 0.1 where pixel-detail isn't visible anyway. Spec'd zoom range is 0.06–2.0 — tier-128 only used at zoom < 0.25, where fuzziness is below perception threshold. |
| R-tree (U4) port introduces a 13KB-gzipped dependency for marginal benefit. | Strict measure-first gate. Don't ship the dep until metrics demand. |
| Adaptive IO sleep (U7) detaches during a slow async fetch and reattaches mid-fetch, causing duplicate observe calls. | The existing `useEffect` cleanup function disconnects on every re-render; the re-attach path always observes a fresh wrapper. Spy-test in U7's test scenarios catches double-observe. |

---

## Documentation / Operational Notes

- After U1 ships: update `docs/issues/2026-05-05-canonical-tree-depth.md` with a note that the operator runbook now also covers preview-tier sweeper.
- After U6 ships: add a `docs/solutions/2026-05-06-canvas-perf-borrowed.md` capturing measurements (cache hit rate, walker call count delta) for institutional record.
- Per-tenant disk budget tunable: `DS_PREVIEW_CACHE_MAX_BYTES_PER_TENANT` (default `200MB`). Document in `.env.example` once U1 lands.
- No client-side feature flag needed — U1 is pure rendering optimization with no perceptible behavior change beyond faster painting.
- Rollback plan for U1: revert the migration is unsafe (CHECK constraint widening on a populated table requires re-encoding); instead, frontend can revert to `format=png&scale=2` URLs by reverting the `pickPreviewTier` integration. Backend keeps serving both shapes.

---

## Sources & References

- DesignBrain perf findings: `docs/solutions/2026-05-01-001-designbrain-perf-findings.md`
- Zeplin canvas learnings: `docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md`
- Asset cache schema: `services/ds-service/migrations/0019_asset_cache.up.sql`, `0020_asset_cache_image_fill.up.sql`
- Existing render pipeline: `services/ds-service/internal/projects/asset_export.go`
- DesignBrain refs (out of repo, read-only):
  - `~/DesignBrain-AI/internal/canvas/image_tile_generator.go` (preview generation pattern)
  - `~/DesignBrain-AI/web/src/engine/materials/ImageTileManager.ts:111-130,86-88,294-339` (tier selection + fallback)
  - `~/DesignBrain-AI/web/src/engine/render/RenderEngine.ts:646-651` (RAF self-pause)
  - `~/DesignBrain-AI/internal/canonical/canonical_hash.go:9-28` (semantic-hash pattern)
  - `~/DesignBrain-AI/web/src/engine/scene/ViewportCuller.ts` (rbush spatial index)
- Prior canvas-v2 plan: `docs/plans/2026-05-05-002-feat-zeplin-grade-leaf-canvas-plan.md`
- Recent canvas-v2 commits: `5fdd25a` (HOT/WARM IO + queue + epoch), `b3584fa` (onError fallback), `ff65394` (Chunks 3+4)
