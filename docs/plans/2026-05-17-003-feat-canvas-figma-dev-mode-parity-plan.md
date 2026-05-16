---
title: "feat: Canvas Figma-Dev-Mode parity for leaf canvas"
status: active
created: 2026-05-17
type: feat
depth: deep
brainstorm: docs/brainstorms/2026-05-17-canvas-figma-dev-mode-parity-requirements.md
ideation: docs/ideation/2026-05-17-canvas-figma-dev-mode-parity-ideation.md
---

# feat: Canvas Figma-Dev-Mode parity for leaf canvas

**Target repo:** indmoney-design-system-docs (this repo)
**Branch:** `main` (works alongside the in-flight MCP server plan — see Cross-Stream Coordination)

---

## Summary

Bring Figma-Dev-Mode-grade interaction to the single-screen viewer (leaf canvas): click selects the deepest frame, hover outlines that frame *and* illuminates the containing autolayout's padding/gap, the camera runs on a critically-damped spring with the full Figma hotkey set, illustrations and icons render as inline `<svg>` (with silent PNG fallback), and a screen-space chrome layer outside the world-transform keeps selection rings, breadcrumb, and distance lines crisp at any zoom. The existing `AtomicChildInspector` drawer gains Figma-Dev-Mode Layout / Typography / Fills sections driven by the current selection, with a Dev Mode flag (Shift+D) toggling canvas annotations.

The plan covers the full brainstorm — no narrowing, no slicing. Six survivors plus the inspect-panel extension land together across four phases.

---

## Problem Frame

PMs, designers, and developers reading design-system documentation flip between this canvas and Figma constantly to verify what they're looking at. The renderer reached near-pixel-perfect fidelity after two audit rounds in May 2026 (13 bug-class fixes), but the *interaction layer* is the inverse of Figma:

- Click selects the deepest atom under the cursor (`LeafFrameRenderer.tsx:151-178`), so clicking a button's label glyph selects the glyph, not the button. The first click of every session feels wrong.
- Hover paints only on the deepest atom (`hover-signal.ts`). Hovering across a row of stat tiles lights up three text-node halos instead of revealing the layout structure.
- Camera is functional but flat — linear `easeInOutCubic` 320ms snap (`camera-snap.ts:76-82`), no spring on wheel/pinch, bounds clipped at `[0.18, 2.0]`, and every Figma hotkey is missing.
- Illustrations and icons fly over the wire as `<img src=…?format=svg>` (`useIconClusterURLs.ts:212`) even though the pipeline already exports SVG bytes (`pipeline.go` Stage 9 `renderSVGClustersForVersion`); the browser re-rasterizes them and we lose all DOM access.
- `MeasurementOverlay.tsx` lives inside `worldRef` so chips scale with zoom — 2px rings become 16px bars at 8×, vanish near minimum zoom.

The cost is concrete: every inspection task involves at least one Figma round-trip to verify structure, spacing, or frame name. That round-trip is the moment the team mentally downgrades the canvas from "the place to read about the design system" to "a faster preview of Figma."

---

## Cross-Stream Coordination (MCP Server Parallel Work)

A second plan, `docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md`, is being implemented on `main` in parallel. That work touches the ds-service backend (new `internal/mcp/` dir, new `subflow.go` / `prd.go` / `figma_role.go`, modifies `drd_collab.go` and `repository_figma_autosync.go`, migrations `0036`/`0037`/`0038`) and adds `app/projects/[id]/prd/*` routes on the frontend. Two coordination points:

1. **Shared file: `services/ds-service/internal/projects/pipeline.go`.** MCP U2 wires section→sub_flow upsert into the autosync path; our U8 adds a post-Stage 9 SVG-inlining mutation pass in the goroutine block at `pipeline.go:726-854`. **Different code paths in the same file** — merge conflicts are manageable but only with small commits and frequent rebases. The implementer should pull from `main` before each commit on U8.
2. **Shared doctrine: "designer name is canonical."** MCP KTD-4 ("designer's naming is canonical, server does not filter / infer / guess") and our R6 ("designer name is canonical, structural heuristics fall back") reinforce each other. When U8 adds a name-aware eligibility short-circuit in `pipeline_cluster_prerender.go`, mirror MCP's vocabulary so the codebase has one shared rule, not two parallel ones.

No other files overlap. The canvas plan does not touch `app/projects/`, `internal/mcp/`, or any of the new MCP migrations.

---

## Requirements Traceability

All requirements carry forward verbatim from the brainstorm doc (`docs/brainstorms/2026-05-17-canvas-figma-dev-mode-parity-requirements.md`). The plan addresses every R-ID:

- **Asset routing** R1-R6 → U7, U8
- **Selection model** R7-R12 → U4
- **Hover & inspection feedback** R13-R18 → U5, U6
- **Camera mechanics** R19-R23 → U2, U3
- **Chrome layer & overlay architecture** R24-R27 → U1
- **Dev Mode** R28-R31 → U9, U10
- **Actors A1-A3** (design-system reader, designer/asset author, pipeline operator) → preserved through U4-U10 affecting A1 and U8 affecting A2/A3
- **Flows F1-F4** (inspect a frame, inspect illustration up close, measure distance, navigate by name) → covered by the test scenarios in U4-U10
- **Acceptance Examples AE1-AE8** → mapped per-unit via `Covers AE<N>.` prefixes in test scenarios

---

## Key Technical Decisions

- **KTD-1. Chrome layer substrate: a single screen-space SVG layer mounted as sibling of `worldRef`** in `leafcanvas.tsx`. Inside it, pre-allocated `<rect>` / `<path>` / `<text>` elements addressed by ref are mutated for selection, hover, padding bands, gap fills, distance lines, marquee, breadcrumb chip, and dimension labels. *Rationale:* SVG handles screen-space geometry natively, supports gradients/strokes/text without extra plumbing, and the attribute-mutation hot path is well-understood. Alternative considered — positioned `<div>` elements with CSS borders — works for ring-only chrome but doesn't scale to distance lines, padding bands, and text labels without re-inventing what SVG already provides. (Per brainstorm "Deferred to Planning" Q on chrome substrate.)

- **KTD-2. nodeId → worldRect spatial store as a module-level singleton in `leafcanvas-v2/spatial-store.ts`**, populated by the canonical_tree loader, invalidated only on tree mutation. Reads via `getRect(nodeId)` and a subscription API. *Rationale:* mirrors the existing pattern of module-level singletons (`gesture-tracker`, `leaf-zoom-signal`, `hover-signal`) used to bypass React reconciliation at 60Hz. Zustand for this would force re-renders on every camera tick.

- **KTD-3. Critically-damped spring integrator hand-rolled in `leafcanvas-v2/spring.ts`**, ~40 lines of math (semi-implicit Euler, fixed-step at rAF cadence with sub-stepping for stability). Used by `camera-snap.ts` as a drop-in replacement for `easeInOutCubic`. *Rationale:* keeps the existing RAF loop pattern; avoids pulling in `@react-spring/web` (`@react-spring/three` already present is 3D-only) or coupling to Framer Motion's React-fiber-bound spring. Spring parameters (stiffness, damping) tunable via constants for side-by-side comparison with Figma.

- **KTD-4. Centralized keymap module in `leafcanvas-v2/keymap.ts`** with an action table and a single `window.addEventListener("keydown", …)` registered from `AtlasShellInner.tsx`. *Rationale:* today's hotkey handlers are scattered across 6+ files (AtlasShellInner, leafcanvas, atlas, InlineTextEditor, SearchInput, Shell). Adding 15+ Figma hotkeys without consolidation would compound the sprawl. The keymap module exposes named actions (`canvas.fit-all`, `canvas.next-named-frame`, `selection.descend`, …) and a dispatcher that gates on canvas focus.

- **KTD-5. Click semantics flip to deepest FRAME/COMPONENT/INSTANCE/GROUP** — the inverse of Figma's strict "outermost frame on page" rule. (Per brainstorm Key Decision: better fit for a docs viewer; the user signed off on this in the brainstorm.) Cmd+click bypasses to atomic level. `findAtomicTarget` (`LeafFrameRenderer.tsx:1160-1180`) and `ATOMIC_TYPES` (`:66-74`) are the single seam — a new walker `findFrameTarget` is added alongside, and `handleClick` dispatches by modifier.

- **KTD-6. Server-side SVG inlining lands as a post-Stage 9 mutation pass on `screen_canonical_trees`**, not a rewrite of `extractCanonicalTree`. *Rationale:* `extractCanonicalTree` runs in Stage 2/3 before any SVG export has happened. Stage 9 already writes SVG bytes to disk via `RenderAssetsForLeaf` → `persistAssetBytes`. The cheapest seam is at `pipeline.go:810` (immediately after `renderSVGClustersForVersion` returns): load each affected screen's canonical_tree, walk to each `svgID`, splice `svg_markup` strings, recompress (zstd L19, 64MB cap is acknowledged), `UPDATE screen_canonical_trees`. Avoids hoisting eligibility to Stage 2 and avoids a schema migration.

- **KTD-7. Extend `AtomicChildInspector`, not replace it.** *Rationale:* the brainstorm called for "a right-side inspect panel showing layout/typography/fills." Research found `AtomicChildInspector.tsx` already mounted in `AtlasShellInner.tsx:395` with resize plumbing (`:327`). Replacement would force a follow-up to relocate the existing DRD/Violations/Decisions/Copy/Activity/Comments tabs. Adding Layout/Typography/Fills sections to the top of the same drawer preserves the current authoring affordances. (Plan-time scoping decision; surfaced to user at synthesis and confirmed.)

- **KTD-8. PNG fallback is silent and retains the pyramid for named frames.** *Rationale:* per brainstorm Key Decision — visibly broken named-frame illustrations damage user trust more than the marginal storage/API savings of dropping the pyramid would deliver. `useIconClusterURLs` short-circuits ONLY when `svg_markup` is present on a node; otherwise the existing PNG path runs unchanged.

- **KTD-9. Marquee/lasso continues to walk the canonical tree directly** (no R-tree at this scale). Today's lasso scans atomic nodes per-frame; widening it to FRAME/COMPONENT/INSTANCE/GROUP doesn't change the algorithmic complexity. Revisit if leaf-canvas frame counts approach 5,000+. (Per brainstorm Scope Boundaries.)

---

## High-Level Technical Design

The architecture splits into three coordination layers. The diagram below shows how state flows between them at runtime.

```
                        ┌──────────────────────────────┐
                        │  React tree (leafcanvas.tsx) │
                        │  - LeafFrameRenderer per     │
                        │    frame                     │
                        │  - AtomicChildInspector      │
                        │    (right drawer, extended)  │
                        └──────────────┬───────────────┘
                                       │ reads (no writes during animation)
                                       ▼
┌─────────────────────────────────────────────────────────────────────┐
│  Module singletons (the hot-path state layer — bypass React)        │
│                                                                      │
│  camera-state.ts        ──→  {x, y, z, target, springState}         │
│  spatial-store.ts       ──→  Map<nodeId, worldRect> + subscribe()   │
│  hover-signal.ts        ──→  {screenID, figmaNodeID, ancestorChain} │
│  selection-state.ts     ──→  {primary, bulk, modifier} (Zustand)    │
│  keymap.ts              ──→  action table + dispatcher              │
│  spring.ts              ──→  critically-damped integrator           │
│  dev-mode-state.ts      ──→  {on: boolean}                          │
└─────────────────────────────────────────────────────────────────────┘
                                       │ reads on every rAF tick
                                       ▼
                ┌──────────────────────────────────────────┐
                │  Chrome layer (screen-space SVG sibling) │
                │  - selection rings, hover outlines       │
                │  - padding bands, gap fills              │
                │  - distance lines, dimension chips       │
                │  - marquee rect, breadcrumb chip         │
                │                                          │
                │  Single rAF callback reads               │
                │  (camera, spatial-store, hover, sel)     │
                │  → mutates SVG element attrs via refs    │
                │  → zero React renders                    │
                └──────────────────────────────────────────┘
```

*This diagram is directional guidance for review, not implementation specification. Each box is a unit's territory; arrows are read/write relationships.*

### Data shape: `svg_markup` field on `CanonicalNode`

```ts
// app/atlas/_lib/leafcanvas-v2/types.ts — additive change
interface CanonicalNode {
  id: string;
  name: string;
  type: "FRAME" | "TEXT" | "VECTOR" | ...;
  // ... existing fields ...
  svg_markup?: string;  // NEW — populated by server post-Stage 9 for named illustration/* and icon/* frames
}
```

When `svg_markup` is present, `nodeToHTML.ts` emits `<div dangerouslySetInnerHTML={{__html: svg_markup}} />` and skips the cluster-URL path entirely. When absent, the existing `<img>` cluster path runs unchanged.

### Spring integrator contract

```ts
// app/atlas/_lib/leafcanvas-v2/spring.ts — directional sketch
interface SpringState { value: number; velocity: number; }
function springStep(s: SpringState, target: number, stiffness: number, damping: number, dt: number): SpringState
// Critically-damped: damping = 2 * sqrt(stiffness * mass), mass = 1
// Tuning starts at stiffness=180, damping=26; iterate against side-by-side Figma reference.
```

Caller (`camera-snap.ts`) holds three independent `SpringState` for x/y/z and advances them per rAF tick until all three reach `|value - target| < epsilon AND |velocity| < epsilon`.

---

## Output Structure

```
app/atlas/_lib/
  leafcanvas.tsx                     # MODIFIED — chrome-layer mount, keymap registration
  leafcanvas-v2/
    AtomicChildInspector.tsx         # MODIFIED — extend with Layout/Typography/Fills sections
    LeafFrameRenderer.tsx            # MODIFIED — selection semantics, hover composition, marquee widening
    MeasurementOverlay.tsx           # DEPRECATED — chrome layer absorbs its responsibilities
    camera-snap.ts                   # MODIFIED — easeInOutCubic → spring
    hover-signal.ts                  # MODIFIED — ancestor-chain composition
    nodeToHTML.ts                    # MODIFIED — svg_markup branch in renderClusterPlaceholder
    useIconClusterURLs.ts            # MODIFIED — skip clusters with svg_markup
    types.ts                         # MODIFIED — add svg_markup?: string to CanonicalNode
    canvas-v2.css                    # MODIFIED — Figma-blue selection/hover colors, magenta padding, blue gap
    spring.ts                        # NEW — critically-damped spring integrator
    camera-state.ts                  # NEW — module-level camera signal (read by chrome layer)
    spatial-store.ts                 # NEW — nodeId → worldRect derived store
    chrome-layer.tsx                 # NEW — screen-space SVG overlay component
    keymap.ts                        # NEW — central action table + dispatcher
    dev-mode-state.ts                # NEW — Dev Mode global flag signal
    inspector-property-groups.tsx    # NEW — Layout/Typography/Fills section components for AtomicChildInspector
    breadcrumb.tsx                   # NEW — breadcrumb chip rendered in chrome layer
    selection-state.ts               # NEW — selection-store wrapper (single + bulk + modifier-aware)
    __tests__/
      spring.vitest.ts               # NEW
      spatial-store.vitest.ts        # NEW
      chrome-layer.vitest.tsx        # NEW
      keymap.vitest.ts               # NEW
      dev-mode-state.vitest.ts       # NEW
      camera-snap-spring.vitest.ts   # NEW (or extend existing camera-snap.vitest.ts)
      selection-semantics.vitest.ts  # NEW
      composed-hover.vitest.ts       # NEW
      marquee-frames.vitest.ts       # NEW
      breadcrumb.vitest.ts           # NEW
      svg-markup-render.vitest.ts    # NEW
      inspector-property-groups.vitest.tsx  # NEW
      distance-lines.vitest.ts       # NEW
  AtlasShellInner.tsx                # MODIFIED — keymap registration; Esc layered close extension

services/ds-service/internal/projects/
  pipeline.go                        # MODIFIED — Stage 9 invokes post-render SVG inlining
  pipeline_cluster_prerender.go      # MODIFIED — name-aware eligibility short-circuit (mirrors KTD-4 doctrine with MCP plan)
  svg_inliner.go                     # NEW — load canonical_tree, splice svg_markup, recompress, UPDATE
  svg_inliner_test.go                # NEW
  canonical_tree.go                  # MODIFIED — helper to walk-and-mutate tree blob
  canonical_tree_test.go             # MODIFIED — add svg_markup walk-and-mutate test

tests/
  canvas-figma-parity.spec.ts        # NEW — Playwright end-to-end for the AE1-AE8 scenarios
```

This tree is a scope declaration. Per-unit `**Files:**` lists are authoritative; the implementer may adjust if implementation reveals a better layout.

---

## Implementation Units

### U1. Spatial store + screen-space chrome layer foundation

**Goal:** Land the architectural primitives every other client-side unit depends on: a `nodeId → worldRect` derived store, a screen-space SVG chrome layer mounted as sibling of `worldRef`, and a module-level camera signal the chrome layer can read on every rAF tick.

**Requirements:** R24, R25, R26, R27

**Dependencies:** none (foundation)

**Files:**
- `app/atlas/_lib/leafcanvas-v2/spatial-store.ts` (NEW)
- `app/atlas/_lib/leafcanvas-v2/camera-state.ts` (NEW)
- `app/atlas/_lib/leafcanvas-v2/chrome-layer.tsx` (NEW)
- `app/atlas/_lib/leafcanvas.tsx` (MODIFIED — mount chrome layer; expose camera state)
- `app/atlas/_lib/leafcanvas-v2/canvas-v2.css` (MODIFIED — Figma blue `#0d99ff` for selection/hover)
- `app/atlas/_lib/leafcanvas-v2/__tests__/spatial-store.vitest.ts` (NEW)
- `app/atlas/_lib/leafcanvas-v2/__tests__/chrome-layer.vitest.tsx` (NEW)

**Approach:**
- `spatial-store.ts` exposes `setNodeRect(screenID, nodeID, rect)`, `getNodeRect(screenID, nodeID)`, `subscribe(listener)`, and an `invalidate(screenID)` for tree-mutation flushes. Populated by `LeafFrameRenderer` after canonical-tree load (per-screen). Module-singleton with HMR guard via `globalThis.__lcSpatialStoreWired`.
- `camera-state.ts` lifts the per-leaf `camRef` (`leafcanvas.tsx:158`) into a module signal `setCamera(leafID, {x, y, z})` / `getCamera(leafID)` / `subscribe(leafID, listener)` so the chrome layer (mounted at leafcanvas.tsx scope) can read without prop drilling.
- `chrome-layer.tsx` mounts as sibling of `.lc-world` in `leafcanvas.tsx` (around `:523-527`). Internal structure: one `<svg width="100%" height="100%">` with pre-allocated `<g>` groups for `selection`, `hover`, `padding`, `gap`, `distance`, `marquee`, `breadcrumb`, `dimension`. Each group's children are mutated by ref-driven imperatives (no React render in the rAF tick).
- Chrome layer subscribes to `camera-state`, `spatial-store`, `hover-signal`, `selection-state` (added in U4) and runs ONE rAF callback that reads all four, computes screen-rects via `worldRect → screenRect = {x: (rect.x - cam.x) * cam.z, y: (rect.y - cam.y) * cam.z, w: rect.w * cam.z, h: rect.h * cam.z}`, and mutates element attrs.
- CSS adds Figma-Dev-Mode colors as CSS vars: `--canvas-selection: #0d99ff; --canvas-hover: #0d99ff; --canvas-padding: #ff00ff; --canvas-gap: #0d99ff;`. Selection ring opacity 1.0; hover opacity 0.6 so selected-and-hovered reads as selected first.

**Patterns to follow:**
- Module-singleton pub/sub: `app/atlas/_lib/leafcanvas-v2/hover-signal.ts` (same shape).
- HMR guard: `globalThis.__lcXxxWired` pattern already in `hover-signal.ts` and `gesture-tracker.ts`.
- rAF-driven imperative DOM: `applyCameraToDOM` at `leafcanvas.tsx:167`.

**Test scenarios:**
- `spatial-store` returns `undefined` for unknown nodeID; returns last-set rect after `setNodeRect`.
- `spatial-store` invalidation on `invalidate(screenID)` clears only the named screen's entries, not others.
- `spatial-store` subscriber receives notifications on `setNodeRect` and `invalidate`.
- `camera-state.subscribe(leafID, …)` fires on every `setCamera(leafID, …)`; does not fire for a different `leafID`.
- `chrome-layer` mounts with the correct number of pre-allocated `<g>` groups; their `id` attributes match the contract.
- Given a leaf with two frames at world coords {0,0,100,100} and {200,0,100,100} and camera {x:0, y:0, z:2}, the chrome-layer rAF callback writes screen-rect `{0,0,200,200}` and `{400,0,200,200}` to the selection group's `<rect>` elements when those frames are selected.
- Chrome-layer paints at 2px stroke regardless of camera z (z=0.2 and z=8 both produce 2px).
- During a 1-second simulated camera animation, the chrome layer's React component does not re-render (assert via render-counter ref); only its inner SVG element attrs mutate.

**Verification:** Chrome layer is visible in DOM as a sibling of `.lc-world`; pre-allocated groups exist with correct IDs; spatial-store is populated when a leaf canonical tree loads; camera-state mutations propagate to the chrome layer's rAF callback.

---

### U2. Spring camera integrator replacing `easeInOutCubic`

**Goal:** Replace the linear ease-in-out-cubic snap at `camera-snap.ts:140-192` with a critically-damped spring integrator on x/y/z. All transitions — wheel/pinch zoom, snap-to-fit, fly-to-selection — feed the same spring (no mixed easing models).

**Requirements:** R19, R20, R21

**Dependencies:** U1 (camera-state singleton)

**Files:**
- `app/atlas/_lib/leafcanvas-v2/spring.ts` (NEW)
- `app/atlas/_lib/leafcanvas-v2/camera-snap.ts` (MODIFIED — `easeInOutCubic` removed; `animateCamera` uses spring)
- `app/atlas/_lib/leafcanvas.tsx` (MODIFIED — wheel/pinch deltas feed the spring instead of direct write)
- `app/atlas/_lib/leafcanvas-v2/__tests__/spring.vitest.ts` (NEW)
- `app/atlas/_lib/leafcanvas-v2/__tests__/camera-snap-spring.vitest.ts` (NEW; supersedes the existing easing test if present)

**Approach:**
- `spring.ts` exports `springStep(state, target, stiffness, damping, dt)` returning a new `SpringState`. Semi-implicit Euler; fixed-step at 1/60s with sub-stepping if the rAF dt exceeds 32ms (frame drop safety). Critically-damped: stiffness=180, damping=26 (initial; tunable).
- `animateCamera` (`camera-snap.ts:140-192`) holds three `SpringState` for x/y/z. Each rAF tick: step each axis; if all three reach equilibrium (within 0.1px and 0.001px/s velocity), terminate. Otherwise call `setCamera(leafID, {x: sx.value, y: sy.value, z: sz.value})` and continue.
- `leafcanvas.tsx:319-353` (pointer drag) and `:362-422` (wheel/pinch) currently mutate `camRef` directly. Switch to setting the spring `target` instead. Direct-write fast path retained for raw pan-drag (no spring needed mid-gesture); pinch/wheel zoom *target* updates while gesture is active, spring catches up after gesture-end via `canvasGestureTracker` settle signal.
- Zoom ceiling raised to 32×, floor recomputed per-leaf as `fit-all with 5% padding` (R22).

**Patterns to follow:**
- Existing `animateCamera` API: `animateCamera(from, to, onTick, onDone)` (`camera-snap.ts:140-192`). Spring rewrite preserves the same surface so callers don't change.
- rAF cancellation: existing `snapAnimRef` cancellation pattern at `leafcanvas.tsx:321-323`.

**Test scenarios:**
- Covers AE5. `spring.springStep` with stiffness=180, damping=26, from {value:0, velocity:0}, target=100, dt=1/60: after ~250-300ms simulated time, value within 1% of target with velocity near 0; no overshoot for critically-damped configuration.
- Spring respects sub-stepping when dt > 32ms (simulate a 50ms frame; output still stable, no oscillation).
- `animateCamera` with spring terminates on equilibrium; does not loop forever.
- `animateCamera` is cancellable mid-flight (assert that calling cancellation halts further `setCamera` writes).
- Wheel-zoom feeds the spring's target, not the camera state directly; mid-gesture, the camera value lags slightly behind the target (visible spring catch-up).
- Covers AE5. During a Shift+1 fit-all simulated flight, the chrome layer's render-counter does not increment.
- Covers AE6. With the zoom ceiling raised to 32× and an inline-SVG illustration present, simulated pinch-zoom to 8×, 16×, and 32× keeps the illustration crisp (vector content has no LOD ceiling). PNG-pyramid fallbacks remain crisp up to their 2048-tier bound. Verified end-to-end in `tests/canvas-figma-parity.spec.ts`.

**Verification:** Replace the easeInOutCubic snap and pinch-zoom feel against a side-by-side Figma reference (manual check); existing `camera-snap.vitest.ts` passes with the new integrator; spring params are constants in one place so they're tunable.

---

### U3. Centralized keymap module + Figma hotkey set

**Goal:** Introduce one keymap module (`leafcanvas-v2/keymap.ts`) with an action table and a single keydown listener registered from `AtlasShellInner.tsx`. Implement the full Figma hotkey set scoped to canvas focus.

**Requirements:** R23 (full hotkey list)

**Dependencies:** U1 (chrome layer for Z-drag rectangle, Space/H transient modes). U2 is recommended-before but not strictly required — keymap handlers can be authored against the existing camera API surface, and the spring integrator from U2 takes over the same call sites without changing the keymap.

**Files:**
- `app/atlas/_lib/leafcanvas-v2/keymap.ts` (NEW)
- `app/atlas/_lib/AtlasShellInner.tsx` (MODIFIED — register keymap once; extend layered-Esc close)
- `app/atlas/_lib/leafcanvas.tsx` (MODIFIED — expose camera actions to keymap)
- `app/atlas/_lib/leafcanvas-v2/__tests__/keymap.vitest.ts` (NEW)

**Approach:**
- Action table maps `{keyCombo: string, action: string}` → handler. Combos use a stable serialization (e.g., `"Cmd+0"`, `"Shift+Enter"`, `"Z+Drag"` — the drag-modifier ones flag a transient state in keymap).
- Action namespaces: `canvas.*` (camera), `selection.*` (Enter/Shift+Enter/Tab/Esc/Cmd+A), `search.*` (Cmd+F), `mode.*` (Shift+D).
- Dispatcher gates on canvas focus: a focused leaf element OR cursor over the canvas. Cmd+F / Cmd+A only fire when canvas is the active focus target; otherwise let the browser handle them.
- Implementation note: full set per R23 — Esc, Cmd+A, Tab/Shift+Tab, Enter/Shift+Enter/`\`, Cmd+click (handled in U4 via modifier read), Shift+1, Shift+2, Cmd+0, +/−, N/Shift+N, Z+drag (transient mode), Space+drag (transient mode), H (toggle persistent hand-tool), Cmd+F, Shift+D.
- `AtlasShellInner.tsx`'s existing layered Esc (`:177-195`) is preserved but extended: now first action is "close breadcrumb if open" (added in U5), then existing layers.
- N/Shift+N requires a stable "named-frame order" — compute as canvas-coordinate-sorted (top-to-bottom, then left-to-right) at canonical-tree-load time, store in spatial-store sidecar.

**Patterns to follow:**
- Existing keydown registration: `AtlasShellInner.tsx:177-195` pattern.
- Action invocation: existing imperative camera methods exposed from leafcanvas.tsx (`zoomIn`, `zoomOut`, `fitAll`, `focusOnFrame`, `requestCameraSnap`).

**Test scenarios:**
- Covers AE7. `Cmd+F` while canvas is focused fires `search.open-name-palette` action; same key while a text input is focused passes through to browser.
- `Shift+1` fires `canvas.fit-all`; resolves to the leaf's fit-all camera transition (spring from U2).
- `Shift+2` fires `canvas.fit-selection`; with no selection, no-op.
- `Cmd+0` fires `canvas.zoom-100`.
- `N` fires `canvas.next-named-frame`; with no named frames, no-op; with three named frames in canvas-order, cycles A → B → C → A.
- `Z` held: transient mode `zoom-region`; pointer-drag draws a rectangle (chrome layer); release fires `canvas.zoom-to-region` with the rect bounds.
- `Space` held: transient mode `pan-tool`; pointer-drag pans; release exits transient mode.
- `H` toggles persistent hand-tool; second `H` toggles off.
- `Shift+D` toggles Dev Mode (`dev-mode-state` flag from U9).
- Hotkey conflicts: Cmd+A inside an InlineTextEditor text input selects the text (not the canvas all-frames); confirm by simulating focus on `InlineTextEditor`.
- Esc layered close fires breadcrumb-close before atomic-deselect.

**Verification:** Type each hotkey while a leaf is open; confirm camera transitions, selection changes, and mode toggles behave as specified. Confirm browser defaults still fire when canvas isn't focused.

---

### U4. Frame-first selection model + Cmd+click deep-pick + Enter/Shift+Enter cycling + multi-select widening

**Goal:** Flip click semantics from "deepest atom" to "deepest FRAME/COMPONENT/INSTANCE/GROUP" while preserving Cmd+click for atom-level selection. Add Enter/Shift+Enter level cycling and widen multi-select (Shift+click + marquee + Cmd+marquee + Shift+marquee) to operate on the new frame target.

**Requirements:** R7, R8, R9, R10, R11, R12

**Dependencies:** U1 (chrome layer renders the selection ring), U3 (keymap dispatches Enter/Shift+Enter)

**Files:**
- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` (MODIFIED — `findFrameTarget`, modifier-aware dispatch, marquee widening)
- `app/atlas/_lib/leafcanvas-v2/selection-state.ts` (NEW — wraps existing Zustand selection with modifier-aware add/remove/toggle helpers and ancestor-chain precompute)
- `app/atlas/_lib/leafcanvas-v2/canvas-v2.css` (MODIFIED — selection ring is now drawn by chrome layer, but data attributes still kept for inspector binding)
- `app/atlas/_lib/leafcanvas-v2/__tests__/selection-semantics.vitest.ts` (NEW)
- `app/atlas/_lib/leafcanvas-v2/__tests__/marquee-frames.vitest.ts` (NEW)

**Approach:**
- Add `findFrameTarget(el, wrapper)` alongside existing `findAtomicTarget` (`LeafFrameRenderer.tsx:1160-1180`). The new walker climbs until it finds `data-figma-type` ∈ `{FRAME, COMPONENT, INSTANCE, GROUP}`. `ATOMIC_TYPES` (`:66-74`) remains as-is for the Cmd+click path.
- `handleClick` dispatches: `event.metaKey || event.ctrlKey` → `findAtomicTarget` (existing behavior); plain click → `findFrameTarget` (new default).
- Enter descends: read selection's ancestor chain (precomputed via `selection-state.ts`), select first child frame. Shift+Enter ascends: select parent frame.
- Multi-select: Shift+click toggles into bulk; marquee from whitespace selects every frame whose bounding box intersects the marquee rect; Cmd+marquee includes nested; Shift+marquee removes intersecting frames from current bulk.
- Marquee bounding-box test uses spatial-store rects, not DOM `getBoundingClientRect` (spatial-store from U1 is authoritative; avoids layout-thrash).
- Selection chrome paint is now driven by `selection-state` subscriber in the chrome layer (U1). The `data-atomic-selected` attribute is retained for AtomicChildInspector's existing binding logic.

**Patterns to follow:**
- Existing `findAtomicTarget` walker pattern (`:1160-1180`).
- Existing marquee `onPointerDown/Move/Up` at `:622-735`.
- Zustand selection store usage (read at `:768`, mutations at `:146-149`).

**Test scenarios:**
- Covers AE2. Click on a vector path inside an inlined illustration SVG selects the `illustration/<name>` frame; Cmd+click on the same path selects the vector path itself.
- Plain click on a button glyph inside a Button frame selects the Button frame; Cmd+click selects the glyph.
- Enter on a selected `section/wallet-cards` frame descends to its first child frame; second Enter descends one more level.
- Shift+Enter on a selected frame selects its parent; at the leaf root, no-op.
- Shift+click on a second frame adds it to bulk selection; selection ring renders around the union (via chrome layer); Shift+click again on either frame removes it from bulk.
- Marquee drag from canvas whitespace selects every frame whose bbox intersects; Cmd+marquee additionally includes nested children that intersect; Shift+marquee subtracts intersecting frames.
- Cmd+A selects every frame at the current depth (defined as: every frame at the same depth as the current selection, OR every top-level frame on the leaf if nothing is selected).
- Esc clears selection; second Esc closes breadcrumb (added in U5).
- Marquee + spatial-store: simulate 100 frames in spatial-store, marquee covers 30 of them; assert exactly 30 in selection.
- Cmd+click on canvas whitespace (no frame under cursor) is a no-op — existing selection is preserved; the click does not start a marquee, does not deselect, does not throw.
- Plain click on canvas whitespace starts a marquee (existing behavior preserved).
- Enter pressed with empty selection is a no-op (guard against null current-selection); Shift+Enter pressed with empty selection is also a no-op.

**Verification:** AE2 passes in Playwright (canvas-figma-parity.spec.ts); Vitest unit tests pass; spatial-store-driven marquee performance is ≤16ms for ≤100 frames per leaf canvas (current scale).

---

### U5. Composed hover (deepest-frame outline + autolayout padding/gap auto-illumination) + breadcrumb chip

**Goal:** Hover paints a 2px screen-space outline on the deepest frame under cursor, and whenever that frame's ancestor chain contains an autolayout container, the container's padding bands and gap fills illuminate simultaneously. Plus the breadcrumb chip showing the selection's ancestor chain.

**Requirements:** R13, R14, R15, R17, R18

**Dependencies:** U1 (chrome layer paints the outline/bands/chip), U4 (selection-state's ancestor-chain precompute)

**Files:**
- `app/atlas/_lib/leafcanvas-v2/hover-signal.ts` (MODIFIED — emit `{screenID, figmaNodeID, ancestorChain, autolayoutAncestor?}`)
- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` (MODIFIED — `onPointerMove` walks via `findFrameTarget`; ancestor chain precompute)
- `app/atlas/_lib/leafcanvas-v2/breadcrumb.tsx` (NEW — renders inside chrome layer)
- `app/atlas/_lib/leafcanvas-v2/chrome-layer.tsx` (MODIFIED — wires composed hover paint: outline + padding bands + gap fills + dimension chip)
- `app/atlas/_lib/leafcanvas-v2/MeasurementOverlay.tsx` (DEPRECATED — its responsibilities absorbed by chrome layer; remove mount from `LeafFrameRenderer.tsx:902-908` once chrome-layer parity confirmed)
- `app/atlas/_lib/leafcanvas-v2/__tests__/composed-hover.vitest.ts` (NEW)
- `app/atlas/_lib/leafcanvas-v2/__tests__/breadcrumb.vitest.ts` (NEW)

**Approach:**
- Hover target = `findFrameTarget` (same walker U4 added). Hover emits the full ancestor chain (precomputed) and the nearest autolayout ancestor.
- Chrome layer subscribes to hover-signal. On each hover update + each rAF tick: paint 2px outline on the hover target's screen-rect; if `autolayoutAncestor` is present, paint padding bands (magenta over inner-padding regions) and gap fills (blue over gap regions) on the ancestor's screen-rect; paint dimension chip (W×H of hover target in px, screen-space fixed pixel size) at the outline's most-visible edge.
- Padding bands: ancestor has `layoutMode`, `padding{Top,Right,Bottom,Left}`, `itemSpacing`. Bands are rectangles at the inner edge; gap fills are rectangles between child rects.
- Dimension chip avoids clipping at viewport bounds via bbox-vs-effective-viewport intersection; the effective viewport accounts for the right-side inspect drawer's current open width (`window.innerWidth - drawerWidth`) so chips flip away from the drawer, not into it.
- Breadcrumb chip renders the selection's ancestor chain segments as clickable text. Multi-selection: longest common ancestor chain; no common parent → leaf-canvas root.
- `MeasurementOverlay` is kept mounted in parallel for one commit (parity verification), then removed in a follow-up commit within U5 once chrome-layer composed-hover renders identical output.

**Patterns to follow:**
- Existing `hover-signal.ts` pub/sub.
- Existing `MeasurementOverlay.tsx` padding/gap render math (`:188-193` + supporting code) — port to chrome-layer.

**Test scenarios:**
- Covers AE3. Cursor over a button glyph inside an autolayout row: hover-signal emits the Button frame as target AND the autolayout row as `autolayoutAncestor`; chrome layer paints 2px outline on the Button frame's screen-rect AND padding bands + gap fills on the row's screen-rect.
- Hover over a non-autolayout frame: outline paints; padding/gap bands do NOT paint (no `autolayoutAncestor`).
- Hover over the leaf root: outline + dimension chip; no padding bands (root has no autolayout ancestor).
- Dimension chip at viewport edge: flips to opposite edge when default placement would clip.
- Breadcrumb: with single selection `Screen › section/wallet › illustration/empty-state › icon/star`, all four segments render and are clickable.
- Covers AE4. Breadcrumb with two selected frames sharing no common parent: chip shows leaf-canvas-root segment only.
- Breadcrumb click on a parent segment fires `selection-state` mutation to select that ancestor.
- Hover updates fire on `pointermove` (rAF-throttled), not on settle.

**Verification:** AE3 + AE4 pass in Playwright; composed hover visually matches Figma side-by-side (manual); `MeasurementOverlay` removed once chrome-layer parity confirmed (separate commit within U5).

---

### U6. Alt-hover red distance lines

**Goal:** With a node selected, holding Alt + hovering another node draws four red distance segments (top / bottom / left / right gap) with px labels, bounds-to-bounds, rendered in the chrome layer.

**Requirements:** R16

**Dependencies:** U1 (chrome layer paints distance lines), U5 (hover signal carries the target)

**Files:**
- `app/atlas/_lib/leafcanvas-v2/keymap.ts` (MODIFIED — Alt is a transient modifier; chrome layer reads modifier state)
- `app/atlas/_lib/leafcanvas-v2/chrome-layer.tsx` (MODIFIED — distance lines paint path)
- `app/atlas/_lib/leafcanvas-v2/__tests__/distance-lines.vitest.ts` (NEW)

**Approach:**
- Keymap tracks Alt held/released state as a transient modifier signal.
- Chrome layer subscribes to `(selection, hover, alt-modifier)`. When all three are set: compute four distance segments from selection bbox to hover bbox (top edge of one to bottom edge of other, etc — bounds-to-bounds). Paint as red `<line>` elements; px labels as `<text>` at midpoints.
- Alt release or hover clear: lines disappear.

**Patterns to follow:**
- Chrome-layer pre-allocated `<g id="distance">` group from U1.

**Test scenarios:**
- Given selection at world-rect `{0, 0, 100, 100}` and hover at `{200, 50, 100, 100}` with Alt held: four distance segments paint with labels "200px" (horizontal gap), "0px" (or null for overlap on vertical axis), etc.
- Alt released: distance lines disappear (group children removed/hidden).
- Selection cleared while Alt is held: distance lines disappear.
- Distance lines paint at 2px stroke regardless of camera z.

**Verification:** Manual side-by-side with Figma's Alt-hover behavior; unit tests for distance math.

---

### U7. Client SVG inline rendering — `svg_markup` branch in `nodeToHTML`

**Goal:** When a canonical-tree node carries `svg_markup`, inline it as `<svg>...</svg>` via `dangerouslySetInnerHTML` instead of routing through the cluster-URL → `<img>` path. `useIconClusterURLs` short-circuits for these nodes.

**Requirements:** R3, R4, R5

**Dependencies:** none on client side (independent of U1-U6); U8 produces the data this consumes

**Files:**
- `app/atlas/_lib/leafcanvas-v2/types.ts` (MODIFIED — add `svg_markup?: string` to `CanonicalNode`)
- `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts` (MODIFIED — new branch in `renderClusterPlaceholder` at `:256-263`)
- `app/atlas/_lib/leafcanvas-v2/useIconClusterURLs.ts` (MODIFIED — skip clusters whose tree node carries `svg_markup` in `collectClusterIDsWithBBox` at `:114-127`)
- `app/atlas/_lib/leafcanvas-v2/__tests__/svg-markup-render.vitest.ts` (NEW)
- `app/atlas/_lib/leafcanvas-v2/__tests__/nodeToHTML.test.ts` (MODIFIED — add svg_markup branch tests)

**Approach:**
- `renderClusterPlaceholder` (`nodeToHTML.ts:256-352`): first check `node.svg_markup`. If present, emit `<div data-cluster-svg="true" data-figma-id={node.id} dangerouslySetInnerHTML={{__html: node.svg_markup}} style={{positioning + sizing wrappers existing}}>`. Skip the cluster-URL Map lookup, the `<img>` path, and the pending/failed placeholders.
- `useIconClusterURLs.ts:114-127` (`collectClusterIDsWithBBox`): filter out nodes carrying `svg_markup` — no need to mint a URL or wait on asset-stream for them.
- For repeated instances of the same `svg_markup` content on one leaf, U7 emits each instance inline (no `<symbol>`/`<use>` deduping in this unit — that's a perf-tier optimization deferred to a follow-up if profiling shows it matters; the brainstorm called it out as step (d) of R4, but it's not load-bearing for v1).
- `svg_markup` is a server-trusted string (originates from Figma's `?format=svg` endpoint, server-side). The renderer trusts the server-supplied bytes; security note in Risks.

**Patterns to follow:**
- Existing `renderClusterPlaceholder` data-attribute conventions (`data-cluster="true"`, `data-figma-id`, etc).
- Existing `dangerouslySetInnerHTML` usage in the codebase (search confirms it's used elsewhere; consistent with React conventions).

**Test scenarios:**
- Covers AE2 (client side). A canonical-tree node with `name="illustration/empty-state-watchlist"` and `svg_markup="<svg viewBox='0 0 100 100'>...</svg>"` renders as inline `<svg>` in the DOM; no `<img>` tag emitted.
- A node with `svg_markup` is NOT collected by `collectClusterIDsWithBBox` (no URL minted).
- A node without `svg_markup` falls through to the existing cluster-URL path (PNG render via `<img>`).
- Hover events fire on individual `<path>` elements inside the inlined SVG (DOM-accessible — confirms the path is reachable, not opaque like `<img>`).
- Covers AE1 + AE5 (client behavior). When `svg_markup` is undefined for a named illustration AND a PNG URL is present in `clusterURLs`, the renderer falls back to the PNG path silently — no broken-asset placeholder.

**Verification:** Render a sample leaf canvas with one inlined-SVG illustration and one PNG-fallback illustration; visually confirm both render; inspect DOM to confirm one is `<svg>` and the other is `<img>`.

---

### U8. Server-side SVG inlining — post-Stage 9 mutation pass

**Goal:** After Stage 9 renders SVG bytes to disk for SVG-eligible clusters, load each affected screen's `canonical_tree` blob, splice the SVG markup into the matching nodes' `svg_markup` field, recompress, and `UPDATE screen_canonical_trees`. Plus a name-aware short-circuit in `pipeline_cluster_prerender.go` so `illustration/*` and `icon/*` named frames skip eligibility heuristics (mirrors MCP plan's KTD-4 doctrine).

**Requirements:** R1, R2, R5, R6

**Dependencies:** none (server-side, independent of client units)

**Files:**
- `services/ds-service/internal/projects/pipeline.go` (MODIFIED — call `InlineSVGMarkup` after `renderSVGClustersForVersion` returns at `:810`)
- `services/ds-service/internal/projects/pipeline_cluster_prerender.go` (MODIFIED — name-aware short-circuit in `isCluster`/`walkClustersWithSVGFlag`)
- `services/ds-service/internal/projects/svg_inliner.go` (NEW — `InlineSVGMarkup(ctx, store, tenantID, fileID, versionIndex, screensWithSVGs)`)
- `services/ds-service/internal/projects/canonical_tree.go` (MODIFIED — helper `MutateCanonicalTree(tree, mutator func(node)) (newTree, error)` for walk-and-mutate)
- `services/ds-service/internal/projects/svg_inliner_test.go` (NEW)
- `services/ds-service/internal/projects/canonical_tree_test.go` (MODIFIED — add walk-and-mutate test)

**Approach:**
- After `renderSVGClustersForVersion(bgCtx, p.AssetExporter, in, svgs)` returns at `pipeline.go:810`, call `InlineSVGMarkup(bgCtx, p.Store, in.TenantID, in.FileID, in.VersionIndex, svgs)`.
- `InlineSVGMarkup` for each screen with at least one inlined SVG:
  1. Load canonical_tree via `ResolveCanonicalTree` (`canonical_tree.go:190`).
  2. For each svgID in the screen: read SVG bytes from `data/assets/{tenant}/{file}/{version}/{nodeID}.svg`.
  3. Call `MutateCanonicalTree(tree, mutator)` where `mutator(node)` checks `node["id"] == svgID` and if so sets `node["svg_markup"] = string(svgBytes)`.
  4. Marshal back to JSON, compress via `CompressTreeZstd` (`canonical_tree.go:144`).
  5. `UPDATE screen_canonical_trees SET canonical_tree_zstd = ? WHERE tenant_id = ? AND file_id = ? AND version_index = ? AND screen_id = ?`.
- Name-aware short-circuit in `pipeline_cluster_prerender.go`: when classifying clusters, any node with `name` matching `^\s*illustrations?\s*\/` or `^\s*icons?\s*\/` (mirror of TS regexes from `node-classifier.ts:87-88`) is flagged as SVG-eligible regardless of structural eligibility heuristics. Eligibility heuristics still gate UNNAMED clusters.
- 64MB decompression cap (`canonical_tree.go:43`) is acknowledged. Average SVG markup is small (~2-20KB per illustration); even with 100 inlined illustrations per screen, the recompressed tree should stay well under cap. Add a guard: if a single screen's inlined tree exceeds 32MB, log a warning and skip inlining (PNG fallback runs).
- Failure handling: SVG fetch failed at `renderSVGClustersForVersion` time → no bytes on disk → `InlineSVGMarkup` skips that node silently (R5 — silent PNG fallback). The `screen_canonical_trees` row is not updated; client renders via the existing PNG path.

**Patterns to follow:**
- Existing canonical-tree compression: `CompressTreeZstd` / `DecompressTreeZstd` (`canonical_tree.go:144-180`).
- Existing tree resolution: `ResolveCanonicalTree(legacy, gz, zstd)` (`:190`).
- Existing per-screen UPDATE pattern in the same package.

**Test scenarios:**
- Covers AE1. Given a frame named `illustration/empty-state-watchlist` contains an image fill (Figma can't export clean SVG), `renderSVGClustersForVersion` returns no bytes; `InlineSVGMarkup` skips that node; the canonical_tree's row is unchanged for that screen; client renders via PNG fallback.
- Given a frame named `illustration/empty-state-success` exports cleanly, `InlineSVGMarkup` reads bytes from disk and writes them as `svg_markup` on the matching tree node; `UPDATE` executes once; subsequent `ResolveCanonicalTree` returns the inlined version.
- `MutateCanonicalTree` walks all descendants and only mutates nodes matching the predicate; non-matching nodes are unchanged byte-for-byte.
- Recompressed tree decompresses back to the same JSON structure with `svg_markup` field present.
- Name-aware short-circuit: a frame named `illustration/has-image-fill` (otherwise SVG-ineligible structurally) is flagged for SVG export attempt — and silently falls back via the failure path above.
- Name-aware short-circuit: a frame named `chart-data-viz` (matches CHART_NAME_RE but not ILLUSTRATION_NAME_RE) is NOT short-circuited; existing eligibility logic runs.
- 32MB guard: simulate a tree with 100 inlined SVGs each 500KB → 50MB inlined → guard fires; warning logged; PNG fallback path used.
- Idempotency: re-running Stage 9 + InlineSVGMarkup produces the same result (no duplicate inlining).

**Verification:** Run `cmd/import-figma-url` on a test file containing both named and unnamed clusters; confirm `screen_canonical_trees` rows carry `svg_markup` for named frames where SVG export succeeded; client renders inlined SVGs as `<svg>` and PNG fallbacks as `<img>`.

---

### U9. Dev Mode render flag (Shift+D) + annotation paint

**Goal:** Introduce a global Dev Mode flag toggled by Shift+D. When on, autolayout frames render their padding bands and gap fills *always* (independent of hover); TEXT nodes render with baseline guides; image fills render their constraint mode (FILL / FIT / STRETCH) as a corner annotation.

**Requirements:** R28, R29

**Dependencies:** U1 (chrome layer paints annotations), U3 (Shift+D keymap)

**Files:**
- `app/atlas/_lib/leafcanvas-v2/dev-mode-state.ts` (NEW — module-level boolean signal with subscribe)
- `app/atlas/_lib/leafcanvas-v2/chrome-layer.tsx` (MODIFIED — Dev Mode subscriber paints always-on padding/gap bands for every autolayout frame; baseline guides; image-constraint annotations)
- `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts` (MODIFIED — read dev-mode-state; emit `data-dev-mode-baseline-guide` on TEXT nodes; emit constraint-mode corner annotation on image-fill nodes)
- `app/atlas/_lib/leafcanvas-v2/__tests__/dev-mode-state.vitest.ts` (NEW)

**Approach:**
- `dev-mode-state.ts` exposes `getDevMode()`, `setDevMode(on)`, `subscribe(listener)`. Module singleton, HMR-guarded.
- Shift+D from keymap (U3) toggles the flag.
- Chrome layer subscribes. When ON: iterate every frame in spatial-store for the active leaf, paint padding bands + gap fills for each autolayout frame (no hover required). Hover-driven painting (U5) is additive — when both Dev Mode is on AND hover is active on a different frame, the hovered frame's hover-state highlights stack on top.
- TEXT nodes in Dev Mode: `nodeToHTML.ts` emits a `data-dev-mode-baseline-guide` data attr; CSS in `canvas-v2.css` paints a 1px underline at the text's baseline via `::after` pseudo-element.
- Image-fill nodes in Dev Mode: `nodeToHTML.ts` emits a small corner badge showing the constraint mode. Painted as CSS pseudo-element.

**Patterns to follow:**
- Module-singleton pub/sub: `hover-signal.ts`.
- `data-*` attribute conventions in `LeafFrameRenderer.tsx`.

**Test scenarios:**
- Shift+D toggles `getDevMode()` from false → true → false on successive presses.
- Subscribers fire on each toggle.
- When Dev Mode is on, chrome layer's padding-band group contains entries for every autolayout frame in spatial-store (not just the hovered one).
- TEXT nodes in Dev Mode carry `data-dev-mode-baseline-guide="true"`; same nodes without Dev Mode do not carry the attribute.
- Image-fill nodes show the constraint-mode badge in Dev Mode; do not show in normal mode.
- Toggling Dev Mode does not unmount any DOM (assert via mount-counter): same nodes re-paint with additional annotations.

**Verification:** Toggle Shift+D in a running canvas; visually confirm padding bands appear on every autolayout frame; baseline guides on text; constraint badges on image fills.

---

### U10. AtomicChildInspector extension — Layout / Typography / Fills sections + multi-selection handling

**Goal:** Extend the existing `AtomicChildInspector.tsx` drawer with three Figma-Dev-Mode property sections at the top: Layout (W/H, layout mode, padding, gap, primary axis alignment), Typography (for TEXT nodes — font family/size/weight/line-height/letter-spacing/color), Fills & strokes (solid fills as hex, image fills as indicator, strokes as weight + color). Frame name shown at the top. Multi-selection shows "Multiple" for differing values.

**Requirements:** R30, R31

**Dependencies:** U4 (selection-state with multi-selection support)

**Files:**
- `app/atlas/_lib/leafcanvas-v2/AtomicChildInspector.tsx` (MODIFIED — add three property-group sections at the top; preserve existing tabs below)
- `app/atlas/_lib/leafcanvas-v2/inspector-property-groups.tsx` (NEW — `<LayoutGroup>`, `<TypographyGroup>`, `<FillsStrokesGroup>` components)
- `app/atlas/_lib/leafcanvas-v2/__tests__/inspector-property-groups.vitest.tsx` (NEW)

**Approach:**
- The drawer's existing tabs (DRD / Violations / Decisions / Copy / Activity / Comments) remain unchanged below the new property sections (per KTD-7).
- Property groups read the current selection via the existing Zustand selection (`useAtlas((s) => s.selection.selectedAtomicChild)` and `selectedAtomicChildren`).
- Selection → canonical-tree node lookup via the existing `findByFigmaID` helper (already imported by `MeasurementOverlay.tsx:42`).
- Layout group reads: `node.absoluteBoundingBox` for W/H; `node.layoutMode`, `node.paddingTop/Right/Bottom/Left`, `node.itemSpacing`, `node.primaryAxisAlignItems`.
- Typography group reads: only if `node.type === "TEXT"`; reads `node.style.fontFamily`, `fontSize`, `fontWeight`, `lineHeightPx`, `letterSpacing`, `fills[0]` for color.
- Fills/strokes group reads: `node.fills[]` (SOLID → hex, IMAGE → "Has image fill" indicator), `node.strokes[]` (weight + color).
- Multi-selection (when `selectedAtomicChildren.size > 1`): collect property values across all selected nodes. For each property, if all values are equal show the value; if any differ, show "Multiple".
- Empty state (0 selected): show "No selection".

**Patterns to follow:**
- Existing `AtomicChildInspector.tsx` structure.
- Existing `findByFigmaID` helper signature.

**Test scenarios:**
- Single TEXT selection: shows Layout + Typography + Fills sections; values populated from canonical-tree node.
- Single non-TEXT selection (e.g., Button frame): shows Layout + Fills sections; Typography section is always rendered as a section shell with "—" placeholders for each property (matches Figma Dev Mode's behavior — section never hides, communicating "this property category exists but doesn't apply here").
- Covers AE8. Two button frames with different background fills: Layout section shows W/H/layoutMode at actual values (when those match); Fills section shows "Multiple" for the differing fill; shared properties show actual values.
- Empty selection: all three sections show empty state.
- Existing tabs (DRD / Violations / Decisions / Copy / Activity / Comments) remain functional below the new sections.
- Property groups update on selection change.

**Verification:** AE8 passes in Playwright; manual side-by-side with Figma Dev Mode's Inspect panel; existing inspector tabs continue to work as before.

---

## System-Wide Impact

| Surface | Impact |
|---|---|
| `services/ds-service/internal/projects/pipeline.go` | Stage 9 gains a post-render mutation pass. **Shared with MCP plan U2** — see Cross-Stream Coordination. |
| `services/ds-service/internal/projects/pipeline_cluster_prerender.go` | Name-aware eligibility short-circuit. Mirrors MCP KTD-4. |
| `services/ds-service/internal/projects/canonical_tree.go` | New `MutateCanonicalTree` helper. Read-side `ResolveCanonicalTree` unchanged — existing readers don't need updates. |
| `screen_canonical_trees` table | Schema unchanged (canonical_tree is a JSON blob). Existing rows continue to work; new ingestions carry `svg_markup` on named frames. |
| `app/atlas/_lib/leafcanvas-v2/` | Substantial — 8 new files, 10 modified files. Module-singleton pattern preserved. |
| `app/atlas/_lib/leafcanvas-v2/MeasurementOverlay.tsx` | Deprecated. Removed in a separate commit within U5 once chrome-layer parity is confirmed. |
| `app/atlas/_lib/AtlasShellInner.tsx` | Keymap registration; layered Esc extended. |
| `app/atlas/_lib/leafcanvas.tsx` | Chrome layer mount; camera-state lifted from `camRef` to module signal. |
| `/atlas` overview page | Untouched. Atlas parity is explicitly deferred (Scope Boundaries). |
| Existing canvas keyboard handlers (6+ scattered files) | Untouched directly; the new keymap module sits alongside them. A future cleanup unit (not in this plan) could consolidate. |

---

## Scope Boundaries

### Deferred to Follow-Up Work

- **`<symbol>` + `<use>` SVG deduping** for repeated icon instances on one leaf (R4 step (d)). Inlining each instance is the v1 shape; deduping is a perf optimization revisited if profiling shows it matters. (See open question on R4 coverage claim.)
- **R-tree spatial index for hit-test/marquee** (KTD-9; revisit at 5,000+ frames per leaf).
- **MeasurementOverlay full removal commit** lands within U5 but is sequenced after chrome-layer composed-hover parity is confirmed.
- **Spring parameter tuning final pass** — initial constants in U2; final tuning is a separate pass after the full set lands and side-by-side comparison can run end-to-end.

### Outside Scope

- **Atlas (multi-screen overview) Figma parity.** Brainstorm Scope Boundaries; deferred to a phase-2 effort with the same chrome-layer + spring-camera primitives ported.
- **Selection-in-URL deep-linking.** Brainstorm Scope Boundaries — selection state is ephemeral in v1; share-this-selection is its own feature, not a v1 trim.
- **Camera-state-in-URL.** Brainstorm Scope Boundaries — camera state is ephemeral; reload returns to fit-all by design.
- **Code generation in the inspect panel** (CSS / Swift / Android). Brainstorm Scope Boundaries.
- **Design-token mapping** (hex → token name). Brainstorm Scope Boundaries.
- **Minimap navigator** (rejected from ideation; not Figma Dev Mode behavior).
- **Compare changes between versions** (Figma Dev Mode has it; not in v1).
- **Peer-iframe inspect panel.** Single React tree per brainstorm Scope Boundaries.
- **WebGL/Canvas2D substrate migration.** HTML-DOM remains the substrate per Zeplin canvas learnings.
- **One-infinite-canvas reframing** (atlas/leaf collapse). Big-bet rewrite, rejected in ideation.

---

## Verification

End-to-end verification:
1. Run a dev build (`npm run dev`); open a leaf canvas with named illustrations and icons.
2. Re-import a test Figma file (`extract:figma`) to populate `svg_markup` server-side.
3. Inspect DOM: named illustrations render as `<svg>`, others as `<img>` (PNG fallback).
4. Click any frame; selection ring renders in chrome layer at 2px regardless of zoom.
5. Hover any frame inside an autolayout row; outline + padding bands + gap fills + dimension chip all paint.
6. Press Shift+1, Shift+2, Cmd+0, N, Shift+N, +/−, Z+drag, Space+drag, Cmd+F, Shift+D — each fires its action; camera transitions feel spring-driven, not eased.
7. Hold Alt with a frame selected; hover another frame; red distance lines + px labels appear; release Alt → disappear.
8. Toggle Shift+D; autolayout frames show padding/gap always; text shows baselines; image fills show constraint badges.
9. Open AtomicChildInspector with a frame selected; Layout/Typography/Fills sections at the top show correct property values; multi-select shows "Multiple" for differing properties.
10. Run `tests/canvas-figma-parity.spec.ts` Playwright — covers AE1-AE8.

Per-unit verification lives in each unit's `**Verification:**` field.

---

## Sequencing

```
Phase 1 — Foundations (must precede Phases 2 & 4):
  U1 (spatial store + chrome layer foundation)
     │
     ├─→ U2 (spring camera)  ──┐
     │                          │
     └─→ U3 (keymap)  ──────────┤
                                ▼
Phase 2 — Selection & hover:
  U4 (selection semantics + marquee) ─→ U5 (composed hover + breadcrumb) ─→ U6 (Alt-hover distances)

Phase 3 — Asset routing (independent of Phases 1, 2, 4 — can run in parallel):
  U7 (client svg_markup branch)
  U8 (server post-Stage 9 inlining)
     [Both end-to-end need both, but U7 and U8 can land in any order]

Phase 4 — Dev Mode & inspect panel (depends on Phases 1 & 2):
  U9 (Dev Mode flag + annotations) ─→ U10 (inspect-panel extension)
```

Recommended landing order: U1 → U2 → U3 → U7 → U8 → U4 → U5 → U6 → U9 → U10. U2 before U3 so the keymap's camera actions invoke a spring-driven snap from day one rather than calling a stale linear easer. U7/U8 (asset routing) can interleave anywhere after U1.

Implementer pulls from `main` before each commit on U8 to absorb MCP-plan pipeline.go changes.

---

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| **Merge conflicts on `pipeline.go` with the MCP plan.** Both plans modify this file. | Small commits scoped to non-overlapping code paths (U8 touches the goroutine block at `:726-854`; MCP U2 touches the autosync section path). Pull from `main` before each commit. Cross-Stream Coordination section flags this explicitly. |
| **`svg_markup` field on canonical_tree bloats compressed tree past the 64MB decompression cap.** | 32MB guard in `InlineSVGMarkup`: log warning + skip inlining → PNG fallback. Acceptable for the long-tail edge case (single screen with 100+ inlined large illustrations). |
| **Chrome-layer rAF callback drops frames if spatial-store grows large.** | Profile at 100, 500, 1000 frames/leaf. Current scale is ~50/leaf so headroom is large. If needed, reduce paint to viewport-intersecting nodes only (the rect-vs-viewport test is cheap). |
| **Spring tuning takes longer than expected to match Figma's feel.** | Spring params are module-level constants in `spring.ts`. Side-by-side comparison is a manual test. Brainstorm Key Decision already says "matches Figma" is subjective. Accept tuning iteration cost. |
| **`dangerouslySetInnerHTML` for server-supplied SVG opens a script-injection vector if the server is compromised.** | SVG markup originates from Figma's official API (server-trusted). Server-side ingest at `RenderAssetsForLeaf` already trusts the response bytes. Add a defense-in-depth strip of `<script>` tags during `InlineSVGMarkup` (regex-strip is sufficient given the controlled source). |
| **MeasurementOverlay deprecation breaks consumers (e.g., `useHoveredBandHint` cross-highlighting from inspector).** | Keep MeasurementOverlay mounted for one commit within U5 alongside chrome-layer composed-hover; remove only after parity confirmed. Migrate `useHoveredBandHint` consumers to chrome-layer in the same unit. |
| **Cmd+F/Cmd+A hotkey collisions with browser defaults.** | Focus-scoping in keymap dispatcher: hotkeys fire only when canvas is the active focus target. Validated via the test scenario in U3 ("Cmd+A inside an InlineTextEditor text input selects the text"). |
| **Repeated SVG instances bloat DOM weight.** | Folded into Deferred work — `<symbol>` + `<use>` deduping is the perf optimization if profiling shows it matters. |

---

## Open Questions (to be discussed before extending)

- **Spring final parameters** — initial constants in U2; final tuning is a separate pass with side-by-side Figma reference.
- **Z+drag zoom-to-region implementation share with marquee** — both use the chrome layer's marquee `<rect>` mechanic; decide during U3 whether to share infrastructure or keep independent.
- **Browser-default conflict mapping for Cmd+F, Cmd+A, `\`, Space across Chrome/Safari/Firefox** — validated during U3; mitigation already in plan (focus-scoping).

---

## Dependencies / Assumptions

- **Figma SVG export returns deterministic node IDs across runs.** Brainstorm assumption. If non-deterministic, the v1 plan still works (no `<symbol>`/`<use>` deduping in v1); the follow-up deduping unit becomes harder.
- **Designer adherence to the naming convention** (`illustration/<name>`, `icon/<segment>/...`) determines which frames get SVG-first treatment. Unnamed vector groups continue through the existing PNG pyramid path.
- **Pipeline Stage 9 SVG export is already production** (verified in Phase 1 research).
- **`@react-spring/three` is installed but is 3D-only.** Hand-rolling the spring integrator (U2) avoids pulling in `@react-spring/web` and keeps the existing rAF-loop pattern.
- **No AGENTS.md / CLAUDE.md / STRATEGY.md** at the repo root (verified). Conventions inferred from observed patterns: module-level pub/sub for hot-path state; HMR guards via `globalThis.__lcXxxWired`; imperative DOM tagging via `data-*` attributes; strict TS in `leafcanvas-v2/`; tests under `__tests__/` siblings (`.vitest.ts` or `.test.ts`).
- **Playwright config** at `playwright.config.ts` already runs tests from `tests/` matching `*.spec.ts`. New end-to-end spec `tests/canvas-figma-parity.spec.ts` follows the existing convention.
- **MCP plan is in-flight on `main` in parallel.** Coordination via small commits + frequent rebases. Cross-Stream Coordination section documents the one overlap point.

---

## Past Learnings Referenced

- `docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md` — dual-path flex+absolute renderer is the right substrate; resist WebGL on the canvas layer. Plan respects this (KTD-1 keeps DOM substrate).
- `docs/solutions/2026-05-01-001-designbrain-perf-findings.md` — LOD ladder + level-0 fallback to eliminate pop-in. Plan retains the existing PNG pyramid (R5/KTD-8) which already implements LOD; v1 doesn't change this.
- `docs/solutions/2026-04-30-001-projects-phase-1-learnings.md` — canonical_tree lives in sibling `screen_canonical_trees` (not on `screens`). Plan preserves this separation; `svg_markup` lives inside the canonical_tree JSON, not in a new column.
- `docs/solutions/2026-05-01-001-phase-6-closure.md` — materialise-on-write for derived spatial structures (worker rebuilds index; gate SSE on worker flush). Spatial-store in U1 is in-memory only (per-session, populated at tree-load); the materialise-on-write pattern is the right shape if we ever need a persistent spatial index server-side (deferred per KTD-9).
