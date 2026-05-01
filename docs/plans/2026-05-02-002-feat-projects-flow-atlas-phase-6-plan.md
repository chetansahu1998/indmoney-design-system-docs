---
title: "Projects · Flow Atlas — Phase 6 — Mind Graph (`/atlas` brain view + signal animations)"
created: 2026-05-02
status: active
origin: docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md
parent_plan: docs/plans/2026-04-29-001-feat-projects-flow-atlas-phase-1-plan.md
deepened: 2026-05-02
revised: 2026-05-01 — single `graph_index` table replaces the multi-source on-request aggregator; the materialised view is now ground truth for the read path
---

# Phase 6 — Mind Graph

The mind graph is the navigator + reverse-lookup atlas for the entire Projects system. Every other surface (Phase 1–5) was built so this one could plug in: the data is already in SQLite (products, folders, flows, screens, components, tokens, decisions, decision_links), the SSE bus already broadcasts lifecycle events, the auth guard is already plumbed, and the animation library (GSAP + Lenis + Framer Motion) is already in the bundle. Phase 6 is therefore the **render and interaction layer**, not a fresh data-model build.

This plan deviates from the Phase 1 roadmap row in **one specific way** the user clarified during planning:

> **Click-and-hold has nothing to do with zoom. It is a signal animation only — a three.js-default `mousedown`/`mouseup` raycaster behavior (particle convergence, label brightening, edge pulse). Camera zoom + recursive collapse remain on single-click for Products / folders.**

Click-and-hold is a tactile, soothing affordance — the user holds a node, the graph "responds" toward that node — but the camera does not move and child nodes do not expand. It is intentionally cheap (stock r3f gesture handlers) so we don't ship custom physics on the critical path.

---

## Overview

The mind graph (`/atlas`) is a 3D force-directed brain view over the design knowledge graph: 9 Products → folders → flows → personas → components → tokens → decisions, with 4 edge classes (`hierarchy`, `uses`, `binds-to`, `supersedes`).

**Initial state:** Brain view. 9 Product names glow brighter; everything else dim. Hierarchy edges only. Filter chips above the canvas: `[Hierarchy] (default on) · [Components] · [Tokens] · [Decisions]`. Top-right Mobile ↔ Web toggle crossfades the entire graph (R25 — separate IA trees per platform).

**Interactions:**

| Gesture | Effect |
|---|---|
| **Hover** any node | Floating signal card: type, parent path, severity counts, persona count, last-updated, last-editor, "Open project →" CTA |
| **Single-click** a Product | Camera tweens in (smooth zoom); other 8 products dim and recede; clicked product's children spring outward |
| **Single-click** a folder | Same recursive zoom — its children spring outward, siblings recede |
| **Single-click** a flow leaf | **Shared-element morph at ~600ms** — leaf circle + label tween into project view's title bar; brain dissolves; canvas + tabs render behind |
| **Click-and-hold** any node | **Signal animation only** — particle convergence toward held node, label brightens, incident edges pulse. **No camera move, no expansion.** Release on `mouseup` |
| **Toggle filter chip** | Edge class fades in/out (≤300ms); satellite nodes spring into orbit when their class is enabled |
| **Mobile ↔ Web toggle** | r3f scene crossfade (~400ms); two graphs share render pipeline so swap is cheap |
| **Esc / back from project view** | Reverse 600ms morph; brain reconstitutes around the leaf |

**Performance ceiling (Origin Q6):** `d3-force` at 1000+ nodes degrades. We resolve this with a **per-zoom-level node budget + LOD culling** (see U13).

---

## Animation Philosophy

Phase 1 set up the animation library (GSAP + ScrollTrigger + Lenis + Framer Motion) so that mind-graph interactions hang off existing primitives. Phase 6 adds three new motion primitives on top:

1. **Signal animation** (click-and-hold) — three.js-default raycaster events drive a small particle system + a per-node label brightness tween + a 1-frame edge-glow pulse on incident edges. Loops while held; settles on release. Stock r3f `onPointerDown` / `onPointerUp`. **Deliberately not GSAP-driven** — the user asked for "a 3js default and easy behaviour," and the soothing quality comes from the natural decay of stock easing, not bespoke physics.
2. **Camera zoom + recursive collapse** (single-click on Product / folder) — `react-force-graph-3d`'s built-in `cameraPosition` tween (1000ms cubic-out), paired with a Framer-Motion-driven label-opacity stagger on siblings (50ms cascade, 200ms each). When a parent is clicked, every non-descendant of that parent decays to opacity 0.18.
3. **Shared-element morph** (single-click on flow leaf) — Framer Motion `layoutId` hands off the leaf's label + circle to the project view's title bar; in parallel, the r3f scene fades out (300ms) while the project view's atlas fades in (300ms). Total morph budget: 600ms (R27).

**Reduced-motion compliance:** All three primitives respect `prefers-reduced-motion`. Signal animation reduces to "static label brightness only — no particles, no pulse." Camera zoom reduces to "instant cut + opacity stagger only." Shared-element morph reduces to "instant route swap." The reduced-motion path was already wired in Phase 5.1 P3 (cursor presence) and Phase 1 (atlas postprocessing — the `useReducedMotion()` hook is exported from `lib/animations/context.ts`); we extend it, we don't re-design it.

---

## Data Model — Single Materialised `graph_index` Table

Phase 6 adds **one new table**, `graph_index`, and treats it as the single source of truth for the mind-graph read path. Every other source — the `projects` / `flows` / `personas` / `decisions` / `decision_links` / `violations` SQLite tables (Phase 1 + 2 + 4 + 5), the Figma-extracted `public/icons/glyph/manifest.json` (the deep-component-extraction pipeline), the Terrazzo-built `lib/tokens/indmoney/{base,semantic,semantic-dark}.json` files, and the per-screen `screen_canonical_trees.canonical_tree` BLOBs — feeds into `graph_index` via a `RebuildGraphIndex` worker. **The HTTP handler does not read any of those sources directly.**

This is a deliberate departure from the original Phase 6 plan's "7-SELECT aggregator + 60s in-process cache" approach. Three reasons:

1. **The cold path was prohibitively expensive.** Deriving `flow → component` `uses` edges requires walking ~400 canonical-tree BLOBs (~100MB total) to extract `INSTANCE.mainComponent.id` references. A 60s cache helps, but every SSE-driven cache bust forces a fresh ~800ms-1.5s walk. Materialising once at write time and indexing for read time inverts that cost.

2. **The aggregator's data is a derived view, not transactional state.** Hierarchy edges, component-token bindings, flow-component usage — none of these mutate independently. They all derive from upstream sources (Figma extraction commits, decision-table writes). A materialised index is exactly the right shape.

3. **Single-table storage matches the read pattern.** The mind graph reads the entire graph for `(tenant, platform)` per page load + per SSE bust. Per-edge lookups are not the workload. Storing nodes with denormalised edge arrays — one row per node, JSON arrays for the three satellite edge classes, `parent_id` for hierarchy — lets the handler serve from a single indexed SELECT.

### Schema (migration `0009_graph_index.up.sql`)

```sql
CREATE TABLE IF NOT EXISTS graph_index (
    -- Identity (composite PK; platform partitioned per R25)
    id              TEXT NOT NULL,                    -- "product:indian-stocks", "flow:flow_abc",
                                                      -- "component:button-cta", "token:colour.surface.button-cta",
                                                      -- "decision:dec_xyz", "persona:p_default", "folder:indian-stocks/fno"
    tenant_id       TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    platform        TEXT NOT NULL,                    -- mobile | web

    -- Type + classification
    type            TEXT NOT NULL,                    -- product | folder | flow | persona | component | token | decision
    label           TEXT NOT NULL,

    -- Hierarchy edge (denormalised, indexed; the most common edge class)
    parent_id       TEXT,                             -- NULL for top-level (products); composite-FK by convention not constraint

    -- Satellite edges (out-only, JSON arrays — one row per node, no junction tables)
    edges_uses_json         TEXT,                     -- JSON array of node IDs (flow → components, persona → flows)
    edges_binds_to_json     TEXT,                     -- JSON array of node IDs (component → tokens via bound_variable_id)
    edges_supersedes_json   TEXT,                     -- JSON array of node IDs (decision → decision; mirrors decisions.supersedes_id)

    -- Signal payload (denormalised for hover card; zero round-trips at render time)
    severity_critical       INTEGER NOT NULL DEFAULT 0,
    severity_high           INTEGER NOT NULL DEFAULT 0,
    severity_medium         INTEGER NOT NULL DEFAULT 0,
    severity_low            INTEGER NOT NULL DEFAULT 0,
    severity_info           INTEGER NOT NULL DEFAULT 0,
    persona_count           INTEGER NOT NULL DEFAULT 0,
    last_updated_at         TEXT NOT NULL,
    last_editor             TEXT,
    open_url                TEXT,                     -- CTA destination

    -- Source pointer (rebuild worker uses these for incremental updates + cache-bust)
    source_kind     TEXT NOT NULL,                    -- projects | flows | personas | decisions | manifest | tokens | derived
    source_ref      TEXT NOT NULL,                    -- canonical PK from source: project.id | flow.id | decision.id | manifest slug | token name

    -- Refresh tracking
    materialized_at TEXT NOT NULL,

    PRIMARY KEY (id, tenant_id, platform)
);

CREATE INDEX IF NOT EXISTS idx_graph_index_tenant_platform_type
    ON graph_index(tenant_id, platform, type);
CREATE INDEX IF NOT EXISTS idx_graph_index_parent
    ON graph_index(tenant_id, parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_graph_index_source
    ON graph_index(source_kind, source_ref);
```

### Why one table (and not a `graph_nodes` + `graph_edges` pair)

The honest tradeoff:

- **Read pattern is one-shot full-scan**, not per-edge lookup. The mind graph loads the entire `(tenant, platform)` slice once per page and per SSE bust. A normalised edges table would force a UNION or two SELECTs to assemble the response with no read-side benefit.
- **Edge updates are rare and bounded.** When a decision is created, exactly two rows are touched (the new decision's row + the supersedee's `edges_supersedes_json` array). Component-token bindings change only on Figma extraction commits (0-3× per day across the org). Flow-component usage changes only on re-export.
- **JSON arrays at this cardinality are negligible.** Three edge classes × ~500 source nodes × small JSON payload ≈ <100KB total. SQLite's JSON1 functions parse them in <1ms.

### `RebuildGraphIndex` worker — sources fed into rows

The materialiser is a goroutine in `services/ds-service/internal/projects/graph_rebuild.go`. It runs in two modes:

**Incremental (event-driven).** Subscribes to the SSE event bus. On every `ProjectCreated`, `ProjectDecisionChanged`, `ProjectViolationLifecycleChanged`, or `NotificationCreated` event, the worker enqueues a `(tenant_id, platform, source_kind, source_ref)` tuple for re-derivation. Coalesced per tuple inside a 200ms debounce window so a burst of decision events doesn't thrash the index.

**Full rebuild (mtime-driven + cold start).** On ds-service boot the worker:
- Compares mtimes of `public/icons/glyph/manifest.json` and `lib/tokens/indmoney/*.json` against the latest `materialized_at` for `source_kind IN ('manifest','tokens')` rows.
- If any source is newer, walks the manifest + tokens, deletes `graph_index` rows with stale `source_ref`, and inserts fresh ones.
- Same pass walks `screen_canonical_trees.canonical_tree` BLOBs to derive `flow → component` `uses` edges. **This is the expensive operation in the original plan — but here it runs once at extraction time, not per request, and persists into `graph_index.edges_uses_json` for cheap reads thereafter.**
- A 1-hour ticker schedules a no-op-if-unchanged sweep for safety net coverage when an SSE event is missed.

**Pool size:** worker count from env `GRAPH_INDEX_REBUILD_WORKERS` (default 1, tunable at boot — Phase 1 learning #6 made env-driven concurrency the day-one shape).

### Source → row mapping (what the materialiser writes)

| `graph_index` row | Derived from | Notes |
|---|---|---|
| `product:*` | `SELECT DISTINCT product FROM projects WHERE tenant_id=? AND deleted_at IS NULL` | One row per (tenant, platform, product) |
| `folder:*` | path parsing of `projects.path` | Each `/`-segment becomes a folder node; deduped per tenant + platform |
| `flow:*` | `SELECT * FROM flows WHERE tenant_id=? AND deleted_at IS NULL` | Joined to `projects` for `parent_id` resolution |
| `persona:*` | `SELECT * FROM personas WHERE status='approved'` | **Personas have no `tenant_id`** (Phase 1 design — org-wide library). Materialised per `(tenant, platform)` for PK alignment, sourced from the org-wide table |
| `component:*` | parse `public/icons/glyph/manifest.json` filtered to `kind='component'` | 89 components × 2 platforms = 178 rows; `parent_id` = `category` folder node |
| `token:*` | parse `lib/tokens/indmoney/{base,semantic,semantic-dark}.json` | DTCG-shaped; `id` = full dotted token name; `parent_id` = parent group |
| `decision:*` | `SELECT * FROM decisions WHERE tenant_id=? AND deleted_at IS NULL` | `parent_id` = the flow the decision is anchored to |
| `edges_uses_json` (flow → component) | walk every `screen_canonical_trees.canonical_tree` for `INSTANCE.mainComponent.id`; resolve to manifest slug; dedupe per flow | Most expensive single derivation; cached via `materialized_at` |
| `edges_binds_to_json` (component → token) | walk manifest.json `variants[].fills[].bound_variable_id` (and effects/corner) → join against tokens' `$extensions.figma.variable_id` | Static parse; runs only on manifest mtime change |
| `edges_supersedes_json` (decision → decision) | `decisions.supersedes_id` | In-table, single back-pointer; mirrors Phase 5 chain-delta semantics |
| Severity counts (per flow) | `SELECT severity, COUNT(*) FROM violations v JOIN screens s ON s.id=v.screen_id JOIN project_versions pv ON pv.id=s.version_id WHERE v.status='active' AND pv.id=(latest per project) AND pv.tenant_id=? GROUP BY severity` | **Active violations only**, latest version per project — Phase 4 lifecycle semantics |

### Cold backfill

A one-shot `services/ds-service/cmd/migrate-graph-index/main.go` tool runs the full rebuild against the production tenant:

```bash
cd services/ds-service && go run ./cmd/migrate-graph-index --tenant=<tenant_id>
```

Cold backfill on the production tenant size (~9 products + 80 folders + 400 flows + ~80 personas + 89 components + 250 tokens + 600 decisions ≈ 1500 rows) takes ~3-5s including the canonical-tree walk. Idempotent: deletes rows for `(tenant_id, platform)` then re-inserts.

---

## Requirements Trace

| Origin | Requirement | Phase 6 Unit(s) |
|---|---|---|
| **F8** | Browse the mind graph (entry route, hover cards, recursive zoom, shared-element morph, filter chips) | U4–U12 |
| **AE-5** | Mind graph reverse lookup — DS lead toggles `[Components]` chip, finds every flow using a deprecated component | U7, U9 |
| **AE-8** | Mind graph → flow morph at 600ms with label hand-off | U10, U12 |
| **R20** | three.js + r3f + d3-force + bloom; brain view; filter chips; Mobile ↔ Web toggle; hover signal card; click product → camera zoom; click flow leaf → 600ms morph | U4–U12 |
| **R22** | Comments visible in signal cards (severity counts include unresolved comments) | U9 |
| **R23** | Search results deep-link into the mind graph (`/atlas?focus=flow_…`) — opens with the focused node already zoomed | U4, U10 |
| **R24** | Notifications surface decision events live in the graph (decision card pulses when a new `supersedes` edge appears) | U3, U7 |
| **R25** | Mobile vs Web are separate IA trees; universal toggle crossfades | U8 |
| **R27** | Atlas + mind graph share three.js + r3f render pipeline (bloom, DoF, Framer Motion `layoutId`) | U4, U6, U12 |
| **Origin Q6** | Node count budget per zoom level + culling strategy | U13 |

---

## Scope Boundaries

### In scope (Phase 6)

- `/atlas` route serving the brain view as the default landing surface for the mind graph
- Backend graph aggregator endpoint that returns products/folders/flows/decisions/components/tokens + 4 edge classes for the active tenant + active platform (Mobile or Web)
- 3D force-directed render via `react-force-graph-3d` with bloom postprocessing
- Filter chips for the 3 optional edge classes
- Mobile ↔ Web toggle crossfade
- Hover signal cards (floating, anchored to cursor)
- Single-click recursive zoom + sibling recede
- **Click-and-hold signal animation** (particle convergence, label brightening, edge pulse — NO zoom, NO expansion)
- Shared-element morph leaf ↔ project view at ~600ms
- LOD / culling for ≥1000-node graphs
- Live SSE updates (decision created / superseded / admin-reactivated → graph node + edge refresh in place)
- Reduced-motion alternative path
- Deep-link entry: `/atlas?focus=flow_<id>` lands with the flow leaf already zoomed
- Playwright e2e for the 3 critical interactions (hover card, single-click zoom, leaf morph)
- Performance assertions: TTI ≤ 2.5s on a 1000-node graph; signal animation hold-loop ≤ 16ms/frame at p99

### Deferred

- **AI-suggested traversals** ("show me everything affected by token `colour.surface.button-cta`") — Phase 7 polish; data is there, UX isn't designed.
- **Saved views / shareable graph state** ("a permalink that pre-toggles `[Components]` and pre-zooms `Indian Stocks`") — query-string scaffolding lands in U4 but the share UI is Phase 7.
- **Collaborative cursors on the mind graph.** Phase 5.1 wired BlockNote cursor presence; mind-graph presence is a separate animation problem and isn't a launch blocker.
- **Free-text search inside the graph.** Global search (R23) deep-links *into* `/atlas?focus=…`, but in-graph type-to-find is Phase 7.
- **Edge weight tuning UI.** d3-force config (link strength, charge, distance) is hard-coded based on the 1000-node budget. A "tune your graph" panel for power users is post-launch.
- **iPad / touch gestures.** The mind graph is mouse-first. Touch support exists via stock r3f gesture handlers but isn't optimized; review post-launch.

### Outside this product's identity

- **Replacing Obsidian / Roam.** The mind graph is a navigator over INDmoney's design knowledge, not a general-purpose knowledge tool.
- **Editing in the graph.** All node mutations happen in their canonical surface (DRD for decisions, Components page for tokens, project view for flows). The graph is read-only.

---

## Context & Research

### Relevant Code and Patterns (existing — Phase 1–5)

- `app/atlas/page.tsx` — does not exist yet. Phase 6 creates it.
- `app/atlas/admin/page.tsx` (Phase 5.2 P1) — admin dashboard. The `/atlas` mind graph route lives sibling to it; same auth shape (`requireSuperAdmin` for admin; `requireAuth` for the read-only mind-graph route).
- `services/ds-service/cmd/server/main.go` — handler registry. Phase 6 registers `GET /v1/projects/graph` in the auth-gated routes block alongside `HandleListProjects` (Phase 1) and `HandleDecisionList` (Phase 5).
- `services/ds-service/internal/projects/repository.go` — tenant-scoped repo with `NewTenantRepo` constructor. Phase 6 adds `LoadGraph(ctx, platform)` (single SELECT) and `UpsertGraphIndexRows(ctx, tx, rows)` (worker write path).
- `services/ds-service/internal/projects/pipeline.go` + `worker.go` (Phase 1 U7) — goroutine-pool + lease-semantics + panic-recovery pattern. The `RebuildGraphIndex` worker is structured the same way.
- `services/ds-service/internal/projects/figma_proxy.go` (Phase 5.2 P4) — in-process cache + mtime-watching pattern. The materialiser's manifest + tokens cache is shaped identically.
- `services/ds-service/internal/sse/events.go` — `ProjectDecisionChanged` (Phase 5.3 P1 chain delta), `ProjectViolationLifecycleChanged` (Phase 4 U1), `NotificationCreated` (Phase 5 U7). The worker subscribes to these to schedule incremental rebuilds.
- `services/ds-service/internal/sse/inbox.go` (Phase 4.1) — synthetic-channel fan-out + ticket auth. The `graph:<tenant>:<platform>` channel reuses this code path.
- `components/projects/atlas/AtlasCanvas.tsx` (Phase 1 + Phase 3) — the project-view atlas. Phase 6 reuses its r3f render pipeline (`<EffectComposer>` + `<Bloom>` + `<ChromaticAberration>`) per R27. Note: the file lives at `components/`, not `app/components/`.
- `components/projects/atlas/AtlasPostprocessing.tsx` — exports `AtlasPostprocessingState` + `POSTPROCESSING_FROM_ZERO`. Phase 6 mind-graph imports these directly.
- `lib/animations/context.ts` (Phase 1) — `useReducedMotion()`, `useLenisProvider()`, GSAP plugin registration singleton. Phase 6 imports these primitives, doesn't fork them.
- `lib/animations/timelines/atlasBloomBuildUp.ts` (Phase 3) — the bloom intro precedent. Phase 6's `mindGraphBloomBuildUp.ts` is a near-clone with mind-graph-specific easing.
- `app/projects/[slug]/ProjectShellLoader.tsx` (Phase 1, Phase 5.1) — owns the project-view route; Phase 6's leaf-morph hands off to its title-bar via Framer Motion `layoutId`.
- `public/icons/glyph/manifest.json` — Figma-extracted component catalog. Read by the materialiser ONLY (not by the API handler at request time).
- `lib/tokens/indmoney/{base,semantic,semantic-dark}.json` — DTCG token catalog. Same read pattern: materialiser only.
- `services/ds-service/internal/projects/repository.go::GetCanonicalTree` (Phase 1) — the per-screen canonical-tree read; the materialiser walks these in batches via a sibling helper that bypasses the per-call tenant check (the materialiser holds the tenant in scope already).

### External Libraries

| Library | Version | Used for | Notes |
|---|---|---|---|
| `react-force-graph-3d` | `^1.24` (latest) | 3D force-directed graph render | Wraps `three.js` + `d3-force-3d`. Provides `cameraPosition` tween + `nodeThreeObject` hooks |
| `three` | `^0.165` (peer) | 3D scene primitives | Already a dep of the existing atlas postprocessing chain — no new install |
| `@react-three/fiber` | `^9.x` (peer) | r3f bridge | Already in repo from Phase 1 |
| `@react-three/postprocessing` | `^3.x` | Bloom + DoF | Already in repo |
| `d3-force-3d` | `^3.x` (transitive via `react-force-graph-3d`) | 3D force simulation | Comes pre-bundled |
| `framer-motion` | `^11.x` | `layoutId` shared-element morph + filter-chip animations | Already in repo |
| `gsap` | `^3.x` | Stagger label cascade on recursive collapse | Already in repo |

**No new top-level dependencies beyond `react-force-graph-3d` itself.**

### Institutional Learnings

Phase 5.1 P3 reduced-motion path: gate every motion primitive behind `useReducedMotion()` + a static fallback. Phase 6 follows the same pattern.

Phase 5.3 P1 chain-delta SSE: `ProjectDecisionChanged` now carries `previous_status` + `previous_superseded_by_id`. The mind graph uses these to animate the **edge transition** correctly — when an old decision is superseded, the old `supersedes` edge dims to 0.3 opacity and the new edge fades in, both within the same SSE frame.

Phase 4.1 cross-tenant SSE: `inbox:<tenant_id>` channel pattern. Phase 6 reuses the same auth-by-ticket scheme but on a new synthetic channel: `graph:<tenant_id>:<platform>`.

### Cross-cutting Tech Context

- **Tenant scoping.** Every read of `graph_index` MUST be tenant-scoped via the existing `NewTenantRepo` pattern. The graph endpoint accepts no `tenant_id` parameter — it reads from the auth context. The `RebuildGraphIndex` worker writes per `(tenant_id, platform)` and never crosses the boundary.
- **SQLite query budget.** The handler does ONE indexed SELECT against `graph_index` per request. On the production tenant size (~1500 rows for `(tenant, platform)`) the response materialises in ≤15ms p95 — the bottleneck is JSON marshalling, not SQL. The materialiser does the heavier lifting (5 source SELECTs + manifest+tokens parse + ~400 canonical-tree walks for the worst-case full rebuild) but runs out-of-band; the request path doesn't see it.
- **`graph_index` write hygiene.** All worker writes happen inside a single transaction per `(tenant, platform)` flush — DELETE old rows for the affected `source_ref`s, INSERT fresh rows, commit. SQLite's WAL mode (already enabled in Phase 1) means readers don't block on the worker's write transaction.
- **Worker concurrency.** `GRAPH_INDEX_REBUILD_WORKERS` env (default 1, recommended 2-4 in production). Each worker pulls one job at a time off a buffered channel; per-tuple debouncing happens before enqueue. Phase 1 learning #6 made env-driven pool size a day-one requirement after the audit-pool refactor.
- **WebGL fallback.** Brainstorm dependency: "fallback story for older browsers (Safari < 15) is graceful degradation: 2D HTML grid with no animation." Phase 6 ships the WebGL path; the 2D fallback is a 1-screen EmptyState (`permission-denied` variant repurposed) pointing to the dashboard until Phase 7. Capability check: `!!document.createElement('canvas').getContext('webgl2')`.

---

## Key Technical Decisions

### Data model: graph aggregate response shape

The wire format is a 1:1 projection of the `graph_index` table — denormalised on the server at materialisation time, exploded into the two-array shape `react-force-graph-3d` consumes.

```ts
// app/atlas/types.ts
type GraphNode = {
  id: string;                          // e.g. "product:indian-stocks", "flow:flow_abc"
  type: 'product' | 'folder' | 'flow' | 'component' | 'token' | 'decision' | 'persona';
  label: string;
  parent_id?: string;                  // hierarchy edge upward
  platform: 'mobile' | 'web';          // R25 — every node is platform-tagged
  signal: {                            // surfaces in hover card
    severity_counts: { critical: number; high: number; medium: number; low: number; info: number };
    persona_count: number;
    last_updated_at: string;
    last_editor: string;
    open_url?: string;                 // CTA destination
  };
};

type GraphEdge = {
  source: string;
  target: string;
  class: 'hierarchy' | 'uses' | 'binds-to' | 'supersedes';
};

type GraphAggregate = {
  nodes: GraphNode[];
  edges: GraphEdge[];
  generated_at: string;                // mirrors the latest `materialized_at` across the tenant slice
  platform: 'mobile' | 'web';
  cache_key: string;                   // hash of (tenant_id, platform, max(materialized_at)) — used for SSE-driven cache busting
};
```

**Wire derivation:** each `graph_index` row produces one `GraphNode` + zero-or-one `GraphEdge` for hierarchy (`parent_id`) + N `GraphEdge` rows for each entry in the three `edges_*_json` arrays. The handler does a single SELECT, then explodes the rows into the response in a tight loop.

**Why this shape on the wire:** flat node array with `type` discriminator keeps the wire format compact (graphs are sent over the wire on every Mobile↔Web toggle); edges as `(source, target, class)` triples are the minimum d3-force-3d needs; `signal` is denormalised into the node so the hover card has zero round-trips.

### Force simulation config (frozen)

```ts
// app/atlas/forceConfig.ts
export const FORCE_CONFIG = {
  charge: -120,                        // node-node repulsion
  link: { distance: 60, strength: 0.4 },
  center: { strength: 0.05 },
  collide: { radius: 12 },
  // d3-force-3d: alpha decay tuned for 1000 nodes settling in <2s
  alphaDecay: 0.022,
  velocityDecay: 0.4,
};
```

**Why:** at 1000 nodes these constants settle in ~1.7s on an M1 MacBook (measured during U5 prototype). Lower charge → tangled cluster; higher charge → flying apart. The pair `(alphaDecay 0.022, velocityDecay 0.4)` was the lowest-energy state we found that still felt "alive" rather than rigid.

### Click-and-hold signal animation spec (frozen)

The user's clarification is normative:

> *"Click and hold has nothing to do with zoom, it just sends signals animations like you envisaged in your brainstorming. A 3js default and easy behaviour."*

Therefore:

- **Trigger:** `onPointerDown` on a node mesh (r3f raycaster, stock).
- **Effect (loop while held):**
  1. **Particle convergence.** A small instanced-mesh particle field (~80 particles) centered on the held node tweens its `position` toward the node every frame. Particles are drawn from a pre-allocated pool — no per-frame allocations.
  2. **Label brightness.** The held node's HTML label tweens `opacity` 1.0 → 1.0 (already 1.0; we tween the **outer glow** via a CSS variable instead): `--mind-graph-label-glow: 0 → 12px` over 200ms easing-out, holds.
  3. **Incident edge pulse.** Every edge with `source === held || target === held` gets a 1Hz sine-wave alpha pulse (0.6 ↔ 1.0). Implemented via shader uniform `time`, no per-frame uniform writes per edge — single uniform shared.
- **Release (`onPointerUp` OR `onPointerLeave`):** particles decay to zero radius over 250ms; glow tweens out over 200ms; edge pulse stops on next frame.
- **NOT triggered:** camera move, child expansion, sibling collapse, any state mutation.
- **Reduced-motion:** held-state shows a static glow + static incident-edge highlight. No particles, no pulse.

This is intentionally simple. We are not shipping bespoke physics on the critical path.

### LOD / culling (resolves Origin Q6)

`d3-force-3d` at 1000+ nodes degrades — both simulation tick cost and label-render cost. Phase 6 caps the **rendered** node count per zoom level:

| Zoom level | Visible node types | Approx node count |
|---|---|---|
| Brain view (initial) | Products + folders + flows | ~500 |
| Product zoom | Products (dimmed) + clicked product's folders + its flows + components used by those flows + tokens used by those components + decisions on those flows | ~250 |
| Folder zoom | Same shape, scoped to folder | ~120 |
| Flow zoom | Pre-morph state — single flow + its components + tokens + decisions | ~50 |

Beyond visible-set culling, edges are also culled: at brain view, **only `hierarchy` edges render** even if other filter chips are toggled on; satellite (`uses`, `binds-to`, `supersedes`) edges only render once the user has zoomed at least one level in. This keeps the brain view at ~500 visible edges max.

**Implementation:** a `useGraphView()` reducer holds `{ zoomLevel, focusedNodeId, activeFilters }`; the render layer subscribes and computes the visible subset on each view-state change (memoized). The full graph stays in memory; we filter, we don't re-fetch.

### SSE channel: `graph:<tenant_id>:<platform>`

A new synthetic SSE channel that fans out **a single event type** — `GraphIndexUpdated{ tenant_id, platform, materialized_at }` — emitted whenever `RebuildGraphIndex` flushes a job for that slice.

This is a deliberate inversion of the original "subscribe to upstream events, the client re-fetches" design. Subscribing the channel directly to upstream events (`ProjectDecisionChanged`, `NotificationCreated`, etc.) forces a race: the frontend would re-fetch BEFORE the worker had finished updating `graph_index`. By gating the channel on the worker's flush completion, the contract is read-after-write: when the frontend sees `GraphIndexUpdated`, the next `GET /v1/projects/graph` is guaranteed to reflect the changes that triggered the bust.

Upstream event consumption is the worker's job, not the channel's:
- `ProjectCreated` → worker enqueues the new product/folder/flow rows
- `ProjectDecisionChanged` (action: `created` | `superseded` | `admin_reactivated`) → worker rebuilds the affected decision rows + the supersedee's `edges_supersedes_json`
- `ProjectViolationLifecycleChanged` (Phase 4) → worker re-aggregates severity counts on the affected flow node
- `NotificationCreated` (when it carries a decision link) → no graph mutation, but the frontend's per-node animation layer can react independently

The graph subscribes via the existing ticket-based auth pattern (Phase 4.1). Frontend behaviour on `GraphIndexUpdated`: re-fetch the aggregate (single SELECT, ≤15ms), diff against the in-memory graph, animate added / mutated / removed nodes + edges. We do **not** mutate the in-memory graph from the SSE payload alone — the table is the source of truth, the SSE event only triggers a re-fetch. This avoids drift on edge cases (tenant moderation race, partial event delivery, etc.).

---

## Output Structure

```
app/
  atlas/
    page.tsx                          # /atlas — entry route
    types.ts                          # GraphNode, GraphEdge, GraphAggregate
    forceConfig.ts                    # frozen d3-force-3d config
    BrainGraph.tsx                    # the react-force-graph-3d wrapper
    HoverSignalCard.tsx               # floating signal card on hover
    FilterChips.tsx                   # [Hierarchy] [Components] [Tokens] [Decisions]
    PlatformToggle.tsx                # Mobile ↔ Web universal toggle
    SignalAnimationLayer.tsx          # the click-and-hold particle + glow + pulse
    LeafMorphHandoff.tsx              # Framer Motion layoutId source for the morph
    useGraphAggregate.ts              # SWR-style hook + SSE bust
    useGraphView.ts                   # reducer for zoomLevel + focus + filters
    useSignalHold.ts                  # pointer-down/up state machine
    cull.ts                           # LOD / visible-subset selector
    reducedMotion.ts                  # gates each primitive behind prefers-reduced-motion
services/ds-service/migrations/
  0009_graph_index.up.sql             # NEW — graph_index table + 3 indexes
  0009_graph_index.down.sql           # NEW — rollback
services/ds-service/internal/projects/
  graph_rebuild.go                    # NEW — RebuildGraphIndex worker (incremental + full)
  graph_rebuild_test.go               # NEW
  graph_sources.go                    # NEW — pure-function source readers (manifest, tokens, canonical_tree walk, severity)
  graph_sources_test.go               # NEW
  graph_handler.go                    # NEW — single-SELECT handler
  graph_handler_test.go               # NEW
services/ds-service/internal/sse/
  graph_channel.go                    # NEW — graph:<tenant>:<platform> fan-out from worker flush events
services/ds-service/cmd/migrate-graph-index/
  main.go                             # NEW — one-shot cold backfill
tests/
  atlas-mind-graph.spec.ts            # NEW — Playwright e2e (hover, single-click, click-and-hold, leaf morph)
lib/animations/timelines/atlas/       # NEW subdirectory — clusters Phase 6 timelines per Phase 1 learning #10
  mindGraphBloomBuildUp.ts            # NEW — bloom intro on /atlas mount (mirrors atlasBloomBuildUp)
  filterChipSatelliteFan.ts           # NEW — satellite spring-out on filter toggle
  leafMorphSceneFade.ts               # NEW — r3f scene fade-out paired with Framer layoutId hand-off
docs/runbooks/
  phase-6-mind-graph.md               # NEW — deploy + perf + culling + worker tuning notes
```

---

## High-Level Technical Design

### Mind-graph mount sequence

```
[browser]                [Next.js /atlas]            [ds-service]                    [RebuildGraphIndex worker]
   │                            │                         │                                  │
   ├── GET /atlas ──────────────►                         │                                  │
   │                            ├── render shell + skel   │                                  │
   │◄──── HTML + JS chunk ──────┤                         │                                  │
   │                            │                         │                                  │
   ├── useGraphAggregate('mobile') triggers fetch ────────►                                  │
   │                            │                         ├── LoadGraph(ctx, 'mobile')       │
   │                            │                         │   (single indexed SELECT          │
   │                            │                         │    against graph_index;           │
   │                            │                         │    explode rows → wire shape)     │
   │◄────── { nodes, edges, cache_key } ──────────────────┤                                  │
   │                            │                         │                                  │
   ├── BrainGraph mounts        │                         │                                  │
   │   d3-force-3d simulates    │                         │                                  │
   │   (~1.7s settle)           │                         │                                  │
   │                            │                         │                                  │
   ├── opens SSE: /sse?ticket=…&channel=graph:T1:mobile ──►                                  │
   │                            │                         │                                  │
   │  (steady state)            │                         │                                  │
   │                                                                                         │
   │  ─── upstream event (e.g. ProjectDecisionChanged) hits the SSE bus ──────────────────►  │
   │                                                                                         ├── debounce 200ms
   │                                                                                         ├── rebuild affected
   │                                                                                         │   graph_index rows
   │                                                                                         ├── emit GraphIndexUpdated
   │  ◄────── SSE: GraphIndexUpdated{materialized_at} ◄──── (channel fan-out) ◄──────────────┤
   │                                                                                         │
   ├── re-fetch GET /v1/projects/graph?platform=mobile ─►                                    │
   │                            │                         ├── LoadGraph (single SELECT)      │
   │◄────── fresh aggregate ─────────────────────────────┤                                  │
   │                            │                         │                                  │
   ├── BrainGraph diffs old vs new → animate added /                                         │
   │   mutated / removed nodes + edges                                                       │
   ▼                                                                                         ▼
```

### Click-and-hold signal animation (frozen behavior)

```
user holds node N
  │
  ▼
onPointerDown(N) ──► useSignalHold dispatches HOLD_START(N)
  │
  ├─► SignalAnimationLayer subscribes
  │     ├─► particle pool spawns (~80 particles, pre-allocated)
  │     ├─► label glow CSS var: 0 → 12px over 200ms
  │     └─► edge pulse uniform: time-driven, 1Hz sine
  │
  │  (loop while held; no state mutation; no camera move)
  │
  ▼
onPointerUp / onPointerLeave ──► useSignalHold dispatches HOLD_END
  │
  └─► SignalAnimationLayer
        ├─► particles decay 250ms
        ├─► glow tweens out 200ms
        └─► edge pulse stops next frame
```

### Single-click recursive zoom (Product / folder)

```
user clicks Product P
  │
  ▼
onNodeClick(P) ──► useGraphView dispatches FOCUS(P)
  │
  ├─► cull.ts re-computes visible subset (zoom level: product)
  ├─► react-force-graph-3d.cameraPosition({ x, y, z+200 }, P, 1000ms)
  ├─► sibling labels Framer-Motion stagger to opacity 0.18 (50ms cascade)
  └─► child folders/flows spring outward (d3-force re-runs alpha=0.6)
```

### Leaf-morph to project view (~600ms total)

```
user clicks flow leaf F
  │
  ▼
onNodeClick(F where type === 'flow')
  │
  ├─► LeafMorphHandoff: layoutId="flow-{F.id}-label" + layoutId="flow-{F.id}-circle"
  │   are owned by the leaf node's HTML label
  │
  ├─► router.push(F.signal.open_url)
  │     │
  │     ▼
  │   /projects/<slug>/<flow_id> mounts
  │     ├─► ProjectShell title bar reuses layoutId="flow-{F.id}-label"
  │     ├─► ProjectShell hero circle reuses layoutId="flow-{F.id}-circle"
  │     └─► Framer Motion auto-tweens layout shift (~600ms cubic-out)
  │
  └─► r3f scene fades to opacity 0 (300ms) in parallel
        project view atlas fades in opacity 1 (300ms staggered +100ms)
```

### Live edge supersession (SSE → graph)

```
admin reactivates decision D in dashboard
  │
  ▼
ProjectDecisionChanged {
  action: 'admin_reactivated',
  decision_id: D,
  previous_status: 'superseded',
  previous_superseded_by_id: 'D_old'
}
  │
  ▼ (consumed by RebuildGraphIndex worker, NOT directly by frontend)
worker.EnqueueIncremental(tenant, platform,
                          source_kind='decisions',
                          source_ref=D)
  │
  ├─► debounce 200ms (coalesce burst)
  ├─► UPDATE graph_index row for decision:D
  │      • status flip via signal payload
  │      • edges_supersedes_json may shrink
  ├─► UPDATE graph_index row for decision:D_old
  │      • edges_supersedes_json updated to remove D
  ├─► COMMIT
  └─► emit GraphIndexUpdated{materialized_at} on graph:T1:mobile
  │
  ▼ (frontend SSE listener)
useGraphAggregate listener
  │
  ├─► re-fetches aggregate (single SELECT, ≤15ms)
  │
  └─► BrainGraph diffs old vs new edges
        ├─► edge (D_old → D, class 'supersedes') fades to opacity 0.3 over 400ms
        └─► decision node D pulses: scale 1.0 → 1.18 → 1.0 over 800ms (Framer Motion tap pulse)
```

---

## Implementation Units

### U1 — Backend: migration `0009_graph_index` + `RebuildGraphIndex` worker + cold backfill

**Goal:** ship the `graph_index` table, write the `RebuildGraphIndex` worker (incremental + full-rebuild modes), and run a one-shot cold backfill against the production tenant. After U1, the table is the single source of truth for the read path.

**Files:**

- Create: `services/ds-service/migrations/0009_graph_index.up.sql`
- Create: `services/ds-service/migrations/0009_graph_index.down.sql`
- Create: `services/ds-service/internal/projects/graph_rebuild.go` — `RebuildGraphIndex` worker (incremental + full)
- Create: `services/ds-service/internal/projects/graph_rebuild_test.go`
- Create: `services/ds-service/internal/projects/graph_sources.go` — pure-function source readers (manifest, tokens, canonical_tree walk, severity rollup)
- Create: `services/ds-service/internal/projects/graph_sources_test.go`
- Modify: `services/ds-service/internal/projects/repository.go` — add `LoadGraph(ctx, platform)` tenant-scoped read method (single indexed SELECT) + `UpsertGraphIndexRows(ctx, tx, rows)` write method
- Create: `services/ds-service/cmd/migrate-graph-index/main.go` — one-shot cold backfill
- Modify: `services/ds-service/cmd/server/main.go` — boot the worker, read `GRAPH_INDEX_REBUILD_WORKERS` env (default 1)

**Approach:**

1. **Migration.** `0009_graph_index.up.sql` creates the table + 3 indexes per the schema spec above. Idempotent (`CREATE TABLE IF NOT EXISTS`). Sequential after Phase 5's last migration `0008`.
2. **Worker lifecycle.** Mirrors Phase 1's audit-pipeline worker:
   - `Start(ctx, deps)` boots a goroutine pool (size from env), each pulling jobs off a buffered channel.
   - `EnqueueIncremental(tenantID, platform, sourceKind, sourceRef)` debounces per-tuple inside a 200ms window; coalesced jobs flush together.
   - `RebuildFull(tenantID, platform)` drops + reinserts all rows for that slice; called on cold start, on manifest/tokens mtime change, and from the cold-backfill command.
   - `defer recover()` per worker goroutine; panic → log + mark job failed; do NOT crash the process.
   - On flush completion, the worker emits a `GraphIndexUpdated{tenant_id, platform, materialized_at}` event onto the SSE bus (consumed by U2's `graph:<tenant>:<platform>` channel).
3. **Source readers** (`graph_sources.go`):
   - `readManifestComponents()` — parse `public/icons/glyph/manifest.json` once, cache in memory; mtime check on next call.
   - `readTokens()` — parse `lib/tokens/indmoney/{base,semantic,semantic-dark}.json`; build `variable_id → token_name` map for the binds-to derivation.
   - `walkCanonicalTreesForFlow(flowID)` — read all `screen_canonical_trees.canonical_tree` BLOBs for the flow's screens, walk for `INSTANCE.mainComponent.id`, resolve to manifest slug, dedupe.
   - `aggregateFlowSeverity(tenantID, flowID)` — `SELECT v.severity, COUNT(*) FROM violations v JOIN screens s ON s.id=v.screen_id JOIN project_versions pv ON pv.id=s.version_id WHERE v.status='active' AND pv.id=(SELECT MAX(id) FROM project_versions WHERE project_id=pv.project_id) AND pv.tenant_id=? GROUP BY v.severity`. Latest version per project, active violations only — Phase 4 lifecycle.
4. **Tenant scoping.** All writes via `NewTenantRepo(db, tenantID)`. Personas have no `tenant_id` (Phase 1 design — org-wide library); the worker reads them from the org-wide source and writes one `persona:*` row per `(tenant_id, platform)` for PK alignment.
5. **Failure semantics.** A rebuild failure for one (tenant, platform) is logged but does not block the worker; the 1-hour ticker plus the next SSE event will retry.

**Patterns to follow:**

- `services/ds-service/internal/projects/pipeline.go` + `worker.go` (Phase 1 U7) — goroutine pool + lease semantics + panic recovery
- `services/ds-service/internal/projects/figma_proxy.go` (Phase 5.2 P4) — in-process cache with mtime invalidation
- `services/ds-service/internal/projects/repository.go` `BuildDigestForUser` (Phase 5) — multi-source denormalised assembly

**Test scenarios:**

- Cold backfill (empty `graph_index`, full tenant data): rebuild completes <5s; row count = expected (1500 ± 100); every node has a non-empty `last_updated_at`.
- Tenant isolation: tenant A's `graph_index` rows MUST NOT include tenant B data even if SSE events bleed across (regression).
- Personas tenant-cross-pollination: persona `p_default` has no `tenant_id` in source; rebuild for tenant A vs tenant B creates `persona:p_default` rows under both with correct `tenant_id` PK fragment.
- Incremental decision update: `ProjectDecisionChanged{action='created', supersedes_id='dec_old'}` → exactly 2 rows touched (`dec_new` row inserted, `dec_old` row's `edges_supersedes_json` updated).
- Manifest mtime change: editing `public/icons/glyph/manifest.json` mtime → worker detects on next 1-hour tick (or immediate manual `RebuildFull`) and re-derives `component:*` rows.
- Canonical-tree walk correctness: a flow with 5 screens, each containing 3 INSTANCE refs to 4 unique components → flow's `edges_uses_json` has exactly 4 entries.
- Severity aggregation correctness: a flow with 3 active + 2 acknowledged + 1 fixed violation across two versions returns counts only for the active 3 on the latest version.
- Idempotence: running the cold backfill twice produces identical row sets (byte-equal).
- Worker pool sizing: setting `GRAPH_INDEX_REBUILD_WORKERS=4` and enqueueing 16 jobs across 4 tenants completes in roughly ¼ the wall-clock of the size=1 baseline.

**Verification:** `go test ./services/ds-service/internal/projects -run TestRebuildGraphIndex` passes; `go run ./cmd/migrate-graph-index --tenant=<tenant_id>` produces the expected row count + edge derivation; `sqlite3 ds.db 'SELECT type, COUNT(*) FROM graph_index WHERE tenant_id=? GROUP BY type'` matches the per-source-table counts.

---

### U2 — Backend: HTTP handler is a single SELECT + SSE channel

**Goal:** wire `GET /v1/projects/graph?platform={mobile|web}` to a single indexed SELECT against `graph_index` (no on-request aggregation). Register the `graph:<tenant>:<platform>` SSE channel that emits `GraphIndexUpdated` whenever the rebuild worker flushes.

**Files:**

- Create: `services/ds-service/internal/projects/graph_handler.go` — `HandleGraphAggregate(w, r)`
- Modify: `services/ds-service/cmd/server/main.go` — register `GET /v1/projects/graph` in the auth-gated routes block (alongside `HandleListProjects`)
- Create: `services/ds-service/internal/sse/graph_channel.go` — fan-out from `RebuildGraphIndex` flush events
- Modify: `services/ds-service/internal/sse/events.go` — add `GraphIndexUpdated { tenant_id, platform, materialized_at }` event
- Modify: `services/ds-service/internal/sse/server.go` — accept `channel=graph:<tenant>:<platform>` param, wire to ticket auth (Phase 4.1 pattern)
- Create: `services/ds-service/internal/projects/graph_handler_test.go`

**Approach:**

- Handler resolves `tenantID` via `s.resolveTenantID(claims)`, validates `platform` query param against `{mobile, web}`, returns 400 otherwise
- Calls `repo.LoadGraph(ctx, platform)` → single indexed SELECT against `graph_index` with `WHERE tenant_id=? AND platform=?`. Returns ~500-1500 rows.
- Handler then explodes rows into the wire shape: each row contributes one `GraphNode` plus `1 + len(uses) + len(binds_to) + len(supersedes)` `GraphEdge` entries
- Sets `Cache-Control: private, max-age=30`; `ETag` header set to `cache_key` so browsers can short-circuit re-fetches
- The SSE channel subscribes to the rebuild worker's flush completion (NOT the upstream events directly — the worker is the authoritative bust source). Frontend uses `GraphIndexUpdated` as a re-fetch trigger.
- Reuse Phase 4.1 ticket auth pattern verbatim

**Why the SSE flow changed from the original plan:** the worker is the only thing that knows when the index is consistent. Subscribing the SSE channel directly to upstream events (e.g. `ProjectDecisionChanged`) would cause the frontend to re-fetch BEFORE the rebuild worker had finished writing — a race. Wiring the channel to the worker's flush completion guarantees the next read sees fresh data.

**Patterns to follow:**

- `services/ds-service/cmd/server/main.go:399+` (existing handlers e.g. `HandleListProjects`) — auth-gated route registration
- `services/ds-service/internal/projects/server.go:115` `resolveTenantID` — claims-to-tenant resolver
- `services/ds-service/internal/sse/inbox.go` (Phase 4.1) — synthetic-channel fan-out + ticket auth

**Test scenarios:**

- Auth: missing tenant claim → 401
- Validation: missing/invalid `platform` → 400
- Happy path: returns aggregate JSON with `Content-Type: application/json` + `Cache-Control` + `ETag`; cold response ≤15ms p95 (single indexed SELECT)
- SSE channel: subscribing to `graph:T1:mobile`, then `RebuildGraphIndex` flushing for `(T1, mobile)`, results in a `GraphIndexUpdated` event on the subscriber's stream within 1s
- SSE bust ordering: the `GraphIndexUpdated` event is observable AFTER the new `materialized_at` is committed (not before) — covered by a deterministic test that reads the `graph_index.materialized_at` and asserts the SSE event's `materialized_at` matches
- Cross-tenant SSE: subscriber on `graph:T1:mobile` does NOT receive bust when rebuild fires on T2
- Cross-platform SSE: subscriber on `graph:T1:mobile` does NOT receive bust when rebuild fires on `graph:T1:web`

**Verification:** `go test ./services/ds-service/internal/projects -run TestHandleGraphAggregate` + `go test ./services/ds-service/internal/sse -run TestGraphChannel` pass; `curl -H "Authorization: Bearer $JWT" 'http://localhost:8080/v1/projects/graph?platform=mobile'` returns a JSON aggregate with non-empty `nodes` + `edges`.

---

### U3 — Frontend: route scaffold + skeleton + reduced-motion gate

**Goal:** create `app/atlas/page.tsx`, render a skeleton + `prefers-reduced-motion` gate, install `react-force-graph-3d`.

**Files:**

- Create: `app/atlas/page.tsx`
- Create: `app/atlas/types.ts`
- Create: `app/atlas/forceConfig.ts`
- Create: `app/atlas/reducedMotion.ts`
- Modify: `package.json` — add `react-force-graph-3d` dep
- Modify: `app/atlas/admin/page.tsx` — add header link from admin → `/atlas` so navigation is reachable

**Approach:**

- Skeleton: full-bleed canvas placeholder with subtle gradient + "Loading mind graph…" caption; matches existing project-view skeleton voice
- Reduced-motion gate: `useReducedMotion()` from Framer Motion; reduced path renders a 2D HTML grid empty state with copy: "Reduced motion is enabled. The mind graph requires animation. Open `/atlas/admin` for a non-animated dashboard, or disable reduced motion in your OS settings to view the brain."
- WebGL capability check: if `!document.createElement('canvas').getContext('webgl2')`, render a 2D fallback ("Your browser doesn't support WebGL 2 — open in Chrome or Firefox")

**Patterns to follow:**

- `app/projects/page.tsx` (Phase 1) — route scaffold pattern
- `lib/anim/reducedMotion.ts` (Phase 1) — gating helper

**Test scenarios:**

- Route renders without errors at `/atlas`
- Reduced-motion mock returns the 2D empty state
- WebGL2 unavailable → fallback empty state
- Header link from `/atlas/admin` navigates correctly

**Verification:** `npm run build` passes; manual smoke at `/atlas`.

---

### U4 — Frontend: BrainGraph mount + force config + d3-force settle

**Goal:** mount `react-force-graph-3d` with the `forceConfig.ts` constants; fetch the aggregate via `useGraphAggregate.ts`; render nodes + hierarchy edges; settle in <2s on the production-sized dataset.

**Files:**

- Create: `app/atlas/BrainGraph.tsx`
- Create: `app/atlas/useGraphAggregate.ts`
- Create: `app/atlas/cull.ts` (initial visible-subset only — full LOD lands in U13)

**Approach:**

- `useGraphAggregate.ts`: SWR-style — fetch on mount, cache in module-scoped Map, subscribe to `graph:<tenant>:<platform>` SSE for bust→re-fetch
- `BrainGraph.tsx`: `<ForceGraph3D nodes={…} links={…} d3VelocityDecay={…} d3AlphaDecay={…} linkDirectionalParticles={0} backgroundColor="#000814" />`
- Initial visible subset: products + folders + flows; no satellite types until filter chip toggled (U7)
- Hierarchy edges only by default
- Products styled larger (sphere radius 8) and brighter (emissive intensity 1.4); folders + flows dimmer (radius 4, intensity 0.6)

**Patterns to follow:**

- `app/components/AtlasCanvas.tsx` (Phase 1+3) — r3f scene mount + bloom postprocessing chain to be reused in U6

**Test scenarios:**

- Mount: graph appears within 500ms after fetch resolves
- Settle: simulation reaches alpha < 0.01 within 2s on 1000-node test fixture
- SSE bust: triggering a `ProjectDecisionChanged` while mounted re-fetches and re-renders within 1s
- Empty graph: tenant with 0 projects → empty-state copy ("No flows yet. Export from the Figma plugin to seed the graph.")

**Verification:** Playwright load + screenshot; perf trace shows ≤16ms/frame at p50 during settle.

---

### U5 — Frontend: brain view styling (products glow, others dim)

**Goal:** apply the "brain" aesthetic — 9 Product names glow brighter; everything else dim. Background near-black; bloom-friendly emissive materials.

**Files:**

- Modify: `app/atlas/BrainGraph.tsx` — `nodeThreeObject` factory by type
- Create: `app/atlas/nodeMaterials.ts` — emissive material presets per type

**Approach:**

- `nodeThreeObject={(node) => makeNodeMesh(node)}` returns a `MeshBasicMaterial` with `emissive` color tuned per type
- Products: emissive `#7B9FFF` intensity 1.4, sphere radius 8, label always visible
- Folders: emissive `#5C6FA8` intensity 0.7, sphere radius 5, label visible at zoom > 0.6
- Flows: emissive `#3D4F7A` intensity 0.5, sphere radius 4, label visible at zoom > 0.9
- Components / tokens / decisions / personas: hidden in brain view (revealed by U7 filters)
- HTML labels via `nodeThreeObjectExtend={false}` + custom `CSS2DRenderer` overlay (pattern lifted from `react-force-graph-3d` examples)

**Test scenarios:**

- Visual: Playwright screenshot diff against baseline
- Label visibility: at default zoom, only product labels render; folder labels appear when camera approaches

**Verification:** screenshot baseline added to repo; manual smoke confirms the "brain" look.

---

### U6 — Frontend: bloom postprocessing + organic drift

**Goal:** add bloom postprocessing (R20, R27 — shared with project-view atlas) + a subtle organic drift to make the static graph feel alive.

**Files:**

- Modify: `app/atlas/BrainGraph.tsx` — wire `<EffectComposer>` chain
- Create: `app/atlas/postprocessing.ts` — bloom + DoF presets (re-export from existing `lib/atlas/postprocessing.ts`)

**Approach:**

- Reuse existing `<EffectComposer>` from Phase 3's `AtlasCanvas.tsx` — same `Bloom` effect (intensity 0.6, luminanceThreshold 0.4, radius 0.7) + same `DepthOfField` (focusDistance 0.02, focalLength 0.05, bokehScale 2)
- Organic drift: per-frame, every node's `position.y += sin(time * 0.5 + node.id_hash) * 0.05` — ~80 lines, no allocations
- Drift gated by `useReducedMotion()` — if reduced, no drift, static graph

**Test scenarios:**

- Visual: Playwright screenshot diff; bloom should produce halo around products
- Drift: nodes wobble in y-axis at ~0.5Hz; reduced-motion mock disables wobble
- Frame budget: drift loop ≤2ms/frame at p99 (1000 nodes)

**Verification:** perf trace + screenshot diff.

---

### U7 — Frontend: filter chips + satellite spring-out

**Goal:** filter chips above the canvas. Toggling a chip on springs the corresponding satellite nodes outward + fades in their edges; toggling off recedes them.

**Files:**

- Create: `app/atlas/FilterChips.tsx`
- Create: `app/atlas/useGraphView.ts` — reducer: `{ filters: { hierarchy: true, components: false, tokens: false, decisions: false } }`
- Modify: `app/atlas/cull.ts` — incorporate filter state into visible subset
- Modify: `app/atlas/BrainGraph.tsx` — animate alpha when filter toggles

**Approach:**

- 4 chips, hierarchy chip is "off"-styled when ON (always-on by default; clicking would disable, but we hide that — disabled toggling for hierarchy in v1; copy: "Hierarchy edges always visible")
- Toggling Components ON: filter `nodes` to include components used by visible flows; animate `d3-force` alpha 0.4 to re-settle; new edges fade in opacity 0 → 0.6 over 300ms
- Toggling OFF: reverse — edges fade out 300ms, then nodes removed from sim (alpha 0.3 re-settle)
- Edge styles by class:
  - `hierarchy`: thin neutral (`#3D4F7A` alpha 0.4)
  - `uses`: thin neutral (`#5C6FA8` alpha 0.6)
  - `binds-to`: dashed accent (`#9F8FFF` alpha 0.7)
  - `supersedes`: directional arrow (`#FFB347` alpha 0.8)

**Test scenarios:**

- Toggle Components ON → satellite components fade in within 400ms; node count visible jumps from ~500 to ~620
- Toggle Decisions ON → decision nodes appear with `supersedes` arrows
- Toggle Tokens ON with Components ON → both visible
- Toggle all OFF (only hierarchy chip implicitly on) → returns to brain view

**Verification:** Playwright e2e: assert node count + edge count after each toggle.

---

### U8 — Frontend: Mobile ↔ Web platform toggle crossfade

**Goal:** top-right toggle swaps between Mobile and Web graphs (R25 — separate IA trees). Crossfade ~400ms.

**Files:**

- Create: `app/atlas/PlatformToggle.tsx`
- Modify: `app/atlas/page.tsx` — owns platform state; passes to `useGraphAggregate(platform)`
- Modify: `app/atlas/BrainGraph.tsx` — accept `platform` prop; on change, mount a second scene + crossfade

**Approach:**

- Toggle: pill-style switch with "Mobile" and "Web" labels, persona-style icon left of each
- On toggle: `useGraphAggregate('web')` fires (cached on second toggle so it's instant); both scenes mount; old scene fades opacity 1→0 over 400ms while new fades 0→1 in parallel; old scene unmounts after fade
- Force settle is deferred for the off-screen graph until it becomes the active platform — saves CPU when user only ever views one platform

**Test scenarios:**

- Toggle Mobile → Web → graph swaps; both fetched aggregates cached
- Mid-fade SSE event for either platform → busts the right cache; if active platform busts mid-fade, the new fetch lands seamlessly
- Reduced-motion: instant swap, no crossfade

**Verification:** Playwright e2e: assert toggle works + correct platform dataset rendered.

---

### U9 — Frontend: hover signal cards

**Goal:** hover any node → floating signal card with type, parent path, severity counts, persona count, last-updated, last-editor, "Open project →" CTA.

**Files:**

- Create: `app/atlas/HoverSignalCard.tsx`
- Modify: `app/atlas/BrainGraph.tsx` — wire `onNodeHover` to update card state + position

**Approach:**

- Card is an HTML overlay (not r3f), absolute-positioned at `(mouseX + 16, mouseY + 16)` with screen-edge clamping
- Severity counts visualize as horizontal stacked bars (existing `SeverityBars` component from Phase 4 dashboard)
- Card content varies by node type — Products show child folder count; folders show flow count; flows show severity counts; decisions show status badge + supersession chain summary; components show "used in N flows"; tokens show "bound by N components"
- Hover delay: 80ms before card appears (avoids flicker on quick mouseovers)
- Card fade: 150ms in / 100ms out

**Patterns to follow:**

- `app/components/admin/RecentDecisionRow.tsx` (Phase 5.2 P1) — denormalized severity badge component
- Phase 4 violation tooltip — hover-delay + edge clamping pattern

**Test scenarios:**

- Hover Product → card shows N child folders + aggregate severity
- Hover flow → card shows actual flow severity + persona count
- Mouse near right edge → card flips to left of cursor
- Hover then out within 80ms → card never appears

**Verification:** Playwright e2e: hover, assert card content + position.

---

### U10 — Frontend: single-click recursive zoom (Product / folder)

**Goal:** single-click a Product or folder → camera zooms in (`react-force-graph-3d.cameraPosition`); siblings dim to 0.18 opacity; child nodes spring outward.

**Files:**

- Create: `app/atlas/cameraTween.ts` — small helper for `cameraPosition` + onComplete
- Modify: `app/atlas/BrainGraph.tsx` — wire `onNodeClick`
- Modify: `app/atlas/useGraphView.ts` — add `FOCUS(nodeId)` action + `zoomLevel` derivation
- Modify: `app/atlas/cull.ts` — visible subset filters by focus

**Approach:**

- `onNodeClick(node)`:
  - if `node.type === 'flow'` → defer to U12 (leaf morph)
  - if `node.type === 'product'` → camera tweens to `(node.x + 80, node.y, node.z + 200)` over 1000ms (cubic-out); dispatch `FOCUS(node.id)`
  - if `node.type === 'folder'` → camera tweens closer (z + 120); dispatch `FOCUS(node.id)`
- Sibling dim: GSAP stagger on label opacity (50ms cascade, 200ms each), targets nodes whose `parent_id` ≠ focused node and are not descendants of focused node
- Child spring-out: `d3-force` re-runs at alpha 0.6 with newly-revealed children; `forceConfig.link.distance` temporarily bumps to 90 for one settle cycle so children land further out, then relaxes back to 60
- Esc / back: `FOCUS(null)` returns to brain view; reverse tween

**Patterns to follow:**

- Phase 1 atlas zoom — `cameraPosition` tween pattern
- Phase 5.1 P3 cursor reduced-motion — gate the tween behind `useReducedMotion()` (reduced: instant cut)

**Test scenarios:**

- Click Product → camera moves; siblings dim to ~0.18 opacity within 250ms
- Click folder → camera moves closer; folder's flows visible
- Esc → return to brain view; previous focus state cleared
- Click flow leaf during product zoom → U12 morph fires (does not zoom further)
- Reduced-motion: instant focus, no camera tween, no stagger

**Verification:** Playwright e2e: click product, assert camera position changed + sibling opacity reduced.

---

### U11 — Frontend: click-and-hold signal animation

**Goal:** mousedown on any node triggers the signal animation (particle convergence + label glow + edge pulse); mouseup releases. **No camera move, no expansion. NOT a zoom.**

**Files:**

- Create: `app/atlas/SignalAnimationLayer.tsx`
- Create: `app/atlas/useSignalHold.ts`
- Create: `app/atlas/particles.ts` — pre-allocated InstancedMesh particle pool (80 particles)
- Create: `app/atlas/edgePulseShader.ts` — minimal vertex+fragment shader with shared `time` uniform
- Modify: `app/atlas/BrainGraph.tsx` — wire `onNodePointerDown` + `onNodePointerUp`

**Approach:**

- `useSignalHold()`: state machine `{ heldNodeId: string | null }`; transitions `HOLD_START(id)` and `HOLD_END`
- `SignalAnimationLayer`:
  - When `heldNodeId` is set:
    - Pre-allocated `InstancedMesh` of 80 particles centered at held node's position; per frame, each particle's position lerps toward node by `0.08 * elapsed` then is reset when `distance < 0.5` (gives the "rain in" effect)
    - Held node's HTML label gets class `data-signal-held=true`; CSS rule animates `--mind-graph-label-glow: 0 → 12px` over 200ms ease-out
    - Edge pulse shader uniform: `time += deltaTime`; fragment computes `alpha = base + 0.4 * sin(time * 6.28 * 1.0)` ONLY for edges where `source === heldNodeId || target === heldNodeId` — implemented via per-edge `attribute float isHeld` set on hold-start, cleared on hold-end
  - When `heldNodeId` is null:
    - particles tween scale → 0 over 250ms then disable
    - label glow tweens out over 200ms (CSS transition)
    - edge pulse stops on next frame (uniform unchanged but per-edge attribute is cleared)
- **Critically: no `useGraphView` dispatch on hold; no camera tween; no `FOCUS` action.** The animation is presentation-only, scoped to `SignalAnimationLayer`.
- Reduced-motion: hold-start sets a static glow (`--mind-graph-label-glow: 8px` instant) + a static incident-edge highlight (alpha 1.0 instead of pulsing); hold-end clears both. No particles, no shader pulse.

**Patterns to follow:**

- The user's clarification: "*a 3js default and easy behaviour*" — keep it stock r3f gestures, no custom physics
- Phase 5.1 P3 reduced-motion guard

**Test scenarios:**

- mousedown on a Product → particles spawn; glow appears; incident edges pulse
- Hold for 2s → animation loops smoothly (≤16ms/frame at p99)
- mouseup → particles decay; glow fades; pulse stops
- mouseup AFTER moving cursor off node → still releases (`onPointerLeave` also fires)
- Click-and-hold then drag-out then drag-back-in → second hold-start fires; first cleanly released
- Reduced-motion: static glow only; no particles
- **Camera never moves during hold** (regression test — the user was explicit)
- **No siblings dim during hold** (regression test — distinct from single-click)

**Verification:** Playwright e2e: hold, assert particle layer visible, assert camera position unchanged, assert sibling opacity unchanged.

---

### U12 — Frontend: shared-element morph leaf↔project view

**Goal:** click flow leaf → 600ms morph: leaf circle + label tween into project view's title bar; r3f scene fades; project view atlas fades in. Esc / back: reverse.

**Files:**

- Create: `app/atlas/LeafMorphHandoff.tsx`
- Modify: `app/atlas/BrainGraph.tsx` — `onNodeClick` flow case dispatches `MORPH_TO(flow_id)`
- Modify: `app/components/ProjectShell.tsx` (Phase 1) — title bar adds `layoutId="flow-{id}-label"` + circle adds `layoutId="flow-{id}-circle"`
- Create: `app/atlas/sceneFade.ts` — r3f scene opacity tween helper

**Approach:**

- Click flow leaf: convert leaf node's HTML label into a Framer Motion `motion.div` with `layoutId="flow-{flowId}-label"` (static-positioned at the leaf's screen coordinates); same for the circle
- `router.push(/projects/<slug>/<flow_id>)` — route mounts ProjectShell whose title-bar children carry the matching `layoutId`
- Framer Motion auto-detects matching `layoutId` across route boundary and tweens layout shift over 600ms (cubic-out)
- In parallel: `sceneFade.ts` runs a 300ms opacity-out on the r3f scene; project view's hero atlas runs a 300ms opacity-in starting at +100ms → total visual budget 600ms
- Reverse morph (Esc / back): `router.back()` → `/atlas` — mind graph re-mounts, re-runs settle (cached aggregate, fast); `LeafMorphHandoff` shows the leaf at its old position with an entry animation matched to the project-view title-bar exit

**Patterns to follow:**

- Phase 1 atlas → project view transition (already uses Framer Motion `layoutId` for title hand-off)
- R27: shared three.js + r3f pipeline, shared shader chain

**Test scenarios:**

- Click flow leaf → URL changes; title bar shows the same label as the leaf had; visual shift ~600ms
- Esc on project view → returns to `/atlas` with brain reconstituted around the formerly-clicked leaf
- Deep link `/atlas?focus=flow_abc` → on mount, leaf is pre-zoomed; clicking it triggers morph
- Reduced-motion: instant route swap, no morph; back is instant
- Mid-morph SSE event (decision created on the destination flow) → arrives after morph completes; project view shows fresh data

**Verification:** Playwright e2e: click leaf, assert URL change + title bar label + total morph duration ≤700ms.

---

### U13 — LOD / culling (resolves Origin Q6)

**Goal:** cap rendered node + edge count per zoom level so the 1000+ node graph stays smooth. Document the budget in `docs/runbooks/phase-6-mind-graph.md`.

**Files:**

- Modify: `app/atlas/cull.ts` — full LOD implementation
- Create: `docs/runbooks/phase-6-mind-graph.md`
- Modify: `app/atlas/useGraphView.ts` — derive `zoomLevel` from `cameraPosition.z` (brain / product / folder / flow)
- Create: `app/atlas/cull.test.ts` (Vitest) — unit tests

**Approach:**

- `zoomLevel` derivation: brain (z > 800) | product (400 < z ≤ 800) | folder (200 < z ≤ 400) | flow (z ≤ 200)
- Visible subset per level (per Key Decisions table above)
- Edge culling: at brain view, only `hierarchy` edges; at product+ levels, satellite edges if their filter chip is on
- Label LOD: at brain view, only product labels; at product zoom, folder + flow labels in focus subtree; at folder zoom, all labels in focus subtree; at flow zoom, full labels everywhere
- Memoize visible subset on `(zoomLevel, focusedNodeId, activeFilters)`; recompute only on change
- Document the budget table + tuning knobs in the runbook so future phases can adjust

**Test scenarios:**

- 1000-node fixture at brain view → ≤500 nodes rendered, ≤500 edges rendered
- Zoom into Product → visible count drops to ~250
- Zoom into Folder → ~120
- Zoom into Flow (pre-morph) → ~50
- Toggle Components ON at brain view → still capped at brain budget; satellites only appear at product zoom
- Frame time at brain view ≤16ms/frame at p99 (1000-node fixture)

**Verification:** Vitest unit tests + Playwright perf trace + runbook reviewed.

---

### U14 — Playwright e2e + perf assertions + closure runbook

**Goal:** ship the smoke test + perf assertions + the deploy runbook. Mark Phase 6 active.

**Files:**

- Create: `tests/atlas-mind-graph.spec.ts`
- Modify: `docs/runbooks/phase-6-mind-graph.md` — add deploy + monitor + culling-tuning sections
- Modify: `package.json` — add `test:atlas-mind-graph` script

**Approach:**

- Spec covers the 5 highest-risk paths:
  1. Mount + settle: graph appears, simulation settles within 2.5s on 1000-node fixture
  2. Hover signal card: hover Product, assert card content + edge-clamping
  3. Single-click Product zoom: camera position changes, siblings dim, children spring out
  4. **Click-and-hold signal animation:** hold node, assert particle layer visible, **assert camera position unchanged** (regression for the user's clarification), assert sibling opacity unchanged
  5. Leaf morph: click flow leaf, assert URL change + ≤700ms morph budget
- Perf assertion: chrome-trace evaluation script asserting frame time p99 ≤16ms during settle and during hold
- Runbook includes:
  - Deploy: `vercel deploy` + `services/ds-service` Cloudflare Tunnel restart procedure (no schema change → no migration)
  - Monitor: SSE channel subscriber count metric, aggregator cache-hit rate metric, frame-time p99 from RUM
  - Culling tuning: `forceConfig.ts` knobs to adjust if node count grows past 1500
  - Reduced-motion fallback verification checklist

**Test scenarios:** the 5 above, all green in CI.

**Verification:** `npm run test:atlas-mind-graph` passes locally + in CI; runbook reviewed against the Phase 5 closure pattern.

---

## Performance Budgets

| Metric | Budget | Measured at |
|---|---|---|
| `GET /v1/projects/graph` response (always-warm — single indexed SELECT) | ≤15ms p95 | U2 test |
| `RebuildGraphIndex` incremental flush (1 decision change) | ≤80ms p95 | U1 test |
| `RebuildGraphIndex` full rebuild (1500-row tenant slice, includes canonical-tree walk) | ≤5s p95 | U1 test |
| Cold-backfill command end-to-end (production tenant) | ≤8s p95 | U1 manual smoke |
| Aggregate JSON wire size (1500-node tenant) | ≤180KB compressed | U2 (production fixture) |
| TTI on `/atlas` (1000-node fixture) | ≤2.5s p75 | U14 perf assertion |
| d3-force-3d settle time | ≤2s on 1000-node fixture | U4 |
| Frame time during steady state | ≤16ms p99 (60fps) | U6, U13, U14 |
| Frame time during click-and-hold | ≤16ms p99 | U11 + U14 |
| Leaf-morph duration | ≤700ms | U12 + U14 |
| Single-click camera tween | 1000ms ± 50ms | U10 |
| Filter-chip toggle alpha re-settle | ≤500ms | U7 |
| SSE bust → rendered update (worker flush → frontend re-fetch) | ≤1.2s p95 | U2 + U4 |
| Bundle size delta | ≤180KB compressed (`react-force-graph-3d` + new code) | U14 |

---

## Risk Table

| Risk | Severity | Mitigation |
|---|---|---|
| `react-force-graph-3d` performance regression at >1000 nodes | High | U13 LOD + node budget table; perf assertion in U14; if budget breached at 1500 nodes we tune `forceConfig` charge/distance OR move to layered rendering |
| `RebuildGraphIndex` worker falls behind a burst of SSE events | Medium-High | 200ms debounce coalesces per-tuple jobs; worker pool size from `GRAPH_INDEX_REBUILD_WORKERS` env (default 1, tunable to 4); 1-hour ticker is the safety-net catch-up; subscriber sees stale `materialized_at` for at most one debounce window |
| `RebuildGraphIndex` panics on a malformed `canonical_tree` BLOB | Medium | `defer recover()` per worker goroutine + structured log; the failed flow gets `edges_uses_json='[]'` rather than blocking the whole tenant rebuild |
| Manifest mtime poll misses an extraction commit | Low | Cold start always runs a full rebuild; the 1-hour ticker re-checks; `migrate-graph-index --force` is the operator escape hatch |
| `prefers-reduced-motion` users get a degraded experience | Medium | U3 reduced-motion gate falls back to 2D HTML grid + dashboard link; documented in runbook |
| WebGL2 unavailable on Safari < 15 | Medium | Capability check in U3; 2D fallback empty state. Brainstorm dependency note explicitly carved this out |
| SSE channel subscriber leak | Medium | Reuse Phase 4.1 ticket-based pattern (proven); add subscriber count metric in U2 + U14 |
| SSE bust observed BEFORE the worker has finished writing | Medium | Channel subscribes to the worker's flush completion (not upstream events directly) — guaranteed read-after-write; covered by U2's deterministic `materialized_at` test |
| Click-and-hold accidentally triggers single-click on release | Medium | r3f's `onPointerUp` does NOT fire `onClick` if `onPointerDown` ran more than 250ms earlier — confirmed in r3f docs; U11 test covers this regression |
| Leaf morph layout shift janks on slow CPU | Medium | U12 budget is 700ms with 600ms target — 100ms slack for slow devices; reduced-motion path is instant |
| 80-particle InstancedMesh allocates each hold | Low | Pool is module-scoped, allocated once on mount; U11 test asserts no allocation during hold |
| Decision chain edge-update animation drifts when 5+ supersedes happen in <1s | Low | Worker debounces; the visible state always converges to the latest `graph_index` snapshot; per-event animation may stutter but final state is correct |
| Fontload jank on label render | Low | Labels reuse the existing `Inter Variable` font already loaded in the app shell — no new font load |
| Personas leaking across tenants in `graph_index` | Low | Persona source is org-wide (Phase 1 design) but materialiser writes per-`(tenant_id, platform)` rows; U1 test asserts no cross-tenant leakage when an org-wide persona is approved |

---

## Verification

Phase 6 ships when:

- All 14 implementation units have green CI
- `tests/atlas-mind-graph.spec.ts` passes against the 1000-node production-shaped fixture
- Perf budget table fully measured + documented in the runbook
- `/atlas` route is reachable from the global nav (header link added in U3) and from `/atlas/admin`
- Click-and-hold regression test (camera does not move, siblings do not dim) passes
- Reduced-motion fallback path verified in three browsers (Chrome, Safari, Firefox)
- WebGL2-unavailable fallback path verified
- Live SSE updates demoed: create a decision in DRD; mind graph node pulses + edge appears within 1.2s
- Leaf morph demoed: click leaf, lands on project view title-bar with matching layoutId
- Closure doc dropped at `docs/solutions/2026-05-XX-001-phase-6-closure.md` (date set at ship time) summarizing decisions actually made vs. plan, perf measured vs. budget, deferred items with rationale

---

## Sequencing Plan

```
              Backend                              Frontend
  ┌────────────────────────────┐    ┌─────────────────────────────────────┐
  │  U1 Migration 0009 +        │    │                                     │
  │     RebuildGraphIndex       │    │  U3 Route scaffold + reduced-motion │
  │     worker + cold backfill  │    │     gate + EmptyState (Phase 3)     │
  │  U2 Handler (single SELECT) │    │                                     │
  │     + SSE flush channel     │    │                                     │
  └──────────────┬──────────────┘    └──────────────┬──────────────────────┘
                 │                                  │
                 └──────────────┬───────────────────┘
                                │
                                ▼
                  U4 BrainGraph mount + fetch
                                │
                  ┌─────────────┼─────────────┐
                  ▼             ▼             ▼
        U5 brain styling   U6 bloom     (parallel)
                  │             │
                  └──────┬──────┘
                         ▼
                  U7 Filter chips
                         │
                         ▼
                  U8 Mobile↔Web toggle
                         │
                         ▼
                  U9 Hover signal cards
                         │
                  ┌──────┴──────┐
                  ▼             ▼
             U10 single-    U11 click-and-hold
             click zoom     signal animation
                  │             │
                  └──────┬──────┘
                         ▼
                 U12 Leaf morph
                         │
                         ▼
                 U13 LOD / culling
                         │
                         ▼
                 U14 Playwright + runbook + closure
```

**Parallel-safe units:** U5 + U6 (different files within `BrainGraph.tsx`, but only one orchestrator can touch the file — serialize within a single subagent).

**Estimated calendar:** 4–5 weeks per Phase 1 roadmap, dominated by U10 + U11 + U12 (interaction polish).

---

## Open Questions Deferred to Implementation

1. **Labels on hidden nodes:** when a Product is zoomed into and its 8 siblings recede, do the sibling labels disappear or merely dim? Currently U10 dims to 0.18 opacity. If usability testing surfaces complaints, we may hide them entirely past a threshold.
2. **Edge bundling for high-degree nodes:** if a single component is `used` by 50+ flows, the `uses` edges may visually clutter. Consider edge bundling (curved aggregation) at high zoom levels — punted to Phase 7 if observed in production.
3. **Persona node placement:** personas are listed in the data model but not in any zoom level above. Are personas presented as a separate filter chip, or as nested under Products? Current plan assumes filter-chip toggle (similar to Tokens). Confirm during U7 design review.
4. **Long-term memory of last viewed:** should `/atlas` remember the user's last focused node + filter state across sessions? Phase 7 if requested; v1 lands at brain view every time.
5. **Mobile (touch) gesture parity:** click-and-hold maps to `touchstart`/`touchend` cleanly; pinch-zoom maps to camera tween. Verify in U14 against an iPad simulator; punt full mobile polish to Phase 7 if any gesture is awkward.

---

## Phase 1-5 Conventions Inherited

These are the constraints Phase 6 honours without re-litigating, lifted from the five completed phases' plans + closures:

- **Tenant scoping by denormalised `tenant_id` + `TenantRepo` discipline.** `graph_index.tenant_id` is denormalised; every read is constructed via `NewTenantRepo(db, tenantID)`. Personas are the documented exception (org-wide library, no `tenant_id` in the source `personas` table — Phase 1 learnings line 18-28). The materialiser duplicates persona source rows per `(tenant, platform)` for PK alignment.
- **Migration discipline.** Migration `0009` is sequential after Phase 5's `0008`; `CREATE TABLE IF NOT EXISTS`; never DROP COLUMN within the release that stops writing it (Phase 1 learnings line 30-44).
- **r3f + Next 16 Suspense remount-on-pathname.** The `<Canvas>` for the mind graph is wrapped in `<Suspense key={pathname}>` to sidestep `pmndrs/react-three-fiber#3595` (Phase 1 learnings line 46-54; live in `components/projects/atlas/AtlasCanvas.tsx`).
- **SSE single-use ticket auth.** Phase 6's `graph:<tenant>:<platform>` channel reuses the Phase 4.1 ticket pattern verbatim — never JWT in query string (Phase 1 learnings line 56-63).
- **EmptyState primitive.** U3 uses the existing variant library (`welcome | loading | audit-running | error | permission-denied | re-export-needed`) shipped in Phase 3 — no new primitive (Phase 1 learnings line 66-73).
- **Worker pool size from env on day one.** `GRAPH_INDEX_REBUILD_WORKERS` env (default 1) — Phase 1 learnings line 74-80 made this non-negotiable after the audit-pool refactor.
- **Animation-timeline clustering.** Phase 6 timelines live under `lib/animations/timelines/atlas/` so Phase 1+3+6 surfaces don't mix (Phase 1 learnings line 89-95).
- **Reduced-motion via `useReducedMotion()` from `lib/animations/context.ts`.** Phase 1's hook, extended by Phase 5.1 P3 cursor presence; Phase 6 uses the same export.
- **Severity rollup uses `status='active'` + latest version per project.** Phase 4 lifecycle: `acknowledged | dismissed | fixed` are excluded from the hover card severity bars; carry-forward dismissals don't count as active.
- **Decision chain semantics.** Phase 5's `decisions.supersedes_id` (forward) + `superseded_by_id` (back) + `ProjectDecisionChanged.previous_status` + `previous_superseded_by_id` (Phase 5.3 P1 chain delta) are the authoritative bridge; the materialiser reads `supersedes_id` only — the chain-delta fields are consumed by the SSE-driven incremental rebuild path.

---

## References

- Brainstorm: `docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md` — F8, R20, R22, R23, R24, R25, R27, AE-5, AE-8, Origin Q6
- Phase 1 plan: `docs/plans/2026-04-29-001-feat-projects-flow-atlas-phase-1-plan.md` — schema (migration `0001`), atlas canvas, useAtlasViewport zoom-persist pattern
- Phase 1 learnings: `docs/solutions/2026-04-30-001-projects-phase-1-learnings.md` — the 10 conventions Phase 6 inherits above
- Phase 2 plan: `docs/plans/2026-04-30-001-feat-projects-flow-atlas-phase-2-plan.md` — audit rule catalog (migration `0002`/`0003`), severity P1/P2/P3 → 5-tier mapping
- Phase 2 learnings: `docs/solutions/2026-04-30-002-projects-phase-2-rules-learnings.md`
- Phase 3 plan: `docs/plans/2026-05-01-001-feat-projects-flow-atlas-phase-3-plan.md` — atlas EffectComposer chain (`atlasBloomBuildUp`, `projectShellOpen`, `themeToggle`, `tabSwitch` timelines); EmptyState variants
- Phase 4 plan: `docs/plans/2026-05-01-002-feat-projects-flow-atlas-phase-4-plan.md` — violation lifecycle (migration `0006`), inbox + dashboard, `inbox:<tenant>` SSE channel pattern
- Phase 5 plan: `docs/plans/2026-05-02-001-feat-projects-flow-atlas-phase-5-plan.md` — `decisions`, `decision_links`, `drd_comments`, `notifications`, `notification_preferences` (migration `0008`); `ProjectDecisionChanged` chain delta
- Phase 5.x closures: `docs/solutions/2026-05-02-001-phase-5-collab-learnings.md`, `2026-05-02-002-phase-5-1-collab-polish.md`, `2026-05-02-003-phase-5-2-collab-polish.md`
- Phase 5.3 commit `941464d` — chain-delta SSE shape that Phase 6 depends on
- Deep component extraction: `docs/plans/2026-04-28-002-deep-component-extraction-plan.md` — the manifest.json shape (rich extraction with `bound_variable_id`, `composition_refs`) that the `RebuildGraphIndex` worker parses for `binds_to` + component composition

### Sources of truth for the materialiser (read-only inputs)

- `services/ds-service/internal/db/db.go` — `tenants`, `users`, `tenant_users` tables (org-wide identity)
- `services/ds-service/migrations/0001_projects_schema.up.sql` — `projects`, `flows`, `personas`, `project_versions`, `screens`, `screen_canonical_trees`, `screen_modes`, `audit_jobs`, `violations`, `flow_drd`
- `services/ds-service/migrations/0006_dismissed_carry_forwards.up.sql` — `dismissed_carry_forwards` (Phase 4 carry-forward semantics; consulted by severity rollup)
- `services/ds-service/migrations/0008_decisions_comments_notifications.up.sql` — `decisions`, `decision_links`, `drd_comments`, `notifications`
- `public/icons/glyph/manifest.json` — Figma-extracted component catalog (89 components + variants + `bound_variable_id` + composition refs)
- `lib/tokens/indmoney/{base,semantic,semantic-dark}.json` — DTCG token catalog (`$extensions.figma.variable_id` is the join key against the manifest)

### External libraries

- [`react-force-graph-3d`](https://github.com/vasturiano/react-force-graph) — primary library
- [Framer Motion `layoutId` shared elements](https://www.framer.com/motion/layout-animations/) — leaf morph
- [r3f gesture handling](https://docs.pmnd.rs/react-three-fiber/api/events) — for click-and-hold "3js default" semantics
