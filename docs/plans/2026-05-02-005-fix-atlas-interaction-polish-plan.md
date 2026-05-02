---
title: "fix: Atlas interaction polish — click orchestration, hover positioning, label centering, scene tokens, illumination tuning + UX principles"
type: fix
status: active
date: 2026-05-02
origin: docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md
---

# Atlas interaction polish — click orchestration, hover positioning, label centering, scene tokens, illumination tuning + UX principles

## Overview

After the integration-seams plan (`docs/plans/2026-05-02-003-feat-atlas-integration-seams-plan.md`) shipped 14+ units, the user opened `/atlas` and reported five concrete defects plus a perspective shift:

> *"hover and click interaction of the atlas is broken when clicking on them it glitches into something loading zooming into the mind graph and labels are left aligned the hover pop up is on top left stuck there. whole thing is broken. background colour of 3js bg is still blue and reduce illumination of nodes by 50% also go deeper into mind graph code from the perspective of a UI designer and then think like a user who is a CEO or a product manager."*

This plan addresses each defect surgically and codifies the design heuristic that should have prevented them. The mind-graph render engine (`BrainGraph.tsx` at 682 lines + `LeafLabelLayer.tsx`, `HoverSignalCard.tsx`, `SignalAnimationLayer.tsx`, `edgePulseShader.ts`) is **kept**. What this plan changes is the orchestration of existing animation systems on click, the projection math for hover/label DOM elements, the hardcoded scene constants, and the UX principles that frame future tuning.

The plan is **post-shipping bug-fix work**, not net-new feature. There are 3 outstanding polish tasks (U14c/U14d/U14e from the transition audit) tracked separately; this plan does not duplicate them — referenced where relevant.

---

## Problem Frame

The mind graph is the brainstorm's **navigator + reverse-lookup atlas** (R20, R22, AE-5, AE-8). Two user lenses must be served:

- **CEO opens `/atlas`** asking "what's our system?" — needs at-a-glance comprehension. Visual hierarchy matters. Excessive bloom and dazzle reduce comprehension by overwhelming the attention budget. Subtle illumination differentiates products from leaves; over-illumination makes everything fight for the eye.
- **PM opens `/atlas`** after a Slack ping asking "where does this flow live and what's broken?" — needs precision pointing. Hover cards must land on the cursor; click must feel deterministic; labels must align with their nodes. A glitchy click sequence destroys trust in the surface as a navigation tool.

Current implementation skews toward eye-candy. The five defects all degrade either comprehension or precision:

1. Click-glitch destroys click-to-navigate reliability (precision)
2. HoverSignalCard stuck top-left destroys hover-to-inspect reliability (precision)
3. Labels left-aligned destroys spatial mapping between text and node (precision)
4. Hardcoded blue background shows the surface ignores the brand's theme (comprehension — feels off-brand)
5. Over-bright nodes reduce visual hierarchy (comprehension — everything glows equally to the eye)

The perspective shift is to lock in a written design heuristic so future polish stays oriented toward comprehension + precision rather than drift toward more dazzle.

---

## Requirements Trace

- R1. Click on a flow leaf in `/atlas` produces exactly one orchestrated transition (camera spring + view-transition morph) with no flicker, jank, or "loading" placeholder visible mid-flight. Covers AE-8.
- R2. Click on a product or folder produces exactly one camera-spring zoom with no morph attempted (the morph belongs only to leaf clicks). Covers F8 step 4.
- R3. `HoverSignalCard` renders adjacent to the hovered node's screen-space position within ±4 px. Hover-out unmounts the card. Covers AE-5.
- R4. `LeafLabelLayer` labels are visually centered on their nodes (text center matches projected node center within ±2 px in both axes). Covers R20 visual hierarchy.
- R5. The Three.js renderer's clear color is theme-aware via `var(--bg-canvas)` (or a clean fallback) — no hardcoded `#000814`. Covers R27 "shared three.js + r3f render pipeline" + the user's theme-respect ask.
- R6. Per-type emissive intensity is halved across the board (products 3.5 → 1.75; folders 1.8 → 0.9; flows 1.2 → 0.6; components/personas 1.0 → 0.5; tokens/decisions 0.8 → 0.4). Bloom strength reduced from current 1.2 → ~0.6 to match. Covers R20 visual hierarchy + the user's "Obsidian-grade feel needs less dazzle."
- R7. New `docs/runbooks/atlas-ux-principles.md` codifies the comprehension+precision design heuristic with named user lenses (CEO, PM, designer) and concrete tuning rules. Future units consult it before re-tuning illumination, animation duration, or interaction feedback. Carries forward A1, A3, A4 from origin.

**Origin actors:** A1 (Designer, in-product), A3 (DS lead — admin), A4 (PM), A5 (Engineer — JSON tab consumer; relevant only as comprehension audience).
**Origin flows:** F8 (Browse the mind graph).
**Origin acceptance examples:** AE-5 (mind graph reverse lookup hover card), AE-8 (mind graph → flow morph).

---

## Scope Boundaries

- **Engine stays.** The 682-line `app/atlas/BrainGraph.tsx` orchestration logic, the `react-force-graph-3d` library binding, the `useGraphView` state machine, `cull.ts` LOD, `edgePulseShader.ts` shader injection, and `SignalAnimationLayer.tsx` particle pool are **not refactored**. We adjust orchestration *timing* and *constants*, not the architecture.
- **No new animation libraries.** The U10 motion grammar (react-spring/three for triggered 3D, GSAP for sequenced DOM, useFrame+damp for ambient) is the codified convention; this plan stays inside it.
- **Esc reverse-morph stays as-is.** U3's receiving end + the followup `?from=` URL rewrite (commit `7994956`) shipped today; this plan does not touch them.
- **Project view (`/projects/[slug]`) is out of scope** — ProjectShell, ProjectToolbar, AtlasCanvas, the tabs are unchanged. Only `/atlas` itself.
- **Token-in-URL screen PNG/KTX2 fix (commit `7a847d9`) is out of scope** — that's the screen-asset auth issue; orthogonal to interaction polish.

### Deferred to Follow-Up Work

- **U14c (CSS easing → canonical constants)**, **U14d (reduced-motion gates on 4 CSS transitions)**, **U14e (document inbox-vs-violations exit intent)** — already tracked from the U13 transition audit; not duplicated here.
- **Comprehensive selective bloom (`@react-three/postprocessing` `<EffectComposer>` adapter for `react-force-graph-3d`'s owned composer)** — flagged in U8's deferred-impl note; out of scope until a Phase 10 effort is scoped.
- **Mind graph at 1000+ nodes (LOD performance ceiling)** — origin Open Question 6; deferred until real-world node counts demand it.

---

## Context & Research

### Relevant Code and Patterns

- `app/atlas/BrainGraph.tsx` `handleNodeClick` (search by symbol; not stable line numbers post-merge) — currently dispatches `view.morphTo(node)` for flow nodes and `view.focus(node)` + camera spring for product/folder. The U10 spring + U2b view-transition + U14a bloom-build-up potentially fire in overlapping windows on the same click. Investigation in U1.
- `app/atlas/BrainGraph.tsx` `nodeThreeObject` factory — material color × `emissiveIntensity`; tone-mapping disabled. U4 halves intensity here.
- `app/atlas/BrainGraph.tsx:674` `backgroundColor={BACKGROUND_COLOR}` — passes the hardcoded constant to `<ForceGraph3D>`. The library writes this to `THREE.Scene.background` via its renderer config. U4 replaces with theme-aware token resolution.
- `app/atlas/forceConfig.ts:46-87` `NODE_VISUAL` map — current emissiveIntensity values (3.5, 1.8, 1.2, 1.0, 1.0, 0.8, 0.8). U4 halves all values.
- `app/atlas/forceConfig.ts:102` `BACKGROUND_COLOR = "#000814"` — hardcoded. U4 replaces with a function reading `getComputedStyle(documentElement).getPropertyValue('--bg-canvas')` and falling back to `#050810` when running outside a browser context (test, SSR safety).
- `app/atlas/LeafLabelLayer.tsx` — projection writes `transform: translate3d(<x>px, <y>px, 0)` from `Vector3.project(camera)`. Need an additional `translate(-50%, -50%)` to center the label on the node center. U3 fix.
- `app/atlas/HoverSignalCard.tsx:132` — `position: fixed` plus `top` + `left` written from a hover-position prop. Either the prop is `(0, 0)` on first hover (initial state never updated) or the projected screen position is `NaN`. U2 investigation + fix.
- `app/atlas/BrainGraph.tsx` — passes hover position to HoverSignalCard via state set from `onNodeHover` callback. Verify the `onNodeHover` event payload carries the screen-position fields the library exposes (`screenX`, `screenY` or via the `cameraPosition()` projection).
- `app/atlas/view-transitions.css` — 600ms `cubic-bezier(0.87, 0, 0.13, 1)` (= `expo.inOut`). U1 may shorten this if the click-glitch root cause is the morph + spring fighting; otherwise leaves alone.
- `lib/animations/conventions.md` (U10 deliverable) — the three-tool motion grammar; U1 honors it.
- `app/globals.css` — `--bg-canvas` token shipped 2026-05-02 in commit `8d93781` (dark `#050810`, light `#0b1424`). U4 reads this token via `getComputedStyle`.

### Institutional Learnings

- `docs/solutions/2026-05-01-001-phase-6-closure.md` — `MeshBasicMaterial` × intensity convention, NOT `MeshStandardMaterial` + `emissive`. Honored in U4.
- `docs/runbooks/transitions.md` (U13 audit) — already documents that `atlasBloomBuildUp` plays on /atlas mount (U14a fix). U1 must verify this doesn't fight the click-spring + view-transition sequencing — the bloom build-up takes 800ms and a click within that window may collide.
- `docs/runbooks/playwright-coverage.md` (U11) — the AE-8 spec is `tests/projects/atlas-leaf-morph.spec.ts`, currently skip-on-empty. After U1 lands, manual verification must produce a screen recording of a leaf-click that the spec authors can use as a baseline.

### External References

None required. This plan operates inside the local code; bugs are about orchestration of already-shipped systems.

---

## Key Technical Decisions

- **Investigation precedes fix on U1.** The click-glitch has multiple plausible root causes (camera spring vs view-transition vs bloom build-up fighting; React StrictMode double-mount; LeafMorphHandoff effect race). U1's first deliverable is a written diagnosis (root cause + frame-by-frame trace) BEFORE code changes, so the fix targets the actual cause not a guess.
- **`BACKGROUND_COLOR` becomes a function, not a const.** The current `export const BACKGROUND_COLOR = "#000814"` is a static value. The fix is `export function backgroundColor(): string` that reads `getComputedStyle(document.documentElement).getPropertyValue('--bg-canvas')` at runtime. SSR-safe via `typeof window !== 'undefined'` guard returning a fallback.
- **Emissive halving lives in `forceConfig.ts`, not BrainGraph.** Single source of truth. Tuning later (e.g. quartering, doubling) is a one-file edit.
- **Bloom strength is tuned alongside emissive.** U8 set bloom strength = 1.2; U4 reduces to 0.6 to match the halved emissive. The threshold (1.0) stays — it's the gate, not the gain.
- **HoverSignalCard positioning fix uses the library's `onNodeHover(node, prevNode)` callback args + `getNodeScreenPosition(node)` if exposed.** If the library doesn't expose a project helper, fall back to the same `Vector3.project(camera)` pattern `LeafLabelLayer.tsx` uses.
- **No instrumentation in shipped build.** U1's investigation may add temporary `console.time` / `console.log` lines; those must be removed before commit.
- **`atlas-ux-principles.md` is a runbook, not a manifesto.** Concrete rules (e.g. "node radii in px should differ by ≥50% between tiers"; "bloom strength stays ≤0.7 unless explicitly designing for hero state"). Each rule traceable to a CEO/PM use case.

---

## Open Questions

### Resolved During Planning

- **Q: Should we update transitions.md (U13's audit) to add the click-glitch finding?** A: Yes — once U1 diagnoses the root cause, append a "Click-glitch findings 2026-05-02" section to `docs/runbooks/transitions.md`. Same doc, additive.
- **Q: Is the perspective-shift item one unit or interleaved?** A: One standalone unit (U5). Codifying after the fixes land lets U5 capture concrete examples ("U1 fix prevented X glitch — that's the kind of comprehension+precision we now require for any future tuning").
- **Q: Do we need a fixture-builder for Playwright coverage of the click-glitch fix?** A: No — visual recording is the gate. The existing `tests/projects/atlas-leaf-morph.spec.ts` covers the post-merge route push; the *visual quality* of the transition is qualitative and verified by screen recording, not assertion.

### Deferred to Implementation

- **Exact halved bloom strength.** Plan specifies ~0.6 as a starting point (half of current 1.2). The three.js skill review (2026-05-02) flagged 0.6 as aggressive given concurrent emissive halving — try 0.7–0.8 first; only drop to 0.6 if 0.8 still feels overdazzling. Tune visually during U4 implementation; final value lands in `BrainGraph.tsx` Bloom config.
- **Linear vs perceived 50% reduction.** The plan halves linear `emissiveIntensity` (3.5 → 1.75 etc). three.js renders in linear space and applies sRGB gamma on output, so linear halving produces `pow(0.5, 1/2.2) ≈ 0.73` perceived brightness, NOT 50%. The user's "reduce illumination 50%" likely meant perceived 50%. If visual A/B suggests the halved values are too bright, use `÷ 2.83` instead (perceived 50%): products 1.24, folders 0.64, flows 0.42, components/personas 0.35, tokens/decisions 0.28. The HDR threshold gating (only luminance > 1.0 blooms) preserves correctly with either strategy — only the perceived contrast is the question. Decide during U4 visual review.
- **Exact `BACKGROUND_COLOR` SSR fallback.** Plan specifies `#050810` (matches dark-theme `--bg-canvas`). Confirm during U4 that this matches the dark-theme `--bg-canvas` value at the time of implementation; if `globals.css` is retuned, update.
- **HoverSignalCard pointer-offset.** Should the card be exactly at the node center or offset by 12-20px so the cursor doesn't occlude it? Decide visually during U2.

---

## Implementation Units

> Sequencing rule: each unit completes a manual verification gate (recordable evidence) before the next starts. The user has been burned by claimed-complete-but-not-verified work twice in this thread; the gates are non-negotiable.

- U1. **Diagnose + fix click-on-node orchestration (precision blocker)**

**Goal:** When a user clicks a node in `/atlas`, exactly one orchestrated transition fires. For flow leaves: camera spring → view-transition morph to project view, with the source label staying readable through the snapshot (U4 LOD pin already handles this). For products/folders: camera spring zoom + child expand, no morph. No flicker, no "loading" placeholder, no overlapping animations.

**Requirements:** R1, R2.

**Dependencies:** None. (U10 spring + U2b view-transition + U14a bloom build-up are already shipped; this unit reconciles their interaction at the click moment.)

**Files:**
- Modify: `app/atlas/BrainGraph.tsx` — `handleNodeClick` (locate by name; not stable line numbers post-merge), camera spring useSpring binding, hover/morphingNode dispatch sequencing.
- Modify: `app/atlas/LeafMorphHandoff.tsx` — verify the `router.push` + `router.replace?from=<slug>` sequence is correct relative to the camera spring's `api.start()` timing.
- Modify (possibly): `app/atlas/view-transitions.css` — only if duration mismatch with the camera spring is identified as a contributor.
- Test: `tests/projects/atlas-leaf-morph.spec.ts` — extend assertions to confirm the `view-transition-name` source element is still mounted at the moment the route push fires (no early unmount race).
- Append: `docs/runbooks/transitions.md` — new section "Click-glitch findings 2026-05-02" with the diagnosis.

**Approach:**
- **Phase 1 — investigation (no code change):**
  - Reproduce the glitch on `localhost:3001/atlas` with DevTools Performance recording. Capture frame-by-frame: click → spring start → view-transition start → route push → leaf label "loading" placeholder appears (?) → settles.
  - Identify exact root cause(s). Likely candidates:
    1. Camera spring + view-transition fight: spring writes camera position WHILE view-transition is taking the source snapshot, so the snapshot captures the camera mid-zoom (looks like a glitch).
    2. LeafMorphHandoff's `router.push` fires before the camera spring has a chance to start, so the view-transition snapshots a mid-spring frame.
    3. atlasBloomBuildUp (U14a) re-fires on click because the `bloomBuildUpPlayedRef` guard misses a re-mount path.
    4. React StrictMode double-mounts LeafMorphHandoff, dispatching two router.push calls.
    5. The "loading" placeholder is the BrainGraphSkeleton from `app/atlas/page.tsx` re-rendering during a Suspense fallback inside the View Transition snapshot.
  - Document the actual cause (or causes) in `transitions.md` before coding.
- **Phase 2 — fix:**
  - For flow leaves: ensure the spring is *not* triggered (no camera move) — only the view-transition morph runs, taking the spring's current position as the static snapshot. The brainstorm calls for "shared-element morph" not "spring + morph." Camera movement on flow-click is over-design. **Concrete check:** in DevTools Performance trace, zero `addEffect` rafz spring frames between click and route push — if any spring frames fire, the snapshot captures a mid-tween camera and the user sees the "glitch."
  - For products/folders: the spring zoom stays — that IS the click affordance.
  - Verify LeafMorphHandoff doesn't double-fire under StrictMode (use a ref guard if it does).
  - If atlasBloomBuildUp re-fires: tighten the guard.
- **Phase 3 — verification:**
  - Record a 10-second screen capture: open /atlas, click a flow leaf, navigate to project, return via Esc, click again. Should be three smooth transitions with no flicker.

**Patterns to follow:**
- Existing useSpring imperative API in BrainGraph (post-U10) — `api.start({ from: <current>, to: <target> })`.
- The `atlasBloomBuildUp` `bloomBuildUpPlayedRef` ref-guard pattern (U14a) — same shape works for handleNodeClick double-fire prevention.
- `lib/animations/conventions.md` — three-tool motion grammar (springs for triggered, GSAP for sequenced, useFrame+damp for ambient).

**Test scenarios:**
- Covers AE-8. Happy path: click flow leaf "Research Product"; assert `/projects/indian-stocks-research` renders within 700ms; assert NO additional state-loading skeleton mounts during the morph window.
- Edge case: click flow leaf inside the 800ms `atlasBloomBuildUp` window — bloom-build-up does not restart; morph fires cleanly anyway.
- Edge case: click product node "Indian Stocks" — camera springs to position; no view-transition fires; no route change.
- Edge case: rapid double-click on same flow leaf — second click is debounced or no-op (spring already in flight, route already pushed).
- Edge case: React StrictMode double-mount in dev — LeafMorphHandoff fires `router.push` exactly once.
- Reduced-motion: click flow leaf → instant route swap, no spring, no morph keyframes; breadcrumb (U2c) provides spatial-continuity cue.

**Verification:**
- **Manual gate:** screen recording of three click sequences (leaf click, product click, leaf click after Esc-back) showing smooth transitions. Recording attached to the U1 commit message or PR description.
- `tests/projects/atlas-leaf-morph.spec.ts` still passes.
- `transitions.md` has a new "Click-glitch findings 2026-05-02" section documenting root cause + fix.
- **Rollback boundary:** revert handleNodeClick orchestration changes (one symbol in BrainGraph.tsx + LeafMorphHandoff.tsx tweaks). Independent of U2-U5.
- **Gate: U2 cannot start until U1's recording is captured.**

---

- U2. **Fix HoverSignalCard positioning (precision blocker)**

**Goal:** When the user hovers a node in `/atlas`, the floating signal card mounts adjacent to the cursor's screen position (or the node's projected screen position, depending on which is more stable). Hover-out unmounts the card. No "stuck top-left" failure mode.

**Requirements:** R3.

**Dependencies:** None (independent of U1 — hover and click are separate event paths).

**Files:**
- Modify: `app/atlas/HoverSignalCard.tsx` — verify `position: fixed` + `top`/`left` writes from props; confirm the hover-position prop is fed live, not just on initial render.
- Modify: `app/atlas/BrainGraph.tsx` — `onNodeHover` callback. Currently sets state with the hovered node; verify it ALSO writes the screen position from the `react-force-graph-3d`'s exposed projection helper (`getNodeScreenCoords` if available, or compute via `camera().project()`).
- Test: `tests/atlas-mind-graph.spec.ts` — extend with a hover assertion that the card's `top` style is non-zero after hovering a node in the visible viewport.

**Approach:**
- **Investigation:** open /atlas in dev, hover a node, inspect HoverSignalCard's computed style. If `top: 0; left: 0`, the prop is never updated. If `top: NaN px`, the projection math is wrong. If the card unmounts/remounts on each hover event, mount-time race.
- **Fix paths (decide based on investigation):**
  - If prop is unfed: BrainGraph's `onNodeHover` callback must write position to state. Mirror `LeafLabelLayer`'s projection math.
  - If projection is wrong: same fix as `LeafLabelLayer` — `Vector3.project(camera)` returns NDC in [-1, 1]; convert to CSS pixels via `(ndc.x + 1) / 2 * canvasWidth` and `(1 - ndc.y) / 2 * canvasHeight` (Y is flipped between NDC and DOM coords). `renderer.getSize(target)` returns CSS pixels by default in three.js r180 — **do NOT multiply by `renderer.getPixelRatio()`**; DPR only enters when reading from the framebuffer, not when projecting to DOM overlays.
  - If mount-time race: convert to a single mounted-always card that toggles `visible: false` instead of mount/unmount.
- **Pointer offset decision:** the card should sit ~16px to the right and below the cursor by default, mirrored to top-left when the cursor is in the right/bottom edge of the viewport (so the card stays on-screen). Decide exact offset visually during impl.

**Patterns to follow:**
- `app/atlas/LeafLabelLayer.tsx` — the multi-node screen-space projection pattern. Same math, single-element variant.
- `getBoundingClientRect()` for computing canvas origin if the renderer offset is non-trivial.

**Test scenarios:**
- Covers AE-5. Happy path: hover a flow node "Research Product" → card mounts within 50ms at the node's screen position (±4 px); card text shows the flow name + signal payload (severity counts, persona count, last-updated).
- Edge case: hover a node at the right edge of the viewport → card flips to the left of the cursor so it stays on-screen.
- Edge case: hover a node, move cursor to another node within 100ms → card re-positions, no flicker.
- Edge case: hover-out → card unmounts (or sets `visible: false`) within 100ms.
- Reduced-motion: card mount/unmount is instant (no fade), but positioning is identical.

**Verification:**
- **Manual gate:** screen recording showing hover sweeping across 5+ nodes with the card tracking smoothly to each.
- Playwright spec extension passes.
- **Rollback boundary:** revert HoverSignalCard prop wiring + BrainGraph onNodeHover update. Independent of U1, U3-U5.

---

- U3. **Center LeafLabelLayer labels on their nodes (precision)**

**Goal:** Each leaf label in `LeafLabelLayer.tsx` is visually centered (text center matches the projected node center within ±2 px in both axes) instead of left-aligned with the node center anchoring the label's top-left corner.

**Requirements:** R4.

**Dependencies:** None.

**Files:**
- Modify: `app/atlas/LeafLabelLayer.tsx` — the per-label `transform` style. Add `translate(-50%, -50%)` (or the equivalent pre-projection adjustment) so the label's own center anchors the projected coordinates.

**Approach:**
- Current `transform: translate3d(<x>px, <y>px, 0)` anchors top-left of the label DOM box to `(x, y)`.
- Two valid fixes:
  - CSS-only: change to `transform: translate(-50%, -50%) translate3d(<x>px, <y>px, 0)`. Works if the label has a fixed-ish width (text + padding); deals with multi-line labels naturally.
  - JS-only: compute label width/height once (via `getBoundingClientRect`) and offset the projected `(x, y)` by half. More accurate but requires re-measuring on text/font/zoom change.
- Recommend the CSS-only path; visually verify on long flow names ("Filters for Stock Screener" — 30 chars).
- Verify the `view-transition-name` from U2b doesn't break with the new transform (the View Transition snapshots the bounding rect, which centers correctly with `translate(-50%, -50%)` already applied).

**Patterns to follow:**
- The `HoverSignalCard.tsx` positioning pattern (after U2 fix) — same translate-50% anchor for cursor-tracking elements.

**Test scenarios:**
- Visual: 50 leaf labels visible, all centered on their nodes (within ±2 px). Long names (>30 chars) center correctly without overlapping the next node.
- Performance: 100 visible leaves → label-update pass under 4 ms (no regression vs U2a baseline).
- Edge case: window resize → labels reposition centered (the projection updates trigger re-paint with the new transform).
- Edge case: retina display (DPR > 1) → labels position correctly (no 2× drift).
- View Transition: clicking a centered label still produces a clean morph (the source snapshot's bounding rect captures the centered label, target snapshot of the project title also centered → smooth tween).

**Verification:**
- **Manual gate:** screen recording / screenshot showing labels visually centered on nodes in the brain view.
- `tests/projects/leaf-label-overlay.spec.ts` (U2a's test) extended to assert each visible label's center aligns with its node's projected center within ±2 px.
- **Rollback boundary:** revert the transform change in `LeafLabelLayer.tsx`. One file. Independent of U1, U2, U4, U5.

---

- U4. **Theme-aware scene background + halve emissive intensity + tune bloom (comprehension)**

**Goal:** The Three.js scene background is theme-aware (reads `--bg-canvas` token at runtime), not hardcoded `#000814`. Per-type emissive intensity is halved across `NODE_VISUAL`. Bloom strength reduces from 1.2 → ~0.6 to match. The result: the brain graph reads as on-brand and visually quieter, supporting comprehension over dazzle.

**Requirements:** R5, R6.

**Dependencies:** None.

**Files:**
- Modify: `app/atlas/forceConfig.ts:46-87` — `NODE_VISUAL` map. Halve every `emissiveIntensity`:
  - product: 3.5 → 1.75
  - folder: 1.8 → 0.9
  - flow: 1.2 → 0.6
  - persona: 1.0 → 0.5
  - component: 1.0 → 0.5
  - token: 0.8 → 0.4
  - decision: 0.8 → 0.4
- Modify: `app/atlas/forceConfig.ts:102` — replace `export const BACKGROUND_COLOR = "#000814"` with `export function backgroundColor(): string` that reads the `--bg-canvas` token at runtime via `getComputedStyle(document.documentElement)`. SSR fallback: `#050810` (matches dark-theme `--bg-canvas` from `globals.css`). Cache the value if perf-critical (it shouldn't be — called at scene-init only).
- Modify: `app/atlas/BrainGraph.tsx:674` — `backgroundColor={BACKGROUND_COLOR}` becomes `backgroundColor={backgroundColor()}`. Re-resolve when theme toggles (subscribe to a `data-theme` attribute change observer or to the existing theme-store).
- Modify: `app/atlas/BrainGraph.tsx` — Bloom config. Locate the `addPass(UnrealBloomPass(...))` from U8 (`strength=1.2, threshold=1.0, radius=0.85`). Reduce `strength` to ~0.6.
- Test: visual; no automated test required for the color change (canvas pixel sampling is brittle).

**Approach:**
- Halving is mechanical: divide each value by 2 in `forceConfig.ts`. Single edit.
- Background runtime-resolution: `getComputedStyle(document.documentElement).getPropertyValue('--bg-canvas').trim()`. Empty string fallback → `#050810`.
- Theme-toggle reactivity: `MutationObserver` on `document.documentElement` watching `attributes: true` + filter `attributeName === 'data-theme'`. On change, re-call `backgroundColor()` and write to `fgRef.current.scene().background`. Tear down observer on BrainGraph unmount.
- Bloom strength: tune visually against the halved emissive. 0.6 is a starting point; 0.5 if too bright, 0.7 if too dim.

**Patterns to follow:**
- Theme tokens already used elsewhere via `var(--bg-canvas)` in CSS (U13's tokenization commit `8d93781`). This unit moves the same pattern into JS.
- `MutationObserver` pattern from `components/files/FilesShell.tsx` (theme attribute) — verify if it's already there; if not, the pattern is well-known.

**Test scenarios:**
- Visual: halve emissive — products noticeably dimmer than current; visual hierarchy still readable (products > folders > flows > components/tokens). Side-by-side recording before/after.
- Visual: theme toggle on /atlas — scene background transitions from dark canvas to light canvas (light theme stays a deep ink per the U2c commit's `--bg-canvas` light-theme value `#0b1424`); not a hard cut.
- Edge case: SSR / server render → `backgroundColor()` returns the fallback `#050810`; no `getComputedStyle` crash.
- Edge case: token not defined (`--bg-canvas` removed from `globals.css`) → fallback `#050810` returned; no crash.
- Performance: halved bloom strength does not change frame budget (it's a postprocess pass, intensity changes are constant-time).

**Verification:**
- **Manual gate:** screen recording showing /atlas with halved illumination + theme toggle round-trip (dark → light → dark).
- `npx tsc --noEmit` clean (function signature change in `forceConfig.ts` may need type updates).
- `npm run build` succeeds.
- **Rollback boundary:** revert forceConfig.ts NODE_VISUAL + BACKGROUND_COLOR + BrainGraph bloom strength. Two files. Independent of U1, U2, U3, U5.

---

- U5. **Codify atlas-ux-principles.md (comprehension + precision design heuristic)**

**Goal:** A new runbook (`docs/runbooks/atlas-ux-principles.md`) codifies the design heuristic that should govern future `/atlas` polish. Two named user lenses (CEO, PM) plus the existing actor set (Designer, DS lead) are translated into concrete tuning rules. Each rule is traceable to a specific user need; future units consult this doc before re-tuning illumination, animation duration, hover, click, or label affordances.

**Requirements:** R7. Carries forward A1, A3, A4 from origin.

**Dependencies:** U1-U4 ideally land first so the doc can cite concrete examples ("U1 prevented X glitch — that's the precision bar").

**Files:**
- Create: `docs/runbooks/atlas-ux-principles.md`
- Optionally update: `docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md` — add a single line in R20 cross-referencing the new principles doc.

**Approach:**
- **Section 1: User lenses.** Three named perspectives:
  - **CEO** — opens /atlas to ask "what's our system?". Needs at-a-glance comprehension: visual hierarchy (products read first, then folders, then flows), no bloom dazzle that flattens the hierarchy, scene background that matches the brand.
  - **PM** — opens after Slack ping ("the Stocks Filter flow has a bug, where is it?"). Needs precision pointing: hover lands on cursor, click is deterministic, labels align with their nodes, search dims non-matches predictably.
  - **Designer (in-product)** — already covered by A1 in origin. Uses /atlas as reverse-lookup ("who else uses this Toast component?"). Needs filter chips that swap satellite-node classes cleanly.
  - **DS lead** — already covered by A3 in origin. Uses /atlas to spot org-wide patterns. Needs the same comprehension as CEO + filter chips for components/tokens/decisions.
- **Section 2: Concrete rules.** Each rule has: rule statement, why (which lens it serves), concrete number / criterion, what violating it costs.
  - Visual hierarchy: node radii differ by ≥50% between tiers (product ≥ 2× folder ≥ 1.5× flow). Bloom strength ≤0.7 unless explicitly designing a hero state. Emissive intensity ratio between top tier (product) and bottom tier (token/decision) ≥4×.
  - Animation: triggered transitions ≤700 ms. Spring physics for camera + hover; constant-velocity for ambient drift; GSAP for sequenced page choreography. No simultaneous overlapping animations on the same element (U1 is the cautionary tale).
  - Hover: card mounts within 50 ms of hover-start. Card re-positions on cursor move within 16 ms (one frame). Hover-out unmounts within 100 ms.
  - Click: exactly one orchestrated transition. Click on flow → morph; click on product/folder → camera zoom; click on satellite (component/token) → re-center graph.
  - Labels: centered on node center within ±2 px. Long names truncate via the existing toolbar styles (CSS `text-overflow: ellipsis` on a max-width container).
  - Color: scene background reads `--bg-canvas` token. Per-type colors live in `forceConfig.ts` `NODE_VISUAL`. No hardcoded hex outside that file.
  - Search: non-matches dim to 0.3 opacity. Match-count visible in the search input (currently missing — flag as a polish followup).
  - Reduced-motion: every animation collapses to instant (0 ms) under `prefers-reduced-motion: reduce`. No exception. CSS rules + JS spring `api.set()` instead of `api.start()`.
- **Section 3: Verification checklist for any future /atlas tuning.** Six-item list: hierarchy preserved, animation grammar honored, hover precision, click determinism, label centering, color tokens.

**Patterns to follow:**
- `docs/runbooks/transitions.md` (U13) — same shape: per-section audit, concrete numbers, traceable to brainstorm IDs.
- `docs/runbooks/playwright-coverage.md` (U11) — coverage matrix style.

**Test scenarios:**
- *Test expectation: documentation only* — this unit's deliverable is `atlas-ux-principles.md`. No automated tests.
- Manual review: a future contributor opens the doc and produces a concrete tuning decision (e.g. "should I bump the bloom strength to 1.0?") — the doc gives them the answer ("no — section 2 caps bloom at ≤0.7 unless designing a hero state, which this is not").

**Verification:**
- **Manual gate:** doc exists and covers the four user lenses + concrete rules + verification checklist.
- One reviewer (the user, or someone they delegate to) reads it and signs off that it accurately reflects the heuristic they wanted codified.
- **Rollback boundary:** delete the doc. No code impact.

---

## System-Wide Impact

- **Interaction graph:** `handleNodeClick` orchestration touches camera spring + view-transition + LeafMorphHandoff + atlasBloomBuildUp ref-guard. U1 reconciles them. `onNodeHover` writes both hovered-node state AND screen position to HoverSignalCard. U2 wires the second.
- **Error propagation:** `getComputedStyle` in `backgroundColor()` requires `document.documentElement` — SSR-guard via `typeof window`. View-transition mid-snapshot camera writes (the click-glitch root cause) propagate as visual artifacts, not exceptions; U1's investigation phase catches them.
- **State lifecycle risks:** MutationObserver on documentElement (U4 theme toggle) — must tear down on BrainGraph unmount or it leaks. `atlasBloomBuildUp`'s `bloomBuildUpPlayedRef` already guards re-mount; U1 may need to extend the guard pattern to LeafMorphHandoff.
- **API surface parity:** `BACKGROUND_COLOR` const → `backgroundColor()` function changes the `forceConfig.ts` export. Any consumer importing `BACKGROUND_COLOR` (only `BrainGraph.tsx:674` per current grep) updates in the same commit. Type signature change is breaking; the rename forces the call site update.
- **Integration coverage:** Click + morph + camera spring fight is exactly the "cross-layer scenarios unit tests alone won't prove." Manual gate via screen recording is required for U1 specifically.
- **Unchanged invariants:** The 682-line BrainGraph orchestration architecture, `useGraphView` state machine, `cull.ts` LOD with U4 morph-source pin, `edgePulseShader.ts` shader injection (incl. U9 depth-fade), `SignalAnimationLayer.tsx` particle pool, `LeafMorphHandoff.tsx`'s `?from=<slug>` history rewrite, U3's reverse-morph receiving end, U5's audit-state badge, U7's decision↔violation cross-link — all stay.

---

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| U1's investigation finds the click-glitch is multiple causes interacting (not one), and the fix grows from "tweak orchestration" to "rewrite handleNodeClick" | Time-box investigation to 2 hours of frame-by-frame trace. If root cause is multiple-cause interaction, surface a follow-up plan rather than scope-creep U1. |
| Removing the camera spring on flow-leaf click feels like a regression to users who liked the spring zoom | Document the change in U1's `transitions.md` append section: "click on flow leaf is morph-only, click on product is spring-only — separate affordances, by design." If users prefer spring-on-leaf, restore via a follow-up unit + tune the timing so it doesn't fight the morph. |
| `getComputedStyle` returning empty string for `--bg-canvas` (token unset) → black-on-black brain | Hard-coded fallback `#050810` covers this. Test scenario explicitly verifies the fallback path. |
| Halved bloom strength makes the brain look flat | Tune visually. The 0.6 starting point is a hypothesis; visual A/B is the validator. Plan accepts that the final value may be 0.5 or 0.7, decided in U4 implementation. |
| MutationObserver on documentElement (U4 theme toggle) leaks if BrainGraph re-mounts under StrictMode without proper teardown | Standard `useEffect` cleanup pattern; verify in U4 implementation with React DevTools profiler showing one observer per mount. |
| HoverSignalCard projection math depends on `react-force-graph-3d` exposing the renderer/camera; if the library changes API in a future minor version, breaks | Pin `react-force-graph-3d` exact version in `package.json` (verify it's already pinned; if not, add to U2's deferred-impl notes). Same risk applies to U2a + U2b which already depend on the same accessors. |
| `atlas-ux-principles.md` becomes stale within a phase or two as the design evolves | Doc carries an explicit "Last reviewed" date in its frontmatter. Future units that re-tune brain visuals must update the date and re-confirm the rule still applies, mirroring `transitions.md`'s convention. |

---

## Documentation / Operational Notes

- **`docs/runbooks/transitions.md`** (existing, U13) — appends a "Click-glitch findings 2026-05-02" section in U1.
- **`docs/runbooks/atlas-ux-principles.md`** (new) — U5 deliverable.
- **`docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md`** (origin) — optionally adds a single line in R20 cross-referencing the new principles doc. Optional, not required.
- **No deploy / migration / SSE-channel changes.** Pure frontend + one runbook. Vercel auto-redeploys on push.
- **No telemetry changes.** The visual quality of the click sequence is the quality bar; no metric to instrument.

---

## Sources & References

- **Origin document:** [docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md](../brainstorms/2026-04-29-projects-flow-atlas-requirements.md) — R20 (mind graph), R22 (comments), R27 (shared render pipeline + transitions ~600ms), AE-5 (hover reverse lookup), AE-8 (mind graph → flow morph), F8 (browse mind graph).
- **Prior plan (this is the post-shipping polish round):** [docs/plans/2026-05-02-003-feat-atlas-integration-seams-plan.md](2026-05-02-003-feat-atlas-integration-seams-plan.md) — U1-U14b shipped today.
- **Transition audit (U13):** [docs/runbooks/transitions.md](../runbooks/transitions.md).
- **Playwright coverage (U11):** [docs/runbooks/playwright-coverage.md](../runbooks/playwright-coverage.md).
- **Motion grammar (U10):** [lib/animations/conventions.md](../../lib/animations/conventions.md).
- **Phase 6 closure (Mind graph engine):** [docs/solutions/2026-05-01-001-phase-6-closure.md](../solutions/2026-05-01-001-phase-6-closure.md) — `MeshBasicMaterial` × intensity convention; canonical reduced-motion hook location.
- **Recent commits forming the surface this plan polishes:**
  - `e1ded7c` U8 per-type emissive bloom hierarchy (this plan halves the values)
  - `ece98ef` U10 unified motion grammar — react-spring/three (this plan reconciles spring + view-transition timing)
  - `bf04e63` U2b view-transition-name CSS wiring (this plan investigates whether 600ms duration is the click-glitch contributor)
  - `1be1f2b` U14a atlasBloomBuildUp on /atlas mount (this plan investigates whether bloom build-up + click overlap)
  - `8d93781` --bg-canvas token (this plan moves the dependency from CSS to JS via `getComputedStyle`)
