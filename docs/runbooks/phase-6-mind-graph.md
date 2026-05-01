---
title: Phase 6 — Mind Graph (/atlas) runbook
created: 2026-05-01
status: active
---

# Phase 6 — Mind Graph runbook

## What it is

`/atlas` is the mind-graph navigator over the design knowledge graph
(products → folders → flows → personas → components → tokens → decisions
with 4 edge classes). It reads from a single materialised SQLite table
`graph_index`, populated by the `RebuildGraphIndex` worker.

## Architecture (read this once)

```
Figma extractor + Phase 1–5 SQLite tables
        │
        ▼
RebuildGraphIndex worker  (services/ds-service/internal/projects/graph_rebuild.go)
        │  (200ms debounce + 1h safety-net + cold-start full rebuild)
        ▼
graph_index table  (migration 0009)
        │
        ▼
GET /v1/projects/graph?platform=mobile|web   (single indexed SELECT, ≤15ms p95)
        │                                            │
        ▼                                            │
useGraphAggregate hook  ◄────  GraphIndexUpdated SSE ┘  (graph:<tenant>:<platform>)
        │
        ▼
BrainGraph.tsx  (react-force-graph-3d + bloom + drift + interaction layers)
```

## Deploy

No schema migration runs automatically — `0009_graph_index.up.sql` is
embedded in `migrations.FS` and applied by `internal/db/migrations.go` on
the next ds-service boot.

```bash
# 1. ds-service deploy (Cloudflare Tunnel restart)
launchctl kickstart -k gui/$(id -u)/dev.indmoney.ds-service

# 2. Run the cold backfill once after first deploy of Phase 6 against the
#    production tenant. Idempotent; safe to re-run.
cd services/ds-service && go run ./cmd/migrate-graph-index --tenant=<tenant_id>

# 3. Vercel auto-deploys the Next.js docs site on push. /atlas lands as a
#    static page; the BrainGraph chunk is dynamic-imported.
```

## Environment variables

| Var | Default | Purpose |
|---|---|---|
| `GRAPH_INDEX_REBUILD_WORKERS` | `1` (max 8) | Worker pool size for the rebuild goroutines. Bump to 2-4 if SSE-driven incremental rebuilds queue under burst. |
| `GRAPH_INDEX_MANIFEST_PATH` | `<RepoDir>/public/icons/glyph/manifest.json` | Path to the Figma-extracted component manifest. |
| `GRAPH_INDEX_TOKENS_DIR` | `<RepoDir>/lib/tokens/indmoney` | Path to the DTCG token catalog directory. |
| `NEXT_PUBLIC_DS_SERVICE_URL` | `http://localhost:8080` | Frontend → ds-service base URL. |

## Monitor

Dashboards / metrics worth watching:

- **`graph_rebuild` worker logs.** Look for `graph_rebuild: full rebuild complete` (steady-state) and `graph_rebuild: flush failed` (operator action). Per-flush row count is logged.
- **`graph_index` row count.** `sqlite3 ds.db 'SELECT type, COUNT(*) FROM graph_index GROUP BY type'` on the production DB — expected ~9 products + ~80 folders + ~400 flows + ~80 personas + ~89 components + ~250 tokens + ~600 decisions ≈ 1500 rows per (tenant, platform).
- **Cache-hit headers.** The `GET /v1/projects/graph` response carries `ETag` (the latest `materialized_at`). Browser-side, a steady stream of 304 responses indicates no-op SSE busts; 200s mean the worker is keeping up.
- **Frame-time p99 from RUM.** `/atlas` should hold ≤16ms/frame at p99 in steady state. Investigate if it climbs past 24ms.

## Tuning knobs (if it regresses)

### `forceConfig.ts` — d3-force-3d

The four most impactful constants live here. Tune in this order:

1. `charge: -120` → more negative spreads the cluster. If the brain view
   feels claustrophobic at 1500 nodes, drop to `-180`.
2. `link.distance: 60` → target inter-node spacing. Bump to `90` if labels
   overlap.
3. `alphaDecay: 0.022` → settle speed. Higher = faster but more abrupt.
   Lower = more "alive" but burns CPU longer.
4. `velocityDecay: 0.4` → damping. Higher = stickier; lower = bouncier.

### LOD budget (`cull.ts`)

| Zoom level | Visible types | Approx node count |
|---|---|---|
| `brain` | products + folders + flows | ~500 |
| `product` | + clicked product's subtree + (filter-chip-toggled satellites) | ~250 |
| `folder` | scoped to folder + satellites | ~120 |
| `flow` | single flow + components + tokens + decisions | ~50 |

If frame time regresses at brain view, the first lever is to drop folders
(only render products + flows). Edit `cullVisibleSubset` in `app/atlas/cull.ts`.

### Worker pool

Default `GRAPH_INDEX_REBUILD_WORKERS=1` is fine for ≤3 tenants. At 10+
tenants with active DRD edits, bump to 4.

## Reduced-motion fallback verification

Quarterly check (post-launch):

1. Enable `prefers-reduced-motion: reduce` in macOS System Settings →
   Accessibility → Display.
2. Navigate to `http://localhost:3001/atlas`.
3. Verify the page shows "Reduced motion is enabled" + the dashboard CTA,
   NOT the WebGL canvas.
4. Click the dashboard link — should land on `/atlas/admin`.
5. Same check on Chrome DevTools "Rendering" → Emulate CSS media feature
   `prefers-reduced-motion` → reduce.

## WebGL2 fallback verification

1. Open Safari < 15 (or use Playwright with `webglPreferences.webgl2 = false`).
2. Navigate to `/atlas`.
3. Verify the page shows "This browser doesn't support WebGL 2" + the
   dashboard CTA.

## Rollback

If the rebuild worker is materialising bad data:

```bash
# 1. Stop traffic to /atlas (set a feature flag if deployed; otherwise
#    revert the route).
# 2. Drop graph_index data — the worker will rebuild from scratch on
#    next start.
sqlite3 ds.db 'DELETE FROM graph_index;'

# 3. Restart ds-service. Cold-start full rebuild populates afresh.
```

The migration itself stays in place — `graph_index` is empty-but-existing
is the safe state.

## Known limits + follow-ups

- **Pre-existing `UpsertProject` bug** (Phase 1, exposed by Phase 6 tests):
  if two projects share `(tenant, product, path)` on different platforms,
  `repository.go:125` recurses infinitely on the slug UNIQUE retry. Phase 6
  tests work around it with distinct paths per platform; the real fix is
  to add `platform` to the slug unique index OR to short-circuit the
  recursion when the lookup returns the same row twice.
- **Click-and-hold edge pulse** — the v1 implementation fades non-incident
  edges via the existing alpha accessor when `heldNodeID` is set. A
  dedicated shader uniform for sine-wave pulsing is deferred (the user's
  spec accepted this trade-off).
- **Component composition edges** — the manifest's `composition_refs`
  field would let us render component → component "uses" edges (when a
  molecule embeds atoms). v1 only renders flow → component. Phase 7.
