# INDmoney DS Docs — Honest Status & Plan

_Maintained snapshot: 2026-04-27. Every claim here is verifiable by reading code or running the pipelines._

## Where we are

| Surface | Promised | Reality | Source |
|---|---|---|---|
| Color | Real Glyph pairs, light + dark | ✅ working | 49 semantic + 89 base extracted from Glyph "Colours" page (`semantic.tokens.json`, `base.tokens.json`, `semantic-dark.tokens.json`) |
| Typography | 18 Glyph TEXT styles | ✅ working | `text-styles.tokens.json` from `/v1/files/:key/styles` filtered to TEXT |
| Spacing / Padding / Radius | Pulled from Figma | ✅ via layout-pattern fallback | `spacing.tokens.json` provenance `figma-layout-scan` (29,289 frames). Each token carries usage count. Variables API gated behind PAT scope `file_variables:read`. |
| Motion | DTCG JSON, future from Figma | ⚠ hand-curated; Figma doesn't expose | `motion.tokens.json` provenance `hand-curated`. Banner explains why. |
| Effects / Shadows | Extracted | ⚠ 0 tokens; Glyph has 0 EFFECT styles + 0 inline shadows on the design-system page | Empty-state UI shows animated 3-tier sample preview + the unlock command. |
| Icons (system) | All Glyph icons | ✅ 58 real system icons | Filtered by `classifyAsset()` from the 864-entry manifest. |
| Components | _was not in original plan_ | ✅ now lives at `/components` | 74 entries — CTAs, progress bars, action bars, status bars. |
| Illustrations | _was not in original plan_ | ✅ at `/illustrations` | 406 entries with native colors restored. |
| Logos | _was not in original plan_ | ✅ at `/logos` | 326 bank/partner logos with native colors. |
| Component variants (Zeplin-style) | Asked-for next | ⏳ in flight | Today the extractor picks 1 variant per COMPONENT_SET; we need all of them surfaced with variable labels (size/state/intent). |
| Figma plugin | Asked-for next | ⏳ in flight | Scaffold landing now; full integration with ds-service is iterative. |

### Honest broken-ness still on the surface

1. **3D · Car / Cash / Goal** entries inside Glyph's `Icon` category render as black silhouettes — not a renderer bug. Glyph drew them as black-outline shapes at 24×24 in the source file. They have no embedded color to preserve. Either Glyph reauthors, or we rebrand them as monochrome icons.
2. **Spacing histogram** has a long tail (74px, 153px, 175px, 235px) that are clearly one-offs from page hero frames, not real tokens. Min-count filter is currently 3; bumping to 5–10 trims the noise but risks dropping legit narrow tokens.
3. **Motion section** still says "hand-curated" because Figma doesn't expose motion. Lottie/Rive integrations would be the path, but that's out of scope for token extraction.

## What DesignBrain analyzes vs. what we replicate

DesignBrain's `internal/extraction/` and `internal/canonical/` packages do four things we don't:

| DesignBrain | Mapped to us | Status |
|---|---|---|
| `analysis_extractor.go` — scores selections, surfaces issues + suggestions | We do **none of this**. Our pipelines extract; we don't critique. | Future: `cmd/audit` that scores a Figma frame against the published token set and reports drift. |
| `canonical_builder.go` + `canonical_hash.go` — content-addressed node hashing for dedupe | We dedupe icons by Figma `variant_id`; no semantic hash. | Adding a node hash means we can detect the same component pasted in 30 places and warn on inconsistencies. |
| `hybrid_extractor.go` — combines REST + plugin selection signal | We are REST-only. | Once the plugin is live, hybrid lets us extract from the user's selection rather than scanning whole pages. |
| `training_extractor.go` — labels for AI design feedback | Far-future. | Out of scope until the rest of the surface is solid. |

## What's left to ship (ranked by user-visible impact)

### High — every designer who opens the site notices

1. **Component inspector (Zeplin-style)** — click any tile on `/components` → smooth expand-in-place with horizontal variant rail, variable chips (size · state · intent), measured spec rail (width × height, padding, radius), copy-spec button. _In progress this turn._
2. **Search-aware page hash** — `/components#search` should focus the search bar (matches Field). Today the hash routes to nothing.
3. **Per-asset detail copy paths** — copy SVG, copy URL, copy slug, copy name — already on Iconography but missing from `/components`, `/illustrations`, `/logos` tiles.
4. **Variant-aware extractor** — manifest currently picks one variant per COMPONENT_SET. The inspector needs all variants with their variable property names.

### Medium — fixes friction once you start using it

5. **Search modal copy paths for components** — ⌘K only indexes color + base palette. Components, icons, illustrations, logos should all be findable.
6. **Theme toggle on gallery pages** — `PageShell` doesn't expose the dark/light toggle; only the foundations page does.
7. **Sidebar dead links on long pages** — bottom nav still says "Coming v1.1 → Components" — outdated now that `/components` exists.
8. **`tests/inspect-live.spec.ts`** — debug spec that survived the cleanup. Either keep + document, or delete.
9. **Spacing histogram noise filter** — current threshold lets 235px through. Bump min-count to ≥5 once we confirm it doesn't drop a real token.

### Plugin — separate track

10. **`figma-plugin/`** — `manifest.json`, `code.ts`, `ui.html`. Local-first: plugin posts the user's Figma selection to `https://localhost:7474/import` (or a hosted ds-service URL). The service pushes back token suggestions, identifies whether the selection drifts from existing tokens, and offers an "inject this as a new component" command.

### Visual polish (designer hat)

- The Foundations page hero is fine; gallery pages have a less-formal masthead. Unify.
- Spacing rows should feel tactile — every row a single hover affordance + bar reveal animation. Today the bar fills aggressively for 235px and visually breaks alignment.
- Typography rows show a CSS-snippet copy on the right edge that's easily missed. Add an explicit "Copy CSS" pill.
- Color tiles are tight at 220px min-width — bump to 240px to keep the leaf name fully visible.
- `DataGapPreview` is one shape (banner + sample). Effects uses it well; Variables/Motion should use a smaller inline variant rather than the full DTI pattern.

## Plan for next 1–2 sessions

1. **This session:** Variants extractor + Component inspector UX + plugin scaffold + this status doc.
2. **Next:** ⌘K cross-index everything (icons/components/illustrations/logos), per-tile copy paths, sidebar/nav cleanup.
3. **Then:** Plugin → ds-service round trip working locally; hybrid extractor + selection analysis (scores against tokens).
4. **Later:** Canonical hashing + drift detection + AI suggestions (the DesignBrain ladder).

## How we test ourselves

- Frontend: `npm run lint` (tsc --noEmit) and `npm run build` must both pass.
- Pipelines: `npm run sync:tokens` exits 0 and the resulting JSON has non-zero counts where we expect.
- Visual: scroll through `/`, `/components`, `/illustrations`, `/logos` on dark + light. Count assets matches the section header. No raw `currentColor` placeholders where colors should render.

When any of those breaks, this doc gets updated before the next deploy.
