---
title: "feat: Files tab + audit pipeline + living docs + plugin guardrails (Foundations-parity UX)"
type: feat
status: active
date: 2026-04-28
origin: docs/brainstorms/2026-04-28-files-tab-audit-pipeline-requirements.md
---

# Files tab + audit pipeline + living docs + plugin guardrails

## Overview

Three interlocking surfaces share one Go audit core to convert the docs site from a static reference into a living, drift-aware system:

1. **Living Foundations + Components** — every swatch / type style / spacing token / component tile gains a `used N times in M files` chip from the latest audit. Zero-usage tokens render at 50% opacity. The Components page reorders SECTION lists by usage. Designers stop reaching for stale options.
2. **Plugin** — designer selects a node in Figma, plugin POSTs to `<ds-service>/v1/audit`, renders fix cards with the closest token name + a "Apply" button. Click writes the bound variable back via Figma's variable APIs. Drift dies at the moment of choice.
3. **Files tab** — peer of Foundations / Components / Illustrations / Logos in top nav. Per-file rollup (token coverage %, DS-component %, top drift hex). Drill into a file → DocsShell-style left sidebar lists every final-design page; main pane shows three audit lenses (component usage, token coverage, drift suggestions). Admin Designer affordances: filter by drift, sort by coverage, batch view of a single drift hex across all audited files.

Plus guardrails: PR-time impact preview when `lib/tokens/indmoney/*.tokens.json` changes (CI re-audits and posts diff), coverage thresholds gate PRs, deprecation propagation flags every consumer, cross-file pattern detection surfaces "promote to DS?" candidates.

The plan reuses DesignBrain primitives that already exist in `~/DesignBrain-AI/internal/{import/figma,canonical,canvas,extraction}/`: `looksLikeScreen`, the `FinalNodeUnderstanding_v1` schema subset, a trimmed `MatchingService` (componentKey + name + style + color signals only), `canonical_hash`, `ImportAnalysisContext`. We port copies into `services/ds-service/internal/audit/`; no upstream dependency.

---

## Problem Frame

INDmoney designers fix token drift "as they remember it" today — there's no live tooling that says *"this `#6B7280` isn't a token; closest match is `surface.surface-grey-separator-dark`."* Engineering has no per-file rollup of how on-token a Figma file is, so drift compounds silently and is caught — if at all — at PR review. The docs site shows abstract swatches without telling designers whether a token is used 200× in production or zero times.

DS leads (Admin Designers) have no governance surface — no way to see which file is worst this week, which custom node keeps reappearing across files, or what would happen if they changed `surface.surface-grey-bg`.

Existing UX gap: Foundations is the only page in the docs site that *feels* like a designed product. `/components`, `/illustrations`, `/logos` ship with `PageShell` only — no left nav, no scroll-spy, no density-aware spacing, no stagger reveals. New surfaces in this plan must hit Foundations parity, not bolt UX on later.

(See origin: `docs/brainstorms/2026-04-28-files-tab-audit-pipeline-requirements.md`.)

---

## Requirements Trace

**From origin (R1–R22):**

- R1–R3. Audit core: ports DesignBrain's schema subset, classification signals (componentKey + name + style + color, no embeddings in v1), drift = OKLCH distance for color / px distance for dimension, ranked by drift × usage count.
- R4–R6. Plugin: Audit command (selection / page / file scope), fix cards with click-to-apply via Figma variable APIs, POSTs to `/v1/audit`.
- R7. Curated `lib/audit-files.json` manifest is the file-list source.
- R8–R11. Files tab routes (`/files`, `/files/<slug>`), DocsShell-style chrome, three-lens panels per screen, provenance line.
- R12. Final-page detection ports DesignBrain's `looksLikeScreen` heuristic; manifest override allowed.
- R13. Left-nav cleanup on `/components`, `/illustrations`, `/logos`.
- R14. Provenance stamps in audit JSON.
- R15–R17. Living docs — usage chips on every Foundations + Components surface, hover popovers, zero-usage de-emphasis, Components reordering by usage.
- R18–R22. Guardrails — PR-time `cmd/audit --diff`, coverage thresholds in CI, plugin staleness banner, cross-file pattern detection, deprecation propagation.

**Plan-local (added during planning, traceable to user emphasis on UX):**

- R23. **Foundations parity bar.** Every new page (`/files`, `/files/<slug>`) and upgraded gallery page (`/components`, `/illustrations`, `/logos`) honors the Foundations motion language — `fadeUp` on section reveal, `stagger` + `itemFadeUp` on grids, `swatchHover` springs, `barGrow` for ranged values, `panelVariants` for modals, `tapScale` for buttons. No new motion idiom unless it composes from `lib/motion-variants.ts`. `prefers-reduced-motion` must collapse all of the above to opacity-only fades.
- R24. **Admin Designer affordances on Files tab.** Per-file: filter by lens (only show screens with coverage < N%, only show screens with > M drift instances). Cross-file: "global drift" view — sorts unbound fills/spaces/styles by frequency × file-count, so one click reveals the 47 places that one custom hex shows up. Exportable as Markdown bullet list (Slack-ready).
- R25. **Density-aware audit panels.** Existing density tokens (compact / default / comfortable) influence audit-row spacing the same way Foundations does. The Files tab does not introduce a parallel density model.
- R26. **Theme-aware tokens chip + audit panels.** No hardcoded grays / blues / blacks in audit UI. Every color reads from `--text-1`, `--text-2`, `--text-3`, `--accent`, `--border`, `--bg-surface`, `--bg-surface-2` already in `app/globals.css`. The audit UI is itself dogfooding the token system it audits.
- R27. **Real loading + empty states.** Skeleton states match the eventual layout (no "Loading…" text). Empty states are designed (icon + headline + sub + the unlock command) — never bare strings. Stale-data states render at amber-bordered banner above the content.
- R28. **⌘K cross-index.** Search modal indexes all four asset kinds (icons, components, illustrations, logos) AND all audited files + screens, with section labels matching the page nav. A user typing "trade" finds both the Trade screen audit and the Trade button component.

**Origin actors:** A1 (Designer), A2 (DS lead / engineering owner = Admin Designer), A3 (Operator), A4 (ds-service Go core), A5 (Plugin), A6 (Docs site).
**Origin flows:** F1 (server sweep), F2 (designer audits in plugin), F3 (DS lead reviews rollup), F4 (designer browses living docs), F5 (DS owner previews token-change blast radius), F6 (staleness banner).
**Origin acceptance examples:** AE1 (drift recommendation), AE2 (plugin click-to-apply round-trip), AE3 (Files tab routing + sidebar), AE4 (final-page detection skips WIP), AE5 (zero-usage de-emphasis), AE6 (PR diff impact comment), AE7 (cross-file canonical-hash candidate).

---

## Scope Boundaries

### Deferred for later

(Carried from origin — sequencing, ships in v1.1+ once v1 is on-screen.)

- Vector / embedding similarity for component matching.
- Multi-tenant DesignBrain reuse (MongoDB + per-tenant Figma PATs).
- Drift trend graphs / week-over-week sparklines.
- Designer-facing file-add UI (admin route).
- Auto-promote-to-DS flow (plan keeps detection only).
- Near-duplicate cross-file clustering (v1 uses exact canonical match).
- Personal "fixes assigned to me" / accountability tracking.
- ⌘K natural-language commands ("show me low-coverage screens").

### Outside this product's identity

(Carried from origin — positioning rejection, not deferral.)

- This is not a Figma plugin marketplace product. v1 ships behind INDmoney's Figma + manifest; we don't optimize for general-purpose use.
- This is not an LLM design critic. No microcopy / tone / accessibility-narrative analysis. The audit is mechanical: tokens, components, hashes, distances.
- This is not a Figma editor. The plugin writes one variable binding at a time; the docs site never writes back to Figma. Files tab is read-only.
- This is not a multi-DS hub. Single DS (INDmoney's Glyph) is the comparison baseline. Tickertape support deferred to v1.1+.

### Deferred to Follow-Up Work

(Plan-local — implementation work intentionally split across follow-up phases.)

- Phase G polish items (drift threshold tuning, plugin error states, ds-service hosting helper) ship in their own follow-up PRs once v1 is in designers' hands.
- The Tickertape-brand variant of `lib/audit-files.json` lands in the multi-brand follow-up (separate from this plan).

---

## Design Quality Bar

Every surface in this plan must hit Foundations parity. Treat this as the acceptance grammar — if a unit ships without these, the unit is not done.

**Motion (composes from `lib/motion-variants.ts` — never invents new idioms)**

- Page reveals use `fadeUp` (450ms, ease-out cubic).
- Grids use `stagger` (children 70ms apart) + `itemFadeUp` (380ms).
- Tiles / cards have hover affordance via `swatchHover` springs (stiffness 300, damping 22).
- Bars / progress respect `barGrow(width)` (500ms width animation, 0.55 opacity rest).
- Modals + popovers use `overlayVariants` + `panelVariants`.
- Press feedback uses `tapScale` (whileTap 0.96).
- `@media (prefers-reduced-motion: reduce)` collapses everything to a 200ms opacity transition.

**Information density**

- Density toggle (compact / default / comfortable) from `useUIStore` propagates via `data-density` on `<html>`. Audit panels read it through `--row-pad-y` CSS variables; do not branch in JS.
- Mobile: every page collapses to a hamburger drawer like Foundations does today.

**Color + theme**

- No hardcoded `#fff`, `#000`, `gray-500`, etc. anywhere in audit UI.
- Every color reads from CSS custom properties already published in `app/globals.css`.
- Light + dark must both look intentional. Test by toggling theme on each new view before merging.

**States**

- **Loading**: skeleton blocks matching the final layout's grid + row heights. Never the word "Loading".
- **Empty (no data yet)**: icon + headline + 1-2 sentence sub + concrete unlock action (CLI command or button). Pattern: existing `DataGapPreview` component on the Effects section. Reuse it, don't reinvent it.
- **Stale (data older than threshold)**: amber-bordered banner above the data. Pattern: existing `provenance` pill on `SpacingSection`.
- **Error (audit JSON failed to parse)**: red-bordered banner with retry CTA + the file path. Never silent.

**Interaction patterns**

- Click anywhere on a tile / row to perform the primary action (copy / open / toggle). Avoid hidden controls.
- Show a "COPIED" or "APPLIED" flash for 1.1s after a successful action. Pattern: existing `ColorSection` swatch tiles.
- Keyboard: `⌘K` for search globally; `Esc` closes any open modal; arrow keys navigate the search list.
- Tooltips on all icon-only buttons + truncated labels.

**Voice**

- Provenance lines are factual and concise: `49 tokens · 89 primitives · source: glyph` (existing pattern). New audit lines: `47 instances · 3 files · audited 2h ago`.
- Never apologize for missing data. State what's there, what's missing, and what unlocks the rest.

**Admin Designer power-tools (Files tab specifically)**

- Filters as URL state (`/files?coverage=lt-80`) so DS leads can share links.
- Sort headers on every table.
- Batch view: when you click a single hex in the global drift list, the right rail shows every file × screen × node id using it, with a "Copy as Slack message" button that produces:
  ```
  *#6B7280 audit (3 files, 47 instances)*
  · INDstocks V4 / Trade screen — 14 nodes
  · INDmoney app / Wealth screen — 21 nodes
  · IND credit / Onboarding — 12 nodes
  Closest token: surface.surface-grey-separator-dark (#6F7686, OKLCH 0.011)
  ```

---

## Context & Research

### Relevant Code and Patterns

- **Foundations chrome (the template):** `components/DocsShell.tsx`, `components/Sidebar.tsx`, `components/Header.tsx`. Sidebar uses `useUIStore`'s `activeSection` + IntersectionObserver scroll-spy.
- **Page shell (current gallery pattern, will be replaced):** `components/PageShell.tsx`. Slim header only; this is what /components, /illustrations, /logos use today.
- **Section structure:** `components/sections/{Color,Typography,Spacing,Motion,Iconography,Effects}Section.tsx`. Each is a `<section id=…>` with sub-anchored `<motion.div>`s for left-nav targets.
- **Motion library:** `lib/motion-variants.ts`. Reuse, don't extend.
- **Density + density toggle:** `lib/ui-store.ts` (`density`, `setDensity`), `applyDensityFromStore`, propagated via `<html data-density>` and CSS variables in `app/globals.css`.
- **⌘K search:** `components/SearchModal.tsx`. Currently indexes Foundations sections + tokens. Phase D extends.
- **Token JSON schema:** `lib/tokens/indmoney/*.tokens.json` (W3C-DTCG with `$extensions.com.indmoney.*`). The audit core treats these as ground truth.
- **Manifest classifier (already kind-stamped):** `lib/icons/manifest.ts` — `classifyAsset()`, `iconsByKind()`, `iconsByCategory()`. The audit's "is this a DS instance" check delegates here.
- **DataGapPreview (empty-state pattern):** `components/ui/DataGapPreview.tsx`. Reuse for audit empty states.
- **Component inspector pattern:** `components/ComponentInspector.tsx` (variant rail, click-to-expand). Files tab "screen detail" panel mirrors this shape.
- **Plugin scaffold:** `figma-plugin/{manifest.json,code.ts,ui.html,tsconfig.json}`. Compiles via `npx tsc` in that dir.

### DesignBrain primitives to port (repo-relative source paths)

> The DesignBrain repo lives outside this repo. Source paths below are absolute references for the porting task only and are not committed anywhere; they're not consumed at runtime by this plan. Port targets are all `services/ds-service/internal/audit/...`.

- **Final-page detection:** `internal/canvas/import_classify.go::looksLikeScreen` (~50 LOC).
- **Canonical hash:** `internal/canonical/canonical_hash.go` + `canonical_builder.go` (subset; strip volatile fields).
- **Schema:** `internal/import/figma/final_node_understanding.go` (subset of `FinalNodeUnderstanding_v1`).
- **Matching:** `internal/import/figma/matching_service.go` (component-key / name / style / color signals only; drop vector + lex / embeddings).
- **Per-file rollup:** `internal/canvas/import_analysis.go::ImportAnalysisContext`.

### Institutional Learnings

- No `docs/solutions/` directory exists yet; this plan can seed one once an "extracted-and-classified" solution is available to write up.

### External References

- **Figma Variables write APIs.** `setBoundVariableForPaint`, `setBoundVariableForEffect`, `setBoundVariableForLayoutSizingAndSpacing`. Behavior across plan tiers needs a one-off test in dev (deferred to implementation).
- **OKLCH distance.** `Lab → OKLab → OKLCH` conversion + Euclidean distance. Standard formula; no external dep needed (port a 50-line color-math file).

---

## Key Technical Decisions

- **`schema_version` field on every audit JSON.** Start at `"1.0"`. Bump on breaking changes. Readers tolerate unknown fields. Plugin + Files tab + cmd/audit all share the schema constant via `services/ds-service/internal/audit/types.go`. (Resolves origin Q: schema-versioning policy.)

- **OKLCH drift threshold defaults to 0.03.** Slightly more permissive than the typical 0.02 that color tooling uses. Per-token-type override allowed in `lib/audit-files.json` per-brand. Audit run logs P50/P95/P99 distances per sweep so we can tune empirically. (Resolves origin Q: drift threshold.)

- **ds-service hosts locally per designer in v1.** Designers run `npm run audit:serve` (which wraps `go run ./services/ds-service/cmd/server`) on their laptop; plugin defaults to `localhost:7474`. No tunnel / Fly / hosted instance. Hosted ds-service is a v1.1 follow-up. Cost: every designer needs Go installed; mitigated by a `Makefile` target that prints exact install steps. (Resolves origin Q: hosting model.)

- **Deprecation metadata lives on token entries directly.** `$extensions.com.indmoney.deprecated: true` + `replacedBy: "<token-path>"` on the token JSON entry. Closest to the token; survives any DS file split. (Resolves origin Q: deprecation location.)

- **Plugin clientStorage cache key = `${file_key}:${ds_rev}`** where `ds_rev` is the SHA-256 of the published manifest's bytes (computed at audit start, returned in the audit response). On mismatch → staleness banner fires. (Resolves origin Q: cache key strategy.)

- **GitHub Action shape: single workflow, two jobs.** `audit-on-tokens-pr.yml` triggers on `paths: ['lib/tokens/indmoney/**']` + reads the proposed tokens, runs `cmd/audit --diff <baseline-ref>`, posts a PR comment via `actions/github-script`. Coverage status check is a second job that fails the PR if any audited file drops below threshold. (Resolves origin Q: GitHub Action shape.)

- **`/components`, `/illustrations`, `/logos` upgrade to DocsShell-style chrome (full sidebar + scroll-spy)** rather than a lighter PageShell variant. Foundations is the template; one shell to maintain; user explicitly asked. (Resolves origin Q: DocsShell vs PageShell.)

- **Canonical hash collision testing happens in implementation.** Sample 200 nodes from the live Atoms page; assert hash distribution. If false-positive rate > 5%, narrow the hash input. (Deferred to implementation per origin.)

- **Audit core is a Go library + a thin HTTP wrapper.** Both `cmd/audit` and `cmd/server` (the plugin endpoint) call into `services/ds-service/internal/audit/Audit()`. Server wraps in CORS + JSON; cmd writes to disk. Single source of truth.

- **Animation library stays at framer-motion.** Already in use across Foundations. No new dep.

---

## Open Questions

### Resolved During Planning

- **What's a "final design page"?** → `looksLikeScreen` heuristic from DesignBrain (frame name contains screen/page/view/modal/dialog/sheet); operators can override per-file in `lib/audit-files.json`.
- **Where does the file list come from?** → `lib/audit-files.json` checked-in manifest, schema:
  ```
  [{file_key, name, brand, owner, final_pages?: ["1234:5678"]}]
  ```
- **How does the plugin run alongside the Files tab?** → Plugin POSTs node trees to `<ds-service>/v1/audit`; ds-service runs the same Go core as `cmd/audit`. Same logic, different inputs.
- **What does "actionable" mean for v1?** → Plugin: yes, click-to-apply via Figma variable APIs. Files tab: no, read-only.
- **What's the chrome for /files vs /components etc.?** → Both use the existing `DocsShell` (now generalized to take a `nav` prop and content slot).
- **Drift threshold per token type?** → Color uses OKLCH 0.03; dimension uses `±1px` rounding tolerance; typography compares fontFamily + fontWeight + fontSize exactly (no fuzzy match in v1).
- **Cross-file matching algorithm?** → Exact canonical-hash match on a normalized node spec (type + dimensions rounded to nearest px + fillStyleId + textStyleId + autoLayout fingerprint, with volatile fields like `id`, `absoluteBoundingBox`, `effects` stripped).

### Deferred to Implementation

- **Figma write API plan-tier behavior.** Test `setBoundVariableForPaint` on the team's actual plan; identify graceful fallback (verdict + copy-token-name) for designers on Free.
- **Canonical hash collision rate** at the chosen normalization granularity. Sample 200 real nodes during U1; tighten if collisions > 5%.
- **Performance of full-file audit on a 30k-node file.** Stream / paginate / cache as needed. v1 target: 4-min sweep on the 3-file manifest.
- **Plugin's UI for click-to-apply across multi-fill nodes.** When a node has 3 fills, all unbound — does the plugin show one fix card per fill, or a stacked card? Try both during U13; pick whichever feels less cluttered.
- **CORS preflight from the Figma plugin sandbox to localhost:7474.** Browsers vary on this. If preflight blocks, ds-service issues `Access-Control-Allow-Origin: null` for plugin requests.

---

## Output Structure

```
services/ds-service/
├── cmd/
│   ├── audit/main.go                        # NEW: full sweep CLI
│   ├── audit-diff/main.go                   # NEW: PR-time diff comparator (Phase F)
│   └── server/main.go                       # NEW or EXTENDED: /v1/audit HTTP endpoint
└── internal/
    └── audit/                                # NEW package
        ├── types.go                          # AuditResult, AuditScreen, FixCandidate, schema_version
        ├── classify.go                       # ports looksLikeScreen + node-kind helpers
        ├── hash.go                           # canonical hash (port + simplify)
        ├── color.go                          # OKLCH conversion + distance
        ├── match.go                          # MatchingService subset (4 signals)
        ├── drift.go                          # drift detection + ranked recommendations
        ├── engine.go                          # Audit() — public entry; consumes file tree, emits AuditResult
        ├── output.go                          # writes per-file + index JSON, populates provenance
        └── server.go                          # HTTP handler for plugin POSTs

lib/
├── audit-files.json                          # NEW: curated file manifest (1-3 entries to start)
└── audit/
    ├── index.ts                              # NEW: TS loader + types mirroring Go schema
    ├── manifest.ts                           # NEW: reads checked-in audit JSON
    ├── types.ts                              # NEW: AuditResult, FixCandidate, etc. (TS shape)
    ├── index.json                            # GENERATED: roll-up of all file audits
    └── <file-slug>.json                      # GENERATED per file in lib/audit-files.json

components/
├── audit/                                    # NEW
│   ├── UsageChip.tsx                         # used N times · M files
│   ├── AuditLensCards.tsx                    # 3-lens triple per screen
│   ├── FixCard.tsx                           # plugin + Files tab share this shape
│   ├── DriftBatchView.tsx                    # admin power-tool: one hex → all uses
│   └── EmptyAuditState.tsx                   # empty/error state per panel
├── files/                                    # NEW
│   ├── FilesIndex.tsx                        # /files
│   ├── FileDetail.tsx                        # /files/<slug>
│   └── FilesSidebar.tsx                      # left nav of screens
└── DocsShell.tsx                             # MODIFIED: takes a `nav` prop so /files, /components etc. reuse

app/
├── files/page.tsx                            # NEW
├── files/[slug]/page.tsx                     # NEW
├── components/page.tsx                       # MODIFIED: switch from PageShell → DocsShell
├── illustrations/page.tsx                    # MODIFIED: same
└── logos/page.tsx                            # MODIFIED: same

figma-plugin/
├── code.ts                                   # MODIFIED: + Audit + click-to-apply
└── ui.html                                   # MODIFIED: + audit panel + fix cards

.github/workflows/
└── audit-on-tokens-pr.yml                    # NEW: PR-time diff + coverage gate

scripts/
└── audit-tokens.ts                           # NEW: thin npm wrapper around go run ./cmd/audit
```

---

## High-Level Technical Design

> *Directional guidance for review, not implementation specification.*

**End-to-end data flow:**

```mermaid
flowchart TB
    Operator([Operator]) -->|edit + commit| Manifest[lib/audit-files.json]
    Operator -->|npm run audit| AuditCmd[cmd/audit]
    Designer([Designer]) -->|opens plugin| Plugin[Figma Plugin]
    DSOwner([DS Owner]) -->|PR with token edits| GH[GitHub Actions]

    Manifest --> AuditCmd
    AuditCmd -->|fetch| Figma[Figma REST API]
    AuditCmd -->|reads| Tokens[lib/tokens/indmoney/*.tokens.json]
    AuditCmd -->|writes| AuditJSON[lib/audit/*.json + index.json]

    Plugin -->|POST /v1/audit| DSService[cmd/server]
    DSService -->|same core| AuditCore[internal/audit.Audit]
    AuditCore --> Tokens
    DSService -->|JSON| Plugin
    Plugin -->|setBoundVariable| Figma

    GH -->|cmd/audit-diff| AuditCmd2[diff baseline vs proposed]
    AuditCmd2 -->|comment| PR[PR comment + status check]

    AuditJSON --> DocsBuild[Next.js build]
    DocsBuild -->|reads| FilesUI[/files routes]
    DocsBuild -->|reads| LivingDocs[Foundations + Components<br/>usage chips]
```

**Files-tab page anatomy (mirror of Foundations DocsShell):**

```
┌────────────────────────────────────────────────────────────────┐
│ Header (brand · sync chip · top nav · ⌘K · density · theme)    │
├──────────────┬─────────────────────────────────────────────────┤
│ Sidebar      │ Main pane                                       │
│              │                                                 │
│ INDstocks V4 │ ┌───────────────────────────────────────────┐  │
│   • Trade    │ │ Trade screen          source: lib/audit/  │  │
│     ▸ Tokens │ │                       INDstocks-v4.json   │  │
│     ▸ Comps  │ │ ┌─────────┐ ┌─────────┐ ┌─────────┐       │  │
│     ▸ Drift  │ │ │ Tokens  │ │ Comps   │ │ Drift   │       │  │
│   • Watchlist│ │ │ 87/92   │ │ 12/3/5  │ │ 2 P1    │       │  │
│   • Login    │ │ └─────────┘ └─────────┘ └─────────┘       │  │
│              │ │                                            │  │
│ INDmoney app │ │ [Drift detail rows w/ Apply-via-plugin     │  │
│   • Wealth   │ │  links]                                    │  │
│   • Profile  │ └───────────────────────────────────────────┘  │
│              │ ┌───────────────────────────────────────────┐  │
│ ─────────    │ │ Watchlist screen        ...                │  │
│ Global drift │ └───────────────────────────────────────────┘  │
│ Promote-to-  │                                                 │
│ DS candidates│                                                 │
└──────────────┴─────────────────────────────────────────────────┘
```

**Plugin audit panel anatomy:**

```
┌────────────────── INDmoney DS Sync ──────────────────┐
│ Mode: ○ Selection  ○ Page  ● File                    │
│ Base URL: http://localhost:7474                      │
│ ┌──────────────────────────────────────────────────┐ │
│ │ Audit summary                                    │ │
│ │ tokens 87/92 (94.5%) · comps 12/3/5 · 2 P1       │ │
│ └──────────────────────────────────────────────────┘ │
│ ┌── Fix card ─────────────────────────────────────┐  │
│ │ "Trade/PriceLabel"  fill #6B7280 (unbound)      │  │
│ │ → surface.surface-grey-separator-dark (0.011)   │  │
│ │ [ Apply ]  [ Skip ]                             │  │
│ └─────────────────────────────────────────────────┘  │
│ [ … 7 more fix cards … ]                             │
│ Log:  audit complete · 14ms                          │
└──────────────────────────────────────────────────────┘
```

---

## Implementation Units

> Phase letters mirror the brainstorm's Next Steps for sequencing readability.
> Recommended ship order (designer-perceived value first): **A → D → E → B → F → C → G**.

### Phase A — Audit core (Go)

- U1. **Port DesignBrain primitives + define audit schema**

**Goal:** Stand up `services/ds-service/internal/audit/` with the typed schema, color math, classifier, and canonical hash. No Figma I/O yet.

**Requirements:** R1, R2, R3, R12 (origin); plus this unit grounds R23.

**Dependencies:** None.

**Files:**
- Create: `services/ds-service/internal/audit/types.go`, `classify.go`, `hash.go`, `color.go`, `match.go`, `drift.go`
- Test:    `services/ds-service/internal/audit/{classify,hash,color,match,drift}_test.go`

**Approach:**
- Port `looksLikeScreen` verbatim into `classify.go`. Add a `ClassifyKind(node)` returning `screen | section | frame | component | text | icon | shape | container | other` matching DesignBrain's `import_classify.go`.
- Port a simplified canonical hash: SHA-256 over a JSON-canonicalized projection of `(type, normalized_dims, fillStyleId, textStyleId, autoLayout fingerprint, sorted child-type sequence)`. Strip `id`, `absoluteBoundingBox`, `effects`, names.
- Color math in `color.go`: sRGB → OKLab → OKLCH; `Distance(a, b)` = sqrt of weighted Lab squared diffs.
- `MatchingService` subset (4 signals: componentKey 0.50, name lex 0.20, fillStyleId 0.20, color 0.10 — re-weighted from the 8-signal default since we drop vector + variant_map + layout + geom).
- Define `AuditResult`, `AuditScreen`, `AuditNode`, `FixCandidate` in `types.go` with `schema_version: "1.0"` constant.

**Test scenarios:**
- *Happy path.* Given a frame named "Trade Screen", `looksLikeScreen` returns true. Given "WIP — sketches", returns false.
- *Edge case.* Given an empty name, returns false. Given a name with mixed case "TRADE SCREEN", returns true.
- *Happy path.* Given two structurally-identical nodes with different `id` and `absoluteBoundingBox`, canonical hash matches.
- *Edge case.* Given two nodes with the same dimensions but different `fillStyleId`, hashes differ.
- *Happy path.* OKLCH distance between `#6F7686` and `#6B7280` returns 0.011 ± 0.001.
- *Edge case.* Distance between identical hexes returns 0.
- *Happy path.* MatchCandidate: given a node with componentKey matching a published library entry, returns `decision: "accept"` with score ≥ 0.50.
- *Edge case.* Given no componentKey but matching name + fillStyleId, returns `decision: "ambiguous"` with the contributing signals listed.
- Covers AE1 (drift recommendation includes OKLCH distance and usage count).

**Verification:** All tests pass; package builds cleanly.

---

- U2. **Audit engine + driver**

**Goal:** Public `Audit(node, tokens, opts) → AuditResult` function that walks a Figma node tree (any subtree), classifies, hashes, matches against the published DS, computes drift, and emits the typed result.

**Requirements:** R1, R2, R3 (origin).

**Dependencies:** U1.

**Files:**
- Create: `services/ds-service/internal/audit/engine.go`, `output.go`
- Test:   `services/ds-service/internal/audit/engine_test.go` (uses fixture JSON node trees from `services/ds-service/internal/audit/testdata/`)

**Approach:**
- `engine.go::Audit` walks the tree DFS; for each `looksLikeScreen` frame, emits an `AuditScreen` block.
- Per node: extract bound variables vs raw fills/text-styles/spacings; diff against published tokens; if unbound and a close match exists (OKLCH ≤ 0.03 or px ≤ 1), emit a `FixCandidate` with priority by `drift × usage_count`.
- Component classification: call `MatchingService.MatchCandidate` for each `INSTANCE` / `COMPONENT` node.
- `output.go::WriteToDisk` serializes per-file + builds the index roll-up (with `crossFilePatterns` from canonical-hash bucketing across files).

**Test scenarios:**
- *Happy path.* Given a fixture file with one screen, 5 nodes, all bound to known tokens — `AuditResult.coverage.fills == 1.0`.
- *Happy path.* Given a screen with one unbound fill `#6B7280` close to a token — emits one FixCandidate with `priority: "P2"`.
- *Edge case.* Given an empty file (no screens), emits `AuditResult{screens: []}` with no error.
- *Error path.* Given a node tree that fails to deserialize, returns wrapped error including node path.
- *Integration.* Two fixture files share an identical canonical hash — `index.json.crossFilePatterns` lists exactly one entry referencing both.
- Covers AE7 (cross-file pattern detection on canonical hash).

**Verification:** Audit run on a 200-node fixture completes in < 250ms; output JSON validates against the schema in `types.go`.

---

- U3. **`cmd/audit` CLI + curated manifest**

**Goal:** A runnable CLI that reads `lib/audit-files.json`, fetches each file via Figma REST, runs the engine, writes per-file + index JSON.

**Requirements:** R7 (origin), R1.

**Dependencies:** U2.

**Files:**
- Create: `services/ds-service/cmd/audit/main.go`
- Create: `lib/audit-files.json` (template with one example entry)
- Modify: `scripts/sync-tokens.ts` (add `audit` step optional)
- Modify: `package.json` (`"audit": "tsx scripts/audit-tokens.ts"`)
- Create: `scripts/audit-tokens.ts` (thin Node wrapper)
- Test:   `services/ds-service/cmd/audit/main_test.go`

**Approach:**
- CLI flags: `--brand indmoney --files lib/audit-files.json --out lib/audit/ --diff <baseline-ref>`.
- Reuses `services/ds-service/internal/figma/client/` for REST.
- Per file: fetches at `depth=0`, runs `Audit()`, writes `lib/audit/<slug>.json`. After all files: writes `index.json` with rollup + crossFilePatterns.
- Stamps `$extensions.com.indmoney.{provenance: "figma-audit", extractedAt, sweepRun, fileRev, designSystemRev}` on each file.

**Test scenarios:**
- *Happy path.* Given a manifest with 1 file (mock REST), CLI exits 0 and writes 2 JSON files (1 per-file + 1 index).
- *Error path.* Given a manifest pointing at a non-existent file_key, CLI logs the error per-file and continues with remaining files.
- *Edge case.* Given an empty manifest, CLI exits 0 silently (no JSON written).
- *Integration.* Round-trip: write a fixture token set + a fixture node-tree mock, run CLI, assert the produced JSON has expected coverage %.

**Verification:** `go run ./services/ds-service/cmd/audit --brand indmoney` against the live Glyph file produces real JSON in `lib/audit/`.

---

- U4. **`cmd/server` HTTP endpoint for plugin**

**Goal:** Run the audit core behind `POST /v1/audit` for the Figma plugin to call. Local dev: `localhost:7474`.

**Requirements:** R6 (origin); plus this unit enables F2.

**Dependencies:** U2.

**Files:**
- Create or Modify: `services/ds-service/cmd/server/main.go`
- Create: `services/ds-service/internal/audit/server.go` (handler)
- Test:   `services/ds-service/internal/audit/server_test.go` (httptest)

**Approach:**
- Handler accepts `{node_tree, scope: "selection"|"page"|"file", file_key, ds_rev}` and returns the `AuditResult` plus a `cache_key` (= `${file_key}:${ds_rev}`) so the plugin knows what to store.
- CORS: `Access-Control-Allow-Origin: *` (or specifically `null` for plugin sandbox) + `Access-Control-Allow-Methods: POST,OPTIONS` + `Access-Control-Allow-Headers: Content-Type`.
- Single goroutine; bounded by `MaxBytesReader` (100 MB).
- Add a `Makefile` target `audit-serve` that wraps `go run ./services/ds-service/cmd/server`.

**Test scenarios:**
- *Happy path.* POST with valid body returns 200 + AuditResult JSON with the same `cache_key` echoed back.
- *Error path.* Empty body → 400; oversized body → 413; malformed JSON → 400 with parse-error detail.
- *Integration.* Preflight OPTIONS returns 204 with allow-origin header; subsequent POST succeeds.

**Verification:** `curl -X POST localhost:7474/v1/audit -d @fixture.json` returns the expected JSON.

---

### Phase D — Living docs (audit data flowing into Foundations + Components)

- U5. **TS audit loader + types**

**Goal:** Mirror the Go schema in TypeScript so all docs UI reads `lib/audit/index.json` + per-file JSON via typed loaders.

**Requirements:** R10, R14, R15 (origin); R23, R25, R26 (plan-local).

**Dependencies:** U2 (schema must be locked).

**Files:**
- Create: `lib/audit/types.ts`, `lib/audit/index.ts`, `lib/audit/manifest.ts`
- Test:   `lib/audit/manifest.test.ts` (Vitest or Node test runner — match existing test infra; default to a tiny inline test runner if none exists)

**Approach:**
- `types.ts`: 1:1 with `services/ds-service/internal/audit/types.go`. Schema-version constant on both ends; loader rejects mismatched versions with a console.warn and falls back to legacy reader.
- `manifest.ts`: `loadAuditIndex()`, `loadFileAudit(slug)`, `usageCount(tokenPath)`, `usageBreakdown(tokenPath)`, `isStale(extractedAt)`.
- Build-time only — these read the committed JSON via Next's `import` statement, so usage chips are static at build.

**Test scenarios:**
- *Happy path.* `usageCount("text-n-icon.blue")` against a fixture index returns the correct sum across files.
- *Edge case.* `usageCount` for a token with zero uses returns `0`.
- *Edge case.* `usageCount` for a token NOT IN the audit returns `undefined` (not 0 — distinguishes "audited but unused" from "missing data").
- *Error path.* `loadAuditIndex` with a bad-shape JSON throws a typed error including file path.

**Verification:** `npx tsc --noEmit` clean; tests pass.

---

- U6. **`UsageChip` primitive + `EmptyAuditState`**

**Goal:** A reusable chip component that shows usage signal across all of Foundations + Components, plus the empty/stale variants.

**Requirements:** R15, R16 (origin); R23, R26, R27 (plan-local).

**Dependencies:** U5.

**Files:**
- Create: `components/audit/UsageChip.tsx`, `components/audit/EmptyAuditState.tsx`
- Test:   `components/audit/UsageChip.test.tsx`

**Approach:**
- Variants: `audited-with-usage` (`47 uses · 3 files` at full opacity), `audited-zero-usage` (`0 uses` with reduced opacity + tag), `not-audited` (`?` chip at 50% with tooltip "not in audit manifest").
- Hover popover (Radix Tooltip / Popover): top-5 use sites, deep-links into Files tab + (if available) Figma `https://figma.com/file/<key>?node-id=<id>`.
- Reuses `swatchHover` motion variant for hover lift.
- Theme-aware via CSS variables; respects `prefers-reduced-motion`.
- `EmptyAuditState` wraps `DataGapPreview` (existing) for parity.

**Test scenarios:**
- *Happy path.* Given `usageCount: 47, fileCount: 3` props, renders `47 uses · 3 files`.
- *Edge case.* Given `usageCount: 0`, renders `0 uses` with reduced opacity + zero-usage tag.
- *Edge case.* Given `usageCount: undefined`, renders `?` chip with the not-audited tooltip.
- *Integration.* Click the chip → URL updates to `/files?token=<path>` (Next router push spy assertion).

**Verification:** Visual smoke on Foundations Color section after U7 wires it in.

---

- U7. **Wire usage chips across Foundations + Components, reorder by usage**

**Goal:** Every swatch / type style / spacing row / motion preset / component tile shows a `UsageChip`. `/components` reorders SECTIONs by total usage descending; zero/single-use entries collapse into a "Rare or experimental" footer.

**Requirements:** R15, R17 (origin); R26 (plan-local).

**Dependencies:** U5, U6.

**Files:**
- Modify: `components/sections/ColorSection.tsx`, `TypographySection.tsx`, `SpacingSection.tsx`, `MotionSection.tsx`, `IconographySection.tsx`, `EffectsSection.tsx`
- Modify: `components/ComponentInspector.tsx` (chip on the tile + per-variant chip)
- Modify: `app/components/page.tsx` (reorder SECTIONs by audit usage at build time)

**Approach:**
- Each existing tile / row gets a `<UsageChip token={p.token}/>` placed where it doesn't compete with the primary affordance.
- Zero-usage entries: wrap their motion-div with `style={{ opacity: 0.5 }}` and append a `data-zero-usage` attr.
- `/components` build: read `auditIndex.componentUsage`, sort each `iconsByCategory("component")` map's lists by `usage_count` desc; collapse `usage_count <= 1` into a `<details>` "Rare or experimental".
- All edits compose `lib/motion-variants.ts` — no new motion idioms.

**Test scenarios:**
- *Integration.* Foundations Color section renders a chip on every visible swatch. Visual snapshot captures one tile with the chip in the expected position.
- *Happy path.* `/components/page.tsx` renders the highest-usage component tile first within each SECTION.
- *Edge case.* SECTION with all zero-usage components renders all entries inside the "Rare or experimental" `<details>`.

**Verification:** Visual sweep on light + dark + each density mode confirms parity with the rest of the section.

---

### Phase E — Plugin audit + click-to-apply

- U8. **Plugin Audit command + selection/page/file scope**

**Goal:** Plugin gains an "Audit" command that scopes to selection / page / file, POSTs the node tree to ds-service, and renders the response.

**Requirements:** R4, R5, R6 (origin).

**Dependencies:** U4.

**Files:**
- Modify: `figma-plugin/code.ts`, `figma-plugin/ui.html`
- Modify: `figma-plugin/manifest.json` (new menu command)

**Approach:**
- New menu entries: `Audit selection`, `Audit current page`, `Audit file`.
- `code.ts` collects the relevant node tree (using `figma.currentPage.findAll` / `figma.root.findAll`) + serializes only the fields the audit core needs (type, name, fills, strokes, effects, autoLayout fields, componentKey).
- POSTs to base URL + `/v1/audit` with `{node_tree, scope, file_key, ds_rev}`. Streams progress to the panel.
- UI panel shows a top summary card (coverage %, comps breakdown, P1/P2 counts) + a scrollable list of `<FixCard>` components.

**Test scenarios:**
- *Happy path.* Mock `figma.currentPage.selection` with one node → plugin POSTs body containing exactly that node's serialized form.
- *Error path.* If POST fails (network), UI shows red-bordered error banner with the URL + retry button (per R27 stale/error pattern).
- *Edge case.* Empty selection → plugin disables the button + tooltip "select a layer first".

**Verification:** Manual: in Figma desktop, select a node → click `Audit selection` → see fix cards within 3s.

---

- U9. **Click-to-apply via Figma variable APIs**

**Goal:** Each `FixCard` has an `Apply` button that writes the suggested token binding to the node.

**Requirements:** R5 (origin).

**Dependencies:** U8.

**Files:**
- Modify: `figma-plugin/code.ts`, `figma-plugin/ui.html`

**Approach:**
- Map `FixCandidate.token_path` to a Figma `Variable` via `figma.variables.getVariableById` (the audit response includes the variable id). Apply via `node.setBoundVariable("fills", variable)` (color) or the appropriate setter for spacing/typography.
- On apply: re-audit just that node, update the card to "Applied · undo with ⌘Z".
- Multi-fill nodes: one card per fill. Try a stacked card UI in U13 polish if it feels cluttered.
- Plan-tier graceful fallback: if `setBoundVariable*` throws "permission denied", swap the Apply button for a `Copy token path` button with a one-line "Your Figma plan doesn't allow plugin variable writes — copy and apply manually" hint.

**Test scenarios:**
- *Happy path.* Click `Apply` on a fill fix → node's `fills[0].boundVariables.color.id` matches the suggested variable id. Card flips to "Applied".
- *Error path.* Plan-tier permission denied → card swaps to copy-path button + hint banner.
- *Edge case.* Apply twice (race): second click is a no-op; card remains "Applied".
- *Integration.* Apply → re-audit shows the node as resolved (covers AE2 round-trip).

**Verification:** Manual round-trip in Figma — pick an unbound rectangle, run audit, click Apply, confirm the variable is bound + fill renders identically.

---

- U10. **Staleness banner + clientStorage cache**

**Goal:** When the plugin opens in a file whose last audit predates the current DS rev, banner appears with `Audit now` shortcut.

**Requirements:** R20 (origin).

**Dependencies:** U9.

**Files:**
- Modify: `figma-plugin/code.ts`, `figma-plugin/ui.html`

**Approach:**
- On plugin start: `figma.clientStorage.getAsync("audit:" + file_key)` → `{ ds_rev, audited_at }`.
- Fetch `<base_url>/icons/glyph/manifest.json` for current `ds_rev` (sha-256 of bytes).
- If mismatch or `audited_at` > 7 days → render banner above the panel: "DS updated · audit may be stale". CTA `Audit now` runs the file-scope audit immediately.

**Test scenarios:**
- *Happy path.* No stored audit → banner shows "First-time audit recommended".
- *Happy path.* Stored audit matches current DS rev + recent → no banner.
- *Edge case.* Stored audit > 7 days even with matching rev → banner shows "Audit > 7 days old".
- *Integration.* Click `Audit now` triggers file-scope audit + clears banner on success.

**Verification:** Manual; flip a token value in `lib/tokens/indmoney/`, refresh manifest, reopen plugin — banner should fire.

---

### Phase B — Files tab UI

- U11. **Generalize DocsShell + introduce nav-prop pattern**

**Goal:** `DocsShell` becomes a reusable container that takes a `nav` (sidebar contents) and `children` (main pane). Foundations passes its existing nav config; Files tab passes file/screen list.

**Requirements:** R10, R11, R23 (origin + plan-local).

**Dependencies:** None blocking.

**Files:**
- Modify: `components/DocsShell.tsx`, `components/Sidebar.tsx`
- Test:   `components/DocsShell.test.tsx` (snapshot of nav prop variants)

**Approach:**
- Extract `nav` config to a prop. Default = current Foundations nav (preserve back-compat by keeping the foundations call site unchanged).
- Sidebar takes the same nav object; activeSection logic stays centralized in `useUIStore` but accepts an array of valid section ids passed in.
- Mobile drawer + scroll-spy + URL hash sync continue to work generically.

**Test scenarios:**
- *Integration.* Foundations renders unchanged after the refactor (visual snapshot match).
- *Integration.* A nav with a different shape (file/screen 2-level tree) renders correctly with scroll-spy on the inner anchor.
- *Edge case.* Empty nav renders without crashing (no left rail, just content).

**Verification:** All Foundations sections still scroll-spy correctly; no regressions on density / theme / mobile drawer.

---

- U12. **`/files` index + `/files/<slug>` detail**

**Goal:** New routes; index lists files as cards, detail uses DocsShell with screens-as-sidebar.

**Requirements:** R8, R9, R10, R11 (origin); R23, R24, R26, R27 (plan-local).

**Dependencies:** U5, U11.

**Files:**
- Create: `app/files/page.tsx`, `app/files/[slug]/page.tsx`
- Create: `components/files/FilesIndex.tsx`, `components/files/FileDetail.tsx`, `components/files/FilesSidebar.tsx`
- Create: `components/audit/AuditLensCards.tsx`, `components/audit/FixCard.tsx`, `components/audit/DriftBatchView.tsx`

**Approach:**
- `FilesIndex`: card per audited file (name · last-audited timestamp · coverage % · DS-component % · headline drift hex). Cards animate in via `stagger` + `itemFadeUp`. Click → `/files/<slug>`.
- `FileDetail` reads `lib/audit/<slug>.json`; renders DocsShell with `FilesSidebar` listing screens + admin tools (Global drift, Promote-to-DS candidates) at the bottom.
- `AuditLensCards` is the per-screen triple (Tokens / Components / Drift). Each panel mirrors existing Spacing-section styling — counts, provenance, drift rows clickable.
- `DriftBatchView` (admin tool): filter by hex / token / file; export-as-Markdown button.
- Empty state if `lib/audit/` has no entries: `EmptyAuditState` → "No audits yet. Run `npm run audit` to populate."

**Test scenarios:**
- *Happy path.* Given fixture `lib/audit/index.json` with 2 files, `/files` renders 2 tiles in coverage-desc order.
- *Happy path.* `/files/indstocks-v4` renders sidebar with 3 screens; clicking one scrolls to its panel triple (covers AE3).
- *Edge case.* `lib/audit/` empty → renders the EmptyAuditState card.
- *Edge case.* `/files/<unknown-slug>` → 404 page.
- *Integration.* DriftBatchView "Copy as Slack message" produces the expected Markdown shape (R24).

**Verification:** `npm run build` passes; visual sweep on dark + light + each density confirms Foundations parity.

---

### Phase F — Guardrails

- U13. **`cmd/audit-diff` and `audit-on-tokens-pr.yml`**

**Goal:** When a PR touches `lib/tokens/indmoney/**`, CI re-audits with the proposed token set, posts a summary comment, and fails if coverage drops below threshold.

**Requirements:** R18, R19 (origin).

**Dependencies:** U3.

**Files:**
- Create: `services/ds-service/cmd/audit-diff/main.go`
- Create: `.github/workflows/audit-on-tokens-pr.yml`
- Modify: `lib/audit-files.json` (add per-file coverage thresholds)

**Approach:**
- `cmd/audit-diff` accepts `--baseline-ref <git-ref>`; checks out that ref's `lib/tokens/indmoney/`, runs audit, then runs again on the proposed tokens, diffs the two `AuditResult`s.
- Output: per-file impact counts (nodes whose binding would change), top-5 visual deltas (largest OKLCH distance changes), and an overall coverage delta.
- Workflow: triggers on PR open / synchronize with `paths: ['lib/tokens/indmoney/**']`. Posts comment via `actions/github-script@v7`. Status check `audit/coverage` fails the PR if any file's coverage drops > 5pp OR below the per-file threshold.

**Test scenarios:**
- *Happy path.* Token change with no coverage impact → comment shows "no nodes affected"; status passes.
- *Happy path.* Token value change affecting 47 nodes → comment lists per-file impact (covers AE6).
- *Edge case.* PR adds a new token (no removals) → comment shows "0 nodes would change binding".
- *Error path.* Missing baseline ref → workflow fails with helpful error.

**Verification:** Run `cmd/audit-diff` locally against a hand-crafted token diff; assert output matches expectation.

---

- U14. **Deprecation propagation + manifest-side flag**

**Goal:** Tokens marked `$extensions.com.indmoney.deprecated: true` cause every node bound to them to surface a P1 fix with a `replacedBy` recommendation.

**Requirements:** R22 (origin).

**Dependencies:** U2.

**Files:**
- Modify: `services/ds-service/internal/audit/drift.go` (deprecation rule)
- Modify: `lib/tokens/indmoney/semantic.tokens.json` (add example deprecation)
- Modify: `components/audit/FixCard.tsx` (deprecated pill)

**Approach:**
- `drift.go` pre-scan the published tokens; build a `deprecated[token_path] = replacedBy?` map.
- Engine emits FixCandidate with `priority: "P1", reason: "deprecated"` for any node bound to a deprecated token.
- Plugin renders a "deprecated" pill on those cards.

**Test scenarios:**
- *Happy path.* Mark a token deprecated → next audit emits P1 fixes for every consumer.
- *Edge case.* Deprecated token without `replacedBy` → fix shows "deprecated, no automatic replacement".
- *Integration.* PR-time diff comment includes "N nodes use newly-deprecated tokens".

**Verification:** Run audit on a fixture with one deprecated token; confirm fix shape.

---

### Phase C — Foundations-parity UX overhaul on existing gallery pages

- U15. **Upgrade `/components`, `/illustrations`, `/logos` to DocsShell chrome**

**Goal:** Each page gets the full DocsShell sidebar + scroll-spy + density + theme + mobile drawer, matching Foundations.

**Requirements:** R13 (origin); R23, R25, R26, R28 (plan-local).

**Dependencies:** U11 (DocsShell generalization).

**Files:**
- Modify: `app/components/page.tsx`, `app/illustrations/page.tsx`, `app/logos/page.tsx`
- Modify: `components/PageShell.tsx` (deprecate or repurpose; Foundations doesn't use it)

**Approach:**
- Each page passes a nav of its own (Components: SECTION names; Illustrations: 2D / 3D / etc.; Logos: bank / merchant / sub-brand). Sub-anchors map to the existing in-page section ids.
- Scroll-spy + URL hash sync work via the generalized DocsShell.
- ⌘K SearchModal extends to index assets across all four kinds (icons, components, illustrations, logos) AND all audited files (R28).

**Test scenarios:**
- *Integration.* `/components` renders a sidebar with one entry per SECTION. Clicking one scrolls to that SECTION; URL hash updates.
- *Edge case.* Mobile viewport collapses to hamburger drawer (mirrors Foundations).
- *Integration.* ⌘K open → search "Trade" returns Trade-screen audit + Trade-button component (if both exist).

**Verification:** Visual parity sweep: all four pages (Foundations + the three gallery pages) feel like one product on dark + light + each density.

---

### Phase G — Polish (deferred to follow-up PRs)

- U16. **Drift threshold tuning + ds-service hosting helper + plugin error states + cross-file canonical promotion**

**Goal:** Final-mile polish based on early designer feedback. Not blocking v1 release.

**Requirements:** R3 (drift), R6 (hosting), R5 (error states), R21 (cross-file).

**Dependencies:** U1–U15 in production for ≥ 1 week.

**Files:**
- Modify: `services/ds-service/internal/audit/drift.go` (per-token-type thresholds via manifest)
- Add:    `Makefile` target `audit-serve`
- Modify: `figma-plugin/code.ts`, `ui.html` (richer error states)
- Modify: `components/files/FileDetail.tsx` (Promote-to-DS sidebar tile)

**Approach:** Iterate based on real audit logs (P50/P95/P99 distance histograms) + designer feedback. Carry as one bundled polish PR.

**Test scenarios:** Defer to time of write.

**Verification:** Designer sign-off on the v1 surface.

---

## System-Wide Impact

- **Interaction graph:** Plugin → ds-service `/v1/audit` → audit core. cmd/audit → audit core. Foundations + Components + Files tab → committed JSON in `lib/audit/`. CI workflow → cmd/audit-diff → PR comment.
- **Error propagation:** Audit core returns typed errors with node paths; HTTP handler maps to 4xx/5xx; CLI prints + continues to next file (one bad file doesn't block the sweep). Plugin shows a banner on any non-200; Files tab renders error state on bad JSON.
- **State lifecycle risks:** Audit JSON files are written atomically (write to `.tmp` then `rename`). Stale audit JSON is detected by comparing `extractedAt` against the current DS rev; UI flags stale data with an amber banner.
- **API surface parity:** `cmd/audit` and `cmd/server`'s `/v1/audit` MUST produce identical results for identical inputs. Single shared `Audit()` function enforces this.
- **Integration coverage:** End-to-end test harness (post-U12): commit a token change → CI runs cmd/audit-diff → PR comment renders → manually click through to the impacted file in `/files` and confirm the screen highlights the affected node.
- **Unchanged invariants:** Foundations existing behavior (scroll-spy, density, theme, ⌘K, mobile drawer) is preserved exactly. The DocsShell generalization in U11 keeps the existing call site working without changes.

---

## Risks & Dependencies

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Figma write APIs unavailable on team's plan tier | Medium | High | Plan-tier check in U9; graceful fallback to copy-token-name; document required tier in `figma-plugin/README.md`. |
| Canonical hash false positives (two real-different nodes hash same) | Medium | Medium | U1 sample test on 200 real nodes; if rate > 5%, narrow normalization (re-add stripped fields). |
| Audit performance on a 30k-node file | Low | Medium | Stream node walk; cap file size in `lib/audit-files.json` per-file; if a file blows past 4-min budget, skip with a warning instead of failing the sweep. |
| Plugin CORS preflight from Figma sandbox to localhost | Medium | Medium | U4 emits `Access-Control-Allow-Origin: null` for plugin requests; document the CORS expectation in `figma-plugin/README.md`. |
| ds-service localhost requirement is friction for designers | Medium | Medium | Provide a one-line `Makefile` install + `audit-serve` target. Hosted ds-service is a v1.1 follow-up. |
| Audit JSON committed bloat | Low | Low | `lib/audit/*.json` size budget: 100 KB per file; CI fails if exceeded. Trim node-level detail to what FixCard actually shows. |
| Designer applies a fix that visually regresses | Medium | Medium | Plugin click-to-apply leaves Figma's native ⌘Z stack intact — undo works as expected. Doc the undo behavior in `figma-plugin/README.md`. |
| OKLCH threshold (0.03) is wrong for INDmoney's palette | Medium | Low | Sweep logs P50/P95 distances; tune in U16 based on data, not guesses. |

---

## Phased Delivery

### Phase 1 (lands first — designer-perceived value priority)

- A (audit core): U1, U2, U3, U4 — pipeline produces real JSON; ds-service serves it.
- D (living docs): U5, U6, U7 — usage chips light up Foundations + Components.

### Phase 2 (designer friction killer)

- E (plugin): U8, U9, U10 — click-to-apply ships; staleness banner.

### Phase 3 (governance)

- B (Files tab): U11, U12 — DS leads get the rollup view.

### Phase 4 (guardrails)

- F (CI): U13, U14 — PR-time diff + deprecation propagation.

### Phase 5 (parity sweep)

- C (gallery upgrade): U15 — `/components`, `/illustrations`, `/logos` reach Foundations parity.

### Phase 6 (polish)

- G: U16 — bundled follow-up.

---

## Documentation Plan

- **`figma-plugin/README.md`**: install + plan-tier expectation + audit command usage + CORS + click-to-apply ⌘Z note.
- **`docs/runbooks/audit.md`** (new): operator runbook — `npm run audit`, reading `lib/audit/index.json`, adding a file to the manifest, interpreting drift recommendations, the `cmd/audit-diff` CI flow.
- **`docs/runbooks/onboard-designer.md`** (existing): add a "How to use the plugin" section.
- **`docs/STATUS.md`** (existing): update once Phase 1 lands.
- **`PORTED_FROM_FIELD.md`** + new `services/ds-service/internal/audit/SOURCE.md`: record the DesignBrain seed SHA used for the port.

---

## Operational / Rollout Notes

- **Local-first rollout.** Designers run `npm run audit:serve` on their laptop. No infra to provision. Hosted ds-service deferred to v1.1.
- **Feature flag.** None. Each phase ships independently; living docs / plugin / files tab don't depend on each other after the audit core lands.
- **Schema migrations.** `schema_version: "1.0"`. Breaking changes bump the major + add a migration note in `docs/runbooks/audit.md`.
- **Monitoring.** v1 emits structured slog logs from `cmd/audit` + `cmd/server`. No metrics endpoint until v1.1.
- **Rollback.** Each phase is a separate PR; reverting it removes only that phase's surface (audit core stays).

---

## Sources & References

- **Origin document:** `docs/brainstorms/2026-04-28-files-tab-audit-pipeline-requirements.md`
- **DesignBrain primitives ported:** see Context & Research → "DesignBrain primitives to port"
- **Foundations chrome (template):** `components/DocsShell.tsx`, `lib/motion-variants.ts`, `lib/ui-store.ts`
- **Existing extractor pipelines:** `services/ds-service/cmd/{extractor,icons,variants,variables,effects}/`, `lib/icons/manifest.ts`
- **Figma plugin scaffold:** `figma-plugin/`
