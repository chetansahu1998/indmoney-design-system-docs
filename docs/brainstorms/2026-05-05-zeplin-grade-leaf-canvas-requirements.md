---
title: Zeplin-grade interactive leaf canvas
status: draft
created: 2026-05-05
brainstorm-skill-version: 3.1.0
---

# Zeplin-grade interactive leaf canvas

## Problem frame

Today's leaf canvas stamps **one flat PNG per screen** — exported once from Figma's REST API, positioned at the screen's stored x/y/w/h, never decomposed. Every layer's identity (text, icon, button, autolayout container) is collapsed into pixels. As a result:

- Engineers can't read CSS / iOS / Android tokens off the canvas — they go back to Figma.
- Designers can't iterate on copy in-context — they go back to Figma.
- Asset extraction (icons, illustrations) means manual Figma exports per node.

We already have the raw material to fix this:

- `screens.canonical_tree` (a gzipped column populated by the audit pipeline) contains the **full Figma node tree per screen** — every child's `layoutMode`, `primaryAxisSizingMode`, `itemSpacing`, padding, type styles, fills, etc.
- We have `FIGMA_PAT` server-side and a working REST client (`figma_resolve.go`).
- The leaf canvas already mounts at `app/atlas/_lib/leafcanvas.tsx` and is wired to per-leaf overlays.

The gap is purely on the rendering side: stop drawing the flat PNG, start reconstructing the frame from `canonical_tree` so each child becomes a real, hoverable, exportable, (text-)editable object.

## Primary jobs (co-equal)

This canvas serves **two** jobs that drive the design:

- **A1: Spec & handoff (Zeplin parity).** Engineers / QAs open a leaf to read pixel-true layout, copy CSS / iOS / Android tokens, eyeball spacing & colors, export individual icons or illustrations as PNG / SVG.
- **A2: Live copy iteration.** Designers / PMs edit text content directly on the canvas; the surrounding layout reflows automatically (autolayout-respecting); changes persist immediately to ds-service so the next viewer sees them.

These are co-primaries, not alternatives. Any v1 that ships with only one is a partial product.

## Decisions captured

### D-spike — De-risk results (2026-05-05)

Built a real-data spike against frame `2882:56419` to validate D1 + D3a before committing to planning. Spike artifacts in `/tmp/spike/` (Python tree-walker → static HTML, side-by-side with Figma's official PNG render).

**D1 dual-path renderer — VALIDATED**

- 2,498 atomic nodes emitted from a 2,980-node tree (482 GROUPs flattened, no visual loss).
- Frame size matches Figma exactly (375 × 812, clipped via `clipsContent: true` → `overflow: hidden`).
- Position math correct on first try: top status bar lands at top, autolayout cards render mid-frame at the right Y offsets, bottom tab bar lands at bottom — all without manual tweaks.
- Autolayout containers (the dark wallet/intraday band, the BANK NIFTY / NIFTY 50 strip, the bottom 5-tab bar) render as `display:flex` with correct `flex-direction`, `gap`, `padding`, `justify-content`, `align-items` mapped from the Figma fields.
- Free-positioned children (status bar elements, hand-positioned headers) render as `position:absolute` with `left = childX - parentX` and stay where Figma puts them.
- Mixed mode (autolayout content cards inside an absolute-positioned screen frame) works without contortion.

What the spike doesn't cover (not D1-thesis blockers — pure rendering polish): VECTOR-as-SVG fetch via `/v1/images?ids=…&format=svg`, IMAGE fills via `/v1/files/{file}/images` to resolve `imageRef`, `@font-face` Basier Circle, default text color when `fills` is empty (currently renders white-on-white). All of these are isolated work items, none require re-architecting D1.

**D3a autolayout reflow — VALIDATED**

Edited a text node ("▲+1.2 Market " → "Buy now extended copy") inside an autolayout HORIZONTAL container with two icon siblings, then measured every layout box:

| Measurement                | Before  | After   | Delta |
| -------------------------- | ------- | ------- | ----- |
| Text node width × height   | 78.5 × 16  | 78.5 × **27.4** | +11.4 px height (wrapped to 2 lines) |
| Parent autolayout container | 118 × 24 | 118 × **25.58** | +1.58 px height |
| Sibling icon Y (1 of 2)    | 260     | **260.78** | +0.78 px reflow |
| Sibling icon Y (2 of 2)    | 260.64  | **261.44** | +0.80 px reflow |

Browser flexbox cascaded the size change end-to-end with **zero JS layout code**. The behaviour matches Figma's source-design intent for autolayout subtrees, which is exactly the user requirement ("respect autolayout; surrounding context auto-adjusts").

**Fidelity-iteration findings (4 spike revisions deep)**

Worked the spike from "structural skeleton" through "icons + image fills + token-mapped text colors". Each revision surfaced learnings now folded into open questions:

- **`visible: false` honoring** — Figma JSON marks hidden states (modals, expandable menus, A/B variants) as `visible: false`. Our walker MUST filter these or they stack on top of real content. Real-data check: 161 of 2,980 nodes (5.4%) in `2882:56419` are hidden states. Add to D1 implementation rules.
- **Autolayout child-sizing semantics** — `min-height` ≠ `height`. Use `flex:1 1 0` when `layoutGrow=1`, `align-self:stretch` when `layoutAlign=STRETCH`, exact `bbox.width`/`height` otherwise. Mixing `min-height` causes containers to grow past designed size, breaking the cascade.
- **Text overflow policy** — Figma's renderer fits text in the bbox at the source font's metrics. Inter (our fallback for Basier Circle) is slightly wider; default to `white-space:nowrap` + `overflow:hidden` on text so layout doesn't balloon. Once Basier Circle is licensed and embedded, drop nowrap and metrics will match.
- **Co-positioned design states** — A frame can contain *multiple* children at the same coords representing different scroll positions, click states, or interactive variants. Some are filtered by `visible:false`; others rely on designer convention (named "ALT", "Open", etc.). Honest answer: **planning needs to surface a "mode" / "state picker" UI for frames that contain multiple states**, not silently render all of them.

**Conclusion**

The two highest-risk *architectural* decisions — render strategy (D1) and reflow contract (D3a) — hold up against real INDmoney Figma data. Cards, autolayout containers, position math, image fills, color tokens, font fallback all render correctly. Pixel-perfect parity on one specific frame is no longer brainstorm work; it's implementation iteration that planning + execution will sharpen against real user feedback.

Spike artefacts under `/tmp/spike/` (Python tree-walker, 4 HTML revisions, side-by-side comparison PNGs) are kept as reference for the planning phase, not promoted into the repo.

Recommend proceeding to `/ce-plan` with the architectural decisions locked and the fidelity-iteration learnings above as planning inputs.

### D0 — Real-data findings (grounding evidence)

Validated decisions below against two live Figma nodes from the **ChatBot** file (`biCujLq55esHwP6LLjbat5`):

- **Section** `2821:23205` ("Updated UI") — `type=SECTION`, 4613 × 3585 px, 26 children.
- **Frame** `2882:56419` ("INDstocks") — `type=FRAME`, 375 × 812 px (mobile), 16 children, parent is the section above.

What the API actually returns and what it means for rendering:

1. **Section is a flat grid of frames at absolute coords.** All 26 frames are 375 × 812 mobile screens laid out in a 11-col × 3-row grid: x increments of ~435 px (375 frame + 60 gutter), y rows at 8109 / 9257 / 10405 (~1148 px row pitch = 812 + 336 gutter). The SECTION itself has `layoutMode=null` — frames are positioned, not autolayout-flowed. Implication for leaf canvas: enumerate the section's `FRAME` children directly; we don't render the section's chrome (no border, no label).
2. **Real INDmoney frames mix autolayout AND absolute positioning.** A full walk of `2882:56419` (2,980 nodes total) finds **562 autolayout containers (~18%)** with `layoutMode`, `itemSpacing`, `padding*`, `layoutAlign`, `layoutGrow` set. Pattern observed:
   - **Top-level frame chrome** (status bar, menu shell, LOADER, navigation bar) — hand-positioned (no autolayout).
   - **Content cards, buttons, lists, indicator strips** ("Trending", "Sensex", "Nifty50", per-item rows) — autolayout containers, often 4–8 levels deep.

   Both modes must coexist; a designer-edited text node is almost always inside an autolayout content card whose siblings DO need to reflow on edit. The dual-path renderer in D1 is mandatory.
3. **Coords are global, large, often negative.** The frame sits at `x=-1481.87, y=9257`. To render a child *inside* its parent frame as `position: absolute`, compute local coords by subtracting the parent's x/y: `localX = child.x - parent.x; localY = child.y - parent.y`.
4. **Child node types we have to handle**: `TEXT` (has `characters` + `style` + `characterStyleOverrides`), `GROUP` (container; flatten — has no own visual), `FRAME` (visual container, rendered like a div), `RECTANGLE` / `ELLIPSE` (shapes — render as styled divs or fetch as image), `VECTOR` (paths — fetch as SVG via images endpoint), `INSTANCE` / `COMPONENT` (rendered like FRAME). Tree depth observed: 5 levels.
5. **`GROUP` nodes don't have their own visual.** They exist as namespacing containers. Their `absoluteBoundingBox` is the union of their children. We can flatten away GROUP wrappers when walking — render the GROUP's children directly at their own absolute coords.
6. **`VECTOR` paths come with `fillGeometry` / `strokeGeometry` arrays in the JSON when `geometry=paths` is requested.** That's complex to reconstruct in CSS. Cheaper and pixel-true: fetch the rendered SVG via `/v1/images/{file}?ids=<id>&format=svg`. Same endpoint also gives PNG / 2x / 3x for D6 export.
7. **Text style fields actually present**: `style.fontFamily`, `style.fontSize`, `style.fontWeight`, `style.letterSpacing`, `style.lineHeightPx`, `style.textAlignHorizontal`, `style.textAlignVertical`, `style.fills` (color array). `characterStyleOverrides` is a per-character index array pointing into a `styleOverrideTable` — used when one word in a sentence has different styling. v1 handles uniform-style text; mixed-style is OQ-4.
8. **Frame size 375 × 812 = iPhone X mobile.** Confirms the mobile-first assumption that the brain platform = mobile path serves; web frames at 1440 × 900+ would be a separate section.

These learnings shape D1 and add D3a below.

### D1 — Render: HTML + CSS reconstruction (flex when autolayout, absolute when not)

Walk `canonical_tree`, emit a React component tree per screen. Each node renders based on whether autolayout is present:

**Path A — autolayout container (`layoutMode` present)** — render as a flex container:

| Figma                                     | CSS                       |
| ----------------------------------------- | ------------------------- |
| `layoutMode: HORIZONTAL` / `VERTICAL`     | `flex-direction`          |
| `itemSpacing`                             | `gap`                     |
| `paddingLeft / Right / Top / Bottom`      | `padding`                 |
| `primaryAxisSizingMode: AUTO` (HUG)       | `flex: 0 0 auto`          |
| `primaryAxisSizingMode: FIXED`            | fixed `width` / `height`  |
| `primaryAxisAlignItems`                   | `justify-content`         |
| `counterAxisAlignItems`                   | `align-items`             |

**Path B — free-positioned container (`layoutMode` null/absent)** — render as `position: relative` with each child `position: absolute` computed from the child's `absoluteBoundingBox` minus the parent's:

```ts
const localX = child.absoluteBoundingBox.x - parent.absoluteBoundingBox.x;
const localY = child.absoluteBoundingBox.y - parent.absoluteBoundingBox.y;
// → <div style={{ position:'absolute', left: localX, top: localY,
//                 width: child.absoluteBoundingBox.width,
//                 height: child.absoluteBoundingBox.height }} />
```

This dual-path is **not optional** — the real INDmoney files (verified D0) are mostly Path B. Earlier draft assumed autolayout everywhere; that was wrong. Both paths must coexist; a single screen may even mix them (autolayout list inside a free-positioned card).

**`GROUP` nodes are flattened.** They have no own visual (no `fills` / `strokes` rendered for groups in our use case); we walk through them and render their children at their own coords. Saves DOM nodes and matches Figma's mental model.

**Why HTML/CSS, not react-konva / fabric / SVG / WebGL** — autolayout reflow on text edit (D3) is a perfect fit for browser flexbox. Native flex handles the cascade for free on every keystroke; canvas/WebGL libraries would force us to write our own layout engine. SVG with `<foreignObject>` for text has known browser bugs and no flexbox. Native HTML also gives us `contenteditable`, accessibility, copy/paste, and i18n input modes for free. Path B's absolute positioning is also trivial in HTML.

Trade-off accepted: ~10k DOM elements when all 200 screens are mounted. Mitigation: **viewport virtualization** — only frames whose bounding box intersects the visible viewport are mounted; off-screen frames render a placeholder skeleton at the right size so the canvas grid stays correct.

### D2 — Editability: text only

The only mutation surface on the canvas is the **text content** (`characters` field) of `TEXT` nodes. Style (font size, weight, color), layout, and structure are read-only. Non-text children (icons, images, frames, instances) are inspectable + exportable but not editable.

Rationale: scoping edits to text means we never have to write back into the Figma file or maintain a divergent "design DB" — text is the only field where the value of in-context editing dominates the cost of rebuilding fidelity.

### D3 — Reflow on text edit must respect autolayout

When a user edits a text node's content, the surrounding layout **must reflow exactly as Figma would**: the text's parent frame grows / shrinks per its `primaryAxisSizingMode`, sibling spacing stays at `itemSpacing`, ancestors that are `HUG` cascade up. This is the explicit user requirement.

Falls out of D1 for free: HTML flexbox reflow on content change is browser-native.

Edge cases planning needs to handle:
- **Text constrained by `FIXED` width** — text wraps within the box; height grows; ancestors react.
- **Text in `FILL` parent that becomes wider than parent** — clipped or wrapped per Figma's `textAutoResize` (NONE / WIDTH_AND_HEIGHT / HEIGHT). Mirror that in CSS via `white-space` + `overflow`.
- **Mixed-style text runs** (bold word inside a sentence) — handled per `characterStyleOverrides`; rendered as nested `<span>`s with style overrides.

### D3a — Reflow behaviour by parent layout mode

The reflow contract is "match the source design's behaviour". Whether the surrounding layout reacts to a text edit depends on the **immediate parent's `layoutMode`**, walked at edit time:

- **Parent has `layoutMode` (autolayout)** — dominant case for editable text, since content cards, buttons, list rows, badges, and chips are autolayout in real INDmoney designs (D0 finding #2). The text's bbox grows / shrinks; siblings respect `itemSpacing` (gap); the parent grows or shrinks per its `primaryAxisSizingMode`; size changes cascade up through ancestor autolayout containers until they hit a `FIXED` ancestor. This is exactly what the user wants ("respect autolayout properties; context auto-adjusts"), and browser flexbox produces it for free.
- **Parent does NOT have `layoutMode` (absolute / hand-positioned)** — applies to text in the top-level frame chrome (status-bar labels, hand-positioned headers, the few floating elements without an autolayout wrapper). The text's own bbox grows per `textAutoResize` (`WIDTH_AND_HEIGHT` → both axes; `HEIGHT` → height only with fixed width; `NONE` → bbox stays, content overflows). **Siblings are NOT reflowed** — they stay where they are. This matches Figma's actual behaviour for non-autolayout parents; we mirror it exactly. Visually, a long override may overlap a sibling — that's the same outcome Figma would produce, so we treat it as design-time signal rather than a bug.

The `[overridden]` badge (D9) is visible in either case so the designer always sees that an override is present; the layout consequence is honest to Figma's source behaviour.

**Outcome**: text edits "respect autolayout properties; surrounding context auto-adjusts" everywhere autolayout exists (the dominant case), and stay put where absolute positioning is the source design intent. No surprise behaviour either way.

### D4 — Persistence: inline override, saved immediately, re-attaches on leaf re-import

Each text edit is an **override** on the screen's child node, saved to ds-service on blur (or 500 ms debounce). The next viewer sees the edited copy immediately. No approval gate, no review queue, no draft branching.

**Storage shape** (table-or-column choice deferred to planning):

Override is keyed on `(screen_id, figma_node_id)` as the primary identifier, with `(canonical_path, last_seen_original_text)` as fallback fingerprints used only when the node-id can't be matched after a re-import. Fields:

| Field                       | Purpose                                                                 |
| --------------------------- | ----------------------------------------------------------------------- |
| `screen_id`                 | FK to `screens`                                                         |
| `figma_node_id`             | Primary anchor — stable across re-imports unless the designer deletes the node |
| `canonical_path`            | Dot-separated path through `canonical_tree` (`0.children.2.children.4`); secondary anchor |
| `last_seen_original_text`   | Fingerprint of the pre-override `characters` value; tertiary anchor    |
| `value`                     | The edited text                                                         |
| `updated_by`, `updated_at`  | Audit                                                                   |
| `status`                    | `active` \| `orphaned` (set when re-import can't find the anchor node) |

**Re-import behavior** (sheet-sync runs every 5 min and re-imports the whole leaf — every screen's `canonical_tree` is rebuilt):

1. After a leaf's screens are re-imported, walk every existing override for those screens.
2. For each override, try to locate the node in the new `canonical_tree`:
   - **First** by `figma_node_id` (~99% of cases — Figma node IDs are stable unless the node is deleted+recreated).
   - **Then** by `canonical_path` (catches the case where Figma re-keyed the node but the structural position is the same).
   - **Then** by `last_seen_original_text` walking the tree (catches "I deleted and recreated the same text").
3. If found, update `canonical_path` to the new path, leave the override active.
4. If not found after all three attempts, mark the override `status = orphaned`. Orphaned overrides surface in a per-leaf "Overrides needing review" inspector tab where the designer can manually re-attach (drag onto a target node) or delete.

This three-tier match was chosen because re-imports happen automatically every 5 min — silent path-shift bugs would be very hard to catch. Keying on Figma node-id catches the common case for free; path + fingerprint fallbacks make the rest recoverable.

**Reverting an edit** = explicit "Reset to original" button per child in the inspector; deletes the override row.

**Conflict policy**: last-write-wins. We accept this for v1 because edits are typically scoped (one designer per leaf at a time); a real conflict regime can layer on later if needed.

**Override-reach in v1** — confirmed via brainstorm; these are the surfaces an override propagates to:

- **Canvas display** (always — that's the point).
- **⌘K global search.** Search for the edited string finds the screen; search for the original string does not. Re-index whenever an override is created / updated / deleted.
- **Asset export filenames.** When the user exports an icon adjacent to an overridden text node, the auto-generated filename uses the override (e.g. `mtf-statement__buy-now-icon.svg`, not `mtf-statement__buy-icon.svg`).
- **Activity feed.** Each override emits an `override.text.set` / `override.text.reset` event into the leaf's existing Activity tab — same surface used by violations, decisions, comments — with old → new diff and the editor's email. Gives PM a rolling paper trail without a separate review queue.
- **Token / code snippets** (see D9). When the user copies CSS / iOS / Android for a TEXT node, the snippet uses the override value, not the Figma original. Engineers copy "what the user will actually see".
- **Per-leaf "Copy overrides" inspector tab.** All active and orphaned overrides for the open leaf, sortable by who/when, with one-click reset. PMs scan this to review every copy edit on a leaf in one place.

**Surfaces that intentionally do NOT reflect the override** (originals win):
- The brain graph's screen / flow / project counts (counts don't change on text edit).
- Underlying Figma file (no write-back; Figma stays canonical for structure / style).

### D5 — Layer depth: frames keep current behavior, atomic children add on top

Two interaction surfaces coexist on the canvas, and **the existing one is unchanged**:

- **Frame selection (current behavior, unchanged):** clicking a screen-level frame's chrome (border, background, empty whitespace) selects that frame exactly as it does today — the inspector switches to the frame's flow / version metadata, the URL updates, etc. Anything the leaf canvas does today when you click a frame still happens.
- **Atomic-child selection (new):** clicking an **atomic** child *inside* a frame — `TEXT`, icon shapes (`RECTANGLE` / `ELLIPSE` / `STAR`), icon `INSTANCE` components, image fills — selects that child for inspect / export / text-edit. The frame stays the active context; the inspector adds a sub-section for the selected child.

Intermediate containers (`FRAME` / `GROUP` / `COMPONENT` *inside* a screen frame) are **not** selectable as separate atomic units; clicking on them passes through to the nearest atomic child. This keeps the in-frame layer panel scannable while preserving the screen-frame selection users already rely on.

Rationale: scope-creeping the existing frame-click interaction would risk regressing every today-working flow (open leaf, hover frame, click frame to navigate). The cheap-and-safe move is "frames behave as today; new behavior fires only when the click lands on a real atomic descendant."

A future iteration may add Alt-click / Option-click to drill into intermediate containers, gated on real demand.

### D6 — Asset export: single + multi-select bulk to zip

Per-asset export from the canvas, two modes:

- **Single** — click an atomic child, the inspector shows "Export PNG / SVG / 2x / 3x"; clicking initiates a download for that one asset.
- **Bulk** — Shift-click or lasso to select N atomic children, then "Export selection (Nx)" downloads a zip with one file per asset, named `<flow-slug>__<node-name>.<ext>`.

Mechanism: the export endpoint maps each selected node to its Figma node-id (already in `canonical_tree`) and calls Figma's `/v1/images/{file}?ids=...&format=svg|png&scale=1|2|3`. Backend caches each asset to ds-service blob storage so repeat exports are zero-cost.

Naming convention is auto-generated from the node label; user can edit names before downloading the zip.

### D7 — Font: Basier Circle pre-embedded as `@font-face`

The text renderer uses **Basier Circle** as the default font, loaded via `@font-face` from a static `.ttf` shipped with the Next.js app. A specific weight + variant set (Regular, Medium, Semibold, Bold — Italic variants on demand) covers all INDmoney design styles.

When the user clicks a text node to edit, the editor uses the same `@font-face` so visible kerning + line-height stay consistent during edit. We accept ~2px width drift vs Figma's renderer as a known limitation; document it as "rendered text on the web matches Figma to within 2px tolerance".

Fonts that aren't Basier Circle (rare, e.g. system Arial fallbacks in legacy frames) fall back to the closest browser-native equivalent and surface a "non-Basier font" badge in the inspector so the engineer knows the spec might drift.

### D8 — Edit affordance: single-click selects, double-click edits

Two distinct gestures, matching Figma / Sketch / Photoshop muscle memory:

- **Single-click an atomic child** → selects it; inspector opens / updates with the node's info, type styles, tokens, and export buttons. Frame remains the active context (per D5). Selection is non-destructive — no mode change, no edit cursor.
- **Double-click a TEXT atomic** → activates `contenteditable` on the same node, focuses the cursor at the click position. Other atomics (icons, shapes, image fills) ignore double-click — they don't have an edit mode in v1.
- **Esc / click-outside / blur** → commits the edit (if changed) and exits edit mode; selection state survives so the user lands back in inspect.

Why: collision risk is the dominant UX hazard here. Designers will frequently click text purely to read its type styles or copy a token; if a single click activated edit mode, every inspection would flicker the cursor and the user would constantly bail out. Double-click matches every adjacent tool, so muscle memory carries over for free.

Hotkey complement: with a TEXT atomic selected, pressing **Enter** (or **F2**) also activates edit mode — keyboard-first users get the same gesture without reaching for the mouse.

### D9 — Ecosystem integrations (Zeplin-inspired, practical-only)

Zeplin's value isn't only the canvas — it's how the canvas plugs into the rest of the design / build pipeline. We adopt the pieces that earn their keep in INDmoney's stack and skip the ones that don't fit.

**In v1 (cheap, high value):**

- **CSV bulk export of all text in a leaf.** One-click "Export all copy" downloads `<leaf>__copy.csv` with columns `screen | node_path | original | current | last_edited_by | last_edited_at`. Drop-in for the i18n / translation pipeline; also lets PM bulk-review copy in a spreadsheet.
- **CSV bulk import** (paired with export). Designer / PM uploads the CSV with edits in the `current` column; rows with diffs become overrides. Conflicts (override edited on canvas in the meantime) surface as a confirm-before-apply dialog.
- **Code snippet uses live text** (already in D4). Critical for engineer handoff.
- **Per-screen spec card.** Right-click a screen frame → "Copy spec link" — produces a deep-link URL like `/atlas/leaf/<slug>?screen=<id>` engineers paste into Linear / Slack / PR descriptions; the linked page renders read-only and pre-selects the screen.
- **`[overridden]` badge** on every overridden text on the canvas (subtle dot indicator, like a Git modified marker). Hover the badge to see the original; clicking jumps to the Activity entry.
- **Figma plugin awareness.** When a designer opens our existing Figma plugin on a frame whose `figma_node_id` has overrides in ds-service, the plugin surfaces a banner: "3 copy overrides on this frame — view in Atlas." Plugin already calls `/v1/projects/...`; the override list is one extra GET.

**v2 candidates (defer; revisit after v1 ships):**

- **Per-locale text variants.** Today's override is one current-string-per-node; v2 could allow `en-IN`, `hi-IN`, `kn-IN` variants in the same override row. Only worth building when actual translation work is requested.
- **Approval / lock state per override.** "PM-approved" badge that prevents further edits without re-approval. Needs a real workflow surface; for v1 the activity-feed paper trail is enough.
- **Slack / Linear notifications on override events.** Easy to wire if we already have a webhook surface, but not a v1 dependency.
- **Side-by-side version diff of a leaf.** Pick `version_index N` vs `M`, see what text + atomics changed. Powerful but its own product surface.

### D10 — Inspect / handoff details

Clicking any atomic child opens an inspector panel with:

- **Layer info**: name, type, dimensions, x/y, parent autolayout direction.
- **Type styles** (TEXT only): font, weight, size, line-height, letter-spacing, color.
- **Fills, strokes, radius, opacity, shadows** as Figma stores them.
- **Tokens panel**: copyable CSS, iOS Swift, Android XML / Compose snippets generated from the above.
- **Export buttons** (D6).

This is the "Zeplin parity" surface. Implementation maps existing fields in `canonical_tree` to formatted snippets; no new design data is required.

## Non-goals (explicitly out of scope)

- Editing layout (sizes, positions, autolayout settings) on the canvas — scope is text content only.
- Editing styles (font, color, weight) on the canvas — read-only inspect.
- Two-way sync back to Figma. Edits live in ds-service overrides; a future iteration may write back to Figma via plugin, but is not v1.
- Component / instance overrides surface (Figma's "Override property" UI) — out of scope.
- Free-positioning drag-to-move — we only render Figma's layout, never let the user re-author it.
- Variant switching (light/dark, breakpoints) on the canvas — handled separately by the existing mode-pair work.

## Dependencies / assumptions

- **`canonical_tree` is populated for every screen** — currently filled by the audit pipeline post-export. For sheet-sync-imported screens (post 2026-05-05) the column may be NULL until the audit pipeline catches up; canvas falls back gracefully to "raw text + flat PNG" rendering for those screens.
- **`FIGMA_PAT` is valid** server-side. Already validated by the existing figma_resolve flow.
- **Basier Circle .ttf files are licensed for web embedding.** PM / legal sign-off needed before D7 ships; use a public-domain near-equivalent (Inter) until cleared.
- **Browser flexbox matches Figma's autolayout to ~2px tolerance** for typical INDmoney content. Pressure-test on the 5 most complex screens during planning before committing.

## Acceptance examples

- **AE-1 — Inspect a text label.** Designer opens a leaf, clicks the "Buy" CTA label. Inspector shows: font Basier Circle Semibold 14px / line-height 20px / color `#0a84ff`, with copyable CSS / iOS / Android snippets.
- **AE-2 — Edit text with reflow.** Designer changes "Buy" to "Buy now". The CTA's frame grows from 56px to 88px wide; siblings on the same row stay 8px apart; the parent card grows. After 500ms blur, the edit is persisted; reloading the page shows "Buy now".
- **AE-3 — Single-asset export.** Engineer clicks the trending-up icon, picks "Export SVG". The icon downloads as `mtf-statement__trending-up.svg`, identical to Figma's export.
- **AE-4 — Bulk asset export.** Engineer shift-clicks 6 product icons across the screen and hits "Export selection". A zip downloads with 6 SVGs at correct names.
- **AE-5 — Reset to original.** Designer edits "Buy" → "Buy now", changes their mind, clicks "Reset to original". Text returns to "Buy"; override row deleted; reflow reverses.
- **AE-7 — Override survives re-import (happy path).** Designer edits a CTA "Buy now" on Tuesday. On Wednesday a designer adds a new sibling icon above the CTA in Figma. Sheet-sync re-imports the leaf at the next 5-min tick. The CTA still says "Buy now" — its `figma_node_id` matched even though `canonical_path` shifted from `0.c.2.c.0` to `0.c.2.c.1`; ds-service silently updated the path.
- **AE-8 — Override orphan (recovery).** Designer in Figma deletes the original CTA node and recreates it under a new node-id with the same name. After re-import, the override can't match by node-id or path, but the `last_seen_original_text` fingerprint matches the new node. Override re-attaches; `canonical_path` and `figma_node_id` are updated to the new values; override stays active. (If even the fingerprint mismatched, the override would be marked `orphaned` and surfaced in the "Overrides needing review" tab.)
- **AE-9 — Engineer copies live text in code snippet.** Engineer selects the CTA, opens "Tokens" tab in inspector, picks "iOS Swift". The snippet shows `Text("Buy now")` — the override, not the original "Buy" — because the engineer needs the user-facing string.
- **AE-10 — PM bulk-reviews copy via CSV.** PM clicks "Export all copy" on a leaf, opens the CSV in Sheets, edits 14 strings in the `current` column, uploads back. ds-service applies them as overrides (one diff per row), Activity tab logs 14 events, the canvas reflects all 14 changes on the next refresh.
- **AE-11 — Search finds an overridden string.** Designer types `"Buy now"` into ⌘K search; the result includes the order-ticket screen even though the underlying Figma file still says `"Buy"`. Searching `"Buy"` no longer surfaces this screen for that string (the override has won).
- **AE-12 — Render a non-autolayout frame from real data.** Opening the ChatBot file's `2882:56419` ("INDstocks", 375×812, 16 children, all absolute) in the leaf canvas: the renderer detects `layoutMode=null` on every node, walks children, computes `localX = child.x - parent.x`, emits a `position:absolute` element per atomic. The result is pixel-aligned with Figma's preview. No flex containers are emitted for this frame.
- **AE-13 — Render a section's grid of frames.** Opening section `2821:23205` ("Updated UI", 26 children) in the leaf canvas: the renderer enumerates the section's `FRAME` children, each becomes a "screen tile" laid out at its absolute (x, y), the canvas pan/zoom navigates the resulting grid. The section's own chrome (border, label) is not rendered; users see frames only.
- **AE-6 — Frame click vs atomic-child click.** Designer clicks the screen frame's outer chrome / empty area; the frame is selected exactly as it is today (inspector shows flow / version metadata, URL updates). Designer then clicks the "Title" text inside that same frame; the title becomes the selected atomic child — the frame stays the active context, and the inspector adds a sub-section with the title's type styles, tokens, and export buttons. Designer clicks on the whitespace of a "Card" container inside the frame; selection passes through to the nearest atomic child (e.g. the card's title), since intermediate containers aren't selectable.

## Open questions for planning

- **OQ-1**: Storage shape for overrides — JSONB column on `screens` vs. dedicated `screen_child_overrides` table? Decide based on read patterns (is it always read with the parent screen, or independently?).
- **OQ-2**: Asset cache — do we proxy SVGs through ds-service or store them as objects in S3 / Vercel Blob? Cache key should include `(file_id, node_id, format, scale, version_index)` so a re-imported screen invalidates correctly.
- **OQ-3**: Virtualization library — `react-window` for the 1D frame list, or a hand-rolled IntersectionObserver-based mounter? The latter handles 2D layouts where frames aren't a uniform grid.
- **OQ-4**: Mixed-style text runs — when only PART of a text run is mixed style (e.g. one bold word), how do we represent the override? Is the override the whole `characters` string, or the per-run `characterStyleOverrides` array?
- **OQ-5**: Tokens — exact mapping for color (hex / rgba / sRGB profile), spacing (px / dp / pt), shadows (CSS box-shadow vs Android elevation). Pull from existing `lib/atlas/tokens.ts` if it exists, otherwise scope a small token registry.
- **OQ-6**: Performance budget — target FPS during text edit on the worst case (500-node screen, viewport showing 4 screens). Set a number (e.g. 55fps) and gate v1 on it.
- **OQ-7**: VECTOR rendering — fetch as SVG via `/v1/images?ids=...&format=svg` (simple, pixel-true, costs an HTTP roundtrip per icon) vs reconstruct from `fillGeometry` / `strokeGeometry` JSON paths (zero-network, but more CSS edge cases). Default to the images endpoint with aggressive caching; benchmark before committing.
- **OQ-8**: GROUP flattening — confirm there are no INDmoney designs that depend on a GROUP's `opacity` / `effects` (drop shadow on the group itself, not children) before we silently flatten them away. If a small percent need GROUP-level effects, render those as a wrapping div with the relevant styles.

## Scope boundaries

### Included in v1

- HTML/CSS reconstruction of every atomic node from `canonical_tree`
- Hover, click, inspect for atomic children
- Type style + token panel for selected node
- Single-asset export (PNG / SVG / 2x / 3x)
- Multi-select bulk export to zip
- Inline text edit with autolayout reflow
- Persist text overrides + reset-to-original
- `@font-face` Basier Circle (with fallback while licensing is pending)
- Viewport virtualization

### Deferred for later (post-v1)

- Layout / style editing on the canvas
- Two-way write-back to Figma
- Component instance override UI
- Variant / breakpoint switching on the canvas
- Free positioning drag handles
- Real-time multi-cursor on the canvas (Yjs already handles DRD but canvas has no collab v1)

### Outside this product's identity

- This is **not** a Figma replacement. The canvas exists for downstream consumers (engineers, PMs, QAs) — the source of truth stays in Figma.
- This is **not** a CMS for marketing copy across the live app. Text overrides here affect the design canvas only; runtime app copy lives in the existing translation pipeline.
