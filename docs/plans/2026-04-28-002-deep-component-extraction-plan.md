---
status: active
created: 2026-04-28
title: Deep component extraction — backend, frontend, plugin
---

# Deep component extraction — backend, frontend, plugin

## Problem

Current pipeline only captures shallow data per component (slug, set_id, variant_id, dimensions). Designers can't see component descriptions, the full variant property matrix (BOOLEAN/TEXT/INSTANCE_SWAP), default-variant detection, autolayout config, or which fills/strokes/effects bind to which Figma Variables. The audit can't honestly say "this fill is bound to surface.bg-grey" — it only sees colors.

## Goal

Make every component a fully realised record visible in three surfaces:
- **Docs site** (`/components`) — description, variant axis matrix, layout config, bound-variable breadcrumbs per variant
- **Plugin audit** — component matches show real metadata (set name, description, axis count); fix candidates name the actual Figma Variable (figma_name, not DTCG slug)
- **Audit core** — coverage % is honest about variable bindings vs raw colors; new fields shipped in `AuditResult.components`

## Phases

### Phase 1 — Backend: rich extraction (Go)

**1.1** New types in `services/ds-service/cmd/variants/types.go`:
- `ComponentProperty` (with `#suffix` preserved)
- `VariantAxis` (matrix axis: name, values, default)
- `LayoutInfo`, `FillInfo`, `EffectInfo`, `CornerInfo`, `ChildSummary`
- Extended `Variant` + `IconEntry` with rich optional fields

**1.2** `cmd/variants/main.go`:
- Bump per-set fetch depth `1 → 3` so we capture root frame + immediate children
- Parse `componentPropertyDefinitions` from the COMPONENT_SET → emit all 4 property types
- Parse each variant's name into `axis_values` map
- Compute default variant via top-left absoluteBoundingBox sort
- Capture root frame's autolayout (mode, padding, gap, alignment, sizing, wrap)
- Capture root frame's fills, strokes, effects, corner radius — with `boundVariables` IDs
- Walk variant's first-level children → emit `ChildSummary` with `componentPropertyReferences` so the cascade is visible
- New helper `parseComponentPropertyDefinitions`, `extractLayout`, `extractFills`, `extractEffects`, `extractCorner`, `extractChildren`

**1.3** `cmd/variants/main.go`:
- Fetch `/v1/files/:key/components` and `/v1/files/:key/component_sets` for durable `key`
- Cross-reference `node_id` → `key` and stamp on `Variant.Key` + `IconEntry.Key`

**1.4** Run extraction once, regenerate `public/icons/glyph/manifest.json` with rich data.

**Verification**: `jq '.icons[] | select(.kind == "component") | select(.prop_defs)' manifest.json | head -100` shows real variants with descriptions and prop defs.

### Phase 2 — Backend: surface rich data through audit core

**2.1** `internal/audit/types.go`:
- Add `ComponentMatch.SetKey` (so plugin can reference the durable key)
- Add `ComponentMatch.Description`, `ComponentMatch.AxisCount` for tooltip context

**2.2** `internal/audit/match.go`:
- When matching by `componentKey`, also capture the matched component's metadata (name, description, axis count) into ComponentMatch

**2.3** `internal/audit/server.go` (HandlePublish):
- Accept richer publish payload from plugin including component_set_key + component_keys (so future variable_key sync can use them)

### Phase 3 — Frontend: TS types + manifest loader

**3.1** `lib/icons/manifest.ts`:
- Mirror new fields on `VariantEntry` and `IconEntry`:
  - `key`, `description`, `doc_links`, `prop_defs`, `variant_axes`, `single_variant_set`
  - On variant: `axis_values`, `is_default`, `layout`, `fills`, `strokes`, `effects`, `corner`, `bound_variables`, `children`
- Helper functions:
  - `componentByKey(key)` — durable lookup
  - `defaultVariant(entry)` — return the default
  - `axisMatrix(entry)` — flatten to a row-of-axis structure for table rendering

**3.2** `lib/audit/types.ts`:
- Mirror new ComponentMatch fields

### Phase 4 — Frontend: ComponentInspector deep render

**4.1** `components/ComponentInspector.tsx`:
- Side-panel `InspectorBody` gains three new sections:
  1. **Description** — markdown render of `entry.description`, with documentation links as a small footer
  2. **Variant Axes** — table view showing `[axis name]  [value 1 / value 2 / value 3 (default ⭐)]`. One row per axis. For BOOLEAN/TEXT/INSTANCE_SWAP, show the prop with its type tag.
  3. **Layout** — when default variant has Layout set, show the autolayout config as a compact spec sheet (mode, padding, gap, alignment) — the kind of thing dev-mode shows
- Each variant row in the rail gains:
  - Default star badge when `is_default`
  - Axis values rendered as compact pills (`Size=Large`, `State=Default`)
  - Bound-variable count chip when `bound_variables` is non-empty (e.g. "🎯 4 bound")
- Mobile sheet body gets the same treatment

**4.2** Tile → inspector animation: keep the dock, but make the inspector header sticky inside its scroll container so the title stays visible as designer scrolls variant list

### Phase 5 — Frontend: plugin enrichment

**5.1** `figma-plugin/code.ts` already returns figma_name; nothing to add server-side.

**5.2** `figma-plugin/ui.html`:
- Component matches in audit results: when `matched_slug` is present, show the matched component's name + axis count as a chip ("Button · 6 axes")
- Add tiny tooltip via `title` attribute carrying the description

### Phase 6 — Tests + verification

**6.1** Go tests:
- `internal/audit/engine_test.go` — keep existing
- `cmd/variants/parse_test.go` — new test for `parseComponentPropertyDefinitions` covering all 4 types

**6.2** TS type-check: `npx tsc --noEmit` must stay clean

**6.3** Playwright walkthrough:
- Add `tests/phase-g-walkthrough.spec.ts` test: visit `/components`, click a tile, inspector shows variant axes table with at least one row

**6.4** Visual sweep: regenerate `test-results/sweep/` screenshots, manually scan for regressions

### Phase 7 — Deploy

**7.1** Run `cd services/ds-service && go run ./cmd/variants` to regenerate manifest with rich data

**7.2** Run `npm run build` (or check `npx tsc --noEmit` + dev server smoke) to validate frontend builds

**7.3** Rebuild plugin: `cd figma-plugin && npx tsc -p .`

**7.4** Commit + push as one cohesive shipment

## Acceptance criteria

- `manifest.json` carries `description`, `prop_defs`, `variant_axes`, `key`, `single_variant_set` on every COMPONENT_SET-kind entry that has them in Figma
- `manifest.json` carries `axis_values`, `is_default`, `layout`, `fills`, `bound_variables`, `children` on every variant whose root has those
- `/components` page inspector renders Description + Variant Axes + Layout sections
- Plugin audit shows component matches with set name + axis count
- All existing Playwright tests still pass
- No console errors on dev-server smoke

## Non-goals

- Recursive deep tree extraction (depth=3 ceiling — don't try to capture every grand-child)
- Variable.key extraction (requires Enterprise scopes; deferred to a later phase that goes through the plugin)
- Per-variant typography style extraction (TEXT children captured but font style not surfaced — separate phase)

## Risks

- **Fetch time**: depth=3 across 89 sets could push runtime past 5 min. Mitigation: parallelize the fetch in batches of 25, retry with backoff on 429
- **Manifest size**: rich data could double the JSON. Acceptable since it's served once at build time; consider a lightweight `manifest.lite.json` if it crosses 5 MB
- **Backwards compat**: old consumers of manifest.json must keep working. Mitigation: all rich fields are `omitempty` and TS types use `?:` optional
