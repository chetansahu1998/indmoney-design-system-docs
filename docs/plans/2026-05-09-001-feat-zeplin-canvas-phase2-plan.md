---
title: "Zeplin canvas Phase 2 — hover, measurement, padding/gap overlays + camera snap"
type: feat
status: active
date: 2026-05-09
origin: docs/brainstorms/2026-05-05-zeplin-grade-leaf-canvas-requirements.md
---

# Zeplin canvas Phase 2 — hover, measurement, padding/gap overlays + camera snap

## Overview

Phase 1 of the Zeplin canvas (plan `docs/plans/2026-05-05-002-feat-zeplin-grade-leaf-canvas-plan.md`, U1–U15, merged 2026-05-06) shipped click-to-select, the atomic-child inspector, tokens panel, asset export, lasso bulk selection, the inline text editor, the state picker, and the renderer + override pipeline. This plan completes the inspect surface — the visual feedback layer engineers expect from Zeplin / Figma Dev Mode — by adding hover state, on-canvas measurement, padding and gap overlays, and a keyboard-triggered camera snap.

The work is **pure frontend** under `app/atlas/_lib/leafcanvas-v2/`. No backend changes. No new dependencies. All measurements come from existing canonical_tree fields (`paddingLeft/Right/Top/Bottom`, `itemSpacing`, `layoutMode`, `absoluteBoundingBox`) — already populated for every screen post the 2026-05-08 re-sync.

The Phase 2 cut deliberately matches the Figma Dev Mode pattern over Zeplin's panel-mediated pattern for padding/gap visualization (direct-on-canvas, not "hover the inspector row to highlight on canvas"), since Dev Mode's UX has lower cognitive load. Camera snap is keyboard / inspector-button only — never on canvas click — to avoid the disorientation that plagues "always snap on click" implementations.

---

## Problem Frame

After Phase 1 shipped, engineers can click an atomic and see its tokens panel, but the canvas itself is silent: no hover feedback, no measurement, no padding visualization, no gap visualization. Compared to Zeplin / Dev Mode, this means an engineer has to repeatedly click each element to read sizes and positions in the inspector — there's no "hover and read" affordance. Three concrete gaps observed in production after this session's user testing:

1. **No hover signal** — the canvas does not react to mouse position. The user described it as "feeling dead between clicks."
2. **No spacing/distance feedback** — to find the gap between two elements, the user has to subtract `Y` values from the inspector across two clicks.
3. **No padding/gap overlay** — auto-layout containers expose their padding values in the inspector, but there's no on-canvas visualization, so engineers can't visually confirm where the padding falls.

Additionally, the user requested a **camera snap** behavior matching Figma Dev Mode — a smooth pan/zoom that brings a selected element into view. Research confirmed Dev Mode's snap is **opt-in, keyboard / layer-panel triggered, never on canvas click**. We follow that pattern.

(see origin: `docs/brainstorms/2026-05-05-zeplin-grade-leaf-canvas-requirements.md` D10 inspect/handoff details)

---

## Requirements Trace

- R1. Hover any atomic child → bounding-box outline + floating `Name  W×H` tooltip appears within one frame; clears within 100ms of cursor leaving.
- R2. With an atomic selected and a different atomic hovered → 4-way cardinal distance lines (top/bottom/left/right edges) drawn between them, with integer px labels at midpoints.
- R3. Hover an autolayout FRAME (`layoutMode === "HORIZONTAL"|"VERTICAL"`) → translucent padding bands inside the bbox (top/right/bottom/left) with numeric labels matching `paddingTop/Right/Bottom/Left` from canonical_tree.
- R4. Hover an autolayout FRAME with multiple children → gap markers between siblings, colored bands with `itemSpacing` numeric label.
- R5. Hold `Alt` while hovering with a selection active → distance labels switch from px to % relative to the nearest common ancestor of selected and hovered.
- R6. Persistent W×H + (X, Y) chips visible on the selected atomic, distinct from the hover tooltip.
- R7. **Camera snap**: pressing `Shift+2` (or clicking a "Scroll into view" affordance in `AtomicChildInspector`) animates the camera to fit the selected atomic with 40px inset, capped at 100% zoom, using `easeInOutCubic` over 320ms. Any pointer input cancels the animation.
- R8. `Esc` layered close: clears hover first, then selection, then frame focus, then closes the leaf — extending the existing layered close from session 2026-05-08.
- R9. Inspector ↔ canvas binding: hovering a Layout Widget row in `AtomicChildInspector` highlights the corresponding band on the canvas (and vice-versa).
- R10. All overlays render correctly under canvas pan/zoom transforms — no jitter, no IntersectionObserver dependence, no visible drift across pinch-zoom.

**Origin actors:** A1 (engineer / developer reading specs), A2 (designer reviewing handoff)
**Origin acceptance examples:** AE-1 (covers R1, R6 — inspect a text label sees type styles; the hover/measurement layer is the visual half of that interaction)

---

## Scope Boundaries

- Rulers / persistent grid overlays — Zeplin/Dev Mode don't have these in viewer mode either.
- Pop-Out compare mode — high effort, lower impact, defer.
- Version Diff overlay (green/yellow added/changed) — no version-history surface yet.
- Constraint pinning visualization — `canonical_tree` doesn't expose constraints stably.
- Component link-status overlay (green/blue/red Storybook-link colors) — no design-system token map.
- Multi-element measurement — Phase 2 only does selected→hovered distance. Multi-select bulk distance defers.
- Pixel-precision toggle — we render at 1× already.
- Per-project unit switching (px/pt/dp) — UX decision deferred; Phase 2 outputs px only.
- Auto-snap on every canvas click — explicitly rejected (see Key Technical Decisions).

### Deferred to Follow-Up Work

- **Pop-Out compare mode** — separate plan after Phase 2 stabilises.
- **Constraint pinning visualization** — blocked on canonical_tree exposing constraints.
- **Mobile/responsive preview modes** — separate scope.
- **Component link-status overlay** — blocked on design-system token map.

---

## Context & Research

### Relevant Code and Patterns

- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx:715-737` — bulk-selected painter useEffect. **Mirror pattern** for `data-atomic-hovered`.
- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx:739-759` — atomic-selected painter useEffect (shipped 2026-05-08). Same painter pattern to extend for hover.
- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx:1098-1118` — `findAtomicTarget` walker. Reuse for hover detection so hover/click/lasso resolve to the same target.
- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx:921-923` — `cssEscapeAttr` (local helper). Reuse for hover query.
- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx:822-837` — wrapper element with all gesture handlers. New `onPointerMove` hover branch slots in **before** the lasso-active early-return.
- `app/atlas/_lib/leafcanvas-v2/StatePicker.tsx` — overlay precedent for pan/zoom-aware in-frame UI. `position: absolute` + `pointer-events: none` on container, opt-in on interactive children. Inherits world transform automatically.
- `app/atlas/_lib/leafcanvas-v2/canvas-v2.css:401-415` — selection outline CSS. Add hover + measurement classes here.
- `app/atlas/_lib/leafcanvas-v2/types.ts:145-203` — `CanonicalNode`, `BoundingBox`, layout fields (paddingLeft/Right/Top/Bottom, itemSpacing, layoutMode, primaryAxisAlignItems, counterAxisAlignItems).
- `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts:412-435` — `positionStyle`, the parent-rebase math. Distance lines must rebase wrapper-local the same way.
- `app/atlas/_lib/leafcanvas-v2/AtomicChildInspector.tsx` — Layout Widget rows; add hover binding to canvas. New `Scroll into view` button feeds R7.
- `app/atlas/_lib/leafcanvas-v2/gesture-tracker.ts` — `canvasGestureTracker.subscribe()` for gesture-end. Gate measurement DOM writes on `!getIsGesturing()`.
- `app/atlas/_lib/leafcanvas-v2/leaf-zoom-signal.ts:102-113` — module-level `useSyncExternalStore` pub/sub precedent. **Reuse pattern** for module-level hover signal (cross-component reach into AtomicChildInspector for R9).
- `app/atlas/_lib/leafcanvas.tsx:150` — `camRef` ({x, y, z}) — the camera state. RAF flush at lines 161-167. Camera snap (U7) writes here.
- `app/atlas/_lib/leafcanvas.tsx:211-247` — `useLayoutEffect` auto-fit. **Reference pattern** for `easeInOutCubic` rAF-driven camera animation.
- `app/atlas/_lib/AtlasShellInner.tsx` Esc layered handler (added 2026-05-08, atomic→frame→leaf). Extend to hover→atomic→frame→leaf for R8.

### Institutional Learnings

- **`docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md` (lines 50-54)** — `pointer-events: none` on full-screen overlay wrappers. Every new overlay must default to this, opting back in only on interactive elements.
- **`docs/plans/2026-05-06-002-fix-canvas-refresh-evidence-driven-debug-plan.md`** — IntersectionObserver-on-transform-parent footgun (Mozilla 1419339, WebKit 209264). Phase 2 overlays must NOT depend on IO for visibility tracking; use camera-driven positioning or `canvasGestureTracker.subscribe(settle, rebind)` if IO is unavoidable.
- **`docs/plans/2026-05-02-005-fix-atlas-interaction-polish-plan.md` (lines 199-219)** — HoverSignalCard polish rules: 50ms mount, 16ms reposition, 100ms unmount, edge-flip clamping near viewport edge, `position: fixed` with prop-fed top/left, no DPR multiplication.
- **`docs/plans/2026-05-06-003-fix-canvas-prerender-review-remediation-plan.md` (lines 49-50, 352-356)** — Module-load `canvasGestureTracker.subscribe` calls must be HMR-guarded with `if (!(globalThis as any).__lcXxxWired)`. Tests need `beforeEach(() => { delete (globalThis as any).__xxx; })` cleanup.
- **`docs/brainstorms/2026-05-05-zeplin-grade-leaf-canvas-requirements.md` (lines 243, 305)** — Documented ~2px tolerance between Figma autolayout and browser flexbox (Inter fallback vs Basier Circle font metrics). Measurement labels should source numbers from `canonical_tree.absoluteBoundingBox` for absolute subtrees (Figma-exact), and explicitly label as "design" vs "rendered" when sourced from `getBoundingClientRect()` on autolayout subtrees.

### External References

- **Zeplin inspect-mode behavior**:
  - [Zeplin Highlights — Help Center](https://support.zeplin.io/en/articles/5465721-zeplin-highlights)
  - [Inspecting Auto Layout properties](https://support.zeplin.io/en/articles/5356280-inspecting-auto-layout-properties)
  - [Measuring Distances in Percentage Units](https://medium.com/zeplin-gazette/measuring-distances-in-percentage-units-in-zeplin-4fc7756ecb68)
- **Camera snap tech spec** (synthesized from):
  - [tldraw `easeInOutCubic` source](https://github.com/tldraw/tldraw/blob/main/packages/editor/src/lib/primitives/easings.ts) — `t < 0.5 ? 4t³ : (t-1)(2t-2)² + 1`
  - [tldraw camera animation docs](https://tldraw.dev/sdk-features/camera) — 320ms default, `inset` padding, rAF-driven, no spring physics
  - [Excalidraw `actionCanvas.tsx`](https://github.com/excalidraw/excalidraw/blob/master/packages/excalidraw/actions/actionCanvas.tsx) — zoom-fit math, `fitToViewport: false` cap-at-100% pattern
  - [Figma `figma.viewport` Plugin API](https://developers.figma.com/docs/plugins/api/figma-viewport/) — `scrollAndZoomIntoView`; confirms Figma's snap is keyboard/plugin-only, not auto-on-click
- **Browser DevTools box model** — analog for padding visualization (margin orange, border black, padding green, content blue with numeric labels). Zeplin's 2024 Layout Widget redesign explicitly adopted this framing.

---

## Key Technical Decisions

- **Camera snap is keyboard/button only, never on canvas click.** Chosen per UX research and user decision: Figma Dev Mode default. Auto-snapping on every click is disorienting when clicking adjacent atomics. Trigger surface: `Shift+2` keyboard shortcut + a "Scroll into view" button in `AtomicChildInspector`.
- **Snap math: pan + zoom-to-fit, capped at 100%.** Element fills viewport with 40px inset on each side (matching tldraw default). When element is smaller than viewport at 100% zoom, snap zooms to 100% (not past — `fitToViewport: false` pattern from Excalidraw). The 100% cap prevents the disorienting "tiny element fills your screen" effect.
- **Easing: `easeInOutCubic` over 320ms, pure rAF, no spring physics.** Matches tldraw default. Spring physics adds overshoot which doesn't fit "smooth pan to inspect."
- **Hover state lives in a module-level pub/sub signal, not in the live store.** Mirrors `leaf-zoom-signal.ts` pattern. Reasons: (1) hover changes 60×/sec during pointer-move; pushing through Zustand causes excessive re-renders, (2) only LeafFrameRenderer + AtomicChildInspector + MeasurementOverlay need it. HMR-guard with `globalThis.__lcHoverWired` per the institutional pattern.
- **Padding/gap visualization is direct-on-canvas (Figma Dev Mode pattern), not Zeplin's panel-mediated pattern.** Hover the parent → bands appear automatically. Lower cognitive load. Inspector ↔ canvas binding (R9) is symmetric — hover the panel row OR the canvas, both light up.
- **Distance lines are 4-way cardinal only (not 8-way).** Matches Zeplin and Dev Mode. Diagonal distances are rarely useful on screen-aligned UI.
- **Measurement labels in px only for v2.** Single unit per project (Zeplin's pattern). Multi-unit (px/pt/dp) deferred — adds complexity without clear demand.
- **Source numeric values from `canonical_tree.absoluteBoundingBox` and layout fields, not `getBoundingClientRect()`** for absolute-positioned subtrees. Avoids the documented ~2px DOM-vs-Figma drift. For autolayout subtrees where DOM is the truth, label as "rendered" to set user expectation. Phase 2 v1 only ships the canonical_tree path; "rendered" annotations are a post-v2 polish.
- **All overlays mount inside `LeafFrameRenderer`'s wrapper** (sibling to `<StatePicker>`). Camera transform on `.lc-world` carries them automatically — no separate projection math, no IntersectionObserver. Confirmed safe per StatePicker precedent.
- **Reuse `findAtomicTarget` for hover detection.** Hover/click/lasso must resolve to the same target. If hover outlines a FRAME but click passes through to the atomic descendant, designers lose trust. One walker, three gestures.
- **Measurement DOM writes are gated on `!canvasGestureTracker.getIsGesturing()`.** During pan/zoom, suppress the overlay to avoid jitter; re-paint on settle (150ms after last gesture tick).

---

## Open Questions

### Resolved During Planning

- **Camera snap trigger** — keyboard / inspector layer-panel only, never on canvas click (user decision).
- **Snap zoom behavior** — pan + zoom-to-fit, cap at 100% (user decision).
- **Distance line directionality** — 4-way cardinal only (matches Zeplin + Dev Mode).
- **Padding/gap visualization style** — direct-on-canvas (Figma Dev Mode), not panel-mediated (Zeplin).
- **Hover state location** — module-level pub/sub signal, not Zustand store.
- **Source of measurement values** — `canonical_tree.absoluteBoundingBox` first; DOM `getBoundingClientRect()` as fallback only for autolayout subtrees with explicit labeling (deferred to post-v2).

### Deferred to Implementation

- **Tooltip pill rendering — inline `<div>` vs portal.** A portal to `document.body` avoids `overflow:hidden` clipping inside frame chrome but breaks the world transform inheritance. Default to inline; switch to portal if clipping shows up in QA.
- **Distance-line stroke style** (solid 1px? 2px? Dashed?) — tune in QA pass with a designer.
- **Padding-band opacity / hue** — a designer should pick the exact CSS values; placeholder is `rgba(255, 152, 0, 0.18)` matching Zeplin's orange family.
- **Alt-modifier ancestor traversal** — arrow keys (up/down) cycle parent context per Zeplin. Implementation needs a parent-walker that respects pruned/inactive nodes; build a simple list-and-index variant first, refine if QA needs faster traversal.

---

## Implementation Units

- U1. **Hover state plumbing — module signal + DOM tag**

**Goal:** Establish the foundational module-level hover signal and the DOM-tagging painter, mirroring the `data-atomic-selected` pattern shipped 2026-05-08.

**Requirements:** R1

**Dependencies:** None

**Files:**
- Create: `app/atlas/_lib/leafcanvas-v2/hover-signal.ts`
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/hover-signal.vitest.ts`

**Approach:**
- New `hover-signal.ts` mirroring `leaf-zoom-signal.ts:102-113` — `useSyncExternalStore` pub/sub with `setHoveredAtomicChild({screenID, figmaNodeID} | null)` and `useHoveredAtomicChild()`.
- Add HMR guard: `if (!(globalThis as any).__lcHoverWired) { (globalThis as any).__lcHoverWired = true; ... }`.
- In `LeafFrameRenderer.tsx`:
  - Wire `onPointerMove` handler **before** the existing lasso-active early-return: read `e.target`, walk via `findAtomicTarget`, call `setHoveredAtomicChild` only when the target changes (avoid 60×/sec churn).
  - Add `onPointerLeave` on the wrapper to clear the signal.
  - Add a third painter `useEffect` mirroring lines 715-737, keyed on hovered signal + `screenID + rendered`. Strips stale `[data-atomic-hovered="true"]`, applies fresh.

**Patterns to follow:**
- `app/atlas/_lib/leafcanvas-v2/leaf-zoom-signal.ts:102-113`
- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx:739-759` (atomic-selected painter)

**Test scenarios:**
- Happy path: `setHoveredAtomicChild({screenID:"s", figmaNodeID:"n"})` → `useHoveredAtomicChild()` returns same value.
- Edge case: setting same value twice → only one notification fires (subscriber identity check).
- Edge case: `setHoveredAtomicChild(null)` → subscribers see null.
- Integration: HMR module-reload should not leak listeners; `globalThis.__lcHoverWired` guards re-init.

**Verification:**
- Hovering an atomic in the dev browser → DOM has `data-atomic-hovered="true"` on exactly one element; leaving the wrapper clears it within 100ms.
- Console `clog("hover", ...)` namespace fires (extending the canvas-log namespaces per the institutional pattern).

---

- U2. **Hover outline + W×H tooltip pill**

**Goal:** Visible hover state — solid 2px outline (cyan-coral) on the hovered atomic, plus a floating `Name  W×H` pill positioned above the bbox.

**Requirements:** R1

**Dependencies:** U1

**Files:**
- Modify: `app/atlas/_lib/leafcanvas-v2/canvas-v2.css`
- Create: `app/atlas/_lib/leafcanvas-v2/HoverTooltip.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/HoverTooltip.vitest.ts`

**Approach:**
- CSS: `.leafcv2-frame [data-atomic-hovered="true"] { outline: 2px solid #fb923c; outline-offset: 1px; }` (orange family per Zeplin convention; placeholder hex — designer to confirm).
- `HoverTooltip.tsx`: a small absolute-positioned `<div>` at the hovered node's bbox top-edge minus tooltip-height. Reads `useHoveredAtomicChild()` + canonical_tree to look up the node. Renders `${name}  ${w}×${h}`. `pointer-events: none`.
- Mount inside `LeafFrameRenderer` return as a sibling to `<StatePicker>`. Position math reuses `positionStyle` semantics from `nodeToHTML.ts:412-435` (rebase wrapper-local).
- Edge-clamp: if tooltip would render outside the frame's top edge, flip below the bbox.
- Apply HoverSignalCard polish rules per the institutional learning: 50ms mount, 16ms reposition, 100ms unmount.

**Patterns to follow:**
- `app/atlas/_lib/leafcanvas-v2/StatePicker.tsx` (overlay precedent — pan/zoom-aware, pointer-events:none)
- `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts:412-435` (parent-rebase math)
- `docs/plans/2026-05-02-005-fix-atlas-interaction-polish-plan.md:199-219` (HoverSignalCard rules)

**Test scenarios:**
- Happy path: hover a TEXT atomic → tooltip shows `${name}  ${w}×${h}` within 50ms.
- Edge case: hover atomic at frame's top edge → tooltip flips below bbox.
- Edge case: hover atomic when nothing in canonical_tree → tooltip is null (no crash).
- Edge case: rapid hover across atomics → only one tooltip rendered at a time (no flicker).

**Verification:**
- Visual: hover a CTA atomic — see orange outline + name + dimensions floating above. Move cursor — tooltip follows the new atomic with one-frame latency.
- Vitest: positionStyle math snapshot test.

---

- U3. **`<MeasurementOverlay>` component scaffold**

**Goal:** A single overlay component that owns the canvas-overlay layer for distance lines, padding bands, gap markers, and selected-node measurement chips. Mounted once per frame as a sibling to `<StatePicker>`.

**Requirements:** R2, R3, R4, R6 (foundation)

**Dependencies:** U1

**Files:**
- Create: `app/atlas/_lib/leafcanvas-v2/MeasurementOverlay.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/canvas-v2.css`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/MeasurementOverlay.vitest.ts`

**Approach:**
- Component takes `{ frameID, wrapperRef, tree, frameBBox }`. Subscribes to `useHoveredAtomicChild()` + `useAtlas(s => s.selection.selectedAtomicChild)`.
- Renders an `<svg>` element absolute-positioned over the frame at `inset: 0`, `pointer-events: none`, `z-index: 92` (between selection outline and lasso z-index 90).
- Initially renders nothing — U4/U5/U6/U8 add content. This unit lands the component skeleton + the prop wiring + the gesture-end gate.
- Subscribes to `canvasGestureTracker.subscribe(gesturing => ...)` to suppress paint during pan/zoom; re-paints on settle.
- HMR-guarded module-level helpers (e.g., a parent-walker memoization) per institutional pattern.

**Patterns to follow:**
- `app/atlas/_lib/leafcanvas-v2/StatePicker.tsx`
- `app/atlas/_lib/leafcanvas-v2/canvas-v2.css:401-415` (selection outline classes)

**Test scenarios:**
- Happy path: component renders empty `<svg>` when no hover, no select.
- Edge case: missing `frameBBox` → component renders null.
- Integration: gesture-tracker subscription re-paints on settle (mock `canvasGestureTracker`).

**Verification:**
- DOM has `<svg class="leafcv2-measurement">` inside every `.leafcv2-frame`.
- No console errors; tsc clean.

---

- U4. **Distance lines: selected → hovered, 4-way cardinal**

**Goal:** When an atomic is selected and a different atomic is hovered, draw 4 cardinal distance lines between their bboxes with integer px labels at the midpoint of each line.

**Requirements:** R2

**Dependencies:** U2, U3

**Files:**
- Modify: `app/atlas/_lib/leafcanvas-v2/MeasurementOverlay.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/canvas-v2.css`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/MeasurementOverlay.vitest.ts` (extend)

**Approach:**
- Math: given selected bbox `S` and hovered bbox `H`, both in wrapper-local coords (rebase from `absoluteBoundingBox` minus frame's bbox origin):
  - Top distance: `S.top - H.bottom` if `H` above `S`, else `H.top - S.bottom` if below, else `0` (overlap).
  - Same for bottom/left/right.
  - Skip line where distance = 0 OR negative (overlap on that axis).
- Render: `<line>` per direction in red (`#ef4444`), 1px stroke. `<text>` element with integer label at midpoint, white text on red rounded-rect background.
- Suppress entirely when selected === hovered (no-op — Zeplin behavior).

**Patterns to follow:**
- `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts:412-435` (parent-rebase to wrapper-local)
- Zeplin's distance-line spec: 4-way cardinal, integer px, midpoint label, red.

**Test scenarios:**
- Happy path: S `{x:0,y:0,w:100,h:50}` and H `{x:0,y:100,w:100,h:50}` (vertically aligned, H below S) → exactly one line (bottom of S to top of H), distance=50, label at midpoint y=75.
- Edge case: S and H overlap on Y axis → no top/bottom line, only left/right (if applicable).
- Edge case: S === H (selected and hovered same node) → no lines rendered.
- Edge case: H is fully inside S → no lines (overlap on both axes).
- Edge case: bbox dims are floats (`width: 343.5`) → label rounds to nearest integer.

**Verification:**
- Visual: select one CTA, hover the next CTA — see 4 red lines with px labels.
- Vitest: distance computation snapshot for 6 layout configurations (above, below, left, right, overlap-Y, overlap-X).

---

- U5. **Padding bands on hovered autolayout FRAME**

**Goal:** Hover a FRAME with `layoutMode === "HORIZONTAL"|"VERTICAL"` → translucent bands appear inside the bbox at top/right/bottom/left, sized to `paddingTop/Right/Bottom/Left`, with numeric labels.

**Requirements:** R3

**Dependencies:** U1, U3

**Files:**
- Modify: `app/atlas/_lib/leafcanvas-v2/MeasurementOverlay.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/canvas-v2.css`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/MeasurementOverlay.vitest.ts` (extend)

**Approach:**
- When hovered node is `FRAME` AND `layoutMode !== "NONE"`:
  - Extract `paddingLeft/Right/Top/Bottom` from canonical_tree node (default 0 when undefined).
  - Render 4 `<rect>`s at the top/right/bottom/left edges of the bbox with the padding values as widths/heights. Fill `rgba(255, 152, 0, 0.18)` (Zeplin orange family, placeholder).
  - Render `<text>` label per band centered in the band with the numeric value.
  - Skip bands where padding is 0.

**Patterns to follow:**
- `docs/brainstorms/2026-05-05-zeplin-grade-leaf-canvas-requirements.md` D10 (padding/gap inspection)
- Browser DevTools box-model framing (Zeplin's 2024 redesign reference).

**Test scenarios:**
- Happy path: FRAME with `paddingLeft:16, paddingRight:16, paddingTop:24, paddingBottom:24` → 4 bands with correct dimensions and labels `16, 16, 24, 24`.
- Edge case: FRAME with `paddingLeft:0` → only 3 bands rendered, no zero-band.
- Edge case: FRAME with `layoutMode: "NONE"` → no bands.
- Edge case: hovered atomic is NOT a FRAME → no bands.

**Verification:**
- Visual: hover a card-with-padding container → see 4 orange bands inside the bbox edges with numbers.
- Vitest: snapshot of band rendering for `padding: {16, 16, 24, 24}` and `padding: {0, 16, 0, 16}`.

---

- U6. **Gap markers between siblings of hovered autolayout FRAME**

**Goal:** Hover a FRAME with multiple children and `itemSpacing > 0` → colored bands between siblings showing the gap, with numeric labels. Direct-on-canvas, automatic on parent hover (Dev Mode pattern, not Zeplin's panel-mediated pattern).

**Requirements:** R4

**Dependencies:** U1, U3, U5

**Files:**
- Modify: `app/atlas/_lib/leafcanvas-v2/MeasurementOverlay.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/canvas-v2.css`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/MeasurementOverlay.vitest.ts` (extend)

**Approach:**
- When hovered node is `FRAME` with `layoutMode === "HORIZONTAL"|"VERTICAL"` AND `children.length >= 2` AND `itemSpacing > 0`:
  - For each pair of consecutive children, compute the gap zone:
    - `HORIZONTAL` mode: zone is between `child[i].right` and `child[i+1].left`, full height of inner content (after padding).
    - `VERTICAL` mode: zone is between `child[i].bottom` and `child[i+1].top`, full width of inner content.
  - Render `<rect>` per gap with fill `rgba(236, 72, 153, 0.22)` (Dev Mode pink family, placeholder).
  - Render `<text>` label centered in the gap with `itemSpacing` numeric value.
- Skip when `primaryAxisAlignItems === "SPACE_BETWEEN"` (gap is dynamic; itemSpacing is unused). For SPACE_BETWEEN, defer; surface as a "Variable gap" badge in v2.1.

**Patterns to follow:**
- `app/atlas/_lib/leafcanvas-v2/types.ts:145-203` (`layoutMode`, `itemSpacing`, `primaryAxisAlignItems`)

**Test scenarios:**
- Happy path: HORIZONTAL FRAME with 3 children, itemSpacing 12 → 2 pink bands with label "12" each.
- Edge case: VERTICAL FRAME with 2 children, itemSpacing 8 → 1 pink band labeled "8".
- Edge case: itemSpacing 0 → no bands.
- Edge case: only 1 child → no bands (no pairs).
- Edge case: SPACE_BETWEEN primaryAxisAlignItems → no bands; surface "Variable gap" badge.

**Verification:**
- Visual: hover an autolayout list → see pink bands between rows with the gap numeric.
- Vitest: snapshot for HORIZONTAL/VERTICAL/SPACE_BETWEEN configurations.

---

- U7. **Camera snap on selected — keyboard + inspector button**

**Goal:** Trigger camera animation (320ms easeInOutCubic, pan+zoom-to-fit, 40px inset, cap 100% zoom) on `Shift+2` keystroke OR a "Scroll into view" button click in `AtomicChildInspector`. Pure rAF, no spring, no auto-on-canvas-click.

**Requirements:** R7

**Dependencies:** None (independent of overlay work)

**Files:**
- Create: `app/atlas/_lib/leafcanvas-v2/camera-snap.ts`
- Modify: `app/atlas/_lib/leafcanvas.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/AtomicChildInspector.tsx`
- Modify: `app/atlas/_lib/AtlasShellInner.tsx`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/camera-snap.vitest.ts`

**Approach:**
- New `camera-snap.ts`: pure helper `animateCamera(camRef, fromCam, toCam, durationMs, onTick, onDone)`.
  - rAF loop interpolating `[x, y, z]` via `easeInOutCubic`: `t < 0.5 ? 4*t*t*t : (t-1)*(2*t-2)*(2*t-2) + 1`.
  - On each frame: write `camRef.current = lerped`; call `onTick()` (which the caller wires to `applyCameraToDOM` in leafcanvas.tsx).
  - `cancelToken` returned; any pointer/wheel event during animation calls cancel → animation aborts at current values, no abrupt snap.
- New helper `computeFitCamera(bbox, viewport, padding=40, maxZoom=1.0)`:
  - `zoom = min((vp.w - 2*pad) / bbox.w, (vp.h - 2*pad) / bbox.h)` clamped to `[MIN_ZOOM, maxZoom]`.
  - `camX = bbox.x + bbox.w / 2`, `camY = bbox.y + bbox.h / 2` (center of bbox in scene coords).
- In `leafcanvas.tsx`: expose `snapToBBox(bbox)` callback via the `LeafCanvasContext` (or props). Wire pointer/wheel handlers to `cancelToken.cancel()` during ongoing snap.
- In `AtlasShellInner.tsx`: `Shift+2` keyboard handler → if `selection.selectedAtomicChild` set, look up bbox from canonical_tree, call `snapToBBox`.
- In `AtomicChildInspector.tsx`: add a "Scroll into view" button in the layer-info section → same snap call.

**Patterns to follow:**
- `app/atlas/_lib/leafcanvas.tsx:211-247` (auto-fit `useLayoutEffect` — reference for camera math + RAF flush sequencing)
- tldraw `easeInOutCubic` formula (verified in research)
- Excalidraw `actionCanvas.tsx` zoom-fit math (verified in research)

**Test scenarios:**
- Happy path: bbox 200×200 in viewport 800×600, padding 40 → newZoom = min((800-80)/200, (600-80)/200) = min(3.6, 2.6) = 2.6, capped to 1.0. camX/camY centered.
- Edge case: bbox is full viewport size → newZoom hits the cap exactly at 1.0.
- Edge case: bbox is tiny (10×10) → newZoom capped at 1.0, not 80×.
- Edge case: bbox dimensions 0 → returns null camera (no-op).
- Integration: `animateCamera` ticks 60×/sec for 320ms → ~19-20 frames; tEnd = 1.0 → onDone fires.
- Integration: cancelToken cancels mid-animation → camera state stays at the last lerped values, no snap-back.
- Integration: `Shift+2` with no selection → no-op (don't crash).

**Verification:**
- Manual: select a small CTA on a screen, press `Shift+2` — camera smoothly pans + zooms to fit it. Press `Shift+2` again on a different selection — re-animates. Pan during animation — animation aborts.
- Vitest: pure-function tests on `computeFitCamera` and `easeInOutCubic` curve sampling.

---

- U8. **W×H + position chips on selected node**

**Goal:** Persistent floating chips next to the selected node showing its W×H + (X, Y) position from `absoluteBoundingBox`. Distinct from the hover tooltip (U2) — these stay visible while selection is active.

**Requirements:** R6

**Dependencies:** U3

**Files:**
- Modify: `app/atlas/_lib/leafcanvas-v2/MeasurementOverlay.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/canvas-v2.css`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/MeasurementOverlay.vitest.ts` (extend)

**Approach:**
- When `selection.selectedAtomicChild` is set, render a small `<text>` label at the bottom-right corner of the selected bbox showing `${w}×${h}` (and `(x, y)` if there's room).
- White text on blue rounded-rect background, matching the selection outline blue.
- Position math: bbox bottom-right + 4px offset; clamp inside frame bounds.
- No tooltip when nothing selected.

**Test scenarios:**
- Happy path: selected bbox 343×56 at (16, 100) → chip "343×56" at (343+16+4, 56+100+4) wrapper-local.
- Edge case: selected bbox at frame edge → chip flips above-left to stay inside.
- Edge case: hover and selected on same node → both chips render (selection wins on outline color).

**Verification:**
- Visual: click a button → "343×56" chip appears below-right of the outline; remains visible until Esc.

---

- U9. **Esc layered close: hover → atomic → frame → leaf**

**Goal:** Extend the Esc layered close from session 2026-05-08 (`atomic → frame → leaf`) to handle hover as the innermost layer.

**Requirements:** R8

**Dependencies:** U1

**Files:**
- Modify: `app/atlas/_lib/AtlasShellInner.tsx`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/hover-signal.vitest.ts` (extend)

**Approach:**
- In `AtlasShellInner.tsx` Esc handler (the existing layered close):
  1. If hover state set → clear hover, return.
  2. If `selectedAtomicChild` set → clear, return.
  3. If `selectedFrameID` set → clear, return.
  4. Else → close leaf.
- Hover clear is the cheapest layer; consumes the keystroke first.

**Test scenarios:**
- Happy path: hover atomic A, Esc → hover cleared, atom-selected unchanged.
- Happy path: select atomic B, hover atomic A, Esc once → hover cleared. Esc again → selection cleared. Esc again → close leaf.
- Edge case: no hover, no selection → Esc closes leaf immediately.

**Verification:**
- Manual: stack hover + selection + frame focus + leaf open. 4 Esc presses unwind cleanly.

---

- U10. **Inspector ↔ canvas hover binding**

**Goal:** Hover a Layout Widget row in `AtomicChildInspector` → corresponding band lights up on canvas (and vice versa). Symmetric two-way binding via the module-level hover signal from U1.

**Requirements:** R9

**Dependencies:** U1, U5, U6

**Files:**
- Modify: `app/atlas/_lib/leafcanvas-v2/AtomicChildInspector.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/MeasurementOverlay.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/hover-signal.ts` (extend signal shape)
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/AtomicChildInspector.vitest.ts` (extend)

**Approach:**
- Extend `hover-signal.ts` with a second axis: `setHoveredBandHint({nodeID, band: "padding-top" | "padding-right" | ...} | null)`. Independent from `setHoveredAtomicChild`.
- In `AtomicChildInspector.tsx`, the Layout Widget rows (`paddingTop`, `paddingRight`, ..., `gap`):
  - On `onMouseEnter` → `setHoveredBandHint({...})`.
  - On `onMouseLeave` → clear hint.
- In `MeasurementOverlay.tsx`, when a band hint matches a band currently being rendered (U5/U6), boost its opacity / change its outline (e.g., 1px solid border on the band).
- Conversely: when `MeasurementOverlay` shows a band on hover, also push the hint so the inspector row highlights.

**Test scenarios:**
- Happy path: hover `paddingTop` row in inspector → corresponding canvas band gets boosted highlight.
- Happy path: hover canvas top-padding band → inspector `paddingTop` row gets background highlight.
- Edge case: hover inspector row when band isn't visible (e.g., padding=0 → band not rendered) → no-op, no error.
- Edge case: rapid hover across rows → only one band highlight at a time.

**Verification:**
- Manual: select a card, then hover `paddingTop: 24` in the inspector — the top band on canvas gets brighter outline.

---

## System-Wide Impact

- **Interaction graph:** Phase 2 adds two new gesture surfaces (hover + camera snap) atop existing click / lasso / contenteditable. Composition rule per institutional learning: single owner per element per moment. The `<MeasurementOverlay>` owns hover/distance/padding/gap rendering; the painter useEffects (U1/U8) own DOM tagging on `data-atomic-hovered/selected`; `<HoverTooltip>` owns the floating pill. No two systems write to the same DOM node concurrently.
- **Error propagation:** Hover signal failures are non-fatal — clearing the signal on missing canonical_tree keeps the canvas usable. Camera snap failures (e.g., bbox is null, viewport size is 0) bail to no-op without throwing.
- **State lifecycle risks:** The module-level hover signal must be cleared on leaf close to avoid stale state when reopening a different leaf. Add a `clearHover()` call in `closeLeaf` flow (`live-store.ts::closeLeaf`).
- **API surface parity:** No backend changes. No new endpoints. `canonical_tree` already populated for every screen post the 2026-05-08 re-sync.
- **Integration coverage:** Hover + select + lasso + camera-snap need a Playwright integration test asserting they don't deadlock each other (e.g., active lasso + Esc + hover sequence).
- **Unchanged invariants:** The existing click → atomic-select → inspector flow stays untouched. Lasso + bulk export untouched. State picker untouched. Inline text editor untouched. Pre-2026-05-08 selection state shape (`selection.selectedAtomicChild`) unchanged.

---

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Hover-signal churn re-renders the inspector (60×/sec during pointer-move) | Inspector subscribes to a hint axis (band-only), not the raw hover signal. Hover signal also de-dups (only fires on changed value). Module-level pub/sub avoids React reconciliation. |
| Distance lines jitter under canvas pan/zoom | All overlay coords are wrapper-local; world transform on `.lc-world` carries them automatically. Gate paint on `!getIsGesturing()`; re-paint on settle. |
| Padding/gap bands clip outside frame chrome | Render inside `.leafcv2-frame` which has `overflow: hidden`. Bands inside paddings of the frame fall inside the bbox by definition. Test on small frames (e.g., 100×100). |
| Camera snap fights an active drag | Pointer/wheel events during snap call `cancelToken.cancel()` → snap aborts at current lerped values. No snap-back. Manual QA: drag-while-snapping should feel like a hand grabbing the canvas mid-flight. |
| `Shift+2` collides with browser shortcuts on some platforms | Use `e.preventDefault()` on the keydown when handling. If ambiguity persists, expose the "Scroll into view" button as the primary surface and treat the keystroke as secondary. |
| `easeInOutCubic` over 320ms feels too slow / too fast in production | Tunable via constant in `camera-snap.ts`. Default to 320ms (tldraw default); ship a designer-driven QA pass post-Phase 2. |
| Browser flexbox vs Figma 2px drift confuses measurement labels | Source numbers from `canonical_tree.absoluteBoundingBox` and layout fields, not `getBoundingClientRect()`. Document this in the inspector copy ("design value, not rendered value") in a follow-up polish pass. |
| Module-level hover signal leaks across HMR | HMR-guard with `globalThis.__lcHoverWired` per institutional pattern. Tests have `beforeEach` cleanup. |

---

## Documentation / Operational Notes

- Capture Phase 2 learnings to `docs/solutions/2026-05-09-001-zeplin-canvas-phase2-learnings.md` after merge.
- Update `docs/brainstorms/2026-05-05-zeplin-grade-leaf-canvas-requirements.md` D10 section with a "Phase 2 shipped" callout.
- Add `clog("hover", ...)`, `clog("measure", ...)`, `clog("camera-snap", ...)` namespaces to `canvas-log.ts` before writing any overlay logic — institutional learning.
- Production rollout: Phase 2 is feature-flag-friendly via `NEXT_PUBLIC_LEAFCANVAS_V2_INSPECT=1`. Default off in v0.1 of this phase; flip on after a designer QA pass on 5 representative screens (1 simple form, 1 chart-heavy, 1 illustration-heavy, 1 dense list, 1 overlay/bottomsheet).

---

## Sources & References

- **Origin document:** [docs/brainstorms/2026-05-05-zeplin-grade-leaf-canvas-requirements.md](docs/brainstorms/2026-05-05-zeplin-grade-leaf-canvas-requirements.md)
- **Phase 1 plan:** [docs/plans/2026-05-05-002-feat-zeplin-grade-leaf-canvas-plan.md](docs/plans/2026-05-05-002-feat-zeplin-grade-leaf-canvas-plan.md)
- **Phase 1 learnings:** [docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md](docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md)
- **Camera footgun (IO + transform-parent):** [docs/plans/2026-05-06-002-fix-canvas-refresh-evidence-driven-debug-plan.md](docs/plans/2026-05-06-002-fix-canvas-refresh-evidence-driven-debug-plan.md)
- **HoverSignalCard polish:** [docs/plans/2026-05-02-005-fix-atlas-interaction-polish-plan.md](docs/plans/2026-05-02-005-fix-atlas-interaction-polish-plan.md)
- **HMR + gesture-tracker hardening:** [docs/plans/2026-05-06-003-fix-canvas-prerender-review-remediation-plan.md](docs/plans/2026-05-06-003-fix-canvas-prerender-review-remediation-plan.md)
- **External — camera snap:**
  - [tldraw `easeInOutCubic` source](https://github.com/tldraw/tldraw/blob/main/packages/editor/src/lib/primitives/easings.ts)
  - [tldraw camera animation docs](https://tldraw.dev/sdk-features/camera)
  - [Excalidraw `actionCanvas.tsx`](https://github.com/excalidraw/excalidraw/blob/master/packages/excalidraw/actions/actionCanvas.tsx)
  - [Figma `figma.viewport` Plugin API](https://developers.figma.com/docs/plugins/api/figma-viewport/)
- **External — Zeplin inspect:**
  - [Zeplin Highlights — Help Center](https://support.zeplin.io/en/articles/5465721-zeplin-highlights)
  - [Inspecting Auto Layout properties](https://support.zeplin.io/en/articles/5356280-inspecting-auto-layout-properties)
  - [Measuring Distances in Percentage Units](https://medium.com/zeplin-gazette/measuring-distances-in-percentage-units-in-zeplin-4fc7756ecb68)
  - [LogRocket: Zeplin vs Figma Dev Mode hands-on](https://blog.logrocket.com/ux-design/zeplin-design-handoff-figma-dev-mode/)
- Related code: `app/atlas/_lib/leafcanvas-v2/` (entire folder is the surface)
- Related session: 2026-05-08 hover-signal precedent (`data-atomic-selected`)
