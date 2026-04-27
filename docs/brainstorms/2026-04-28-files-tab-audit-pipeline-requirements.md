---
date: 2026-04-28
topic: files-tab-audit-pipeline
---

# Files tab + audit pipeline (DesignBrain reuse)

## Problem Frame

INDmoney designers fix token drift "as they remember it." There's no live tooling that says *"this `#6B7280` isn't a token; the closest match is `surface.surface-grey-separator-dark`."* Engineering has no per-file rollup of how on-token a Figma file is, so drift compounds silently and is caught — if at all — at PR review. The docs site today shows abstract swatches without telling a designer whether a token is used 200× in production or zero times.

DesignBrain-AI already computed the right schema (`FinalNodeUnderstanding_v1` with `TokensAndVariables`, `DriftAndExperiments`, governance, and a multi-signal `MatchingService`) for a different product. We can port a subset to **three delivery surfaces** that compound on each other:

- **Plugin** (in-Figma, per-designer): live verdict on the selected node + click-to-apply the correct DS token. Removes the friction at the moment of choice.
- **Files tab** (docs site, team rollup): per-Figma-file score, per-screen drift list, weekly health rollup — governance for DS leads.
- **Living Foundations + Components docs** (existing pages, enriched): every swatch / component / text style gains real usage stats from audits — designers see what's load-bearing, what's experimental, what's drift, instead of treating every token as equal.

All three surfaces share one audit core in `services/ds-service`. v1 ships against the curated INDmoney file manifest and the published Glyph token set. Together they convert the docs site from a static reference into a system that documents itself from production usage AND guards against silent drift.

---

## Actors

- A1. **Designer** — works in Figma, runs the plugin on their current selection / page / file, applies suggested tokens with one click. Primary audience.
- A2. **DS lead / engineering owner** — reads the Files tab to see which files are off-token, which screens drift the most, and which components escape the DS. Doesn't typically open Figma.
- A3. **Operator** — adds Figma file keys to the curated audit manifest, runs the server sweep, reviews diffs before commit/push.
- A4. **ds-service (Go)** — runs the audit core; called both from `cmd/audit` (sweep) and from the plugin's `/v1/audit` endpoint.
- A5. **Plugin (Figma)** — already scaffolded under `figma-plugin/`. Hosts the in-Figma UI, calls ds-service, applies tokens via Figma write APIs.
- A6. **Docs site (Next.js)** — renders the Files tab from committed audit JSON, surfaces drift trends, links back to Figma.

---

## Key Flows

- F1. **Server sweep produces canonical per-file audit**
  - **Trigger:** Operator runs `npm run audit` (calls `cmd/audit`), or scheduled CI job
  - **Actors:** A3, A4
  - **Steps:**
    1. cmd reads `lib/audit-files.json` curated manifest
    2. For each file: fetches the file via Figma REST, finds final-design pages (frame names matching screen / page / view / modal / dialog / sheet)
    3. Walks each final page, classifies every node (DS instance / custom / ambiguous), diffs every fill / text-style / spacing against `lib/tokens/indmoney/*.tokens.json`, computes drift suggestions (closest-token by OKLCH distance for color, by px for dimension)
    4. Writes `lib/audit/<file_slug>.json` per file + a roll-up `lib/audit/index.json`
  - **Outcome:** Files tab data is fresh; commit + push redeploys docs site
  - **Covered by:** R1, R2, R3, R7

- F2. **Designer audits a file from inside Figma**
  - **Trigger:** Designer runs the INDmoney DS Sync plugin → "Audit current selection / page / file"
  - **Actors:** A1, A4, A5
  - **Steps:**
    1. Plugin collects the selected node tree (or current page, or whole file) and POSTs to `<ds-service>/v1/audit`
    2. ds-service runs the same audit core used by F1, returns the canonical schema
    3. Plugin renders verdicts inline: each unbound fill / wrong-token-bound node shows the value + closest DS token + an "Apply" button
    4. Designer clicks "Apply" → plugin writes the bound variable / style id back to the Figma node via write APIs
  - **Outcome:** Drift shrinks at the moment of choice; designer never had to remember the token name
  - **Covered by:** R4, R5, R6

- F3. **DS lead reviews per-file rollup**
  - **Trigger:** DS lead opens `/files` on the docs site
  - **Actors:** A2, A6
  - **Steps:**
    1. Top-level Files page lists every audited file as a card (file name, last-audited timestamp, overall token-coverage %, component-from-DS %, top drift hex)
    2. Click a file card → `/files/<file-slug>` page with a left nav listing each final-design page in that file
    3. Click a screen in left nav → main panel shows the three audit lenses (component usage, token coverage, drift suggestions) for that screen
  - **Outcome:** DS lead sees which files / screens need attention this week
  - **Covered by:** R8, R9, R10, R11

- F4. **Designer browses living docs powered by real usage**
  - **Trigger:** Designer opens `/` (Foundations) or `/components` to remember a token / pattern
  - **Actors:** A1, A6
  - **Steps:**
    1. Each color swatch / type style / component card carries a usage chip — "used in 47 places · 3 files" — derived from the latest audit
    2. Hover the chip → popover lists the screens (with deep-links into the Figma file when possible)
    3. Tokens with zero production usage are visually de-emphasized as "experimental" / "unused" so designers don't reach for stale options
    4. The Components page sorts each SECTION by usage (load-bearing first); single-use entries land in a "Rare / candidates for retirement" footer group
  - **Outcome:** Designer picks the most production-grade option without having to ask. New designers ramp faster because the docs reflect what's *real*, not what was once intended.
  - **Covered by:** R15, R16, R17

- F5. **DS owner previews the blast radius of a token change**
  - **Trigger:** DS owner edits a value in `lib/tokens/indmoney/*.tokens.json` (or proposes via PR)
  - **Actors:** A2, A4
  - **Steps:**
    1. CI runs the audit core in *diff mode* — passing the proposed token set instead of the committed one
    2. The PR description gets a comment: "Changing `surface.surface-grey-separator-dark` from `#6F7686` → `#727A8D` would re-bind 47 nodes across 3 files; 12 of those will visually shift past the contrast threshold"
    3. DS owner can click through to per-file impact pages
  - **Outcome:** Token changes ship with their consequences visible. No more "we changed grey-3 last week and now Trade screen looks wrong."
  - **Covered by:** R18, R19

- F6. **Designer is alerted to staleness when they open Figma**
  - **Trigger:** Designer opens the plugin in a file that was last audited > 7 days ago, OR the DS has changed since their last sync
  - **Actors:** A1, A5
  - **Steps:**
    1. Plugin shows a banner: "DS updated 3 days ago — 2 tokens you used have new values" with an `Audit now` button
    2. Designer clicks → fresh audit runs against the current DS
    3. Affected nodes are highlighted; click-to-apply propagates
  - **Outcome:** Designers stay in step with DS evolution without reading changelogs.
  - **Covered by:** R20

---

## Requirements

**Audit core (shared by sweep + plugin)**

- R1. The audit core lives in `services/ds-service/internal/audit/` (new package). Inputs: a Figma node tree (full file or sub-tree). Outputs: an `AuditResult` matching a v1 subset of DesignBrain's `FinalNodeUnderstanding_v1` shape — namely, per-node: `nodeIdentity`, `tokensAndVariables.{boundVariables, unmappedProperties, mappedTokens}`, `driftAndExperiments.{score, evidence}`, `finalRecommendations[]` (P1/P2/P3 with closest-token references).
- R2. The component-classification signal uses **componentKey + name match + parent-set match**, with the same accept / reject / ambiguous decision shape DesignBrain uses. Vector / embedding signals are explicitly out of scope for v1.
- R3. The drift score for color is OKLCH-space Euclidean distance to the nearest token; for dimension, integer-px distance. Recommendations rank by drift × usage count (a hex used 50× ranks above a one-off).

**Plugin surface**

- R4. The Figma plugin (`figma-plugin/`) gains an "Audit" command alongside its existing "Inject icons / components" commands. Audit can scope to **selection / current page / whole file**.
- R5. For each unbound fill / wrong-token-bound node the plugin shows: current value, closest DS token (path + alias + preview swatch), and a click-to-apply button. Apply uses Figma's `setBoundVariableForPaint` (or equivalent style write API) and tracks success / failure per click. One-click-undo is provided by Figma's native undo stack.
- R6. The plugin POSTs to `<ds-service>/v1/audit` with the node tree it's auditing. The base URL is the docs site URL by default; designer can override to `localhost:7474` for development. CORS is allowed for the docs origin in `figma-plugin/manifest.json`.

**Curated file manifest**

- R7. The list of files to audit lives in `lib/audit-files.json` — a checked-in array of `{file_key, name, brand, owner}`. The audit cmd reads this manifest, no other file-discovery mechanism in v1.

**Files tab UI**

- R8. The docs site gains a `/files` route, peer of `/`, `/components`, `/illustrations`, `/logos` in the top nav.
- R9. `/files` (index) lists every audited file as a tile: name, last-audited timestamp, overall token-coverage % (single number aggregated across screens), DS-component % across instances, and one "headline" drift hex.
- R10. `/files/<file-slug>` follows the **Foundations layout**: left sidebar listing every final-design page (one entry per `looksLikeScreen`-detected frame), main pane with the three audit lenses (component usage, token coverage, drift suggestions) anchored as sub-sections like `#screen-trade`, `#screen-watchlist`. Scroll-spy + URL hash sync work the same way as on the Foundations page.
- R11. Every audit panel shows a provenance line — `source: lib/audit/<file>.json · last sync: <timestamp>` — matching the Foundations / Spacing pattern.

**Final-page detection**

- R12. A page counts as a "final design page" when its frame name matches the heuristic ported from DesignBrain's `looksLikeScreen` — case-insensitive contains screen / page / view / modal / dialog / sheet. Operators can override per-file in the manifest with an explicit `final_pages: ["1234:5678", …]` array when names don't follow the convention.

**Left-nav cleanup (carried as part of this work)**

- R13. `/components`, `/illustrations`, `/logos` gain a working left sidebar matching the Foundations pattern: each top-level group (e.g., for `/components`: SECTION names — Buttons, Input Field, Toggles, …) becomes a left-nav entry that scroll-spies the corresponding sub-anchor. No new content; pure navigation polish.

**Provenance + honesty**

- R14. The audit JSON carries `$extensions.com.indmoney.{provenance, extractedAt, sweepRun, fileRev, designSystemRev}` so the docs UI can show *"audited against tokens at commit `abc123`, Figma file rev `XYZ`."* Stale-against-current-tokens audits are flagged amber.

**Living documentation (audit data flowing back into the docs)**

- R15. **Usage chips on Foundations + Components.** Each color swatch (`ColorSection`), text style (`TypographySection`), spacing/padding/radius row (`SpacingSection`), motion preset, and component tile (`/components`) gains a chip — `used in N places · M files` — sourced from the latest sweep. Zero-usage tokens render with reduced visual weight (50% opacity + a "0 uses" tag).
- R16. **Hover-to-explain popovers.** Hovering a usage chip shows the top 5 places that token / component is used (file → screen → node id), each row a deep-link that opens the docs Files tab for that screen and (where available) Figma directly via `https://figma.com/file/<key>?node-id=<id>`.
- R17. **Components page reordering.** `/components` sorts each SECTION's list by usage (most-used first) and stuffs zero-/single-use components into a collapsed "Rare or experimental" footer per section. The order is recomputed at build time from the audit roll-up; no manual curation.

**Guardrails (turning the audit into a forcing function)**

- R18. **PR-time impact preview for token changes.** When a PR modifies `lib/tokens/indmoney/*.tokens.json`, GitHub Actions runs `cmd/audit --diff` — re-audits the curated file list against the *proposed* tokens and compares against the *committed* tokens. The action posts a PR comment with: number of nodes whose binding would change, per-file impact counts, and a markdown table of the largest visual deltas.
- R19. **Coverage thresholds in CI.** A configurable per-brand threshold (default: token-coverage ≥ 90%, component-from-DS ≥ 70%) is enforced; PRs that drop coverage below the threshold post a status check: `audit/coverage failing — INDstocks V4 dropped from 94% → 82% on PR commit`. Threshold can be temporarily overridden with a labelled exception comment.
- R20. **Plugin staleness banner + drift alert.** When the plugin opens, it fetches `/icons/glyph/manifest.json` and `lib/audit/index.json` and compares against the timestamps it last saw locally. If the DS has changed since last visit *and* the active file has nodes the audit knows are affected, it shows a one-line banner with an `Audit now` shortcut.
- R21. **Cross-file pattern detection.** When the same custom node (matched by canonical hash, not just dimensions) appears in ≥ 2 audited files, the audit emits a `crossFilePatterns[]` entry on the index — surfaced in `/files` as a "Candidates for promotion to DS" tile. v1 detects only exact-canonical matches; near-duplicates are deferred.
- R22. **Deprecation propagation.** A token / component / variant marked `$extensions.com.indmoney.deprecated: true` in the published manifest causes the next audit to flag every consumer (with a P1 recommendation pointing to the replacement, when `replacedBy` is set). Plugin renders these flags with a "deprecated since `<DS rev>`" pill.

---

## Acceptance Examples

- AE1. **Covers R1, R3.** Given a Figma frame whose top-level rectangle has fill `#6B7280` not bound to any variable, when the sweep runs against the published INDmoney token set (which contains `surface.surface-grey-separator-dark = #6F7686`), the audit JSON entry for that node has `tokensAndVariables.unmappedProperties: ["fill"]` and `finalRecommendations: [{title: "Bind fill to surface.surface-grey-separator-dark", action: "bind-variable", priority: "P2", rationale: "OKLCH distance 0.011, used 3× in screen"}]`.

- AE2. **Covers R5, R6.** Given the designer selects an instance node with one unbound fill and clicks "Audit selection" in the plugin, when the plugin receives the audit response, the plugin shows one fix card with the closest token name and an "Apply" button. Clicking Apply causes the node's fill to be bound to that variable, and the audit re-runs and shows the node as resolved within 1 second.

- AE3. **Covers R10.** Given the docs site has audit JSON for `INDstocks V4` with three final-design pages — Trade, Watchlist, Login — when a user navigates to `/files/indstocks-v4`, the left sidebar lists exactly those three screens, scroll-spy correctly highlights the active one, and clicking each entry scrolls smoothly to that screen's panel triple (component usage, token coverage, drift suggestions).

- AE4. **Covers R12.** Given a Figma file whose final-page node names are `Trade Screen`, `Watchlist Page`, `Login Modal`, `WIP — sketches`, `Component playground`, when the sweep runs, the audit emits panels only for the first three; `WIP — sketches` and `Component playground` are skipped because they don't match the screen-keyword heuristic.

- AE5. **Covers R15, R17.** Given the audit shows `surface.surface-grey-bg` is bound 312 times across all audited files and `Card/glassy` is used 1 time in 1 file, when a designer opens `/` and `/components`, the grey-bg swatch shows a `312 uses · 4 files` chip at full weight, while `Card/glassy` renders at 50% opacity inside a "Rare or experimental" footer group of the Cards SECTION.

- AE6. **Covers R18, R19.** Given a PR proposes changing `surface.surface-grey-separator-dark` from `#6F7686` → `#727A8D`, when CI runs `cmd/audit --diff`, the PR comment lists 47 affected nodes across 3 files; the status check passes because token-coverage stayed ≥ 90% but flags one file (Trade screen, contrast ratio drops from 4.6 → 4.1 on muted text). If the change had taken any file below the 90% coverage gate, the status check would have failed.

- AE7. **Covers R21.** Given two custom Card nodes in INDstocks V4 (Trade screen) and INDmoney app (Wealth screen) share the same canonical hash, when the index roll-up runs, `lib/audit/index.json.crossFilePatterns` includes one entry `{hash: 'sha256:abcd', files: ['indstocks-v4', 'indmoney-app'], nodeCount: 2, suggestedName: 'Card-likeWealthSummary'}`. `/files` surfaces it as a "Promote to DS?" tile.

---

## Success Criteria

- **Designer outcome — friction.** A designer can fix one token drift in their current Figma selection in fewer than 10 seconds without leaving Figma or remembering the exact token name. Self-reported survey or pairing observation in the first two weeks post-launch.
- **Designer outcome — onboarding.** A new designer reaches first-correct-token-pick within 1 minute of opening the docs site, because usage chips guide them to the load-bearing options instead of forcing them to read the whole palette.
- **DS lead outcome — visibility.** The DS lead can name the three highest-drift screens across all audited files in under a minute by glancing at `/files`. No spreadsheet, no Slack thread.
- **DS owner outcome — change confidence.** A token-value PR carries an automatic impact comment within 5 minutes of opening; the DS owner can decide to merge / revise without anyone manually opening Figma to check.
- **Documentation honesty.** Every Foundations + Components surface shows production usage. Zero-usage tokens are visually de-emphasized; designers stop being able to "accidentally pick" stale options.
- **Engineering outcome — performance.** Sweep on the curated 3-file manifest runs in CI under 4 minutes wall-time. Plugin `/v1/audit` round-trip on a single screen returns under 3 seconds at P50.
- **Guardrail outcome.** Coverage thresholds gate PRs; deprecated tokens propagate flags within one audit cycle; staleness banner appears in the plugin within one DS-publish cycle.
- **Handoff outcome.** `/ce:plan` reads this doc and produces a phased plan without inventing audit lens definitions, file-list source, plugin behavior, runtime model, UI layout, the documentation-enrichment shape, or the guardrail policies. Anything it does invent is in the explicit `Deferred to Planning` list below.

---

## Scope Boundaries

- **No vector / embedding signals in v1.** MatchingService runs on componentKey + name + style + color only. We can layer embedding similarity later when we have an embedding host. Cross-file pattern detection (R21) uses *exact* canonical hashing only; near-duplicate clustering is deferred.
- **No multi-tenant DesignBrain reuse.** Single-tenant; INDmoney brand only. No MongoDB; audit JSON is checked-in like the icons manifest.
- **No live cron / webhook triggers from Figma.** Sweep is operator-run (CLI) or CI-scheduled. No `repository_dispatch` button on the docs site in v1.
- **No batch-edit "fix all 47 issues across 3 files" UI.** All edits happen one node at a time in the plugin. Files tab is read-only in v1; the only write surface is the plugin's per-node click-to-apply.
- **No accessibility audit (contrast / focus order).** DesignBrain has it; we skip for v1 to keep the audit lens count at three. R18's "contrast threshold" check is a *lightweight ratio gate at PR-diff time*, not a full WCAG audit.
- **No microcopy / tone analysis.** Out of scope; LLM dependency.
- **No drift trend graphs or week-over-week sparklines.** Files tab shows *current state* only. Trend is a v1.1+ feature once we have multiple audit runs landed.
- **No designer-facing file-add UI.** Operators edit `lib/audit-files.json` and commit. v1.1+ adds an admin route.
- **Usage chips do not deep-link into Figma in v1** when the file_key isn't audited or the node id isn't reachable; they degrade gracefully to the docs-site Files tab. Direct Figma deep-linking lands when the operator opts a file in.
- **No "auto-promote to DS" flow.** Cross-file pattern detection (R21) surfaces *candidates* for promotion. Promoting them is a manual designer-led decision; the docs site doesn't write back to Figma.
- **No designer-time API rate-limit budgeting.** v1 assumes Figma API budget headroom for ad-hoc plugin calls. If that breaks, plan-time work introduces a per-designer cache.

---

## Key Decisions

- **Curated `lib/audit-files.json` manifest** is the file-list source. Cheapest, mirrors the existing `lib/tokens/indmoney/` pattern.
- **All three audit lenses (component / token / drift) ship in v1, basic depth.** Token-only would punt the loudest question ("is this from the DS or not"). Adding accessibility was rejected as scope creep.
- **Three delivery surfaces, not two.** Plugin (live designer tool) + Files tab (governance) + Living docs enrichment (usage chips on Foundations + Components). The third surface materially raises baseline value for designers who never open the Files tab.
- **One audit core, three readers.** Go core in `services/ds-service/internal/audit/`. cmd/audit writes per-file + index JSON; ds-service `/v1/audit` exposes the same logic for the plugin; docs build reads the index roll-up for usage chips.
- **Plugin is verdict + click-to-apply, not read-only.** Verdict-only keeps friction at "designer still has to remember the token name." Click-to-apply is the friction-killer that justifies the plugin existing.
- **Final-page detection = `looksLikeScreen` ported from DesignBrain.** Manifest-level override allowed when names break convention.
- **Files tab follows the Foundations layout pattern.** Left nav lists final-design pages; main pane has the three lenses anchored as sub-sections.
- **Left-nav cleanup on `/components`, `/illustrations`, `/logos` rides along.** User flagged it; it's the same pattern; it's cheap once Foundations is the shared template.
- **Guardrails enter via PR-comment diff and coverage thresholds, not via webhooks.** Lower operational surface area; consistent with the manual-sync philosophy that's already in place.
- **Cross-file pattern detection uses canonical hashing (port DesignBrain's), exact match only.** Near-duplicate clustering is deferred. We surface promotion *candidates*; promotion itself stays manual.
- **Documentation reflects production usage, not aspiration.** Zero-usage tokens render at 50% opacity. This single rule converts the docs site from a "what we hope is used" reference into a "what is used" record.

---

## Dependencies / Assumptions

- **Curated file list.** Operator must commit `lib/audit-files.json` with at least one Figma file_key + name before the audit pipeline produces any output.
- **ds-service must be reachable from the plugin.** Either hosted (Cloudflare tunnel / Fly / Vercel function) or run locally per designer. Hosting decision happens in `/ce:plan`.
- **Figma write APIs require the right plan tier.** `setBoundVariableForPaint` is on Pro+; we assume the team is on Pro+ since the Variables API is required for click-to-apply. If a designer is on Free, the plugin gracefully degrades to verdict-only.
- **The published token set is the comparison baseline.** `lib/tokens/indmoney/*.tokens.json` is treated as ground truth. If a designer claims drift is intentional, it's a token-set-update conversation, not an audit-of-the-audit conversation.
- **DesignBrain seed remains a one-time port.** No upstream sync. We copy `looksLikeScreen`, the matching-decision shape, and the `FinalNodeUnderstanding` schema; we own them after the port.

---

## Outstanding Questions

### Resolve Before Planning

_(none — all product decisions are locked)_

### Deferred to Planning

- [Affects R6][Technical] **ds-service hosting model.** Run locally per designer (each laptop runs the Go binary, plugin hits `localhost:7474`), or host one shared instance? Local is simpler but every designer needs Go installed; shared adds tunnel + secret management.
- [Affects R7][Technical] **Initial file list.** The first three INDmoney Figma file keys + final-page conventions. Operator can add to `lib/audit-files.json` before R8 lands.
- [Affects R5][Needs research] **Figma write API behavior across plan tiers.** Confirm `setBoundVariableForPaint` works as advertised on the team's plan; identify graceful fallback.
- [Affects R10][Technical] **Schema-versioning for audit JSON.** When we evolve `AuditResult`, what stops Files tab + plugin from desyncing? Likely a `schema_version` field + tolerant readers, but the exact policy is a planning decision.
- [Affects R3][Needs research] **Color-distance threshold for "drift" vs "intentional."** OKLCH 0.02 is a common cutoff; the right number for INDmoney's palette needs one A/B against existing files before locking.
- [Affects R13][Technical] **Whether `/components`, `/illustrations`, `/logos` get a `DocsShell`-style chrome (sidebar + scroll-spy)** or a lighter `PageShell` variant. Foundations is heavy; gallery pages might want lighter.
- [Affects R15, R16][Technical] **How usage chips degrade when audit data is stale or missing.** Spec says "0 uses" → 50% opacity. What about "audit ran but file isn't on the manifest" — render the chip as `?` or hide it? Plan-level UX decision once the chip component is concrete.
- [Affects R18][Technical] **GitHub Action mechanism for `cmd/audit --diff`.** Path-filter on the workflow + a single job that re-audits, or a separate workflow that posts via `actions/github-script`? Latter is cleaner but more YAML.
- [Affects R21][Needs research] **Canonical hash collision rate at the granularity we hash.** DesignBrain's hash strips volatile fields; we should sample a 200-node test before locking the algorithm, since false positives (two genuinely-different cards hashing the same) would surface bad promotion candidates.
- [Affects R22][Technical] **Where deprecation metadata lives.** On individual token entries in the JSON, on a sidecar `lib/tokens/indmoney/deprecations.json`, or via Figma variable description fields? First option keeps it close to the token; sidecar is easier to grep / diff.
- [Affects R20][Technical] **Plugin's local cache for staleness comparison.** `clientStorage.getAsync` keyed on file_key + DS rev hash. Trivial but worth being explicit at plan time so the cache key collides correctly across plugin restarts.

---

## Next Steps

→ `/ce:plan` for phased implementation. Recommended phasing for the planner to consider (not prescriptive):

1. **Phase A — Audit core in Go.** Port `looksLikeScreen`, build `internal/audit/`, ship `cmd/audit` with the curated manifest. Output: `lib/audit/*.json` (per-file) + `lib/audit/index.json` (roll-up + crossFilePatterns).
2. **Phase B — Files tab UI.** New `/files` + `/files/<slug>` routes; `DocsShell`-style chrome; reads committed JSON. Includes the read-only "Promote to DS?" tile from R21.
3. **Phase C — Left-nav cleanup on /components, /illustrations, /logos.** Same `DocsShell`-style chrome. Pure navigation polish.
4. **Phase D — Living docs (audit → Foundations + Components).** Wire usage chips, hover popovers, zero-usage de-emphasis, Components page reordering. Reads `lib/audit/index.json` at build time.
5. **Phase E — Plugin audit command + click-to-apply.** Plugin posts to `/v1/audit`; renders fix cards; uses Figma write APIs. Includes staleness banner from R20.
6. **Phase F — Guardrails (CI + PR comments).** `cmd/audit --diff` flow, GitHub Action for token-PR diff comments, coverage threshold status check. Deprecation propagation.
7. **Phase G — Polish + thresholds.** Drift-threshold tuning per token type, plugin error states, ds-service hosting decision (local vs hosted), schema-versioning policy.

**Sequencing notes for the planner:**

- Phases A and B are sequential (UI needs the JSON shape locked). Phase A unblocks D, E, F in parallel after the schema is stable.
- Phase C is independent and can ship anytime after Foundations is the agreed chrome template.
- Phase D depends on A but is independent of B / C / E — can ship before the Files tab if shipping designer-visible value faster matters.
- Phase E (plugin) and Phase F (CI guardrails) can ship in either order, but both depend on A.
- Phase G is incremental polish — distribute across the other phases rather than batching.

**Recommended ship order for designer-perceived value:**
A → D (living docs land first; designers feel the change on the docs site they already visit) → E (plugin removes friction at moment of choice) → B (governance view) → F (guardrails) → C (nav polish) → G (tuning).
