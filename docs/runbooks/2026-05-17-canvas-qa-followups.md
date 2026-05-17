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
