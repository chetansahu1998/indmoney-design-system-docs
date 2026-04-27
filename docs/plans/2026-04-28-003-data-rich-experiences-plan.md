---
status: active
created: 2026-04-28
title: Data-rich experiences — expanded plan for dashboard, plugin, plugin-enhanced flows
---

# Data-rich experiences — what to build with the deep extraction

The original plan (002) just enriched the gallery. This plan answers: with all that data
already in `manifest.json` + `lib/tokens/*` + `lib/audit/*`, what designer-facing
surfaces transform the docs from "tokens + tiles" into a working design-system
control room?

---

## Lens: who looks at this and why?

Three personas drive the expansion:

| Persona | Asks | Today's answer | What they'd love |
|---------|------|----------------|------------------|
| **Senior designer building a new screen** | "What component do I use for X? Which variant? What padding?" | Skim Foundations + Components, copy-paste from Figma | One page per component with full spec, variant matrix, layout table, where it's already used |
| **DS lead maintaining Glyph** | "Where am I bleeding tokens? What's drifted? What's stale?" | None (no aggregated view) | Health dashboard with drift hotspots, adoption %, deprecation queue |
| **Engineer implementing a screen** | "What does this component become in code? What's the prop API?" | Hand-write from Figma + read tokens.css | Code snippet per component + per platform, prop API table, copy-as-code button |

---

## Backlog of use cases, ranked by impact

**Tier A — most visible, ship first**

1. **Per-component detail page** (`/components/[slug]`)
   - Full spec sheet: description, variant matrix table, prop API, layout config, fills, effects, code snippets
   - Replaces inline expansion as primary path; the inline inspector stays for quick browsing
   - Adds a "Used in" section once audit data lands (right now empty state)

2. **Design system health dashboard** (`/health`)
   - One page summarizing the system's vital signs:
     - Tokens published / used / drifted (Glyph extraction stats)
     - Audit coverage % (when audit data exists)
     - Top drift patterns ("18px used 247× → snap to 20")
     - Component usage rollup (most-used / least-used)
     - File freshness (latest audited)
     - Recent extractor runs (provenance log)

3. **Token usage drilldown** (`/tokens/[bucket]/[name]`)
   - Per-token page: "where is `surface.bg-grey` used?"
   - Click any swatch on Foundations → goes here
   - Renders: hex value, OKLCH preview, components using it, screens using it (when audit lands), deprecation chain

4. **Drift dashboard tab on `/files`**
   - Aggregate view of top drift patterns across all audited files
   - Group by `(property, observed value)` with snap suggestion
   - Click → list of files where this drift appears

**Tier B — strong polish, ship second**

5. **Variant matrix visualization on component detail**
   - Render the N-dimensional variant grid as a table (or a 2D table when only 2 axes)
   - Each cell is a thumbnail of the matching variant
   - Default-variant gets a star

6. **Code snippet generator** per component per platform
   - Auto-render React Native / SwiftUI / Compose snippets using the prop defs
   - Copy button next to each
   - Snippets reflect the variant's actual prop values

7. **Component changelog** auto-generated from git history of `manifest.json`
   - Shows component additions, renames, deprecations over time

8. **⌘K search expansion**
   - Today: tokens
   - Tomorrow: components, variants, prop names, hex codes

**Tier C — power-user features, ship later**

9. **Prop playground** on detail pages — interactive prop tweaker with live preview
10. **Diff viewer** — compare two variants side-by-side
11. **Token dependency graph** — semantic → primitive flow visualization
12. **Color contrast checker** — overlay every fill pair with WCAG ratio

**Tier D — plugin features**

13. **Plugin: Quick-bind shortcut** — Cmd+Opt+T on selected fill → bind to nearest token
14. **Plugin: Selection inspector** — Figma dev-mode, DS-aware ("this fill should bind to surface.bg-grey")
15. **Plugin: Live coverage badge** — persistent pill showing coverage % of the file
16. **Plugin: Pre-flight check** — before publishing, run audit + surface unbound styles
17. **Plugin: Component swap finder** — detect custom components matching a DS component
18. **Plugin: Auto-generate component spec** — Markdown with description + props + variants

---

## What I'll ship tonight (autonomous run)

Going for **maximum visible impact** before the user wakes up:

### Phase A — Backend extraction (Tier 0 — already started)
- [x] Rich types defined in `cmd/variants/types.go`
- [x] Extraction helpers in `cmd/variants/extract.go`
- [x] Wired into `cmd/variants/main.go` (depth=3 fetch + per-set property defs)
- [ ] Fetch component_set keys from `/v1/files/:key/component_sets` — durable identifier
- [ ] Run extraction once to regenerate manifest.json with rich data
- [ ] Compile, ensure tests pass

### Phase B — Frontend types + loader
- [ ] Mirror new fields in `lib/icons/manifest.ts`
- [ ] New helpers: `componentByKey`, `defaultVariantOf`, `axisMatrix`
- [ ] Type tests via `tsc --noEmit`

### Phase C — ComponentInspector deep render (Tier A)
- [ ] Inspector body adds: Description (with markdown), Variant Axes table, Layout spec, Bound-variables count chip
- [ ] Variant rail row shows axis pills + default star + bound-vars chip
- [ ] Mobile sheet matches

### Phase D — Per-component detail page (Tier A)
- [ ] New route `/components/[slug]`
- [ ] Full spec page: hero with default variant render, description, variant matrix table, prop API table, layout spec, fills/effects breakdown, code snippets stub
- [ ] "Open in Figma" deep link via `figma://` URL when available
- [ ] Tile in `/components` deep-links to detail page

### Phase E — Design system health dashboard (Tier A)
- [ ] New route `/health`
- [ ] Stat cards: total tokens, components, variants, audited files, latest extraction
- [ ] Top drift patterns card (reads `lib/audit/spacing-observed.json`)
- [ ] Component usage card (when audit data exists, otherwise "no audits yet")
- [ ] Wire into top nav as 7th entry

### Phase F — Token detail page (Tier A)
- [ ] New route `/tokens/[bucket]/[name]`
- [ ] Renders: hex preview, OKLCH stats, contrast pairs, components-using stub, deprecation chain
- [ ] Foundations swatch click → navigates here (hash anchor stays for in-page jump)

### Phase G — Plugin enrichment (Tier D - light)
- [ ] Plugin shows component-match metadata (set name, axis count) in audit fix rows
- [ ] Plugin's "open in docs" deep-link points to `/components/[slug]` when fix is component-related

### Phase H — Wire everything up
- [ ] Top nav: add Health
- [ ] Sidebar groups + scroll-spy on new pages
- [ ] Search modal includes components + tokens
- [ ] Footer reflects all routes

### Phase I — Tests + deploy
- [ ] All Playwright tests still pass
- [ ] Add new test: detail page renders for a known slug, health dashboard shows stat cards
- [ ] Push everything as one cohesive shipment

---

## Acceptance criteria

When the user wakes up:
- `/components/[slug]` works for at least 5 components with full data
- `/health` is a real page with real numbers
- `/tokens/[bucket]/[name]` works for at least 3 tokens
- All existing Playwright tests pass
- Dev server boots clean, no console errors
- Plugin loads + shows component matches with metadata

---

## Non-goals tonight

- Code snippet generation in multiple languages (just stub the section, ship later)
- Interactive prop playground (Tier C)
- Token dependency graph (Tier C)
- Live audit coverage chart (depends on audit data which doesn't exist yet)
- Plugin Quick-bind shortcut (significant Plugin API work, ship later)

---

## Risk mitigation

- **Extraction time**: depth=3 might push fetch past 5 min for 89 sets. If so, parallelize with backoff.
- **Manifest size bloat**: rich fields will at least double JSON size. If past 5 MB, generate a `manifest.lite.json` for the gallery and load full only on detail pages.
- **Markdown rendering**: component descriptions are markdown. Use a tiny renderer (or react-markdown) — don't build one.
- **Build time**: each new page is a static SSG'd page; if there are 89 component pages × 343 logo pages × 389 illustration pages, build might balloon. Mitigation: only build component detail pages (kind="component"); illustrations + logos stay as gallery rows.
- **No data states**: every new page must have a real empty state if data is missing — don't silently render blank.
