---
title: "Atlas UX principles — comprehension + precision design heuristic"
created: 2026-05-02
status: living
last_reviewed: 2026-05-02
---

# Atlas UX principles

Design heuristic for `/atlas` (the mind-graph navigator). Future polish consults this doc before re-tuning illumination, animation duration, hover, click, label affordances, or color tokens. Cited from `docs/plans/2026-05-02-005-fix-atlas-interaction-polish-plan.md` U5.

The mind graph is the brainstorm's **navigator + reverse-lookup atlas** (R20, R22, AE-5, AE-8 in `docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md`). Two failure modes destroy it: drift toward eye-candy at the expense of comprehension, or lose precision in the click/hover affordances. This doc names both and pins concrete rules so neither erodes silently.

---

## Section 1: User lenses

Four named perspectives. Every tuning decision is traceable to at least one lens.

### CEO

Opens `/atlas` to ask: **"What's our system?"**

- Wants at-a-glance comprehension: products read first, folders second, flows third.
- Has 30 seconds before they alt-tab. Bloom + animation must not steal those seconds — they're paid for *comprehension*, not *dazzle*.
- Theme: matters. If `/atlas` ignores the brand's light/dark theme, the surface reads as off-brand and the CEO loses confidence in everything downstream.
- Doesn't click into individual flows. The brain itself is the artifact.

**Failure mode:** every node glows equally → CEO can't tell where to look first → bounce.

### PM

Opens `/atlas` after a Slack ping ("the Stocks Filter flow has a bug, where is it?").

- Wants precision pointing: hover lands on cursor, click is deterministic, labels align with their nodes.
- Reads search results immediately — non-matches dim, matches stay sharp, count visible.
- Mental model: graph → flow leaf → click → land on /projects/<slug>. Any glitch in that sequence destroys trust in the graph as a navigation tool.

**Failure mode:** click → "loading" placeholder → /projects → PM thinks "this is broken" and stops using the graph.

### Designer (in-product, A1 from origin)

Uses `/atlas` as the reverse-lookup atlas: "who else uses this Toast component?"

- Toggles filter chips (Components, Tokens, Decisions) and watches satellite nodes appear.
- Hovers components to see usage count.
- Cross-platform comparison: switches Mobile ↔ Web toggle.

**Failure mode:** filter toggle is sluggish; satellite nodes don't read as "satellites" (visual hierarchy unclear).

### DS lead (A3 from origin)

Uses `/atlas` for org-wide pattern spotting.

- Same comprehension needs as CEO + the filter chips.
- Notices when product nodes' violation counts cluster and pulls cross-product patterns.

**Failure mode:** illumination too dazzling → can't distinguish severity counts at a glance.

---

## Section 2: Concrete rules

Each rule has: **statement**, **why** (which lens), **concrete number / criterion**, **what violating it costs**.

### 2.1 Visual hierarchy

- **Node radii differ by ≥50% between tiers.** Why: CEO + DS lead. Concrete: products radius ≥ 2× folder radius ≥ 1.5× flow radius. Cost of violating: the tiers visually flatten; comprehension fails.
- **Bloom strength stays ≤0.7 unless explicitly designing a hero state.** Why: CEO. Concrete: `app/atlas/BrainGraph.tsx` UnrealBloomPass strength = 0.7 (Plan 005 U4). Threshold stays 1.0 (the gate, not the gain). Cost: products feel "matte" or everything blooms equally.
- **Emissive intensity ratio between top tier (product) and bottom tier (token/decision) ≥4×.** Why: DS lead, designer. Concrete: products 1.75 vs tokens/decisions 0.4 → 4.4× ratio. Cost: tokens compete with products for attention.
- **HDR threshold gating preserves selectivity.** Why: technical invariant. Concrete: only materials with `color × emissiveIntensity > 1.0` (post-tone-mapping bypass) bloom. Cost: bloom dazzles low-tier nodes.

### 2.2 Animation

- **Triggered transitions ≤700ms.** Why: PM, designer. Concrete: View Transitions duration `600ms` (cubic-bezier expo.inOut); camera spring `tension 170 / friction 26`. Cost: feels laggy.
- **Three-tool motion grammar.** Why: technical invariant carried from Plan 003 U10 (`lib/animations/conventions.md`). Concrete: react-spring/three for triggered 3D, GSAP for sequenced DOM, useFrame+damp for ambient. Cost: motion feels fragmented.
- **No simultaneous overlapping animations on the same element.** Why: PM. Plan 005 U1 cautionary tale: `router.replace` + `router.push` running ~50ms apart caused the Suspense fallback to flash between them — looked like a glitch. Cost: trust eroded.
- **No camera spring on flow-leaf click.** Why: PM, technical. Concrete: `BrainGraph.handleNodeClick` returns immediately on flow nodes; only the cross-route morph fires. Camera springs are for product/folder zoom only. Cost: spring + morph fight at the View Transition snapshot moment.

### 2.3 Hover

- **Hover card mounts within 50ms of hover-start.** Why: PM. Concrete: react-force-graph-3d's `onNodeHover` fires synchronously; React state update + render = <16ms; card mount within one frame.
- **Card re-positions on graph re-tick within 16ms (one frame).** Why: PM, designer. Concrete: rAF loop in BrainGraph projects the hovered node's screen position each frame, commits when delta ≥0.5 px. Cost: card lags behind drifting nodes.
- **Hover-out unmounts within 100ms.** Why: PM. Standard React unmount via prop change + cleanup. Cost: stale card lingers.
- **Card flips at viewport edges.** Why: PM. Concrete: if `right > viewport.width - 8`, flip to left of node; same for bottom. Cost: card rendered off-screen.

### 2.4 Click

- **Exactly one orchestrated transition.** Why: PM. Concrete: flow → cross-route morph; product/folder → camera spring zoom; satellite (component/token) → re-center graph + dim siblings. Never overlap. Cost: see U1 cautionary tale above.
- **History mutations use `window.history.replaceState`, not `router.replace`.** Why: PM. Plan 005 U1 root cause. Routing primitives invalidate the route + flash Suspense fallbacks. Pure URL mutations bypass that. Cost: `BrainGraphSkeleton` flashes between click and route push.
- **No double-fire under StrictMode.** Why: technical invariant. Concrete: ref-guard pattern (`bloomBuildUpPlayedRef` from Plan 003 U14a). Cost: double `router.push` calls.

### 2.5 Labels

- **Centered on node center within ±2px.** Why: PM, designer. Concrete: CSS `transform: translate(-50%, -50%) translate3d(<x>px, <y>px, 0)` on `LeafLabelLayer.tsx` div (Plan 005 U3). Cost: labels visually disconnected from their nodes.
- **Truncate via existing toolbar styles.** Why: edge case. Concrete: long flow names (e.g. "Filters for Stock Screener" at 30 chars) truncate via `text-overflow: ellipsis` on a max-width container, not by overlapping the next node.
- **DPR-correct projection.** Why: technical invariant. Concrete: `Vector3.project(camera)` returns NDC; convert to CSS pixels via `(ndc.x + 1) / 2 * canvasWidth`. `renderer.getSize(target)` returns CSS pixels in three.js r180 — do NOT multiply by `renderer.getPixelRatio()`. DPR only enters when reading from the framebuffer. Cost: 2× drift on retina displays.

### 2.6 Color

- **Scene background reads `--bg-canvas` token.** Why: CEO. Concrete: `app/atlas/forceConfig.ts:backgroundColor()` reads `getComputedStyle(document.documentElement).getPropertyValue('--bg-canvas')` at runtime, with `#050810` SSR fallback. MutationObserver re-reads on `data-theme` flip. Cost: hardcoded blue ignores the brand's theme.
- **Per-type colors live in `forceConfig.ts:NODE_VISUAL` only.** Why: maintainability. No hardcoded hex outside that file. Cost: tuning becomes a multi-file hunt.
- **Light-theme `--bg-canvas` stays a deep ink (`#0b1424`)** even when the rest of the site is white. Why: technical — the bloom + glowing nodes need darkness to read against. The brain-graph aesthetic is intentionally immersive; this is the documented exception, not a regression. Cost: bloom invisible in light theme.

### 2.7 Search

- **Non-matches dim to opacity 0.3.** Why: PM. Concrete: `app/atlas/SearchInput.tsx` + `BrainGraph.tsx` filter pipeline — non-matching nodes get `material.opacity = 0.3` while matches stay at 1.0. Cost: PM can't find what they searched for.
- **Match count visible in the search input.** Why: PM. **Currently missing — flag as a polish followup**. Cost: PM doesn't know if a search returned 1 match or 100.

### 2.8 Reduced-motion

- **Every animation collapses to instant (0 ms) under `prefers-reduced-motion: reduce`.** Why: a11y. No exception. Concrete: CSS `@media (prefers-reduced-motion: reduce) { animation-duration: 0s }` on view-transition pseudo-elements; JS spring `api.set()` instead of `api.start()`. Cost: a11y regression.
- **Reduced-motion source = `lib/animations/context.ts`.** Why: convention from Phase 6 closure. Cost: parallel hooks drift.

---

## Section 3: Verification checklist for any future /atlas tuning

Before merging any /atlas-touching commit, the implementer verifies:

1. ✅ **Hierarchy preserved** — products visually distinguishable from folders distinguishable from flows. Side-by-side recording before/after.
2. ✅ **Animation grammar honored** — react-spring/three for triggered, GSAP for sequenced, useFrame+damp for ambient. No new motion library introduced.
3. ✅ **Hover precision** — card lands within ±4 px of node center; flips at viewport edges; unmounts on hover-out.
4. ✅ **Click determinism** — exactly one transition per node type; no overlapping animations; no `router.replace` for pure URL mutations.
5. ✅ **Label centering** — `translate(-50%, -50%)` anchor on every label. CSS pixels, not device pixels.
6. ✅ **Color tokens** — no hardcoded hex outside `forceConfig.ts`; scene background reads `--bg-canvas`; theme toggle re-paints.

If any checkbox can't be ticked, the commit goes back to revision before merge.

---

## Section 4: Concrete examples from Plan 005

Each unit cited demonstrates the heuristic in action:

| Unit | Defect | Lens | Rule | Fix landed |
|---|---|---|---|---|
| U1 | Click-glitch | PM | 2.4 "exactly one orchestrated transition" + 2.4 "window.history.replaceState" | `cce57f6` — `router.replace` → `window.history.replaceState` |
| U2 | HoverSignalCard top-left | PM | 2.3 "card lands within ±4 px" | `ea4072e` — manual `Vector3.project` projection + rAF tick |
| U3 | Labels left-aligned | PM, designer | 2.5 "centered on node center within ±2 px" | `281f0e1` — `translate(-50%, -50%)` anchor |
| U4 | Hardcoded blue + over-bright | CEO, DS lead | 2.1 (bloom ≤0.7, ratio ≥4×) + 2.6 (`--bg-canvas` token) | `32582a1` — emissive halved, BG token-aware, bloom 0.7 |

Each fix preserved the engine. The plan never touched the 682-line `BrainGraph` orchestration architecture, the d3-force simulation, the LOD culling, or the audit/decision/violation cross-link wiring. **Surgery on the seams**, not a rewrite.

---

## Section 5: Living-doc review cadence

This doc is "living" — re-read before any /atlas tuning commit. Update the `last_reviewed` frontmatter date on every substantive edit. If a rule turns out to be wrong (e.g. user feedback says products should glow MORE, not less), update the rule + cite the feedback + record the date — don't silently retune in code without updating this doc.

Sister runbooks:
- `docs/runbooks/transitions.md` — per-page transition audit, including the click-glitch findings cross-referenced from U1.
- `docs/runbooks/playwright-coverage.md` — AE-1 to AE-8 spec coverage, including atlas-leaf-morph.
- `docs/runbooks/deploy-domain-setup.md` — operator runbook for the running stack.
- `lib/animations/conventions.md` — three-tool motion grammar codified from Plan 003 U10.
