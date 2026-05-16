---
date: 2026-05-17
topic: canvas-figma-dev-mode-parity
---

# Canvas Figma-Dev-Mode Parity

## Summary

Bring Figma-Dev-Mode-grade interaction to the single-screen viewer (leaf canvas): click selects the deepest frame, hover outlines that frame and illuminates the containing autolayout's padding/gap, the camera uses spring-physics with the full Figma hotkey set, illustrations and icons render as inline SVG with PNG fallback, and a chrome layer outside the world-transform keeps selection rings, breadcrumb, and distance lines crisp at any zoom. Ships with a right-side inspect panel showing the selected node's layout / typography / fills (no code generation in v1).

---

## Problem Frame

PMs, designers, and developers reading design-system documentation flip between our canvas and Figma constantly to verify what they're looking at. Today the leaf canvas paints frames as composable HTML at near-pixel-perfect fidelity (two audit rounds in May 2026 shipped 13 bug-class fixes), but the *interaction layer* is a different story:

- **Click selects the deepest atom under the cursor** — `LeafFrameRenderer.tsx:151-178` — so clicking a button's label glyph selects the glyph, not the button. The first click of every session feels wrong, which permanently colors trust in the canvas.
- **Hover only highlights the deepest atom** via `hover-signal.ts`. Hovering across three "stat tiles" lights up three separate text-node halos instead of showing the row's layout structure.
- **Camera is functional but flat** (`leafcanvas.tsx`): linear `easeInOutCubic` 320ms on snap, no spring on wheel/pinch, zoom bounds clipped at `[0.18, 2.0]`, and missing every Figma hotkey (Esc/Tab/Cmd+A/Shift+1/Shift+2/Cmd+0/N/Shift+N/Z+drag/Space-drag/H/Cmd+F).
- **Illustrations and icons fly over the wire as `<img src=…?format=svg>`** even though the pipeline already exports SVG bytes (`pipeline.go` Stage 9 `renderSVGClustersForVersion`; `useIconClusterURLs.ts:212` already mints `?format=svg` URLs). Because the SVG is wrapped in an `<img>`, the browser re-rasterizes it, we lose all DOM access (no per-stroke hover, no theme tinting, no a11y roles, no copy-as-SVG).
- **Overlay chrome lives inside `worldRef`** — `MeasurementOverlay.tsx` and CSS-attribute-driven selection rings — so chips scale with zoom (2px ring becomes a 16px bar at 8×, vanishes near minimum zoom).

The cost is concrete: every inspection task involves at least one round-trip to Figma to verify the layout structure, the exact spacing, or the named frame name. That round-trip is the moment the team mentally downgrades the canvas from "the place to read about the design system" to "a faster preview of Figma."

---

## Actors

- A1. **Design-system reader**: PMs, designers, and developers who open the canvas to inspect a screen's structure, dimensions, components, and illustrations.
- A2. **Designer (asset author)**: produces the Figma file the canvas reads from. Owns the naming contract (`illustration/<name>`, `icon/<segment>/<segment>/…`) that determines which nodes ship as inline SVG.
- A3. **Pipeline operator**: re-imports Figma files via `cmd/import-figma-url` and monitors Stage 9 SVG export + PNG-pyramid generation; sees the silent SVG→PNG fallback through logs.

---

## Key Flows

- F1. **Inspect a frame**
  - **Trigger:** A1 has a leaf canvas open and wants to know the layout structure / dimensions / properties of one part of the screen.
  - **Actors:** A1
  - **Steps:** Cursor moves over the area → chrome layer paints a 2px screen-space outline on the deepest frame under the cursor AND illuminates the containing autolayout's padding bands (magenta) + gap fills (blue); dimension chip (W × H) snaps to the outline edge → A1 clicks → the same frame is now selected; the inspect panel on the right updates to show layout/typography/fills for the selected node → A1 hits Esc to deselect or Enter to descend one level.
  - **Outcome:** A1 reads layout structure without opening Figma.
  - **Covered by:** R3, R4, R5, R6, R12, R13

- F2. **Inspect an illustration up close**
  - **Trigger:** A1 wants to look at the strokes / paths inside a named illustration frame.
  - **Actors:** A1
  - **Steps:** A1 hovers an illustration → chrome highlights `illustration/<name>` → A1 clicks → the inspect panel shows the frame name and properties → A1 hits Shift+2 to fly the camera to the selection (or double-clicks it) → camera springs to fit the illustration with a small overshoot-and-settle → A1 zooms further with the trackpad past the old 2× ceiling; the inline `<svg>` stays crisp at any zoom because there's no LOD ceiling for vector content → A1 Cmd+clicks one path inside the SVG to select that single vector.
  - **Outcome:** A1 can inspect individual paths and confirm the illustration matches the spec without exporting from Figma.
  - **Covered by:** R1, R2, R7, R8, R9, R12

- F3. **Measure distance between two frames**
  - **Trigger:** A1 has one frame selected and wants to know how far it sits from another.
  - **Actors:** A1
  - **Steps:** A1 holds Alt → cursor over a second frame → chrome layer draws four red distance segments (top/bottom/left/right gap) with px labels at midpoints, bounds-to-bounds → A1 releases Alt → distance lines disappear.
  - **Outcome:** A1 knows the spacing without measuring in Figma.
  - **Covered by:** R10, R11

- F4. **Navigate between named frames on a screen**
  - **Trigger:** A1 wants to jump between named regions of a long screen.
  - **Actors:** A1
  - **Steps:** A1 hits `N` → camera springs to the next named frame in canvas-coordinate order → A1 hits Shift+N → camera springs to the previous one. Or A1 hits Cmd+F → a name-search palette opens → A1 types `illustration/` → matches list → A1 picks one → camera flies to it and selects it.
  - **Outcome:** A1 navigates by name rather than by visual hunting.
  - **Covered by:** R7, R14, R15

---

## Requirements

**Asset routing (SVG-first for named vector groups)**

- R1. Pipeline trusts the designer-name contract: any frame whose name matches `^\s*illustrations?\s*/` or `^\s*icons?\s*/` skips the structural `IsSVGEligible()` check and is targeted for SVG export.
- R2. For SVG-targeted frames, the pipeline fetches Figma `?format=svg&scale=1` for the frame and embeds the raw SVG markup as a field on the canonical-tree node so the client never has to make a runtime fetch.
- R3. The leaf renderer inlines the SVG markup directly into the DOM (`<svg>…</svg>`), not via `<img src=…>`. The inlined SVG is DOM-addressable: themeable via CSS, hoverable per stroke, and traversable for accessibility.
- R4. Repeated instances of the same named vector group on one leaf canvas share a single `<symbol id=…>` in a defs root, referenced by `<use href="#…"/>` from each instance. (The browser dedupes paint and we cut DOM weight.)
- R5. When SVG export fails for a named frame (Figma rate-limit, transient API error, or a frame with image fills / blurs / unsupported features), the renderer silently falls back to the existing PNG pyramid (`<img src=…?format=png>`). The PNG pyramid is still built for every named frame as the backstop.
- R6. The "designer name is canonical" rule is the single source of truth for asset routing — structural classification heuristics may still run for unnamed clusters, but name match wins when both fire.

**Selection model**

- R7. Click selects the deepest frame under cursor among the types `FRAME`, `COMPONENT`, `INSTANCE`, `GROUP`. Vector paths, text glyphs, and other atom-level nodes are not selectable on a default click.
- R8. Cmd+click (Ctrl+click on Windows) bypasses R7 and selects the deepest hit, including atom-level nodes inside an inlined SVG.
- R9. Enter descends one level (selects the first child frame of the current selection); Shift+Enter (or `\`) ascends to the parent. When already at the leaf-canvas root, ascending is a no-op.
- R10. Shift+click toggles a frame into a multi-selection. The selection ring renders as a single outline around the union of selected frames. The inspect panel shows shared properties or a "Multiple selected" state when properties differ.
- R11. Dragging from canvas whitespace (no key) starts a marquee that selects every frame whose bounding box intersects the marquee rectangle. Cmd+marquee includes deeper-nested frames; Shift+marquee removes intersecting frames from the existing selection.
- R12. Escape clears the selection. A second Escape closes the breadcrumb chip if it's open. Cmd+A (Ctrl+A) selects every frame on the leaf canvas at the current depth.

**Hover & inspection feedback**

- R13. Hover paints a 2px screen-space outline (Figma blue `#0d99ff`) on the deepest frame under the cursor — the same target a click would select.
- R14. When the hover target sits inside an autolayout ancestor (any ancestor with `layoutMode != NONE`), the chrome layer automatically renders that ancestor's padding bands (Figma magenta) over its inner padding region and gap fills (Figma blue) across its gap regions. No panel toggle, no Dev Mode toggle required for these to appear on hover.
- R15. A dimension chip (W × H of the hovered frame, in px) renders in the chrome layer at fixed pixel size, snapped to the outline edge that is most visible in the viewport. Chip placement avoids clipping at viewport bounds.
- R16. With a node selected, holding Alt and hovering a second node draws four red distance segments (top / bottom / left / right gap), bounds-to-bounds, with px labels at each segment's midpoint. Segments disappear on Alt release or when the hover clears.
- R17. Breadcrumb chip in the chrome layer shows the ancestor chain of the current selection (e.g., `Screen › section/wallet-cards › illustration/empty-state › icon/star`). Clicking a segment selects that ancestor. On multi-selection, the chip shows the longest common ancestor chain; when no common parent exists, the chip shows the leaf-canvas root.
- R18. Hover overlays and ring rendering update on raw mouse-move (rAF-throttled), not on settle. The user reads first, clicks second.

**Camera mechanics**

- R19. Camera state `{x, y, zoom}` updates via a critically-damped spring driven by a single rAF loop. All transitions — wheel/pinch zoom, snap-to-fit, fly-to-selection — feed the same spring (no mixed easing models). Spring parameters are tuned by side-by-side comparison with Figma rather than copied from published values.
- R20. The camera writes a single `transform: translate(...) scale(...)` to the world-transform layer per rAF tick. No React re-renders fire during camera flight; chrome layer and inspect panel read camera state via refs and update on rAF.
- R21. Selecting a node from search, breadcrumb, or via N / Shift+N springs the camera to fit that node automatically. No manual Shift+2 is required for these entry points; Shift+2 still works as a separate explicit "fit to current selection" hotkey.
- R22. Zoom bounds: lower bound = "fit-all of the leaf canvas with 5% padding"; upper bound raised to ~32× (was `[0.18, 2.0]`). Vector content has no LOD ceiling once SVG-first is in place; the ceiling exists only to prevent runaway gestures.
- R23. Hotkey set, all scoped to canvas focus (must not steal browser defaults when canvas is not focused):
  - **Selection:** Esc (deselect / close breadcrumb), Cmd+A (select all on leaf), Tab / Shift+Tab (cycle siblings of current selection), Enter / Shift+Enter / `\` (descend / ascend), Cmd+click (deep-pick).
  - **Camera:** Shift+1 (fit-all), Shift+2 (fit-selection), Cmd+0 (zoom to 100%), `+` / `-` (zoom in / out), N / Shift+N (next / previous named frame in canvas order), Z held + drag (zoom-to-region rectangle), Space held + drag (pan), H (toggle persistent hand-tool mode).
  - **Search:** Cmd+F (open a name-search palette listing every named frame on the leaf; type to filter; arrow keys + Enter to navigate and select; Esc to close).
  - **Mode:** Shift+D (toggle Dev Mode render flag — see R28-R31).

**Chrome layer & overlay architecture**

- R24. All visual chrome (selection ring, hover outline, padding bands, gap fills, dimension chip, distance lines, breadcrumb, marquee rectangle) renders in one screen-space layer mounted as a sibling of (not child of) the world-transform layer. Chrome elements maintain fixed pixel size regardless of camera zoom.
- R25. A derived `nodeId → worldRect` store is populated when the canonical tree loads, invalidated only on tree mutation. The chrome layer reads `(camera, spatial store)` on every rAF tick and on selection / hover state change, computes screen-rects, and mutates element attributes via refs. Selection and hover state never invalidate from camera motion alone.
- R26. The selection ring stroke is 2px Figma blue `#0d99ff`. The hover outline is 2px Figma blue, with reduced opacity so a selected-and-hovered frame reads as "selected" first.
- R27. Chrome paint completes in the same animation frame as the user input that caused it (selection, hover, camera tick). The acceptable upper bound is one rAF frame (≤17ms at 60Hz); paint-after-state-change is unobservable to the user.

**Dev Mode**

- R28. Dev Mode is a global render flag (`devMode: boolean`) threaded through the renderer, not a separate canvas / route. Toggling it does not unmount any DOM; the same nodes re-paint with additional annotations.
- R29. When Dev Mode is on, autolayout frames render their padding bands and gap fills *always* (independent of hover); text nodes render with a baseline guide; image fills render their constraint mode (FILL / FIT / STRETCH) as a small corner annotation.
- R30. A right-side inspect panel (drawer, collapsible) is always present in v1 and is driven by the current selection. The panel renders three sections for the selected node: **Layout** (W / H, layout mode, padding, gap, primary axis alignment), **Typography** (font family, size, weight, line-height, letter-spacing, text color — present only when the selection is a TEXT node), **Fills & strokes** (solid fills as hex, image fills as a "Has image fill" indicator, strokes as weight + color). The frame name is shown at the top of the panel.
- R31. When 0 nodes are selected the inspect panel shows an empty state. When 2+ nodes are selected with differing values for any displayed property, that property reads "Multiple" rather than picking one value.

---

## Acceptance Examples

- AE1. **Covers R5.** Given a frame named `illustration/empty-state-watchlist` contains an image fill (which Figma cannot export as clean SVG), when the pipeline runs Stage 9, the SVG export fails for that node and the renderer silently uses the PNG pyramid; the canvas shows the illustration with no broken-asset placeholder.
- AE2. **Covers R7, R8.** Given the user clicks directly on a vector path inside an inlined illustration SVG, when no modifier key is held the selection lands on the `illustration/<name>` frame as one unit; when Cmd is held, the selection lands on the individual vector path.
- AE3. **Covers R13, R14.** Given the user's cursor is over a button glyph inside an autolayout row, the hover outline renders around the button frame (deepest frame), AND the autolayout row's padding bands and gap fills illuminate at the same time, simultaneously, on raw hover.
- AE4. **Covers R17.** Given two frames are selected and they share no common parent frame inside the leaf, when the breadcrumb chip renders, it shows the leaf-canvas root as the only segment.
- AE5. **Covers R20, R27.** Given the user presses Shift+1 (fit-all), during the camera flight there are zero React component re-renders of frame content, and the selection ring stays anchored to the selected frame's edge throughout the flight with no visible lag.
- AE6. **Covers R22.** Given the user pinch-zooms past 2× on a leaf canvas with inline SVG illustrations, the illustrations remain crisp at 8×, 16×, 32×; raster PNG fallbacks remain crisp up to the bounds of their 2048-tier pyramid resolution.
- AE7. **Covers R23.** Given canvas is focused and the user presses Cmd+F, a name-search palette opens listing every named frame on the leaf; pressing Cmd+F while the palette is open does not toggle the browser's Find (the canvas hotkey wins).
- AE8. **Covers R30, R31.** Given the user selects two button frames with different background fills, the inspect panel's "Fills & strokes" section reads "Multiple" for the fill value and shows shared properties (W, H, layout mode) at their actual values.

---

## Success Criteria

- A first-time canvas user clicks the illustration on a screen and is selecting "the illustration" on the first try, not a vector path inside it.
- An A1 reader can identify the layout structure (padding, gap, alignment) of an autolayout row without opening Figma — they hover, they see it.
- Side-by-side video with Figma Dev Mode on the same source frame: the click, hover, selection ring, padding visualization, dimension chip, Alt-hover distance line, breadcrumb, and camera flight (springs, Shift+1, Shift+2) read as visually equivalent to a non-technical reviewer. ("Ditto Figma" is signed off subjectively by the user, not by a numeric metric — see Key Decisions.)
- Inline SVG icons stay crisp at 32× zoom; the team stops hitting the "blurry icon at deep zoom" complaint pattern in the next two audit rounds.
- Selection ring stays exactly 2px regardless of zoom; padding bands remain legible at both 0.05× and 32×.
- For `ce-plan`: the brainstorm decisions above are enough that planning does not have to re-resolve "what does click select", "what does hover show", "what's the camera model", "what does the inspect panel show", or "what happens when SVG fails".

---

## Scope Boundaries

- **Atlas (multi-screen overview) parity is deferred.** The `/atlas` page keeps its current interaction behaviour. Same chrome-layer / spring-camera / selection-model architecture should be portable to atlas in a phase-2 effort, but that's not in this scope.
- **No code generation in the inspect panel.** The panel shows layout / typography / fills as values, not as CSS / Swift / Android code.
- **No design-token mapping in the inspect panel.** Hex `#1947FF` reads as `#1947FF`, not as `--ind-color-primary-500`. Design-token lookup is a follow-up.
- **No selection-in-URL deep-linking.** Selection state lives in memory; share-this-selection links are a follow-up.
- **No camera-state-in-URL.** Camera state is ephemeral; reload returns to fit-all.
- **No minimap navigator.** Out of Figma Dev Mode behaviour scope.
- **No "Compare changes" between versions.** Figma Dev Mode has it; not in v1.
- **No peer-iframe inspect panel.** The inspect panel is part of the same React tree, not a detachable window.
- **No R-tree / spatial-index library.** Hit-test, lasso, marquee at current scale (~50 frames per leaf canvas) walk the canonical tree directly. Revisit if leaf-canvas frame counts approach 5,000+.
- **No replacement of the canonical-tree depth=14 fetch limit, the cluster-pyramid system for unnamed clusters, or the asset-stream SSE infrastructure.** All three remain in place; v1 layers on top.
- **No migration of the rendering substrate to WebGL / Canvas2D.** HTML-DOM stays the substrate per the Zeplin canvas learnings.
- **No reframing of the atlas/leaf boundary into a single infinite zoomable canvas.** That ideation candidate was deferred as a big-bet rewrite.

---

## Key Decisions

- **Click target is the deepest FRAME (FRAME/COMPONENT/INSTANCE/GROUP), not Figma's strict "outermost frame on page."** Rationale: our context is documentation reading where a named illustration is one inspection unit; selecting the whole phone screen on every first click would be hostile in a docs viewer. Cmd+click preserves the deep-pick affordance for designers who want to reach atoms.
- **SVG-first with silent PNG fallback** (not SVG-only). Rationale: a visibly broken illustration costs more user trust than the marginal storage / Figma-API savings of dropping the pyramid for named frames. Resilience > savings.
- **Inspect panel ships with property display but no code generation or token mapping in v1.** Rationale: properties deliver real Dev Mode value and unblock the inspection loop; code-gen and token mapping are large surfaces that warrant their own brainstorms and shouldn't be hidden behind a Dev Mode rollout.
- **"Ditto Figma" sign-off is subjective side-by-side comparison, not a numeric metric.** Rationale: Figma doesn't publish camera spring parameters or exact ring opacities; tuning these to feel right is a judgment call. The user (chetansahu) does final side-by-side sign-off.
- **HTML-DOM substrate retained** (no migration to WebGL/Canvas2D). Rationale: autolayout cascades free via flexbox per `docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md`; switching substrate would erase that.
- **Atlas overview deferred to a follow-up phase.** Rationale: scope discipline; same architecture should port to atlas later with the chrome-layer and spring-camera primitives already in place.
- **Multi-select breadcrumb falls back to the leaf-canvas root when there's no common ancestor**, rather than disappearing. Rationale: a non-empty chip is more informative; "you're in this leaf" beats nothing.

---

## Dependencies / Assumptions

- **Figma SVG export returns deterministic node IDs across runs.** The `<symbol>` + `<use>` deduping in R4 relies on this. If IDs are non-deterministic, the deduping silently degrades but doesn't break (each instance just inlines its full markup); the requirement still holds.
- **Designer adherence to the naming convention** (`illustration/<name>`, `icon/<segment>/<segment>/…`) determines which frames get SVG-first treatment. Unnamed vector groups continue through the existing pyramid path. The doctrine R6 says "named name is canonical, structural heuristics fall back" — designers control which frames upgrade to SVG-first by naming them correctly.
- **Pipeline Stage 9 SVG export is already production** (`pipeline.go` `renderSVGClustersForVersion` → `client.go:267` `?format=svg`). Verified.
- **`useIconClusterURLs.ts:212` already mints `?format=svg` URLs.** The consumer-side rewrite to inline-`<svg>` is purely client-side; server endpoints don't change.
- **Canvas keyboard focus model.** Hotkeys are scoped to canvas focus; tab order, focus indicators, and click-to-focus are assumed to exist or to be added during planning so Cmd+F / Cmd+A don't fight browser defaults when the canvas isn't focused.
- **Chrome layer uses HTML/SVG, not Canvas2D.** Final substrate (HTML divs with CSS borders vs. one screen-space `<svg>`) is a planning decision; the requirement is "screen-space, fixed pixel size, no React renders during camera flight" — substrate flexibility is intentional.

---

## Outstanding Questions

### Resolve Before Planning

- *(none)* — all product decisions captured in this brainstorm.

### Deferred to Planning

- [Affects R19, R27][Technical] What spring parameters land on "matches Figma" in side-by-side? Tune during implementation against a recorded Figma screen-capture reference.
- [Affects R24, R25][Technical] Should the chrome layer be one `<svg>` element with `<rect>`/`<path>`/`<text>` children, or positioned `<div>` elements with CSS borders? Both meet the screen-space requirement; pick during planning based on benchmark of attribute-mutation cost.
- [Affects R30][Needs research] Exact placement / collapse behaviour of the inspect panel (right drawer width, breakpoints, what stays visible when collapsed). Defer to a small design pass during planning.
- [Affects R11][Technical] Marquee performance ceiling — naive bounding-box-intersection works at current scale (~50 frames per leaf); planning should benchmark and decide whether to short-circuit at high frame counts or schedule the R-tree work earlier than the "5,000+" trigger noted in Scope Boundaries.
- [Affects R23][Technical] Z-held-and-drag zoom-to-region rectangle implementation: shares marquee infrastructure or independent? Decide during planning.
- [Affects R23][Needs research] Browser-default conflict mapping for Cmd+F, Cmd+A, `\`, Space — confirm canvas-focus-scoping works cleanly across Chrome, Safari, Firefox.
