---
date: 2026-05-17
topic: canvas-figma-dev-mode-parity
focus: rendering pipeline, selection, hover-highlights-layout-groups, snappy/rich camera, SVG-first for illustration/* and icon/* named frames
mode: repo-grounded
---

# Ideation: Canvas Figma-Dev-Mode Parity

> **Source-of-truth rule (user-stated):** Figma Dev Mode behaviour for snap, click, and hover is canonical. Where any item below would conflict with what Figma actually does, Figma wins.

> **Scope rule (user-stated):** "We are not building an MVP." All six survivors are in scope as one initiative.

## Grounding Context

### Codebase context (file:line)
- **Leaf canvas** `app/atlas/_lib/leafcanvas.tsx` (1043 lines) — RAF-driven camera, frame grid, click-to-inspector. Wheel/pinch zoom toward cursor with easeInOutCubic 320ms snap. Zoom bounds `[0.18, 2.0]`. No double-click zoom, no Space-drag pan, no Z+drag, no Shift+1/2/0, no N/Shift+N, no Cmd+F.
- **Frame renderer** `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` (1180 lines) — atomic selection via `data-atomic-selected`, Shift-click multi-select, lasso drag from whitespace, hover via `hover-signal.ts` pub-sub singleton, `MeasurementOverlay` paints padding/gap chips on settle. No Escape-to-deselect for atomics, no Enter/Shift+Enter, no Cmd+click deep-pick, no parent-container auto-highlight on child hover.
- **Tree → HTML** `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts` (1171 lines) — canonical_tree walker; icons/illustrations route through `renderClusterPlaceholder` → `<img>` from cluster-URL hook.
- **Classifier** `app/atlas/_lib/leafcanvas-v2/node-classifier.ts:87-88` — `ICON_NAME_RE = /^\s*icons?\s*\//i`, `ILLUSTRATION_NAME_RE = /^\s*illustrations?\s*\//i`. **Designer-naming convention already recognized.**
- **Pipeline** `services/ds-service/internal/projects/pipeline.go` Stage 9 — already runs `renderSVGClustersForVersion()` calling Figma `?format=svg&scale=1` for SVG-eligible clusters. Cached at `data/assets/{tenant}/{file}/{version}/{node}.svg`. **The SVG export pipeline exists.**
- **Client asset hook** `lib/hooks/useIconClusterURLs.ts:212` — mints `?format=svg` URLs **but consumer renders `<img src=svgURL>` not inline `<svg>`**. Vectors fly over the wire as files and the browser still rasterizes them. No DOM access for theming or hover-per-stroke.
- **MeasurementOverlay** `MeasurementOverlay.tsx` (782 lines) — currently mounted INSIDE `worldRef` so chips scale with zoom.

### Figma Dev Mode (external — Figma engineering blog + help center)
- Selection: blue `#0d99ff` ring, 2px outline-not-fill, screen-space (fixed width regardless of zoom). Click → top-level frame. Enter → descend. Shift+Enter / `\` → ascend. Cmd+click → deep-pick. Shift+click → multi-select toggle. Marquee drag → bounding-box select. Cmd+marquee → include nested. Tab/Shift+Tab → cycle siblings. Cmd+A → select all. Esc → deselect.
- Hover: composed — deepest atom gets thin blue outline AND containing autolayout's padding bands (magenta) + gap fills (blue) illuminate automatically.
- Alt-hover (with node selected) → red distance lines (bounds-to-bounds) between selected and hovered.
- Camera: critically-damped spring on `{x, y, z}`, ~250-300ms settle, ~3% overshoot. Shift+1 fit-all, Shift+2 fit-selection, Cmd+0 100%, N/Shift+N next/prev frame, Z+drag zoom-to-region, Space-drag pan, H persistent hand tool, Cmd+F search-by-name. Pixel grid at ≥400%. Zoom toward cursor focal point.
- Renderer substrate: Figma uses WebGL/WebGPU on a single `<canvas>`. We keep HTML-DOM by deliberate choice (autolayout cascades free via flexbox; per the Zeplin learnings doc). DOM is our substrate; we approximate Figma's compositor model by separating scene paint from chrome paint via a screen-space sibling layer.

### Past learnings
- `docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md` — dual-path renderer (flex autolayout + absolute fallback) is the right substrate; resist WebGL on the canvas layer.
- `docs/solutions/2026-05-01-001-designbrain-perf-findings.md` — LOD ladder (1024/2048/4096), zoom→tier formula, level-0 fallback to eliminate pop-in.
- `docs/solutions/2026-05-02-003-phase-5-2-collab-polish.md` — Figma metadata server-side proxy with TTL cache precedent.

## Topic Axes

1. **Selection model** — click target, multi-select, keyboard nav (Enter/Shift+Enter/Tab/Esc/Cmd+A), deep-pick (Cmd+click), marquee, parent traversal, ring rendering
2. **Hover & inspection feedback** — composed hover (atom outline + autolayout structure), dimension chips, Alt-hover distance lines, breadcrumb ancestor, Dev Mode panel sync
3. **Camera mechanics & feel** — spring physics, Figma hotkey set, fly-to-selection, focal-point pinning, zoom bounds, frame-to-frame nav
4. **Asset routing — SVG-first** — name-prefix routing (`illustration/`, `icon/`), inline `<svg>` vs `<img>`, fetch-from-Figma path, designer-naming-canonical doctrine, `<symbol>` + `<use>` deduping
5. **Render performance & overlay layer** — screen-space chrome layer outside world-transform, `nodeId → worldRect` derived store, rAF chrome layout pass, zero React renders during camera flight

## Ranked Ideas

### 1. SVG-first vector pipeline for `illustration/*` and `icon/*`
**Description:** End-to-end inline-SVG path for named vector groups. (a) Pipeline skips `IsSVGEligible()` for named frames — trust the designer assertion. (b) Pipeline fetches Figma SVG once and embeds the raw markup as `svg_markup` on the canonical_tree node. (c) `nodeToHTML.ts` branches on `node.svg_markup` and emits inline `<svg>...</svg>` via `dangerouslySetInnerHTML`; no asset-stream subscription. (d) Same-named instances become one `<symbol id=...>` in a defs root + N `<use href="#..."/>`. (e) "Designer name is canonical" doctrine threaded through pipeline + classifier + manifest.
**Axis:** Asset routing
**Basis:** `direct:` `node-classifier.ts:87-88` regexes; `pipeline.go` Stage 9 `renderSVGClustersForVersion`; `useIconClusterURLs.ts:212` already mints `?format=svg` URLs that the consumer renders as `<img>` — the gap is purely the inlining step.
**Rationale:** Unlocks crisp-at-any-zoom + per-stroke hover/selection + dark-mode `currentColor` token swap + a11y roles + copy-as-SVG + hover micro-animations + removal of the asset-stream race for vectors. Six downstream features become 10-line PRs.
**Downsides:** Larger canonical_tree payloads; one-time backfill migration; need to verify Figma SVG IDs are deterministic across runs (otherwise `<use>` cache breaks).
**Confidence:** 92%  **Complexity:** Medium  **Status:** Explored

### 2. Composed hover: atom outline + autolayout structure illumination
**Description:** Match Figma's actual hover semantics. Hover target stays at the deepest atom under cursor (thin 2px `#0d99ff` outline, screen-space). Whenever that atom is inside an autolayout ancestor, the ancestor's **padding bands** (magenta over inner padding region) and **gap fills** (blue across gap regions) illuminate simultaneously — no panel toggle. Dimension chip (W × H) snaps to the atom's edge, screen-space. Shift-hover descends; Alt-hover ascends. Paints on rAF-throttled mouse-move, not on settle.
**Axis:** Hover & inspection feedback
**Basis:** `external:` Figma help center (*Select layers and objects*, *Navigate designs in Dev Mode*) + `direct:` `hover-signal.ts` already pub-sub-deduped; `MeasurementOverlay.tsx` already paints padding/gap chips on settle (the data path exists).
**Rationale:** "Read first, click second" is the inspection loop that makes Dev Mode feel like x-ray vision. Today our hover teaches nothing about layout structure.
**Downsides:** Ancestor-chain memoization needed; chip placement edge cases near viewport bounds; need to decide what counts as a container (every `layoutMode != NONE` or only explicitly named groups).
**Confidence:** 90%  **Complexity:** Low-Medium  **Status:** Explored

### 3. Chrome layer outside the world-transform, fed by `nodeId → worldRect` store
**Description:** The architectural backbone. (a) Build `nodeId → worldRect = {x, y, w, h, parentLayoutId}` derived store populated on canonical_tree load, invalidated only on tree mutation. (b) Mount one `<div class="chrome-layer">` as sibling of `worldRef` with a single `<svg>` inside holding pre-allocated `<rect>`/`<path>`/`<text>` elements for selection, hover, padding bands, gap fills, distance lines, marquee, breadcrumb, dimension labels. (c) On every rAF tick and on state change, read `(camera, spatial-store)`, compute screen-rects, mutate attributes via refs. **Zero React renders in the hot path.** (d) Selection ring is always 2px regardless of zoom; padding bands stay legible at any zoom.
**Axis:** Render performance & overlay layer
**Basis:** `external:` Figma engineering blog (*Building a professional design tool on the web*) on compositor separation + `reasoned:` `MeasurementOverlay.tsx` currently inside `worldRef` so chips scale with zoom — Figma's chrome is screen-space.
**Rationale:** Foundation for every other survivor. Without S3, S2's outline scales weirdly, S4's camera animation makes overlays lag, S5's breadcrumb has no screen-space layer to live in, S6's distance lines are stuck in world coords. With S3, future affordances (comments, redlines, AI annotations, presence cursors) become 50-line PRs.
**Downsides:** Selection state migrates out of `LeafFrameRenderer` React state; risk of behavior drift during refactor; need careful design of the derived-store's invalidation contract.
**Confidence:** 85%  **Complexity:** High  **Status:** Explored

### 4. Spring-physics camera + full Figma hotkey set + selection-implies-camera
**Description:** Replace tween snap with a **critically-damped spring** on `{x, y, z}` driven by one rAF loop. Stiffness ~180, damping ~26 (tunable). Wheel/pinch deltas feed the same spring → natural momentum. Full Figma keymap: Esc deselect, Tab/Shift+Tab cycle siblings, Cmd+A select-all, Cmd+0 100%, +/− zoom, Shift+1 fit-all, Shift+2 fit-selection, N / Shift+N jump named frames, Z + drag zoom-region, Space-drag pan, H persistent hand tool, Cmd+F find-by-name palette, Cmd+click deep-pick (S5), Shift+D Dev Mode toggle (S6). Selecting a node from search, breadcrumb, or layer tree implicitly springs the camera to fit it. Once S1 lands, raise the zoom ceiling — vectors have no LOD wall.
**Axis:** Camera mechanics & feel
**Basis:** `external:` Figma blog camera physics + community consensus on settle behavior + `direct:` `leafcanvas.tsx` linear easeInOutCubic 320ms today, bounds `[0.18, 2.0]`, missing every hotkey listed.
**Rationale:** Spring physics is the single biggest "feels like Figma" tell. Linear tweens always read as scripted; springs feel alive. Combined with S3, the rAF loop writes one CSS transform on `worldRef` and one batched attribute mutation on the chrome layer — chrome stays perfectly anchored through the flight.
**Downsides:** Spring tuning is subjective; needs feel-testing. Hotkey collisions with browser defaults (Cmd+F, Cmd+A) require focus-scoping. N/Shift+N requires a stable named-frame ordering.
**Confidence:** 80%  **Complexity:** Medium  **Status:** Explored

### 5. Frame-first selection with Cmd+click deep-pick, Enter / Shift+Enter cycling, breadcrumb strip
**Description:** Default click selects the **top-level frame** under cursor (the screen / section being read), not the deepest text glyph. **Enter** descends one level; **Shift+Enter** (or `\`) ascends. **Cmd+click** deep-picks the deepest hit. **Breadcrumb chip** in the chrome layer above the canvas (`Screen › illustration/hero › group/badge › icon/star`); clicking any segment selects that ancestor. Hover precomputes the full ancestor chain so Enter/Shift+Enter and breadcrumb clicks are O(1). Esc deselects; second Esc closes breadcrumb.
**Axis:** Selection model
**Basis:** `external:` Figma help (*Select layers and objects*) + `direct:` `LeafFrameRenderer.tsx:151-178` currently selects deepest `data-atomic-selected` — the inverse of Figma's behavior.
**Rationale:** "First click feels wrong" permanently damages trust. Today our deepest-atom-wins rule is a one-line semantic difference from Figma that produces a thousand papercuts. Pairs with S4's keymap and S3's chrome layer (breadcrumb is a chrome-layer chip).
**Downsides:** Behavioral change for existing users; need to define "top-level frame" precisely (section root vs phone-screen root vs nearest named).
**Confidence:** 88%  **Complexity:** Medium  **Status:** Explored

### 6. Alt-hover red distance lines + Dev Mode as a render flag (not a route)
**Description:** With a node selected, holding Alt + hovering another node draws four red distance segments (top/bottom/left/right gap) with px labels at midpoints — bounds-to-bounds semantics, fixed pixel weight, renders in the chrome layer (S3). Disappears on Alt release. `devMode: boolean` threads through `nodeToHTML`; in Dev Mode, autolayout padding/gap fills are always visible, TEXT shows baseline guides, image fills show their constraint mode (FILL/FIT/STRETCH) as a corner annotation. Same DOM nodes, different paint. **Shift+D** toggles (Figma's hotkey).
**Axis:** Hover & inspection feedback
**Basis:** `external:` Figma help (*Measure distances between layers*) + Figma blog (*Everything you need to know about Dev Mode*).
**Rationale:** Alt-hover is the most Figma-signature interaction — once your hand learns it, every other tool feels primitive. Dev-Mode-as-flag (not a `/dev` route) guarantees design view and dev view never drift apart — a class of bug that's free to never have.
**Downsides:** "Distance" semantics need decision (bounds-to-bounds vs visible-pixel vs autolayout-padding). Dev Mode visualization choices need design (which annotations, when noisy).
**Confidence:** 78%  **Complexity:** Low-Medium (once S3 lands)  **Status:** Explored

## Sequencing note

S3 is the architectural prerequisite for S2, S5, S6 (all paint into the chrome layer) and pairs with S4 (chrome stability during camera flight). S1 is independent and the biggest user-visible win — can ship in parallel with S3. S4 can land before S3 (linear tween → spring is internal to the camera) but won't feel right until S3 ships the chrome stability.

## Rejection Summary

| # | Idea | Reason Rejected |
|---|------|-----------------|
| A2 | Hover precomputes ancestor chain (standalone) | Subsumed into S5 |
| A3 | R-tree spatial index for hit-test | Premature given current scale (~50 frames/leaf); revisit at >5k nodes |
| A4 | Selection rings as overlay SVG (standalone) | Subsumed into S3 |
| A5 | Selection encoded in URL | Polish-tier; defer until deep-linking is a stated need |
| A6 | Unit-of-selection = component instance | Subject-shift — user said Figma Dev Mode overrides; component-as-unit is not Figma's model |
| A8 | Right-click "Select layer" disambiguation | Subsumed into S5's Cmd+click deep-pick |
| A9 | Pre-rendered selection-rect cache | Subsumed into S3's nodeId→worldRect store |
| B2 | Hover paints padding/gap automatically | Subsumed into S2 |
| B4 | Dimension chips on raw hover | Subsumed into S2 |
| B6 | Peek panel for hovered named frame | Scope expansion beyond Figma parity |
| B8 | Dev Mode panel as peer iframe (BroadcastChannel) | Premature; one-window scope is sufficient |
| B9 | Bidirectional canvas ↔ layer-tree highlight | Layer-tree UI not built yet; defer with that work |
| C3 | Remove [0.18, 2.0] zoom bound | Folded into S4 (becomes pointless once S1 lands) |
| C4 | Vector-only above threshold | Mechanism inside S1 (LOD swap is conceptual, not user-visible) |
| C5 | Mapbox flyTo focal-point pinning | Subsumed into S4's spring camera |
| C6 | Camera = one CSS transform, zero React renders | Subsumed into S3 + S4 |
| C7 | Figma hotkey set (standalone) | Subsumed into S4 |
| C8 | Camera state in URL | Polish-tier; defer |
| C9 | Minimap navigator | Not a Figma Dev Mode behavior — scope expansion |
| C10 | One infinite canvas (atlas/leaf collapse) | Borderline big-bet reframing; strong but high cost — defer until S1–S6 ship |
| D6 | SVG `<symbol>`/`<use>` sprite atlas | Folded into S1 step (d) |
| D7 | SVG-always for design-system clusters | Scope expansion beyond named-vector subset |
| D8 | Server-side render manifest per node | Foundation idea but secondary; revisit via brainstorm if S1 surfaces it |
| D9 | Layer toggles (KiCad-style) debug surface | Not Figma Dev Mode parity |
| E2 | Tile pyramid for atlas LOD | Cluster pyramid already exists (Stage 9); duplicate |
| E3 | content-visibility:auto vs cluster prerender | Speculative replacement of working system |
| E4 | Auto zoom-tier dies for vectors | Subsumed into S1 |
| E6 | rAF-stable chrome layout pass | Subsumed into S3 |
| E7 | Pre-allocated SVG overlay; attr mutation not React | Subsumed into S3 |
