# Canvas Figma-Dev-Mode Parity — QA Round-1 Follow-up Notes

**Created:** 2026-05-17
**Origin:** `tmp/qa-screenshots/BUG-REPORT.md`
**Initiative:** `docs/plans/2026-05-17-003-feat-canvas-figma-dev-mode-parity-plan.md`
**Status:** P1 bugs (1–6) and P2 Bug 7 shipped. P2 Bugs 8 + 9 remain as a separate asset-pipeline reliability initiative; P3 Bugs 10–12 are test infrastructure notes — recorded here so future automation passes do not re-discover the same dead-ends.

---

## Bug 8 — 28% of clusters fail to render (asset-pipeline reliability)

**Out of scope for the canvas-parity initiative.** Recorded here as the starting investigation for a follow-up effort.

### Observed in QA
- 1394 total clusters on the US-Stocks Referral leaf
- 392 (28%) rendered as gray dashed boxes (`data-cluster-failed`)
- DevTools console: 134 × HTTP 500, 3 × `ERR_HTTP2_PROTOCOL_ERROR`, 555+ × `[icon-cluster] image load failed after retries` (now suppressed and aggregated — see Bug 9 below)

### How failures propagate today
1. `services/ds-service/internal/projects/asset_stream.go:495` marks `render_timeout` after the per-cluster budget elapses (60s; bumped from 30s on 2026-05-12 per the comment at `asset_stream.go:79`).
2. The failure is persisted to `figma_render_blocklist` via `MarkFigmaRenderFailure` so retries don't re-spend Figma quota.
3. An SSE `asset-failed` event with `{node_id, reason}` is emitted; the client `clusterFailedIDs` Set picks it up and `renderClusterPlaceholder` paints the slate dashed placeholder (`nodeToHTML.ts:367`).
4. The `<img>` retry chain at `nodeToHTML.ts:330` only triggers when the URL is resolved but the image GET returns ≥400. After 3 retries with 1.5/3/6s backoff it falls back to the same teal dashed placeholder.

### Likely root-cause hypotheses (unconfirmed)
| Hypothesis | Where to look |
|---|---|
| Figma 5-req/sec PAT cap saturation under burst | `services/ds-service/internal/projects/figma_client.go` rate limiter; correlate timeouts with neighbouring 429s in audit_log |
| Cluster prerender budget exhaustion | `pipeline_cluster_prerender.go:ClusterPrerenderTotalBudget`; check whether failed clusters belong to a chunk that hit the budget |
| Render-blocklist stickiness from prior bad import | `figma_render_blocklist` rows older than the version's created_at; sweep via `cmd/admin retention` |
| Concurrent HTTP/2 stream collapse on the static asset path | The 3 `ERR_HTTP2_PROTOCOL_ERROR`s are a server-side concern (asset CDN / Fly proxy); not a ds-service code path |

### Recommended next steps
- Pick a single failing leaf (US-Stocks Referral is reproducible) and capture: tenant_id, file_id, version_id.
- Inspect `figma_render_blocklist` for the file_id — count rows, age distribution, reason mix.
- Inspect `asset_cache` for the version's clusters — which are missing rows entirely vs which have rows but corrupted bytes on disk.
- If the blocklist is the dominant cause, decide whether `cmd/admin retention` should sweep render-blocklist rows on a shorter schedule (currently weeks-old; the sweeper is at `blocklist_sweep.go`).

This deserves its own brainstorm → plan → ship loop. Not a one-PR fix.

---

## Bug 9 — Console flood during normal browse

**Shipped here.** Two parts:

- **Browser-emitted 500s and HTTP/2 errors:** outside our control — same root cause as Bug 8.
- **Our own `[icon-cluster] image load failed after retries` warns:** previously fired once per failed image — 555+ on a single leaf. Replaced at `nodeToHTML.ts` with a module-scope debounced aggregator: 5s flush, max 6 sample IDs per warn, no per-image spam. Future "icon-cluster" warn lines look like `[icon-cluster] 47 cluster image(s) failed after retries (sample: 123:456, 124:789, ...)`.

---

## Bugs 10 – 12 — QA automation infrastructure notes

These are not application bugs. They are limitations of the Chrome-DevTools-MCP-driven automation pass that produced the QA report. Documenting so the next automation pass avoids the same dead-ends.

### Bug 10 — Synthetic `pointermove` doesn't reach React's `onPointerMove`

**Reproduced via:** `element.dispatchEvent(new PointerEvent("pointermove", {...}))`
**Why it doesn't work:** React's SyntheticEvent layer registers a single top-level delegated listener and reconstructs events from native `pointer*` events. `dispatchEvent` from outside the React event system fires on the DOM but React's internal pointer-capture tracking doesn't recognise it as part of a "real" pointer sequence.
**Workaround for future automation:** Use the real-input simulation path (`mcp__chrome-devtools__hover` for hovers, `mcp__chrome-devtools__drag` for drags). These drive the browser's native input pipeline so React sees the events.

### Bug 11 — Synthetic `wheel` events fire inconsistently

**Reproduced via:** `element.dispatchEvent(new WheelEvent("wheel", { ctrlKey: true, deltaY: -100 }))`
**Symptom:** First wheel event zoomed 1× → 2×. Second back-to-back wheel event was a no-op. The `wheel` listener in `leafcanvas.tsx:onWheel` integrates a `canvasGestureTracker.tick()` and a debounce; the second synthetic event likely arrives inside the debounce window.
**Workaround:** Either insert a >150ms gap between synthetic wheel dispatches OR use real input via Chrome DevTools' input.dispatchMouseEvent with `type:"mouseWheel"` (the CDP-level path the MCP server uses for `mcp__chrome-devtools__zoom`-style operations if available, or scripted scroll).

### Bug 12 — DOM `data-atomic-selected` flag lags ~1 frame behind store

**Reproduced via:** click → immediate `querySelector('[data-atomic-selected]')`
**Why it lags:** Zustand's `selectAtomicChild` synchronously updates the store, but the React renderer that adds/removes `data-atomic-selected` is a `useEffect` — scheduled async via the microtask queue + commit phase.
**User-perceived impact:** Zero (the commit fires well under 16ms).
**Workaround for automation:** Insert `await new Promise(r => setTimeout(r, 100))` between a click dispatch and a `data-atomic-selected` assertion. Or query `useAtlas.getState().selection.selectedAtomicChild` directly if a window-exposed handle is available — the Zustand state is synchronously current even when the DOM lags.

---

## What shipped here (commit summary)

- Bug 1, 2, 3, 4, 5, 6 — `44522ef` (`fix(canvas-v2): P1 QA bugs — focus gate, Cmd+0 anchor, inspector theme, dev mode visibility`)
- Bug 7 — `services/ds-service/cmd/backfill-svg-markup/` + `LoadCanonicalTreeForBackfill` exported wrapper
- Bug 9 — debounced cluster-failure aggregator in `nodeToHTML.ts`
- Bug 8 — investigation only; documented above; flagged as a separate initiative
- Bugs 10–12 — recorded in this runbook; no code changes

## Post-ship system audit (2026-05-17 evening)

A three-agent audit of commits `44522ef`, `f03bd88`, `9ad5b15` ran after the round-2 QA closed. Findings split into shipped here vs deferred.

### Shipped in commit `<next>`

- **Audit #4 — 32MB cap silent skip** (`svg_inliner.go`): screens whose tree exceeded `maxScreenInlinedBytesUncompressed` returned `(false, nil)` indistinguishable from "no matching nodes." Now `SVGInlineDeps.OversizeScreens *int` is an optional out-counter; the CLI summary prints `oversize_skipped=N` so operators can spot screens needing manual review.
- **Audit #5 — CLI exit code** (`cmd/backfill-svg-markup/main.go`): the command exited 0 even with per-version errors. Now `os.Exit(1)` when `totalErrors > 0`, so `fly ssh console -C` / CI invocations surface failures.
- **Audit #6 — over-conservative pruned skip** (`cmd/backfill-svg-markup/main.go`): `cleanup.go:54` only removes `<dataDir>/screens/<tenant>/<version>` (the PNG cache); SVG bytes under `<dataDir>/assets/<tenant>/<file>/v<vi>/` are not swept. The CLI used to skip pruned versions defensively, masking legitimate inlineable work. The skip is removed.
- **Audit #7 — UPDATE missing tenant predicate** (`svg_inliner.go`): the `UPDATE screen_canonical_trees` filter was `WHERE screen_id = ?` only. Even though the CLI + Stage 9 caller pre-scope by tenant, a future caller (new HTTP handler, fix script) could cross-tenant by accident. The UPDATE now joins through `screens WHERE tenant_id = ?` as defense in depth.
- **General-purpose audit flag — `collectClusterIDs` non-bbox symmetry** (`useIconClusterURLs.ts`): `walkWithBBox` skips nodes with `svg_markup`; `walk` (the non-bbox variant used by tests + some legacy paths) did not. Added matching skip so callers that mint URLs never request assets for already-inlined nodes.
- **Audit #3 — sanitize symmetry** (`asset_export.go`): the relaxed `;`-allowing validator combined with no `;`-to-`_` sanitizer in `sanitizeNodeIDForFS` is a latent ambiguity, but Figma never emits the `I1:2_3:4` form (`_` is not in Figma's id alphabet), so `I1:2;3:4` and `I1:2_3:4` cannot collide in practice. The function gained an explanatory comment instead of a behaviour change — flipping it would break every existing SVG file on disk (`I9644_185666;376_8996.svg`).

### Deferred follow-ups

- **Audit #1 — concurrency race between CLI and live Stage 9** (HIGH, file separately): when the operator runs `backfill-svg-markup` while a designer hits Re-import on the same tenant/version, both processes can race the read-modify-write on `screen_canonical_trees`. The CLI bypasses `AcquirePrerenderSlot` (which is in-process anyway). The reviewer's suggested fix is optimistic locking via `AND updated_at = ?` in the UPDATE — non-trivial because `loadCanonicalTreeRaw` needs to return `updated_at` and the call sites need to handle "row changed, retry." For now: **operators should pause ingestion (or run when traffic is low) before invoking the CLI**. This bullet point is the contract until a follow-up PR adds the row-version check.
- **Audit #2 — `svg_markup` skip vs re-exported Figma SVG bytes** (MEDIUM): the inliner's "skip nodes that already have non-empty `svg_markup`" guard means a Figma re-export that emits FRESH SVG bytes for the same node id is NOT picked up by either the live re-run path or the CLI. The header comment at `svg_inliner.go:38-40` says the opposite ("re-reads … overwriting any prior svg_markup") — the two are contradictory. The right fix is one of: (a) drop the skip and always re-sanitize (cheap), (b) hash-compare on-disk bytes against a fingerprint stored alongside markup, (c) expose a `--force` CLI flag. Recorded so a future contributor can resolve.
- **Architectural envelope-walker audit**: the Explore agent verified all canonical_tree walkers in the repo (`pipeline_organism_match.go:WalkOrganismCandidatesWithIndex`, `svg_eligibility.go:IsSVGEligible`, `screen_image_fills.go:walkForImageRefs`, `screen_overrides_reattach.go:indexCanonicalTree`, `graph_sources.go:walkInstancesForComponentRefs`, `LeafFrameRenderer.tsx:unwrapCanonicalTree`, `useIconClusterURLs.ts`, etc.) handle the Figma envelope correctly. No 4th instance of the bug class found.
- **`svg_markup` end-to-end plumbing audit**: server write → API pass-through → TypeScript type → renderer branch → URL-skip → idempotency. All paths verified correct. One transient window noted (Stage 6 commit → Stage 9 inline completion serves PNG-only trees for ~1-2 minutes; graceful R5 fallback so not user-broken).

## How to verify the backfill (Bug 7)

Local:

```bash
cd services/ds-service
# Dry run across every tenant — confirms candidate counts without writing.
go run ./cmd/backfill-svg-markup --dry-run

# Real run scoped to one tenant.
go run ./cmd/backfill-svg-markup --tenant <tenant-id>

# Full run across every active tenant.
go run ./cmd/backfill-svg-markup
```

Fly:

```bash
fly ssh console -C "/usr/local/bin/backfill-svg-markup --dry-run"
fly ssh console -C "/usr/local/bin/backfill-svg-markup"
```

The command is idempotent — re-running skips already-inlined nodes via the early-return at `svg_inliner.go:206`. Per-screen failures log and continue; the binary exit code reflects only outer-loop fatal errors (DB open, tenant list).
