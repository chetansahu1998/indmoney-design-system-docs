---
title: "feat: Atlas integration seams — cross-route morph, two-phase UI, visual hierarchy, motion grammar"
type: feat
status: active
date: 2026-05-02
origin: docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md
---

# Atlas integration seams — cross-route morph, two-phase UI, visual hierarchy, motion grammar

## Overview

The Atlas mind-graph engine and the project-view atlas canvas are individually well-built. The 682-line `app/atlas/BrainGraph.tsx` is real three.js + r3f with InstancedMesh particle pools, edge-pulse shaders, organic drift, and proper material caching. `components/projects/atlas/AtlasCanvas.tsx` ships LOD + texture cache with frames at preserved Figma (x, y). The data layer (`useGraphAggregate`, `useGraphView`, `useSignalHold`, `cull.ts`, `forceConfig.ts`) is solid.

What is broken is the **integration choreography between these surfaces** plus a few **missing UX touches** on tabs and visual hierarchy. Specifically:

1. The signature flow-leaf → project view morph (R27, AE-8) is half-wired. `app/atlas/LeafMorphHandoff.tsx` mounts a Framer Motion `motion.div` with `layoutId={`flow-${node.id}-label`}` but Framer's `layoutId` does **not** bridge Next.js App Router route changes (vercel/next.js#49279, still open in 2026). The target side has no matching anchor either — `components/projects/ProjectToolbar.tsx:92-94` renders the title without any morph hook, and `components/projects/ProjectShell.tsx` doesn't pass `flowName` to it. Result: every flow-leaf click collapses into a pop-in, not a morph.
2. There is no Esc / back reverse-morph (F8 step 6). `ProjectShell.tsx` has no Escape keydown handler.
3. The two-phase pipeline (R3, F2) does not manifest visually — `ProjectShellLoader.tsx` does one fetch and renders. There is no `view_ready` vs `audit_complete` progression even though `lib/projects/client.ts:321-411` already subscribes to those events.
4. Bloom is uniform across all glowing types (`BrainGraph.tsx:204-210` — single `UnrealBloomPass` strength 0.6, threshold 0.4). The brainstorm wants per-type emissive hierarchy (products glow brightest). Edges don't depth-fade.
5. Motion grammar is fragmented — GSAP timelines (4 files in `lib/animations/timelines/`), Framer Motion `layoutId` opacity transitions, raw `useFrame` lerps with constant velocity (SignalAnimationLayer particle approach), `Math.sin` organic drift, custom rAF camera dollies. No spring physics, four different easing families.
6. Verification harness — `tests/atlas-mind-graph.spec.ts` exists but the brainstorm's acceptance examples (AE-1 through AE-8) are not all covered, and the spec is opt-in until CI wires WebGL2.

This plan fixes the seams without rewriting the engine. The 682-line BrainGraph stays. AtlasCanvas + LOD + texture cache stay. The data layer stays. What changes is the cross-surface choreography, visual hierarchy, and tab progression.

**The user explicitly chose the View Transitions API for the cross-route morph. That decision is locked.** Implementation note: React 19.2.4 stable does **not** export a `<ViewTransition>` component (that lives in React Canary). We use the **browser-native** View Transitions API directly: `view-transition-name` CSS property on both source and target DOM elements + Next.js 16.2 `experimental.viewTransition: true` which auto-wraps router transitions in `document.startViewTransition()`. No React component import; just CSS + the Next config flag.

Items B (auto-fix + lifecycle) and F (decisions tab) have many shipped components but **also have specific gaps surfaced during plan review**: `LifecycleButtons.tsx` lacks the Reactivate action (R8 mandates it; admin reactivate ships in `commit 941464d` Phase 5.3 backend but no per-row UX); `CategoryFilterChips.tsx` has zero persona or theme dimension (R14 explicitly requires "filtered by active persona × theme"); `DecisionsTab.tsx` lacks a Linked Violations subsection on cards (AE-4); the Violations → Decisions tab cross-link does not exist. These are net-new wiring, not just polish — surfaced inline in U6/U7 below.

---

## Problem Frame

The brainstorm's product identity (`docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md`) frames Projects as a four-lens system over a unified design knowledge graph. The mind graph (R20) is the navigator; the project view (R12) is where work happens. R27 explicitly specifies how they hand off: "shared-element morphs at ~600ms (Framer Motion `layoutId` for label/title hand-off, r3f scene crossfade)."

Phase 6 closure (`docs/solutions/2026-05-01-001-phase-6-closure.md`) shipped the mind-graph but the receiving side of the morph was never built. Phase 5/5.1/5.2 closure docs cover the project shell, tabs, decisions, and DRD — but the morph integration was deferred. As a result, the most demonstrable user interaction in the entire product (AE-8: "click the leaf, the brain dissolves, the canvas + tabs render behind") visibly fails.

The two-phase pipeline (R3, F2) was also deferred at the UI layer — the backend emits `project.view_ready` and `project.audit_complete` events, the SSE client subscribes to them (`lib/projects/client.ts:365-371`), but the project view does not render distinct visual states for "canvas ready, audit running" vs "audit complete." Users see blank → fully loaded.

The visual hierarchy work (selective bloom + edge depth-fade) and motion grammar consolidation are quality-tier improvements that distinguish "Obsidian-grade" from "we shipped three.js." They are explicitly called out in the brainstorm (R20 "subtle organic drift," R27 "shared three.js + r3f render pipeline ... same shader chain (bloom postprocessing, depth-of-field)") but were never tuned.

---

## Requirements Trace

- R1. **Cross-route morph contract via View Transitions API.** Replace the broken Framer `layoutId` hand-off with React 19 `<ViewTransition name={`flow-${slug}-label`}>` on both /atlas (source) and /projects/[slug] (target). Source: brainstorm R27 + F8 step 5 + AE-8.
- R2. **Esc / back reverse-morph.** Pressing Escape (or browser back) in the project view returns to /atlas with the source node re-focused at the same zoom level. Source: brainstorm F8 step 6.
- R3. **Two-phase UI progression.** Project view renders `view_ready` (canvas + tab shell, audit running spinner, violation count = 0) and progressively updates as `audit_progress` events arrive, settling at `audit_complete` (final counts). Source: brainstorm R3 + F2.
- R4. **Per-node-type emissive hierarchy.** Products bloom strongest, folders/flows dimmer, components/tokens/decisions only when their filter is on. Single `<Bloom luminanceThreshold={1}>` driven by per-material `emissiveIntensity`. Source: brainstorm R20 + AE-5.
- R5. **Edge depth-fade.** Edges fade with z-distance from the camera (DoF analog without a full DoF pass). Source: brainstorm R20 + R27.
- R6. **Unified motion grammar.** Install `@react-spring/three` for triggered 3D transitions (camera dolly, hover scale, particle convergence). Retain GSAP for sequenced DOM choreography (project shell entrance, tab switch). Standardize easing on a single curve family for camera + transitions. Source: brainstorm R27 (shared render pipeline + transitions ~600ms).
- R7. **Auto-fix CTA + lifecycle controls verified against spec.** Items already exist; verify they meet brainstorm spec (R7, R8, R11, AE-2). Polish persona × theme inline filter chips on Violations tab (R14: "filtered by active persona × theme") if missing.
- R8. **Decisions tab supersession + violation linking verified.** SupersessionChain + DecisionLinkAutocomplete already exist; verify they meet brainstorm spec (R15, AE-4).
- R9. **Verification harness extends Playwright suite.** Cover AE-1 through AE-8 with the existing `tests/projects/*.spec.ts` convention; ensure the morph + reverse-morph + two-phase progression have specs.

**Origin actors:** A1 (Designer in-product), A2 (Designer other product), A3 (DS lead), A4 (PM), A5 (Engineer), A8 (Docs site).
**Origin flows:** F2 (hybrid two-phase pipeline), F3 (project view), F5 (designer addresses violation), F8 (mind graph navigation).
**Origin acceptance examples:** AE-2 (auto-fix in Figma — covers R7), AE-4 (DRD + decision linkage — covers R8), AE-5 (mind graph reverse lookup hover card — covers R20 / partly visual hierarchy R4), AE-8 (mind graph → flow morph — covers R1, R2).

---

## Scope Boundaries

- **Engine stays.** The 682-line `app/atlas/BrainGraph.tsx`, `app/atlas/SignalAnimationLayer.tsx` (190L), `app/atlas/edgePulseShader.ts` (98L), `app/atlas/forceConfig.ts` (92L), `app/atlas/cull.ts`, `app/atlas/types.ts`, `app/atlas/useGraphAggregate.ts`, `app/atlas/useGraphView.ts` (77L; minor extension only), `app/atlas/useSignalHold.ts` are not rewritten. We may add a `view.morphFromProject(slug)` reverse function on `useGraphView` (R2) but otherwise the data layer is untouched.
- **AtlasCanvas + LOD stay.** `components/projects/atlas/AtlasCanvas.tsx`, `AtlasFrame.tsx`, `AtlasControls.tsx`, `AtlasPostprocessing.tsx`, `lod/pickLOD.ts`, `lod/viewportRing.ts`, `textureCache.ts` are not modified except for U2's title-anchor wiring (which lives outside AtlasCanvas) and U7 if any tab-cross-link surfaces require it.
- **No new SSE channels.** Per Phase 5.2 closure, publish `project.view_ready` and `project.audit_complete` on the existing SSE infrastructure (already done backend-side); add new client-side handlers only if needed. Channel pattern stays `inbox:<tenant_id>` for cross-cutting events; project-specific events stay on `project:<slug>`.
- **Backend HandlePatchViolation stays.** Lifecycle PATCH handler is already shipped (`services/ds-service/internal/projects/server.go:1005-1264`); this plan does not touch it.

### Deferred to Follow-Up Work

- **`figma://plugin/...` deep-link convention.** `FixInFigmaButton.tsx:31` currently uses `figma://plugin/${PLUGIN_ID}/audit?violation_id=...`. Phase 5.2 closure noted "there is no `figma://` convention here — use Figma https URLs through `ParseFigmaURL`." Auditing whether the current deep-link works, or whether it should switch to https with a plugin-side route, belongs in a separate plan focused on plugin↔docs handshake. Not in scope here.
- **Visual regression tooling.** No visual-regression infrastructure exists today. The verification harness (U11) uses Playwright behavioral assertions (DOM presence, attribute states, route changes), not pixel-diff snapshots. Pixel-diff coverage is a separate plan.
- **Comprehensive auto-fix expansion** (brainstorm: "comprehensive auto-fix"). Beyond the existing token + style coverage, expanding to instance-override unwinding etc. is a separate Phase.
- **Plugin Phase 4 U11 handler completeness.** `FixInFigmaButton.tsx:6-8` notes the plugin-side handler "until that ships, the link still works — clicking opens the plugin, which presents an 'audit mode' tab." Verifying / completing the plugin-side fetch + node-locate flow is a plugin-repo task.

---

## Context & Research

### Relevant Code and Patterns

- **`app/atlas/LeafMorphHandoff.tsx`** — currently mounts the broken Framer `motion.div` with `layoutId`. Replaced by a `<ViewTransition>` source wrapper in U2.
- **`app/atlas/BrainGraph.tsx:392-417`** — `handleNodeClick` calls `view.morphTo(node)` on flow nodes (line 397). U2 keeps this state but switches the rendered hand-off to ViewTransition. U4 uses the morphingNode signal to pin the LOD HOT.
- **`app/atlas/useGraphView.ts:37,59-61,73`** — `morphTo(node)` exists; `morphFromProject(slug)` does not. U3 adds it.
- **`components/projects/ProjectShell.tsx:121-128`** — `TAB_DEFINITIONS = [drd, violations, decisions, json]`, default tab `"violations"`. Tab strip lines 589-632, content panel 633-701. GSAP entrance via `projectShellOpen(scope).play()` lines 215-226. U5 adds the view-ready vs audit-complete state machine to the existing `projectViewReducer` (lines 195-204) and renders progressive states.
- **`components/projects/ProjectToolbar.tsx:92-94`** — title element is `<span style={{ color: "var(--text-1)" }}>{flowName ?? project.Name}</span>`. U2 wraps this in `<ViewTransition name={`flow-${slug}-label`}>`. ProjectShell at lines 523-531 must also pass `flowName={project.Name}` (currently omits the prop, falling back through the `??`).
- **`lib/projects/client.ts:321-411`** — `subscribeProjectEvents` already subscribes to `view_ready`, `audit_complete`, `audit_failed`, `export_failed`, `audit_progress`. U5 surfaces what's already flowing.
- **`components/projects/tabs/ViolationsTab.tsx`** — already has `LifecycleButtons` (line 440-451), `FixInFigmaButton` (line 452), `CategoryFilterChips` (line 308). Verifying gap is U7 (e.g., persona × theme inline filter chip set).
- **`components/decisions/SupersessionChain.tsx`** + `components/decisions/DecisionCard.tsx` — already render supersession chains and decision metadata. U7 verifies decision↔violation cross-link.
- **`lib/animations/easings.ts`** — defines the canonical curves. `EASE_DOLLY = "expo.inOut"` is defined but unused; U10 promotes it to the standard camera curve.
- **`lib/animations/timelines/{atlasBloomBuildUp,projectShellOpen,tabSwitch,themeToggle}.ts`** — existing GSAP timelines. U10 keeps these (sequenced DOM work) but introduces `@react-spring/three` alongside for triggered 3D transitions.
- **`tests/atlas-mind-graph.spec.ts`** + `tests/projects/*.spec.ts` — Playwright fixtures + 5 existing atlas specs. U11 extends, doesn't bootstrap.

### Institutional Learnings

- **Phase 6 closure (`docs/solutions/2026-05-01-001-phase-6-closure.md`)** — `MeshBasicMaterial` × 1.5 brightness for "active" highlight is the convention; do not introduce `MeshStandardMaterial` + `emissive` for the bloom hierarchy. U8's per-type emissive intensity drives bloom via base-color brightness, not lighting.
- **Phase 1 learnings (`docs/solutions/2026-04-30-001-projects-phase-1-learnings.md`)** — wrap `<Canvas>` in `<Suspense fallback={…}>` with `key={usePathname()}` to remount on route change (R3F + Next 16 disposed-ref bug, pmndrs/react-three-fiber#3595). Both AtlasCanvas and BrainGraph already follow this; U1's morph integration must preserve it.
- **Phase 5.1 (`docs/solutions/2026-05-02-002-phase-5-1-collab-polish.md`)** — reduced-motion changes *behavior* (e.g., always-on cursor labels), not just opacity 1. U2 reduced-motion path replaces the morph with a 1.5s outline pulse on the title (the established Phase 5.1 pattern).
- **Phase 5.2 (`docs/solutions/2026-05-02-003-phase-5-2-collab-polish.md`)** — fan SSE out of one EventSource via module-level listener sets; do not open per-component sockets. U5 reuses the existing `subscribeProjectEvents` subscriber.
- **Phase 6 closure** also notes — pin morph-source nodes HOT through the entire morph timeline or LOD culls them mid-flight. U4 is exactly this.
- **Reduced-motion source = `lib/animations/context.ts`**, not `app/atlas/reducedMotion.ts` (Phase 6 closure correction). U2 + U10 import from the canonical location.

### External References

- **Next.js View Transitions guide** — [nextjs.org/docs/app/guides/view-transitions](https://nextjs.org/docs/app/guides/view-transitions). Pattern: `experimental: { viewTransition: true }` in `next.config.ts`, `<ViewTransition name="flow-{slug}-label">` on both source and target. Source/target with same `name` cross-fade automatically on route change.
- **vercel/next.js#49279** — confirms Framer `layoutId` does not bridge App Router route changes (still open 2026).
- **`@react-spring/three@10.0.3`** — compatible with `@react-three/fiber@9.6.x` + `three@0.180.x` + React 19. Reagraph ships this exact matrix. Pin exact versions, do not range-match (regression-prone in early v10.0.x per pmndrs/react-spring#2376).
- **`@react-three/postprocessing` Bloom pattern** — single `<Bloom luminanceThreshold={1} mipmapBlur intensity={1.2}>` + per-material `emissiveIntensity` + `toneMapped={false}` on emissive materials. `SelectiveBloom` is incompatible with `InstancedMesh` without a custom shader hack; we use the threshold approach instead.
- **Critical morph trap** — the browser snapshots the WebGL canvas as a raster texture during a View Transition. The morph target must be a DOM element, not a canvas-rendered mesh. Our morph target is the title bar `<span>` (DOM); this is fine. Do not attempt to morph into a node mesh.

---

## Key Technical Decisions

- **Browser-native View Transitions API over Framer Motion `layoutId` for the cross-route morph.** User-confirmed. Implementation: `view-transition-name` CSS property + `experimental.viewTransition: true` in `next.config.ts` (Next 16.2 auto-wraps router nav in `document.startViewTransition`). **NOT** React's `<ViewTransition>` component — that's in React Canary, not 19.2.4 stable. Same-document support broad; cross-document support shipped Safari 18.2 (Dec 2024), Chrome/Edge full, Firefox flag-gated (~60-70% real coverage May 2026 incl. iOS lag). Firefox-default and other unsupported users get the **breadcrumb static fallback** described under Reduced-Motion + Unsupported Browser Strategy below.
- **Reduced-Motion + Unsupported Browser Strategy.** A static breadcrumb in the project toolbar (`/atlas › <flow name>`) provides the spatial-continuity cue when motion is unavailable. The breadcrumb is rendered always (in motion users it serves as a navigation breadcrumb; in no-motion users it's the substitute for the morph). The clicked-source flash (1.5s outline pulse on the toolbar title) is supplementary, not primary. This rejects the original plan's "outline pulse alone" approach since the pulse is temporally disconnected from the click and reads as a glitch.
- **Single `<Bloom luminanceThreshold={1}>` with HDR emissive per material**, not `SelectiveBloom`. SelectiveBloom doesn't work with InstancedMesh (per Three.js forum + react-postprocessing maintainer guidance). Per-type emissive intensity drives bloom via threshold gating. `MeshBasicMaterial` color × intensity, not `MeshStandardMaterial` + lighting.
- **Edge depth-fade via custom `onBeforeCompile` shader injection on the existing `LineBasicMaterial`**, not a postprocess pass. Postprocess DOF would blur the entire scene; we want only edges to fade with z-distance.
- **`@react-spring/three` for triggered 3D transitions; GSAP for sequenced DOM choreography; `useFrame` + `MathUtils.damp` for constant-velocity drift / particle approach.** Three tools, three jobs. The unification is the convention, not "one library only" — fragmenting was the bug, not having multiple libraries.
- **`view_ready` UI state = canvas + tab shell + spinner; `audit_complete` = final counts.** No intermediate state machine beyond what the existing `projectViewReducer` already tracks.
- **Items B and F are verification + polish only.** The components exist. The plan does not rebuild them.
- **`figma://plugin/...` deep-link in `FixInFigmaButton.tsx` is left as-is** for this plan. Phase 5.2 closure flagged it; reconciling that convention is a separate plan (deferred above).
- **No new SSE channels.** Project events stay on `project:<slug>`; tenant-cross-cutting events stay on `inbox:<tenant_id>`. Both are already established.
- **Reduced-motion source is `lib/animations/context.ts`.** Do not introduce a parallel hook.
- **`@react-spring/three` install pinned at `10.0.3` exact**, not a range, per upstream regression history.

---

## Open Questions

### Resolved During Planning

- **Q: Should the morph use Framer Motion `layoutId`, native View Transitions, or a GSAP-orchestrated faux-morph?** A: Native React 19 `<ViewTransition>` (user-confirmed). Trade: experimental flag accepted.
- **Q: Are auto-fix and lifecycle controls actually missing?** A: No — `LifecycleButtons.tsx`, `FixInFigmaButton.tsx`, `DecisionLinkAutocomplete.tsx`, `CategoryFilterChips.tsx` all exist. The original "12-day rewrite" estimate was wrong by ~3 days; B and F shrink to verification + polish.
- **Q: Is Playwright already set up?** A: Yes — `playwright.config.ts` exists, 13+ specs in `tests/projects/`, including `atlas-mind-graph.spec.ts`, `auto-fix-roundtrip.spec.ts`, `violation-lifecycle.spec.ts`. U11 extends; does not bootstrap.
- **Q: `@react-spring/three` compatibility with current stack?** A: Yes — `10.0.3` works with `@react-three/fiber@9.6.x` + `three@0.180.x` + React 19 (matrix proven by Reagraph). Pin exact.
- **Q: Selective bloom vs threshold-driven bloom for the per-type emissive hierarchy?** A: Threshold-driven (single `<Bloom luminanceThreshold={1}>`). SelectiveBloom + InstancedMesh requires a fragile shader hack.
- **Q: Where does the title-anchor `<ViewTransition>` wrap actually go?** A: `components/projects/ProjectToolbar.tsx:92-94`, around the `<span>{flowName ?? project.Name}</span>`. ProjectShell.tsx must pass `flowName={project.Name}` (currently doesn't, falls through ??).

### Deferred to Implementation

- **Exact ViewTransition CSS** for the named transition (duration, easing curve, transform-origin). Browser default is a 250ms cross-fade + transform; we may want to override via `::view-transition-old(flow-...)` / `::view-transition-new(flow-...)` selectors in a global CSS file. Decide during U2 implementation based on visual fidelity tests.
- **Whether the Esc reverse-morph triggers `router.back()` or an explicit `router.push('/atlas?focus=' + sourceNodeID)`**. Both work; choose the one that better preserves the brain's prior camera position. Decide during U3.
- **Bloom strength tuning per node type.** Plan specifies "products brightest"; exact emissive intensity numbers (e.g., 3.5 for products, 1.2 for folders, 0 for components when filter off) get tuned visually during U8.
- **Edge depth-fade near/far values.** Tuned visually during U9 against the actual force-graph spread.
- **Spring tension/friction for camera dolly.** `{ tension: 170, friction: 26 }` is the canonical react-spring default; we may tune during U10.

---

## Implementation Units

> Unit prefixes (U1, U2, ...) are stable plan-local IDs. They are not renumbered when reordered or split. Sequencing is by dependency: **U1 → U2 → U3 → U4 must land in order**; U5 / U6 / U7 are parallelizable after U2; U8 / U9 / U10 are parallelizable independent of the morph chain; U11 lands last because it verifies everything.

### Phase A — Cross-route morph foundation

- U1. **Enable experimental View Transitions in Next config and remove the broken layoutId code**

**Goal:** Turn on Next.js `experimental.viewTransition` so router navigation auto-wraps in `document.startViewTransition()`, and gut the dead Framer Motion `layoutId` code in `LeafMorphHandoff.tsx` so the new wiring has a clean substrate.

**Requirements:** R1.

**Dependencies:** None.

**Files:**
- Modify: `next.config.ts` (add `experimental: { viewTransition: true }`)
- Modify: `app/atlas/LeafMorphHandoff.tsx` (remove `motion.div` with `layoutId`; keep the morphing-node state observer and the route push but render no Framer animation)
- Modify: `package.json` (NO React install — we use the browser-native API, not React's Canary `<ViewTransition>` component)
- Test: `tests/atlas-mind-graph.spec.ts` — assert clicking a flow leaf navigates to `/projects/<slug>` without console errors (smoke only at this phase)

**Approach:**
- Add `experimental: { viewTransition: true }` to `next.config.ts` alongside the existing `transpilePackages` block.
- In `LeafMorphHandoff.tsx`, replace the `motion.div` block with a no-op JSX (the file's job becomes "observe morphingNode and trigger router.push"). The `view-transition-name` CSS happens in U2 on the actual rendered label, not in this handoff component. This unit primarily *removes* code.
- Reduced-motion branch in `LeafMorphHandoff.tsx`: trigger an immediate `router.push` (no animation, no pulse — the spatial-continuity cue is the breadcrumb on the receiving page, U2c).

**Patterns to follow:**
- `next.config.ts` already has `transpilePackages` for `react-force-graph-3d` + `three-spritetext` (per Phase 6 closure). Add `experimental` block alongside, do not nest inside transpilePackages.
- Reduced-motion check: `useReducedMotion()` from `lib/animations/context.ts` (canonical).

**Test scenarios:**
- Happy path: click a flow leaf in /atlas; route changes to /projects/<slug>; no console errors. Covers F8 step 5 (the navigation, not yet the morph).
- Edge case: reduced-motion users — clicking a leaf still navigates; no Framer / View Transition motion fires.
- Error path: invalid slug → /projects/<slug> renders the existing 404 path (`ProjectShellLoader.tsx:107`).

**Verification:**
- `npm run build` succeeds with the experimental flag.
- DevTools network tab shows clean route push on flow-leaf click.
- No "layoutId target not found" warnings in the console.
- **Manual checklist:** open /atlas, click a flow leaf, confirm route changes, confirm no Framer warning. **Record a 5-second video before marking U1 complete.**
- **Rollback boundary:** revert next.config.ts + LeafMorphHandoff.tsx; one commit, two files. Behavior returns to current broken-morph baseline.

---

- U2a. **Build leaf-label DOM overlay layer (sub-project)**

**Goal:** Render flow-leaf labels as a DOM layer positioned over the WebGL canvas, projected from each node's three.js world position to screen-space on every frame. Required because the browser cannot morph a WebGL-rendered sprite into a DOM element via View Transitions — the canvas is rasterized as one texture during the snapshot. The morph source must be DOM.

**Requirements:** R1 (load-bearing prerequisite).

**Dependencies:** U1.

**Files:**
- Create: `app/atlas/LeafLabelLayer.tsx` — new component. Subscribes to graph node positions; on every `useFrame` tick (throttled to ~30fps to avoid DOM-thrash), projects each flow-type node's `THREE.Vector3` world position to screen space using `camera.project()` × renderer canvas size, applies CSS `transform: translate(...)` to the label `<div>`. Reads `fgRef.current.camera()` + `renderer().getSize()` from `BrainGraph`.
- Modify: `app/atlas/BrainGraph.tsx` — mount `<LeafLabelLayer>` as a sibling to the `<ForceGraph3D>` element (positioned absolutely; pointer-events none so it doesn't hit-test). Pass it the visible flow nodes + the fgRef.
- Modify: `app/atlas/BrainGraph.tsx` — turn off the library's built-in `nodeLabel` (canvas-sprite labels) for flow-type nodes only. Keep canvas labels for non-flow types (folders, products) since they don't morph.
- Test: `tests/projects/leaf-label-overlay.spec.ts` (new) — assert leaf-label DOM positions stay within ±2px of expected screen positions during a force-simulation tick.

**Approach:**
- Browser API: each label is `<div style={{ transform: 'translate3d(${x}px, ${y}px, 0)' }}>{label}</div>` inside a parent `position: absolute; inset: 0; pointer-events: none` overlay.
- Projection: `Vector3.project(camera)` returns NDC; multiply by canvas width/height/2 and offset by canvas center to get pixel position. Account for retina DPR (the renderer's `getSize()` returns CSS pixels not device pixels by default — verify).
- Throttle: 30fps cap (every other frame) is plenty for label position updates; force-graph runs at 60fps but labels don't need that.
- Off-screen culling: labels with screen-space position outside viewport get `display: none` so we don't ship 100+ off-screen DOM nodes.
- Force-simulation pause during morph: when `view.morphingNode` is set, freeze label projection updates until route navigation completes (else the source label snapshot is stale). Listen for the next-route-mount signal (Next.js's `useSelectedLayoutSegment` change) to resume.
- Hit-test conflict: labels are `pointer-events: none`; the canvas underneath handles clicks.
- Library quirk to verify in implementation: if `react-force-graph-3d` does not expose camera/renderer cleanly, this unit forks the projection math from `HoverSignalCard.tsx` (which uses the same library and projects one node's screen position).

**Patterns to follow:**
- `app/atlas/HoverSignalCard.tsx` (single-node screen-space projection — same projection math, multi-node).
- `useFrame` throttling: existing `app/atlas/SignalAnimationLayer.tsx:108-169` shows the rAF throttling pattern.

**Test scenarios:**
- Happy path: 50 flow leaves visible; all label DOM positions match expected screen positions (within ±2px) during force-simulation steady state.
- Edge case: camera dolly mid-flight; labels track smoothly without stutter (frame budget < 8ms for the projection pass).
- Edge case: force simulation re-heat (e.g., new node added); labels reposition correctly.
- Edge case: window resize; labels reposition correctly.
- Edge case: retina display (DPR > 1); labels position correctly (no 2× drift).
- Performance: 100 visible leaves → label-update pass under 4ms; verified via `performance.measure`.

**Verification:**
- Manual demo: open /atlas, click around, scroll, resize window — labels track nodes pixel-perfectly.
- Performance trace: label-update pass under frame budget.
- **Rollback boundary:** revert LeafLabelLayer.tsx + BrainGraph mount. Library's canvas-sprite labels resume.
- **Gate: U2b cannot start until U2a is verified.**

---

- U2b. **Wire `view-transition-name` on leaf labels and project title**

**Goal:** When a flow leaf is clicked in /atlas, the leaf-label DOM element morphs to the project view's title bar via the browser-native View Transitions API. Both source and target carry CSS `view-transition-name: flow-${slug}-label`. Browser handles the morph automatically because Next 16.2's `experimental.viewTransition` wraps router nav in `document.startViewTransition()`.

**Requirements:** R1.

**Dependencies:** U2a (overlay must exist).

**Files:**
- Modify: `app/atlas/LeafLabelLayer.tsx` — apply inline style `style={{ viewTransitionName: `flow-${slug}-label` }}` to each leaf-label `<div>`. Slug comes from the node payload.
- Modify: `components/projects/ProjectShell.tsx` — pass `flowName={initialProject.Name}` (currently omits the prop, falls through `??` in the toolbar). Pass `slug` so the toolbar can build the same view-transition-name.
- Modify: `components/projects/ProjectToolbar.tsx:92-94` — apply inline style `style={{ viewTransitionName: `flow-${slug}-label`, color: 'var(--text-1)' }}` to the title `<span>`.
- Create: `app/atlas/view-transitions.css` — define `::view-transition-old(flow-*)` and `::view-transition-new(flow-*)` rules for duration (600ms) and easing (cubic-bezier matching `EASE_DOLLY = "expo.inOut"`).
- Test: `tests/projects/atlas-leaf-morph.spec.ts` (new) — click a flow leaf; assert title bar element has same text as source leaf within 700ms of route change.

**Approach:**
- The View Transitions API matches DOM elements with the same `view-transition-name` across the old/new page snapshots and animates between their bounding rects + opacity. No JS needed beyond the CSS property and Next's auto-wrap.
- Same `view-transition-name` must appear on at most ONE element per page snapshot at any time (else browser throws `InvalidStateError`). Since each flow leaf has a unique slug and only one project page renders at a time, this constraint is trivially satisfied.
- Custom CSS for duration/easing via `::view-transition-old(flow-*)` and `::view-transition-new(flow-*)` selectors (universal selector matching all dynamic names that start with `flow-`).
- Reduced-motion + Firefox-default: the CSS `@media (prefers-reduced-motion: reduce)` block sets `animation-duration: 0s` on the view-transition pseudo-elements; the breadcrumb in U2c handles the spatial-continuity cue. Firefox-default users (without the experimental browser flag) see instant nav with the breadcrumb — no morph attempt at all (Next.js falls through cleanly when the API is unavailable).

**Technical design (directional, not implementation):**

```tsx
// /atlas LeafLabelLayer.tsx
<div
  key={node.id}
  style={{
    transform: `translate3d(${x}px, ${y}px, 0)`,
    viewTransitionName: `flow-${node.slug}-label`,
  }}
  className="leaf-label"
>
  {node.label}
</div>

// /projects/[slug] ProjectToolbar.tsx
<span style={{ color: 'var(--text-1)', viewTransitionName: `flow-${slug}-label` }}>
  {flowName ?? project.Name}
</span>
```

```css
/* app/atlas/view-transitions.css */
::view-transition-old(flow-*),
::view-transition-new(flow-*) {
  animation-duration: 600ms;
  animation-timing-function: cubic-bezier(0.87, 0, 0.13, 1); /* expo.inOut */
}

@media (prefers-reduced-motion: reduce) {
  ::view-transition-old(flow-*),
  ::view-transition-new(flow-*) {
    animation-duration: 0s;
  }
}
```

**Patterns to follow:**
- Slug propagation: `ProjectShell.tsx` already receives `slug` as a prop (`ProjectShellLoader.tsx:147`); pass it to `ProjectToolbar`.

**Test scenarios:**
- Covers AE-8. Happy path: click flow leaf "Learn Touchpoints"; assert `/projects/indian-stocks-learn-touchpoints` renders within 700ms; assert title bar text matches source leaf.
- Edge case: long flow name (>40 chars) — morph completes; title bar truncates per existing toolbar styles.
- Edge case: special chars in slug — encoding round-trips.
- Reduced-motion: instant nav; breadcrumb visible (U2c); no morph animation.
- Browser: Firefox-default (flag off) — instant nav; breadcrumb visible; no morph attempt; no console error.
- Error path: stale leaf (flow renamed) → 404 path renders.

**Verification:**
- Manual demo across Chrome / Edge / Safari 18.2+ / Firefox-default: record video for each. Confirm morph in the first three; confirm clean fallback in Firefox-default.
- Lighthouse: frame budget < 16ms during transition.
- **Rollback boundary:** revert U2b CSS + style additions. U2a overlay still works; just no morph.
- **Gate: do not start U3 until video evidence of working morph in Chrome + Safari is recorded.**

---

- U2c. **Static breadcrumb on project view as the no-motion fallback**

**Goal:** Project toolbar always renders a static `/atlas › <flow name>` breadcrumb. For motion-supported users this provides standard wayfinding; for reduced-motion + Firefox-default users this *replaces* the morph as the spatial-continuity cue (per the corrected Reduced-Motion + Unsupported Browser Strategy in Key Technical Decisions).

**Requirements:** R1 (fallback path).

**Dependencies:** None.

**Files:**
- Modify: `components/projects/ProjectToolbar.tsx` — add a breadcrumb element above or alongside the title: `<nav><Link href="/atlas">/atlas</Link> › <span>{flowName}</span></nav>`. Inherits text styles from existing toolbar.
- Test: `tests/projects/breadcrumb.spec.ts` (new) — assert breadcrumb renders on every project page, with /atlas link working.

**Approach:**
- The breadcrumb is always-on. It's not gated by reduced-motion. For motion users, it's standard wayfinding (R12 implies a breadcrumb-style header anyway). For no-motion users, it's the spatial-continuity substitute.
- Click on `/atlas` link triggers reverse navigation (no morph in no-motion users; full reverse-morph in motion users via U3).

**Test scenarios:**
- Happy path: project view renders breadcrumb with /atlas link + flow name.
- Click /atlas link: navigates to /atlas (with reverse morph if motion supported).
- Edge case: flow has slashes in path (e.g., "F&O / Learn") — breadcrumb renders the slash literally without breaking the navigation.

**Verification:**
- Manual: open project page; breadcrumb visible; click /atlas, returns to atlas.
- **Rollback boundary:** revert ProjectToolbar breadcrumb additions. Atlas-side morph + reverse morph still work; no-motion users lose the spatial cue.

---

- U3. **Reverse morph on Esc / browser back from project to /atlas**

**Goal:** Pressing Escape or browser back in the project view returns the user to /atlas with the source node re-focused at its prior zoom level. The title bar morphs back to the leaf position via the same `<ViewTransition>` name.

**Requirements:** R2.

**Dependencies:** U2.

**Files:**
- Modify: `components/projects/ProjectShell.tsx` — add a `useEffect` that listens for `keydown` on `Escape` and calls `router.back()`. The browser's view-transition hook fires on `router.back()` automatically (the `<ViewTransition>` source/target are still wrapped, just played in reverse).
- Modify: `app/atlas/useGraphView.ts` — add `morphFromProject(slug)` action that re-applies focus to the source node based on URL `?focus=` param when /atlas mounts. Set zoom level back to `flow` so the leaf is centered.
- Modify: `app/atlas/page.tsx:58` — already reads `focus` from query string; ensure the focus dispatch happens before the View Transition animation completes (within React Transitions / before the `useEffect` paint).
- Test: `tests/projects/atlas-reverse-morph.spec.ts` (new) — open /projects/<slug>, press Escape, assert /atlas renders with the source flow leaf re-focused.

**Approach:**
- The View Transitions API handles the reverse animation automatically: when the route changes back, the same `name` triggers a reverse morph. No additional motion code needed.
- The semantic work is making /atlas restore the prior camera/zoom state. URL preservation via `?focus=<slug>` in the leaf-click step (already supported by `app/atlas/page.tsx:58`); on reverse navigation, `router.back()` returns to that URL, and `useGraphView` re-applies focus on mount.
- Esc keydown listener should be installed on `document`, not on a specific element, so it works regardless of focus.
- Reduced-motion path: same as U2 — outline pulse on the leaf source node when /atlas re-mounts (Phase 5.1 convention).

**Patterns to follow:**
- Existing `app/atlas/page.tsx:58` already extracts `focus` from URL params — this is the receiving end of reverse navigation.
- `useEffect` cleanup pattern for keydown listeners: `() => document.removeEventListener("keydown", ...)`.

**Test scenarios:**
- Happy path: open project view, press Escape; /atlas renders; source node is focused (camera zoomed to it). Covers F8 step 6.
- Edge case: press Escape twice quickly → only one back navigation fires (debounce or use `router.replace` semantics).
- Edge case: open project view via direct URL (not from /atlas); press Escape → falls back to `/atlas` root, no source-node focus (clean default state).
- Edge case: input field focused (e.g., DRD editor) → Escape does NOT navigate back (stops propagation if target is editable).
- Reduced-motion: Escape navigates back; outline pulse on source leaf instead of morph.

**Verification:**
- Manual: open project, Esc, verify /atlas re-focuses the source. Repeat with browser back button.
- Manual: open DRD tab, type in editor, press Escape — expects editor to handle Escape (cancel something) without route navigation.
- **Rollback boundary:** revert keydown handler + useGraphView extension + page.tsx focus dispatch. Forward morph still works.
- **Gate: U4 may proceed in parallel; U5/U6/U7 may proceed once U3 is verified.**

---

- U4. **Pin morph-source node HOT through transition (LOD)**

**Goal:** During the morph transition, the source flow leaf must remain rendered at HOT LOD so its label stays crisp through the View Transition snapshot. Per Phase 6 closure: "selected/hovered nodes forced HOT regardless of viewport — that invariant is load-bearing for any cross-surface choreography."

**Requirements:** R1 (transition fidelity).

**Dependencies:** U2.

**Files:**
- Modify: `app/atlas/cull.ts` — extend `cullVisibleSubset` to include nodes referenced in `view.morphingNode` regardless of viewport.
- Modify: `app/atlas/useGraphView.ts` — ensure `morphingNode` state persists for the duration of the View Transition (settle time ~700ms); auto-clear after the transition completes.
- Test: visual inspection (no automated test — LOD rendering quality is hard to assert programmatically).

**Approach:**
- The morphingNode state is set on flow-leaf click (U2). Add a `morphingNode` reference to the `cullVisibleSubset` allowlist alongside focus / hover.
- Auto-clear timer: set `morphingNode` to null 800ms after route change (View Transition default is 250ms, brainstorm wants 600ms; 800ms gives a buffer).
- Verify the source leaf doesn't pop out during the snapshot.

**Patterns to follow:**
- Existing pin-to-HOT pattern in cull.ts (need to check exact mechanism — pin-set referenced in `docs/solutions/2026-05-01-001-designbrain-perf-findings.md`).

**Test scenarios:**
- *Test expectation: visual inspection only.* No automated assertion — LOD-mid-transition is a perceptual quality check.

**Verification:**
- Visual demo: record morph at 60fps; play back at 0.25× speed; confirm leaf label stays crisp throughout.
- **Rollback boundary:** revert cull.ts + useGraphView extension. Morph still works, may have a brief LOD pop.
- **Gate: U4 + U3 must both be verified before declaring Phase A complete.**

### Phase B — Two-phase pipeline UI surfacing

- U5. **`view_ready` vs `audit_complete` visual progression**

**Goal:** The project view renders a `view_ready` state immediately after canvas mount (canvas + tab shell visible, audit-running spinner in toolbar, violation count = 0) and progressively updates as `audit_progress` events arrive. At `audit_complete`, the spinner is replaced by final counts. Reflects the brainstorm's two-phase pipeline (R3, F2).

**Requirements:** R3.

**Dependencies:** None (parallelizable with U6, U7 after U2).

**Files:**
- Modify: `components/projects/ProjectShell.tsx` — extend the `projectViewReducer` (lines 195-204) to track an explicit `auditState: 'pending' | 'running' | 'complete' | 'failed'`. Render the spinner + count placeholder when `auditState === 'running'`.
- Modify: `components/projects/ProjectShell.tsx:355-390` — in the `subscribeProjectEvents` handler, dispatch `auditState` transitions on `view_ready`, `audit_progress`, `audit_complete`, `audit_failed`.
- Modify: `components/projects/ProjectToolbar.tsx` (or create a new sub-component `ProjectToolbar/AuditStateBadge.tsx`) to render the spinner / count badge.
- Test: `tests/projects/two-phase-pipeline.spec.ts` (new) — mock SSE event stream; assert the spinner appears at view_ready and count appears at audit_complete.

**Approach:**
- Today: ProjectShell reducer dispatches on `audit_complete` (sets a flag, fires a toast); no `view_ready` action, no progressive count.
- Add: `viewReady` reducer action sets `auditState='running'`. `auditProgress` action increments a count. `auditComplete` sets `auditState='complete'` and final counts. `auditFailed` sets `auditState='failed'` with an error message.
- Render: in the toolbar, when `auditState === 'running'`, show a small inline spinner + "Audit running" tooltip. When 'complete', show the count badge (already styled in existing tabs).
- Reduced-motion: spinner is already CSS animation; add `@media (prefers-reduced-motion)` to swap to a static "..." indicator.

**Patterns to follow:**
- Existing reducer pattern in ProjectShell.tsx:195-204.
- Spinner: existing CSS spinner from `lib/animations/`. Or use a static dot if reduced-motion.

**Test scenarios:**
- Covers F2. Happy path: open project view; view_ready event arrives within 100ms (mocked); spinner appears; audit_complete arrives at 60s (mocked); spinner replaced by final count.
- Edge case: `view_ready` never arrives (backend slow) → loading skeleton stays past 15s timeout, then shows "Slow to render — refresh?" affordance.
- Error path: `audit_failed` event → spinner replaced by red badge with retry CTA.
- Edge case: `audit_progress` events arrive out of order → reducer uses event timestamps to ignore stale increments.
- Reduced-motion: static dot indicator, not animated spinner.

**Verification:**
- Manual demo: trigger an export from the plugin; record the project view; confirm the progression spinner → count.
- Playwright: test passes against a mocked SSE stream.
- **Rollback boundary:** revert ProjectShell reducer extension + ProjectToolbar badge. Project view still loads; just no progressive UI.

### Phase C — Tab UX verification + polish

- U6. **Add Reactivate action + persona × theme filter chips to Violations tab**

**Goal:** Close the specific gaps surfaced during plan review against R7, R8, R11, R14, AE-2:
1. `LifecycleButtons.tsx` adds a Reactivate action (R8 mandates `Active → Acknowledged → Fixed | Dismissed` plus DS-lead override Dismissed → Active; backend ships in commit 941464d Phase 5.3, no per-row UX yet).
2. `CategoryFilterChips.tsx` is category-only today; add separate persona-chip row + theme-chip row per R14 ("filtered by active persona × theme"). Theme is currently NOT a filter dimension anywhere.
3. Auto-fix CTA gating verified end-to-end (the `auto_fixable` guard is in `ViolationsTab.tsx:452`, not in `FixInFigmaButton` — verify and document).

**Requirements:** R7, R8, R11, R14.

**Dependencies:** None (parallelizable with U5, U7, and U1-U4 morph chain — no morph dependency).

**Files:**
- Modify: `components/projects/tabs/violations/LifecycleButtons.tsx` — add a third button "Reactivate" visible only when violation status is `dismissed` AND user has admin role. Wire to `patchViolationLifecycle(slug, id, "reactivate", reason)` (the lib already supports this action per `lib/inbox/client.ts`).
- Create: `components/projects/tabs/violations/PersonaFilterChips.tsx` — chip row mirroring `CategoryFilterChips` shape; reads available personas from ProjectShell context; multi-select (or single-select; decide during impl).
- Create: `components/projects/tabs/violations/ThemeFilterChips.tsx` — chip row for `light` / `dark` / both (`mode_label` is already a filter param at the `listViolations` API level per `lib/projects/client.ts:288`).
- Modify: `components/projects/tabs/ViolationsTab.tsx` — render the new chip rows beneath `CategoryFilterChips` (line 308); pipe their state into the `listViolations` query string.
- Modify: `app/atlas/admin/page.tsx` (admin role check pattern) — confirm or extract a `useIsAdmin()` hook from claims for the Reactivate gating. If not extractable, inline the check.
- Test: extend `tests/projects/violation-lifecycle.spec.ts` with reactivate; new `tests/projects/violation-filters.spec.ts` for persona × theme.

**Approach:**
- Reactivate uses the existing PATCH endpoint (action enum already includes `reactivate` per `services/ds-service/internal/projects/server.go:1258`). UI just exposes it.
- Persona / theme chip components use the same pattern as `CategoryFilterChips` — multi-select chip row, all ON by default.
- Filter state lives in `ViolationsTab` local state; passed to `listViolations` as query params. `listViolations` already accepts `persona_id` + `mode_label` (verified in `lib/projects/client.ts:285-292`).
- The `auto_fixable` guard verification: confirm `ViolationsTab.tsx:452` only renders `<FixInFigmaButton>` when `v.AutoFixable === true`. If guard is missing, add it (defensive — the button without the guard would link without the underlying capability).
- The `figma://plugin/...` deep-link in `FixInFigmaButton.tsx:31` is **not** changed in this plan (deferred per the Scope Boundaries section; flagged to a separate plugin↔docs handshake plan).

**Patterns to follow:**
- `CategoryFilterChips.tsx:83` for chip-row component shape.
- Admin gating: existing pattern in `app/atlas/admin/*` — confirm during impl whether there's a shared `useIsAdmin()` hook or it's inline per route.

**Test scenarios:**
- Happy path: dismiss a violation; admin user sees Reactivate button on dismissed row; click Reactivate, type reason, submit; row reappears in active list. Covers R8.
- Happy path: multi-select two personas in the chip row; violations list filters to violations with persona_id in the selected set. Covers R14.
- Happy path: select `light` only in theme chips; only light-mode violations remain. Covers R14.
- Happy path: violation with `auto_fixable: true` shows Fix in Figma button; without it, button hidden. Covers R11.
- Edge case: non-admin user clicks Reactivate (shouldn't be visible) → defense-in-depth; PATCH returns 403, error inline.
- Edge case: all chips OFF → empty result with "No violations match your filters" empty state.
- Error path: PATCH lifecycle returns 5xx → error inline; row doesn't change state.

**Verification:**
- Manual: walk a violation through Active → Acknowledged → reactivate-by-admin → Active.
- Manual: toggle every chip combination; counts update.
- **Rollback boundary:** scoped to ViolationsTab + 2 new chip components + LifecycleButtons reactivate. Easy revert per file.

---

- U7. **Add Linked Violations subsection on DecisionCard + bidirectional Violations↔Decisions cross-link**

**Goal:** Close the specific gaps surfaced during plan review against R15, AE-4:
1. `DecisionCard` lacks a "Linked violations" subsection — add it. Required client method: `listLinkedViolations(decisionID)` may not exist yet (verify; build if missing).
2. Violations → Decisions cross-link does NOT exist today (the reverse direction works via `?decision=<id>` deep-link to DecisionsTab, but there is no link from a violation row TO that decision). Add it.
3. Verify the existing supersession-chain rendering and decision deep-link outline-pulse continue to work.

**Requirements:** R8, R15.

**Dependencies:** None (parallelizable with U5, U6, and the morph chain).

**Files:**
- Read first to confirm: `components/decisions/DecisionCard.tsx`. Determine where the "Linked violations" subsection should land within the card.
- Modify: `components/decisions/DecisionCard.tsx` — add a "Linked violations" collapsible subsection. Fetches linked violations on mount via `listLinkedViolations(decision.ID)`.
- Modify (or create): `lib/decisions/client.ts` — verify `listLinkedViolations(decisionID): Promise<ApiResult<{violations: Violation[]}>>`. If missing, add it. Backend endpoint: confirm during impl (`services/ds-service/internal/projects/server.go` already has decision↔violation link tables per `decision_links` migration; verify the corresponding GET handler exists).
- Modify: `components/projects/tabs/ViolationsTab.tsx` — when `violation.linked_decision_id` is non-empty, render a `<Link>` that switches tab to Decisions + appends `?decision=<id>` to the URL. Tab switch lives in ProjectShell's `setActiveTab` setter; pass it down (currently the prop chain doesn't include tab control — extend it).
- Modify: `components/projects/ProjectShell.tsx` — expose `setActiveTab` to children via context or prop drilling so ViolationsTab can switch tabs.
- Test: `tests/decisions/violations-cross-link.spec.ts` (new) — link a decision to a violation; assert violations row shows "View decision" link; click; assert tab switches and decision highlighted.

**Approach:**
- Read DecisionCard first: if "Linked violations" UX would clutter the compact card variant, render the subsection only on the expanded variant (the `compact` prop is already used per `DecisionsTab.tsx:266`).
- Cross-link direction: Violations row → Decisions tab. Reuse the `?decision=<id>` URL pattern (existing). No new SSE channels.
- Backend client: `listLinkedViolations` may need to call `GET /v1/decisions/<id>/violations` or use a join from `decision_links` — confirm during impl. If the backend handler doesn't exist, this unit grows by ~0.5d.
- Cross-tab navigation: ProjectShell already has tab routing via `?tab=<name>` URL param (verify in ProjectShell.tsx); reuse if present, add if not.

**Patterns to follow:**
- `?decision=<id>` deep-link + outline pulse: existing Phase 5.1 pattern at `DecisionsTab.tsx:53,257-264`.
- Tab routing via URL param: pattern from ProjectShell.tsx — verify during impl.

**Test scenarios:**
- Happy path: open DecisionsTab; existing supersession chain renders. Covers R15.
- Happy path: decision card expanded → Linked Violations subsection lists violations; click a violation → tab switches to Violations + violation highlighted (or scrolled-to + outlined).
- Happy path: violation linked to decision → "View decision" link in violation row; click → tab switches to Decisions + decision highlighted via outline pulse. Covers AE-4.
- Edge case: decision with no linked violations → Linked Violations section shows "No linked violations" empty state.
- Edge case: violation linked to superseded decision → still navigable; superseded badge visible on the decision card.
- Edge case: tab change interrupts an in-flight linked-violations fetch → fetch is cancelled cleanly (no setState-on-unmount warning).
- Error path: `listLinkedViolations` 5xx → empty section rendered with retry CTA.

**Verification:**
- Manual: link a decision to a violation via existing UI; verify both directions work.
- Playwright: cross-link spec passes.
- **Rollback boundary:** scoped to DecisionCard + ViolationsTab cross-link + ProjectShell tab-control prop drilling. Each addition reverts independently.

### Phase D — Visual hierarchy + motion grammar

- U8. **Per-type emissive intensity hierarchy via threshold-driven bloom**

**Goal:** Products glow strongest, folders/flows dimmer, components/tokens/decisions only when their filter is on. Single `<Bloom luminanceThreshold={1}>` driven by per-material `emissiveIntensity` × HDR color.

**Requirements:** R4.

**Dependencies:** None (parallelizable with U9, U10).

**Files:**
- Modify: `app/atlas/forceConfig.ts` — extend `NODE_VISUAL` map to include `emissiveIntensity` per type. Products: 3.5. Folders: 1.8. Flows: 1.2. Components: 1.0 (visible only when component filter on). Tokens: 0.8. Decisions: 0.8. Numbers tuned during implementation.
- Modify: `app/atlas/BrainGraph.tsx:75-89` — `getSharedNodeMaterial` updates: `MeshBasicMaterial` color is `baseColor × emissiveIntensity`. Set `toneMapped: false` on the material so HDR brightness passes through to the bloom threshold.
- Modify: `app/atlas/BrainGraph.tsx:204-210` — replace `UnrealBloomPass` with `@react-three/postprocessing` `<Bloom luminanceThreshold={1.0} mipmapBlur intensity={1.2} kernelSize={KernelSize.LARGE}>`. Wrap in an `<EffectComposer>` if not already wrapped (verify current setup).
- Test: visual + Playwright pixel-presence (assert canvas pixels include high-luminance values where products should be).

**Approach:**
- HDR brightness: `MeshBasicMaterial` color × intensity ≥ 1.0 → bloom threshold passes → bloom kicks in.
- `toneMapped: false` is mandatory; otherwise ACES filmic clamps the brightness back below threshold.
- Filter chip integration: when components filter is OFF, set component nodes' opacity to 0 (already handled in `BrainGraph.tsx:316`) which also zeros the bloom contribution.
- Per Phase 6 closure: keep `MeshBasicMaterial`. Do not introduce `MeshStandardMaterial` + `emissive` (lighting overhead).

**Patterns to follow:**
- Existing `getSharedNodeMaterial` factory; just extend the brightness math.
- `@react-three/postprocessing` import — already in package.json (`^3.0.4`).

**Test scenarios:**
- Visual demo: products noticeably brighter than folders/flows in a record.
- Playwright pixel-presence: read 4-5 pixels at known product positions; assert R/G/B values exceed 0.95 (HDR signal).
- Reduced-motion: emissive intensity halved (less visual noise for sensitive users).

**Verification:**
- Visual: side-by-side before/after recording.
- **Rollback boundary:** revert forceConfig + BrainGraph material/bloom changes; one or two commits.

---

- U9. **Edge depth-fade via shader injection**

**Goal:** Edges fade with z-distance from the camera (depth-of-field analog without a full DoF pass). Holographic feel.

**Requirements:** R5.

**Dependencies:** None.

**Files:**
- Modify: `app/atlas/edgePulseShader.ts` — extend the existing edge material's `onBeforeCompile` to inject a depth-based alpha decay: `gl_FragColor.a *= 1.0 - smoothstep(near, far, vViewZ);` where `near` and `far` are uniforms (e.g., near = 50, far = 300; tuned).
- Modify: `app/atlas/BrainGraph.tsx:356` (`linkColor` accessor) and adjacent linkOpacity — pass `near`/`far` uniforms to each edge material instance.
- Test: visual.

**Approach:**
- Inject after the existing pulse `uTime` uniform calculation; multiply final alpha.
- Skip for reduced-motion users (depth-fade is subtle but adds visual complexity).

**Patterns to follow:**
- Existing `onBeforeCompile` injection in `edgePulseShader.ts:50-95` for the pulse uniform.

**Test scenarios:**
- *Test expectation: visual inspection only.*

**Verification:**
- Visual: record the brain view rotating; assert distant edges visibly dim.
- **Rollback boundary:** revert shader injection only. Edges revert to constant opacity.

---

- U10. **Unified motion grammar — install @react-spring/three, retire rAF lerps for triggered transitions**

**Goal:** All triggered 3D transitions (camera dolly on click, hover scale, particle convergence) move to `@react-spring/three` springs. Sequenced DOM choreography (project shell entrance, tab switch) keeps GSAP. Constant-velocity drift (organic node sway) keeps `useFrame` + `MathUtils.damp`. Three tools, three jobs — codified.

**Requirements:** R6.

**Dependencies:** None (parallelizable with U8, U9; benefits from U2 for the morph timing alignment).

**Files:**
- Modify: `package.json` — add `"@react-spring/three": "10.0.3"` (exact pin), `"@react-spring/core": "10.0.3"`. `npm install`.
- Modify: `app/atlas/SignalAnimationLayer.tsx:97-105` — replace the constant-velocity `APPROACH_SPEED = 18` lerp with a spring (`{ tension: 200, friction: 30 }`). Particles spring toward node on hold, spring back on release.
- Modify: `app/atlas/BrainGraph.tsx:392-417` — replace the `cameraPosition()` tween (currently library-default, mechanical) with a `useSpring` for camera position. Spring config `{ tension: 170, friction: 26 }` (canonical react-spring default) — tune during implementation.
- Modify: `components/projects/atlas/AtlasCanvas.tsx` — camera dolly (the click-to-snap pattern, lines ~155-191) replaced with a spring.
- Modify: `lib/animations/easings.ts` — promote `EASE_DOLLY = "expo.inOut"` to the standard camera tween curve (currently defined but unused). Document the decision in a header comment.
- Modify: `lib/animations/timelines/projectShellOpen.ts` — verify all GSAP timelines reference the canonical easings; if any use raw `cubic.out` or `expo.out`, alias via the easings.ts constants.
- Keep: `lib/animations/timelines/{atlasBloomBuildUp,projectShellOpen,tabSwitch,themeToggle}.ts` — sequenced DOM choreography; GSAP stays.
- Keep: `BrainGraph.tsx:243-258` — organic drift via `Math.sin`. This is constant-velocity ambient motion; springs are wrong for it. Optionally damp via `MathUtils.damp` for smoother position-wrapping.
- Test: visual (motion feel).

**Approach:**
- Springs replace lerps for *triggered* transitions: camera dolly, hover scale, particle convergence on click-hold.
- GSAP stays for *sequenced* DOM choreography: project shell entrance (parallel reveals with stagger), tab switch (out → in handoff).
- `useFrame` + damp stays for *ambient* motion: organic drift, edge pulse uniform tick.
- Document the convention in a `lib/animations/conventions.md` (new) so future code knows which tool to pick.

**Patterns to follow:**
- `@react-spring/three` imperative API: `const [{ position }, api] = useSpring(...); useEffect(() => { api.start({ position: target }) }, [target]);` (per Reagraph + react-spring r3f guide).
- Keep existing timelines structure under `lib/animations/timelines/` (Phase 1 closure recommendation).

**Test scenarios:**
- *Visual inspection primarily — motion feel is qualitative.*
- Happy path: hover a node → spring scale (overshoot + settle); click-hold → particles spring toward node.
- Performance: frame budget < 16ms during a camera dolly; spring physics doesn't melt the GPU.
- Reduced-motion: springs collapse to instant transitions (skip animation).

**Verification:**
- Side-by-side video: before (lerp/tween mechanical feel) vs after (spring physics).
- Performance trace: confirm frame budget held.
- New `lib/animations/conventions.md` exists and documents the three-tool convention.
- **Rollback boundary:** revert package.json + the modified animation files. Returns to current motion baseline.
- **Gate: U10 should land before U11 (verification harness should test the final motion experience, not the in-flight one).**

### Phase B+ — Critical wiring gaps surfaced 2026-05-02 19:30 (added post-execution-start)

These two units were added after U2a + U6 shipped, when running the project view at `/projects/indian-stocks-research` revealed the canvas + tabs render blank because the API response is incomplete. They are NEW critical-path items that block any meaningful demo of the project view.

- U12. **Bundle versions/screens/screen_modes/personas in HandleProjectGet response**

**Goal:** Fix the silent wiring gap that makes the project view's atlas canvas + tabs render with zero data even though the DB has 238 screens across 3 flows for our test project. `services/ds-service/internal/projects/server.go:447` currently returns only `{project: p}`. The client-side type at `lib/projects/types.ts:215-226` declares `versions?`, `screens?`, `screen_modes?`, `available_personas?` as optional, with comment lines 217-219 explicitly stating: *"The plan's later units (U7/U8) extend the GET response with these arrays; we declare them as optional today so client.ts can read them without a version bump on the server side."* The server-side extension was deferred and never landed. `ProjectShellLoader.tsx:84-87` reads each field with `?? []` fallback, so the page silently renders empty — no error, no hint, just blank.

**Requirements:** Renders R12 functional (project view requires screens to render anything).

**Dependencies:** None (independent of U2b/U7; touches Go server code, not the morph chain).

**Files:**
- Modify: `services/ds-service/internal/projects/server.go:432-470` — `HandleProjectGet` extends to fetch + return versions, screens, screen_modes, personas alongside the project.
- Possibly modify: `services/ds-service/internal/projects/repository.go` — add `ListVersionsByProject(ctx, projectID)`, `ListScreensByVersion(ctx, versionID)`, `ListScreenModesByVersion(ctx, versionID)`, `ListPersonasForTenant(ctx, tenantID)` if not already present (verify; many of these may exist piecemeal under different names).
- Verify (read-only): `components/projects/ProjectShell.tsx:319` and `:475` already do their own `?? []` fallbacks on `versions` — those second fetches may be redundant once the bundled response lands; document but don't refactor in this unit.
- Test: `services/ds-service/internal/projects/server_test.go` — extend `TestHandleProjectGet` (or add) to assert response includes versions, screens, screen_modes, available_personas as non-empty arrays when data exists.

**Approach:**
- Active version resolution: from `?v=<id>` query param, or latest version_index for the project (mirrors the pattern in HandleListViolations).
- Cross-tenant scope check stays — repo already gates by tenant.
- No SSE channel changes. No client changes needed (the optional types already accept the bundled shape).
- One round-trip = whole bundle. If load times suffer at scale (50+ flows × 50+ screens × ~5 modes), revisit with pagination — out of scope today.

**Patterns to follow:**
- Existing `HandleListViolations` (server.go ~line 472) shows the version-resolution + tenant-scoped fetch pattern in this file.
- Existing repo methods for list-by-version (verify exact names during impl).

**Test scenarios:**
- Happy path: GET /v1/projects/indian-stocks-research returns versions[2], screens[238], screen_modes (whatever count), available_personas (whatever count exists for tenant).
- Edge case: project with zero screens (just-created, pre-pipeline) → arrays present but empty; client renders empty state.
- Edge case: explicit `?v=<id>` returns screens for that specific version.
- Edge case: cross-tenant slug → 404 (existing behavior; verify).
- Error path: DB query failure on screens fetch → 500 with diagnostic; project fetch already returned successfully so partial-failure handling matters (decide: 500 the whole thing, or return project + log the screens error).

**Verification:**
- Manual: `curl -sS http://localhost:8080/v1/projects/indian-stocks-research -H "Authorization: Bearer $TOK" | python3 -m json.tool` — assert top-level keys include `project`, `versions`, `screens`, `screen_modes`, `available_personas`.
- UI: open `/projects/indian-stocks-research` — atlas canvas renders 19+ Research Product frames at preserved Figma (x,y); JSON tab shows screen tree; Violations tab shows the audit-pipeline result.
- **Rollback boundary:** server.go HandleProjectGet revert (one function). Client-side optional types absorb the shape change either way.
- **Gate: do not start U13 transition audit until U12 lands** — without screens to render, transitions have nothing to fade in/out, and the audit conclusions will be wrong.

---

- U13. **Per-page transition + entry/exit audit (the "senseless transitions" surface)**

**Goal:** Comprehensive audit + reconciliation of every page transition against the brainstorm's R27 expectation ("shared three.js + r3f render pipeline ... transitions between mind graph and project view are shared-element morphs at ~600ms"). The user's verbatim feedback (2026-05-02): *"once i click on the node the transitions is senseless you need to research each pages entry and exit at different page states etc."* This is broader than U2b/U3 (which only address the leaf morph + Esc reverse).

**Requirements:** R27 (transition fidelity); cross-cutting.

**Dependencies:** U2b + U3 + U4 + U12 (all need to land first; transitions can't be audited on broken page rendering).

**Files (audit, then targeted edits):**
- Read + assess: `lib/animations/timelines/atlasBloomBuildUp.ts` (atlas page initial paint).
- Read + assess: `lib/animations/timelines/projectShellOpen.ts` (project view mount: toolbar + atlas canvas + tab strip + tab content).
- Read + assess: `lib/animations/timelines/tabSwitch.ts` (intra-project tab switching).
- Read + assess: `lib/animations/timelines/themeToggle.ts` (light↔dark).
- Read + assess: `app/atlas/view-transitions.css` (created in U2b — duration/easing for cross-route morph).
- Read + assess: `/components` page mount — verify ComponentCanvas's mount animation is consistent with the unified curve language from U10.
- Read + assess: `/inbox` mount + filter changes — confirm transitions are present and consistent.
- Modify: each timeline file as needed to align with the unified easing family from U10 (recommend `EASE_DOLLY = "expo.inOut"` for camera-ish moves, `EASE_PAGE_OPEN = "expo.out"` for entrance-only).
- Documentation: `docs/runbooks/transitions.md` (new) — single page documenting every page entry, exit, and intra-page transition with the chosen curve + duration. Operator + future contributor reference.

**Approach:**
- For each page state transition (entry, exit, tab switch, filter change), capture: current curve + duration, brainstorm-implied curve + duration, gap, fix.
- The "senseless transitions" complaint suggests transitions that fire at the wrong time (after route mount instead of during) or with mismatched durations (250ms entrance + 600ms exit feels broken). Audit for these specific issues.
- Reduced-motion: every transition must collapse to a 0ms instant via `useReducedMotion()` from `lib/animations/context.ts` (canonical) OR via `@media (prefers-reduced-motion: reduce)` in CSS for the View Transitions side.
- Sample transitions to instrument with `performance.measure` so the audit can produce concrete numbers, not vibes.

**Patterns to follow:**
- The existing timeline files all follow the same shape (export `function timelineName(scope): Timeline { ... }`); audit findings should propose changes that preserve the shape.
- Single-curve consolidation per U10's motion-grammar plan.

**Test scenarios:**
- Each page mount: assert visible content within 800ms of route change.
- Tab switch within /projects/[slug]: assert no flash-of-empty-content during the 150ms out + 180ms in.
- /atlas → /projects/[slug] (forward morph): assert title bar text matches source leaf within 700ms (already in U2b's spec; verify it's still passing).
- /projects/[slug] → /atlas (Esc reverse): assert source flow leaf re-focused within 700ms (already in U3's spec).
- Reduced-motion: each transition lands instantly (0ms duration on the animation property; final state visible on first paint).

**Verification:**
- Document the audit findings in `docs/runbooks/transitions.md` (the audit IS the deliverable; targeted edits are follow-ups).
- Manual demo of each page transition with screen recording at 60fps.
- **Rollback boundary:** each timeline edit is independent; revert any one without affecting the others.

---

### Wiring Gaps Audit (2026-05-02 19:35) — items found while debugging the canvas-blank bug

These are gaps similar in shape to U12's HandleProjectGet defect — code that says "U7/U8 will extend this" or "TODO(U*-prod-wire)" but the follow-up never landed. Surfaced now so they can be tracked rather than continuing to silently break things downstream.

**In scope for this plan (folded into U13 follow-ups or separate units):**
- `lib/projects/types.ts:217-219` — comment about U7/U8 extending GET response — addressed by U12.
- `components/projects/ProjectShell.tsx:319, 475` — second `r.data.versions ?? []` reads suggest two different fetch paths populate version state. May become redundant after U12; document during U12 impl.

**Out of scope for this plan — flag for a separate backend audit-rules plan:**
- 8 `TODO(U*-prod-wire)` markers in `services/ds-service/internal/projects/rules/` — `theme_parity.go:65`, `a11y_contrast.go:80`, `cross_persona.go:325`, `prototype.go:38`, `loaders.go:2`, `component_governance.go:47`, `a11y_touch_target.go:83`, `flow_graph.go:40`. Production wiring of audit-rule loaders deferred; affects WHICH violations the audit pipeline detects. Backend-only; doesn't block atlas integration seams. **Recommend separate `/ce-plan` for "audit-rules production wiring closure."**
- `services/ds-service/internal/figma/dtcg/adapter.go:585` — "follow-up /v1/files/<key>/nodes calls (deferred to v1.1)" — token-extraction depth limit. Not in plan scope.

---

### Phase E — Verification

- U11. **Playwright spec covering AE-1 through AE-8**

**Goal:** Every brainstorm acceptance example has a Playwright spec or a documented "deferred to manual / out of scope" note. The morph + reverse morph + two-phase progression have specs that fail if any of U1-U10 regresses.

**Requirements:** R9.

**Dependencies:** U1-U10 all complete.

**Files:**
- Modify: `tests/projects/atlas-leaf-morph.spec.ts` (created in U2) — extend with multi-leaf coverage.
- Modify: `tests/projects/atlas-reverse-morph.spec.ts` (created in U3) — extend.
- Modify: `tests/projects/two-phase-pipeline.spec.ts` (created in U5) — extend.
- Modify: `tests/atlas-mind-graph.spec.ts` — extend with the per-type bloom presence assertion (U8).
- Test: new specs as needed for AE-1, AE-3, AE-5, AE-6, AE-7 — many already exist (`plugin-export-flow.spec.ts`, `audit-fanout.spec.ts`, `auto-fix-roundtrip.spec.ts`).

**Approach:**
- Audit the brainstorm AE-1 through AE-8 against existing specs.
- Map each AE to a spec (existing or new):
  - AE-1 (designer exports a flow): `tests/projects/plugin-export-flow.spec.ts` (exists).
  - AE-2 (theme parity auto-fix roundtrip): `tests/projects/auto-fix-roundtrip.spec.ts` (exists).
  - AE-3 (cross-persona consistency): may need new spec.
  - AE-4 (DRD + decision linkage): `tests/decisions/decision-creation.spec.ts` (exists; verify decision↔violation cross-link covered, extend if not).
  - AE-5 (mind graph reverse lookup hover card): `tests/atlas-mind-graph.spec.ts` (exists; verify hover card content).
  - AE-6 (re-export preserves DRD, refreshes audit): may need new spec.
  - AE-7 (token publish fans out): `tests/projects/audit-fanout.spec.ts` (exists; verify scope).
  - AE-8 (mind graph → flow morph): `tests/projects/atlas-leaf-morph.spec.ts` + `atlas-reverse-morph.spec.ts` (created in U2 + U3).
- For specs that need video evidence per the verification gate, capture videos to `test-results/videos/<spec>.mp4` and reference them in this plan's "Verification artifacts" section (created during execution).

**Patterns to follow:**
- Existing Playwright fixture pattern in `tests/projects/`.
- Trace retain-on-failure (already configured in `playwright.config.ts`).

**Test scenarios:**
- All AE-1 through AE-8 covered by either an existing or newly-added spec.
- The morph spec asserts: route changes, target text matches source text, transition completes within 700ms, no console errors.
- The reverse-morph spec asserts: Esc triggers route back, source node re-focused, no console errors.
- The two-phase spec asserts: spinner appears at view_ready, count increments at audit_progress, final count at audit_complete.
- The bloom spec asserts (visual / pixel-presence): products have higher canvas-pixel luminance than other types.

**Verification:**
- Run `npx playwright test` against a local dev server. All specs pass.
- Each AE has a spec mapped (mapping documented inline in this plan post-execution).
- **Rollback boundary:** specs only; no behavior changes.

---

## System-Wide Impact

- **Interaction graph:** `<ViewTransition>` wrapping introduces a new React component into both /atlas leaf rendering and /projects/[slug] toolbar. Esc keydown listener is global on document — must defer to focused inputs (DRD editor, version selector) so it doesn't hijack their semantics.
- **Error propagation:** Failed View Transition (e.g., name mismatch, browser unsupported) should fall back to instant route change with no animation. Reduced-motion users get the same instant path.
- **State lifecycle risks:** `morphingNode` state in `useGraphView` must auto-clear after the transition (~800ms timer). If the user navigates away mid-morph (e.g., direct URL paste), state could persist; clear on /atlas unmount.
- **API surface parity:** No backend API changes. Lifecycle PATCH, SSE channels, and project fetch all unchanged.
- **Integration coverage:** the U11 spec suite covers AE-1 through AE-8, the cross-feature seams that unit tests alone won't catch.
- **Unchanged invariants:** `BrainGraph.tsx`'s 682 lines of three.js + r3f are not refactored. Material / geometry caches stay. Particle system stays. Edge-pulse shader stays (extended in U9 for depth-fade only). `AtlasCanvas` LOD/texture cache untouched. Backend handlers untouched. SSE channel patterns untouched. Reduced-motion source (`lib/animations/context.ts`) untouched.

---

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Next.js `experimental.viewTransition` flag changes API in a 16.x minor | Pin Next 16.2.x exact in package.json; test on every Next upgrade; document the experimental marker in `next.config.ts` comments |
| Browser support: Safari 18 quirks, Firefox flag-gated | Reduced-motion fallback is the same outline-pulse path; users on unsupported browsers get the no-animation path automatically |
| Canvas snapshot during ViewTransition rasterizes the WebGL surface | Source is a DOM overlay (U2's "leaf-label-layer" pattern), not a canvas mesh. Verified during U2 implementation. |
| `react-force-graph-3d` may not expose label DOM positioning natively | If true, render the leaf-label DOM overlay as a sibling to the canvas, projecting node positions via the library's exposed projection function. Already a known pattern from `HoverSignalCard.tsx`. |
| Spring tuning is qualitative — too bouncy or too stiff | Tune in U10 implementation by visual feedback; capture before/after recordings; default to `{ tension: 170, friction: 26 }` |
| Existing GSAP timelines may conflict with new springs on the same element | Springs operate on r3f primitives (camera, mesh); GSAP operates on DOM. No overlap unless future code targets the same target with both — explicitly forbidden in `lib/animations/conventions.md`. |
| Plan's "B and F are mostly shipped" verification could find more gaps than expected | If verification finds significant gaps (e.g., LifecycleButtons backend integration broken), surface them in this plan's "Notes" section and re-scope. The plan's effort estimate has 1-2 day slack. |
| Per-type emissive bloom may visually overpower the design | Tune intensity values during U8 implementation; record before/after; if overpowering, halve all values. |
| `morphingNode` state auto-clear timer could fire before transition completes | `::view-transition` pseudo-elements do NOT reliably fire `transitionend` cross-browser. Use the next-route-mount signal (Next.js's `useSelectedLayoutSegment` change) as the primary clear, with an 800ms `setTimeout` safety net. Implementer detail in U4. |
| Force simulation continues to tick during the View Transition snapshot, leading to a stale source-label position | U2a's overlay subscribes to `view.morphingNode`. When set, freeze the projection updates (return early from the throttled `useFrame` projection pass). Resume on next route mount. |
| `<ViewTransition>` React component referenced in early plan drafts isn't in React 19.2.4 | Resolved during planning. We use the browser-native `view-transition-name` CSS property + Next 16.2's `experimental.viewTransition: true` (auto-wraps router nav in `document.startViewTransition`). No React component import. |
| Original "outline pulse" reduced-motion fallback is temporally disconnected from the click and reads as a glitch | Replaced with a static `/atlas › <flow name>` breadcrumb (U2c) that's always-on, providing spatial-continuity context whether or not motion is supported. Pulse retained only as a supplementary cue on the source side, not as the primary fallback affordance. |
| Cross-document View Transitions support narrower than headline numbers (Safari 18.2 Dec 2024 + iOS lag, Firefox flag-gated) | Real coverage May 2026: Chrome/Edge full, Safari 18.2+ partial (60-70% iOS), Firefox flag-off (~5% by default). Firefox-default users see instant nav with breadcrumb fallback (U2c) — no morph attempt, no console error. |

---

## Documentation / Operational Notes

- **`lib/animations/conventions.md`** (new, U10) — documents the three-tool motion convention: react-spring/three for triggered 3D, GSAP for sequenced DOM, useFrame+damp for ambient.
- **`docs/runbooks/deploy-domain-setup.md`** — already exists; this plan's changes don't alter the deploy story.
- **`docs/solutions/2026-05-XX-001-atlas-integration-seams.md`** (new, post-execution) — capture the View Transitions adoption + the Phase 5.1 outline-pulse-as-reduced-motion-fallback as institutional learnings.
- **No migration scripts.** No DB schema changes. No new SSE channels.

---

## Sources & References

- **Origin document:** [docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md](../brainstorms/2026-04-29-projects-flow-atlas-requirements.md)
- Related learnings:
  - `docs/solutions/2026-05-01-001-phase-6-closure.md` (mind graph + bloom + reduced-motion conventions)
  - `docs/solutions/2026-04-30-001-projects-phase-1-learnings.md` (R3F + Next 16 dynamic-import discipline)
  - `docs/solutions/2026-05-02-002-phase-5-1-collab-polish.md` (reduced-motion behavioral conventions, deep-link choreography)
  - `docs/solutions/2026-05-02-003-phase-5-2-collab-polish.md` (SSE fan-out, plugin deep-link conventions)
- Related code: `app/atlas/BrainGraph.tsx:392-417` (handleNodeClick), `app/atlas/LeafMorphHandoff.tsx`, `app/atlas/useGraphView.ts:37-77`, `components/projects/ProjectShell.tsx:121-128, 195-204, 343-392`, `components/projects/ProjectToolbar.tsx:92-94`, `lib/projects/client.ts:321-411`, `lib/animations/easings.ts`
- External docs:
  - [Next.js View Transitions guide](https://nextjs.org/docs/app/guides/view-transitions)
  - [next.config viewTransition reference](https://nextjs.org/docs/app/api-reference/config/next-config-js/viewTransition)
  - [vercel/next.js#49279 — Framer layoutId across App Router](https://github.com/vercel/next.js/issues/49279)
  - [@react-spring/three v10 + r3f v9 compatibility (Reagraph package.json)](https://github.com/reaviz/reagraph/blob/master/package.json)
  - [@react-three/postprocessing Bloom](https://react-postprocessing.docs.pmnd.rs/effects/bloom)
- Related issues / PRs: none yet.

---

## Phased Delivery

**Re-estimated post-review.** The original 7-9d estimate was wrong: U2's DOM-overlay layer is its own sub-project (now U2a + U2b + U2c), U6/U7 are net-new wiring not "verify" (Reactivate UX, persona×theme chips, linked-violations subsection, cross-link), U10's spring rollout touches three call sites, and **post-execution-start the canvas-blank bug surfaced U12 (HandleProjectGet wiring gap) and U13 (per-page transition audit)** — 2 more days. Honest estimate: **13-16 person-days** with the parallelism shown below. Critical path is U1 → U2a → U2b → U3 → U12 → U13, ~7-8 days serial.

### Phase A — Cross-route morph foundation (must land first; ~5-6 days serial critical path)

| Unit | Days | Parallel-with |
|---|---|---|
| U1 (config + cleanup) | 0.5d | — |
| U2a (DOM overlay layer) | 2-2.5d | U6/U7/U8/U9 may start |
| U2b (view-transition-name wiring) | 1d | U6/U7/U8/U9 continue |
| U2c (breadcrumb fallback) | 0.5d | parallel with U2b |
| U3 (Esc reverse) | 1.5d | U4 in parallel |
| U4 (LOD pin) | 1d | U3 in parallel |

**Gate: do not declare Phase A complete until U2b + U3 have video evidence of working forward AND reverse morph in Chrome + Safari.**

### Phase B — Tab UX completion (start day 1, parallel with Phase A; ~3-4 days)

U6 + U7 do not depend on the morph chain. Start them on day 1.

| Unit | Days |
|---|---|
| U5 (two-phase UI progression) | 1.5d |
| U6 (Reactivate + persona×theme chips) | 2d |
| U7 (Linked violations + cross-link) | 2d |

### Phase C — Visual hierarchy + motion grammar (start day 1, parallel; ~5 days)

U8 + U9 + U10 do not depend on the morph chain. Start them on day 1.

| Unit | Days |
|---|---|
| U8 (per-type emissive bloom hierarchy) | 1.5d |
| U9 (edge depth-fade shader) | 1d |
| U10 (springs + motion conventions) | 2.5d |

### Phase D — Verification (last; ~2 days)

U11 runs last because it asserts the final motion + interaction baseline.

| Unit | Days |
|---|---|
| U11 (Playwright AE-1 through AE-8 coverage) | 2d |

### Critical path

**U1 → U2a → U2b → U3 (with U4 in parallel) → U11 = ~7-8 days serial.**

Parallel work absorbs U5/U6/U7/U8/U9/U10 across that window. Total elapsed if executed by 1 person: **11-14 days** (serial bottleneck dominated by Phase A's overlay sub-project + the 3 substantial Phase B/C units).

If 2 people are available: one drives Phase A serial, one drives Phases B+C in parallel. Total elapsed drops to ~7-8 days.

---

## Operational / Rollout Notes

- **No feature flag.** The View Transitions experimental flag is in `next.config.ts`; it's a global toggle. If it causes issues, revert `next.config.ts` (one-line revert).
- **No staged rollout.** Local + Vercel preview environments use the same `next.config.ts`. Any regression is visible immediately on push.
- **Reduced-motion users get the Phase 5.1 outline-pulse fallback** — already established UX convention.
- **Browser support:** Chrome / Edge full support; Safari 18+ partial; Firefox behind flag (~78% global support per Can I Use March 2026). Falls through to instant nav for unsupported browsers.
- **Manual verification gates per unit are non-negotiable.** Each unit has a "record video before marking complete" gate. The user has explicitly rejected "claimed-complete-but-not-verified" work twice in this thread.
