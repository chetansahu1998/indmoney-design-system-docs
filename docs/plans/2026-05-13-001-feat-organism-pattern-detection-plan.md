---
title: "Organism Pattern Detection — surface hand-built DS-component lookalikes + recommend promotions"
type: feat
status: active
created: 2026-05-13
date: 2026-05-13
---

# Organism Pattern Detection

> **One-line frame.** Designers across product files rebuild published DS organisms (List, Card, Position Card) by hand from atom-level INSTANCEs instead of using the published organism INSTANCE. Figma's `componentId` graph captures the atom relationships but loses the organism relationship entirely. This plan makes the missing link first-class: detect each hand-built organism, classify against the published variant set, surface the verdict in the designer's authoring loop, expose adoption + drift to the DS team, and recommend brand-new components when an unmatched pattern recurs across files.

## Overview

End-of-2026-05-12 status: atom-level composition is already tracked. `cmd/variants` writes per-component `composition_refs[]` into `public/icons/glyph/manifest.json`; the Phase 7 U6 graph indexer (`services/ds-service/internal/projects/graph_sources.go:511-621`) reads those and emits `uses` edges into `graph_index.edges_uses_json`. The mind-graph view renders molecule → atom dependencies correctly.

What's broken is the layer above atoms. Empirical evidence from a 14-node Figma probe across 6 files (Tax Centre, Equity Tracking, Savings Account, Dashboard v5, INDstocks V4/V5, US Stocks v2, Glyph DS file):

- **11 wild FRAMEs** structurally match published organism components like `List 343`, `List 311`, `List on Surface`, `List on Card`.
- **0 of those 11 are real INSTANCEs** of the published organisms (every wild outer FRAME has `componentId == ""`).
- **Inside every wild FRAME, the atom INSTANCEs ARE real** (`Left Icon/Default = 229:4715`, `Right Text = 228:5960`, `Right Icon = 228:6123`, `Separators = 1:9988`, etc.).
- **`wild-sav` uses 9 published atoms including `Overline`, `Subtext`, and `Badges`** — an organism composition richer than any published `List on Surface` variant.

This is the canonical "rebuild from atoms" anti-pattern. Designers want the result that the published `List on Surface` would produce but reach into atoms because they need an override (extra slot, custom copy, missing variant) the published component doesn't expose. The relationship is lost forever.

**This plan ships four parts:**

| Part | What | Touches |
|---|---|---|
| **A — Organism fingerprinting** | Pipeline stage that walks every screen's canonical_tree, fingerprints organism-shaped FRAMEs, matches against published organism signatures derived from the manifest, persists verdicts | `services/ds-service/internal/projects/pipeline.go`, new migration `0024_detected_organism_match`, new module `pipeline_organism_match.go`, manifest reader extension, `repository.go` |
| **B — Plugin nudge UI** | Plugin menu command "Check selection against DS" calls a new ds-service endpoint that returns the verdict for the selected node id; renders "looks like X variant; replace with INSTANCE?" inside the plugin UI | `figma-plugin/code.ts`, `figma-plugin/ui.html`, `internal/projects/server.go` (new endpoint) |
| **C — Adoption + drift dashboard** | New atlas admin tab. Per-organism table: counts of `instance / exact / near / novel / drift`. Drill-down: per-frame diff descriptors, cross-file aggregation | `app/atlas/admin/_lib/`, ds-service aggregation endpoints |
| **D — Promote-to-component recommendations** | Cluster the *unmatched* corner of the fingerprint corpus across versions; rank by frequency × stability × atom-reuse rate; surface ranked candidate-component list for DS-team review | extends Part A's storage, new clustering pass, dashboard surface in Part C |

Each part is independently shippable. **A unblocks B, C, and D.** B/C/D are siblings and can land in any order once A is in.

**Why these four together:**
- A alone has no consumer surface — the fingerprint corpus would sit in the DB unread.
- B alone (plugin without dashboard) gives one designer at a time a verdict but no organizational view.
- C alone (dashboard without plugin) shows the gap but provides no in-context closing affordance.
- D piggybacks on A's already-computed fingerprints for unmatched frames — it costs almost nothing to ship if A is built right.

---

## Problem Frame

### Today's failure mode (concrete)

A designer working in the Tax Centre file needs a "stock holding row" with an Overline (RELIANCE) above the Heading (Reliance Industries Ltd.) and a green right-text amount (+₹1,14,816). The published `List on Surface (Left Icon=Yes, Right Icon=Yes, Right Text=Yes)` variant supports all three slots but **the right-text color override** isn't a published prop. So the designer:

1. Drops a FRAME named "List/Full width" (close to the published name).
2. Drags individual atom INSTANCEs into it: `Left Icon/Default`, `Right Text` (with color override), `Right Icon`, `Separators`.
3. Hand-arranges the autolayout.
4. Ships the screen.

Result: Figma sees five INSTANCEs of valid DS atoms inside a vanilla FRAME. **The "this is supposed to be a `List on Surface`" intent vanishes.** Repeat 47 times across 6 product files.

### What this costs

| Cost | Today |
|---|---|
| **Drift risk** | wild-tax-1's right-text is green; published default is gray. No audit catches it because the wrapper isn't classified as `List on Surface`. |
| **Adoption blindness** | DS team has no signal that `List on Surface` is at 0% adoption. The team's perception ("our DS is healthy") diverges from reality. |
| **Variant pressure invisible** | 5 files independently composed `List on Surface + Overline + Badges`. None of them ever filed a request. The DS team has no idea this variant is needed. |
| **Atlas render cost** | 47 frames each become 47 unique canonical_trees, 47 Stage-9 prerenders, 47 asset-streams. With organism detection, the canonical render of one organism could serve all 47. |
| **Cross-file consistency** | The same conceptual list row in Tax Centre, Dashboard, and Savings looks subtly different because each was hand-rebuilt. No mechanism makes them converge. |

### What "fixed" looks like

1. **Pipeline-time**: every screen import emits one `detected_organism_match` row per organism-shaped FRAME, with verdict + variant inference + diff descriptor.
2. **Plugin-time**: designer selects a FRAME, runs "Check against DS", sees "Matches `List on Surface (LI+RI+RT)`. [Replace with INSTANCE] [Mark as intentional fork] [Why?]".
3. **DS-team-time**: a dashboard shows per-organism adoption + drift + ranked candidate-new-components.
4. **Sustaining**: the existing `flow_graph` / `theme_parity` / `a11y_contrast` rules gain a sibling "organism_adherence" rule that fires on Stage 7 audits.

The verdicts are read-only — designers stay sovereign. The system surfaces information; it doesn't auto-rewrite frames.

---

## Requirements Trace

- R1. For every imported `project_version`, every FRAME in every screen's canonical_tree that contains ≥2 INSTANCEs of published atoms is fingerprinted and classified against the published organism set (Part A).
- R2. Each fingerprint produces one `detected_organism_match` row keyed by `(version_id, screen_id, frame_id)` with: `suspected_component_slug`, `match_kind ∈ {exact, near, novel, unrelated}`, `atom_signature` (canonical hash), `diff_json` (per-slot delta vs. closest variant), `confidence` (0.0–1.0).
- R3. Re-running the pipeline on a refreshed manifest must update verdicts (idempotent — same canonical_tree + same manifest → same verdicts; deterministic).
- R4. The Figma plugin can ask ds-service for the latest verdict on a given Figma node id and render an actionable verdict card (Part B).
- R5. The atlas admin surface exposes a per-organism adoption + drift dashboard with drill-down to specific frames (Part C).
- R6. Frames that match no published organism but recur K≥3 times across N≥2 files with stable fingerprints are surfaced as promotion candidates ranked by frequency × structural stability × atom-reuse (Part D).
- R7. Detection ignores copy-only and locale-only diffs (₹ vs $, "Total" vs "P&L" label text, ticker name) — these are *content* not *structure*. Detection fires only when atom-set or slot-arrangement differ.
- R8. The verdict storage is tenant-scoped and tenant-isolated. No cross-tenant clustering surfaces patterns from another tenant's files (privacy default).
- R9. Detection runs once per `project_version` import, not per render. Read paths (plugin endpoint, admin queries) are cache-only / DB-only — no recomputation at request time.
- R10. The implementation uses no new heavy dependencies — pure Go on the server side, no embedded ML, no new vendored libraries beyond `modernc.org/sqlite` + existing stdlib.

---

## Scope Boundaries

- **Detection only — no auto-rewrites.** The plugin can offer "Replace with INSTANCE" as a designer-initiated action, but the system never mutates a Figma file without explicit click-through.
- **Organism layer only.** Atom-level detection is already shipped via the manifest + `graph_index`. This plan does not touch the atom layer.
- **In-tenant only.** Part D's clustering runs within a single tenant's projects. Multi-tenant promotion ("X% of org-tenants independently built this pattern") is out of scope — see "Outside this product's identity" below.
- **No live render reuse yet.** Even after detection, Stage 9 still renders each frame's canonical_tree independently. Reusing one cached render across 47 detected matches (Atlas grouping mentioned in the value list above) is a follow-up — the detection corpus is the prerequisite, not the consumer.
- **Existing manifest format unchanged.** Organism signatures are *derived* from the manifest at read time; we do not extend the manifest schema, and we do not change `cmd/variants`.
- **English-locale-aware naming heuristics only.** Slot inference uses Figma node names (`Left Text`, `Right Icon`, `Overline`). If a tenant introduces a localized manifest in the future, naming heuristics will need a locale layer — out of scope here.

### Deferred to Follow-Up Work

- **Atlas render reuse**: once Part A's corpus is stable, a follow-up plan can teach `LeafFrameRenderer` to look up a detected match's `canonical_render_key` and skip per-frame rendering. Separate PR.
- **Cross-tenant pattern aggregation**: an org-wide DS-team view that finds patterns recurring across N+ tenants. Requires explicit tenant opt-in + a new privacy contract. Separate plan.
- **A new audit rule `organism_adherence`**: builds on Part A's stored verdicts and emits Phase-2-shaped `Violation` rows ("frame X is a near-match for `List on Surface` — propose replacement"). Separate U-spec in a future audit-rules sprint.
- **Figma plugin client-side mutation flow for "Replace with INSTANCE"**: U9 in this plan defines the action surface; the actual Figma `figma.createInstance(...)` + slot-override mapping logic lands in a follow-up plugin PR after Part B's verdict surface is shipping value.

### Outside this product's identity

- **An automated component-promotion robot.** Part D ranks candidates; humans on the DS team still decide what to publish. The system does not commit to the Glyph DS file.
- **A general-purpose Figma diff tool.** We diff hand-built organisms against their *intended* DS counterparts. We do not diff arbitrary frames against each other.

---

## Context & Research

### Relevant Code and Patterns

**Pipeline stages (where Part A inserts itself):**
- `services/ds-service/internal/projects/pipeline.go` — orchestrates Stages 2-9 per import. Stage 6 commits screens + canonical_trees; Stage 6.5 spawns the cluster prerender; Stage 7 runs the audit rule registry (`flow_graph`, `theme_parity`, `a11y_contrast`, `a11y_touch_target`, `cross_persona`, `component_governance`). Organism detection inserts at **Stage 6.7** — after canonical_trees are durable, before audit-time consumers can read verdicts.
- `services/ds-service/internal/projects/pipeline_cluster_prerender.go::ExtractClusterIDs` — the closest existing pattern. Walks the canonical_tree, classifies nodes, dedupes ids. Organism fingerprinting will mirror this shape: one walker function, one match function, batch insert.

**Manifest reader (where organism signatures come from):**
- `services/ds-service/internal/projects/graph_sources.go:500-621` — `componentManifest` Go shape and `BuildComponentRows` reader. We extend the manifest parser to produce **organism signatures**: for each `kind="component"` entry that has `composition_refs[]` with `atom_slug` values, emit an `OrganismSignature{ Slug, AtomSlugs[], VariantProps[] }` keyed by the entry's slug.
- The manifest's `composition_refs[]` already contains the atom-slug list per organism — that IS the organism signature, just re-shaped for fingerprint comparison.

**TS-side classifier reference (for parity assumptions):**
- `app/atlas/_lib/leafcanvas-v2/node-classifier.ts::classifyNode` — establishes the TS contract for which node names map to which kind. The Go-side organism walker will mirror the LAYOUT_NAME_HINTS check + the ICON_NAME_RE / ILLUSTRATION_NAME_RE / chart-name patterns so we don't misclassify in opposite directions.

**Migration pattern:**
- `services/ds-service/migrations/0023_figma_render_blocklist.up.sql` is the most recent and most similar pattern: tenant-scoped, indexed by `(tenant_id, file_id, node_id)`, with `STRICT` mode. Migration `0024_detected_organism_match.up.sql` follows the same shape.

**Admin tab pattern:**
- `app/atlas/admin/figma-blocklist/page.tsx` is the closest sibling — same admin shell, table + filter + drill-in pattern. Part C's organism dashboard reuses the layout, swaps the columns + drill-in detail card.

**Plugin command pattern:**
- `figma-plugin/code.ts` already handles `personas.fetch`, `projects.list-existing`, `auditSelection`. Adding `organism.check-selection` follows the same: code-side message handler that posts to ds-service with the Figma node id, ui.html-side panel that renders the response.

### Institutional Learnings

- **Go ↔ TS classifier parity is load-bearing.** The 2026-05-13 "Vector pollution" bug (commit `9cb26f7`) was a TS-side state-picker false-positive on auto-named co-positioned siblings. Lesson: any new classifier on the Go side must mirror TS's name-aware filters (the `LAYOUT_NAME_HINTS` + `FIGMA_DEFAULT_NAME_RE` patterns from `visible-filter.ts`) or risk pipe-side false matches.
- **Manifest may lag.** `public/icons/glyph/manifest.json` is rebuilt by `cmd/variants` on cadence. Detection on a project_version whose canonical_tree was extracted *after* a recent manifest publish may match against newer organism signatures than were live at extract time. Idempotency (R3) means re-running on the next pipeline pass with the newer manifest must update verdicts — the storage row's `manifest_version` field exists for exactly this.
- **`composition_refs` table (DB) is dormant.** It was scaffolded in `internal/projects/types.go:184-194` for cross-version composition but never built. Part A's `detected_organism_match` table is a *different* table — not a replacement — because the dormant one's columns (`version_id`, `target_version_id`, `composition_type`) describe a relationship we don't compute. Document the distinction so future readers don't conflate them.

### External References

None needed. The detection is structural-pattern-matching over JSON trees with known schemas, classifiers we already maintain, and storage we already use. No new domain expertise required.

---

## Key Technical Decisions

- **Detection runs as Stage 6.7 in `pipeline.go`.** Reason: canonical_trees must be durable (Stage 6 commits them) before fingerprinting, but the audit-rule registry (Stage 7) is a natural downstream consumer of the corpus. Inserting between 6.5 (cluster prerender, async) and 7 (audit) is the right slot.
- **Verdicts are stored per `(version_id, screen_id, frame_id)`, not per frame globally.** Reason: re-imports produce new `project_version` ids; the corpus shouldn't conflate verdicts across versions of the same file. Cross-version aggregation happens at query time (Part C).
- **Fingerprint = canonical hash of `(atom_slug_set, slot_topology)`.** `atom_slug_set` is the lexicographically sorted set of atom slugs the frame's INSTANCE children resolve to. `slot_topology` is the bbox-ordered sequence of atom-slot positions (e.g. `LEFT_ICON | LEFT_TEXT | RIGHT_TEXT | RIGHT_ICON`). Hash uses SHA-256 truncated to 16 bytes (32 hex chars). Reason: deterministic, locale-invariant, copy-invariant, and short enough to index efficiently.
- **`match_kind` is bucketed by atom-set Jaccard similarity against published variant signatures.** `exact` = Jaccard 1.0 on atom-slug set AND slot-topology hash match. `near` = Jaccard ≥ 0.7 OR atom-set match with slot-topology drift. `novel` = ≥ 2 atom INSTANCEs but Jaccard < 0.5 against every published organism. `unrelated` = the FRAME has < 2 atom INSTANCEs (rejected from the corpus). Thresholds are stored in a config struct so tuning doesn't require migrations.
- **Part D's clustering is in-process, not a separate job.** After the per-version Stage 6.7 pass, an aggregation step queries unmatched + novel verdicts across all view_ready versions in the tenant, buckets by fingerprint hash, and writes one `promotion_candidate` row per cluster with `frequency`, `file_count`, `stability_score`, `atom_reuse_rate`. Runs ~once per pipeline-completion event; cheap because the hash join is a SQL group-by.
- **Plugin verdict endpoint is read-through cache only.** No recomputation at request time. If a Figma node id has no verdict row, the plugin renders "No verdict yet — has this file been imported?" with a link to trigger an import.
- **No new vendored dependencies.** SHA-256 from `crypto/sha256` (stdlib). Jaccard similarity in 6 lines. JSON diff in `internal/projects/organism_diff.go`. Total new Go code under 1500 lines.

---

## Open Questions

### Resolved During Planning

- **Threshold for near vs novel.** Resolved: Jaccard ≥ 0.7 → near; Jaccard < 0.5 → novel; in between → near (conservative default favors fewer "novel" classifications since novel feeds Part D recommendations). Thresholds live in a Go const struct, tunable without migration.
- **Nested organisms (a `List on Card` containing 3 `List on Surface` rows).** Resolved: walk depth-first; classify each candidate frame independently. A nested `List on Surface` inside a `List on Card` gets its own verdict row, not absorbed into the parent's. The parent's atom-set includes "INSTANCE of `List on Surface`" as a slot (when it IS an INSTANCE). When the parent's inner row is also hand-built, both verdicts exist; the dashboard shows the parent → child relationship via `parent_frame_id`.
- **Where the "Replace with INSTANCE" action runs.** Resolved: client-side in the plugin (deferred to follow-up — see Scope Boundaries). Part B's U9 ships only the *action surface* + telemetry; the actual mutation logic is a separate PR after we see designer engagement rates.
- **Tenant scope for Part D.** Resolved: tenant-scoped only. Cross-tenant deferred to a follow-up plan with an explicit opt-in privacy contract.
- **Manifest version coupling.** Resolved: each `detected_organism_match` row stores the manifest mtime hash at detection time. Stale-verdict warnings render in Part C when the current manifest hash drifts from the row's stored hash.
- **Idempotency of re-runs.** Resolved: row primary key is `(version_id, frame_id)`. Re-running on the same version is an UPSERT; same input → same output. Version-bumps create new rows (intentional — preserves per-version history for trend graphs).

### Deferred to Implementation

- **Exact bbox-tolerance for slot topology matching.** Currently planned as 1 px rounding (same as `visible-filter.ts::bboxKey`). May need 4 px or proportional tolerance for organisms that scale; pin down with the U3 test fixtures.
- **How to surface `unrelated` rows.** They take ~10 bytes each and are 80%+ of frames in a project. Decide at U1 implementation: store them as a count summary or full rows. Strong lean: store the count only, write full rows only for kinds in `{exact, near, novel}`.
- **Promotion candidate naming.** Part D suggests "this pattern looks like X uses + Y atoms" but doesn't auto-name the proposed component. UX decision at U14 — whether the dashboard shows a placeholder name like `proposed-organism-7a3f` or asks the reviewer to name it inline.
- **Whether the plugin polls or push-subscribes for verdict freshness.** Plugin runs on the designer's local machine; ds-service has SSE infrastructure already. Decide at U8 implementation based on whether import-during-edit is a real workflow.

---

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

### Detection pipeline (Part A)

```
Stage 6 commits screens + canonical_trees
        │
        ▼
Stage 6.5 ──► cluster prerender (existing, async)
        │
        ▼
Stage 6.7 ──► organism fingerprint pass (NEW)
        │       │
        │       ├─ load manifest → derive OrganismSignature[] (cached per pipeline run)
        │       ├─ for each screen: walk canonical_tree
        │       │     for each FRAME with ≥2 atom-INSTANCE descendants:
        │       │         build fingerprint (atom_slug_set + slot_topology hash)
        │       │         classify against signature catalog → kind + best-match + diff
        │       │         emit row
        │       ├─ aggregate unmatched + novel by fingerprint hash → promotion_candidate rows
        │       └─ UPSERT all rows in one transaction
        │
        ▼
Stage 7 runs the audit rule registry (existing — flow_graph, theme_parity, …)
        │       │
        │       └─ (future) organism_adherence rule reads Stage 6.7's rows → emits Violations
```

### Fingerprint shape (DSL sketch — directional)

```
OrganismFingerprint :=
    atom_set       : sorted_unique[atom_slug]
    slot_topology  : [
        {slot_kind: LEFT_ICON | LEFT_TEXT | RIGHT_TEXT | RIGHT_ICON | BADGE | SEPARATOR | …
        ,bbox_rank: int             // bbox-ordered position 0..N-1
        ,atom_slug: string          // resolved atom this slot holds
        }
    ]
    hash           : sha256_16(canonical_json(atom_set ++ slot_topology))
```

### Match classification (decision matrix)

| Has ≥2 atom INSTANCEs? | Jaccard vs best published variant | Slot-topology hash match? | match_kind |
|---|---|---|---|
| No | – | – | `unrelated` (count-only, not stored as full row) |
| Yes | 1.0 | yes | `exact` |
| Yes | 1.0 | no | `near` (atom set matches but ordering drifted) |
| Yes | 0.7–0.99 | – | `near` |
| Yes | 0.5–0.69 | – | `near` (with `confidence` field reflecting Jaccard) |
| Yes | < 0.5 | – | `novel` |

### Storage shape (sketch)

```sql
-- migration 0024
CREATE TABLE detected_organism_match (
    version_id              TEXT NOT NULL REFERENCES project_versions(id) ON DELETE CASCADE,
    screen_id               TEXT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
    frame_id                TEXT NOT NULL,                  -- canonical_tree frame node id
    tenant_id               TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    suspected_slug          TEXT,                            -- e.g. "list-on-surface", NULL when novel
    suspected_variant_key   TEXT,                            -- e.g. "li=yes,ri=yes,rt=yes"
    match_kind              TEXT NOT NULL CHECK (match_kind IN ('exact','near','novel')),
    fingerprint_hash        TEXT NOT NULL,                   -- sha256_16 hex
    atom_signature_json     TEXT NOT NULL,                   -- the sorted atom_slug set
    slot_topology_json      TEXT NOT NULL,                   -- the slot ordering
    diff_json               TEXT,                            -- per-slot delta vs best variant
    confidence              REAL NOT NULL,                   -- 0.0–1.0
    manifest_hash           TEXT NOT NULL,                   -- manifest mtime hash at detection
    parent_frame_id         TEXT,                            -- nested-organism parent
    detected_at             TEXT NOT NULL,
    PRIMARY KEY (version_id, frame_id)
) STRICT;
CREATE INDEX idx_detected_organism_tenant_kind ON detected_organism_match (tenant_id, match_kind);
CREATE INDEX idx_detected_organism_fingerprint ON detected_organism_match (tenant_id, fingerprint_hash) WHERE match_kind = 'novel';
CREATE INDEX idx_detected_organism_slug ON detected_organism_match (tenant_id, suspected_slug, match_kind);

CREATE TABLE promotion_candidate (
    tenant_id              TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    fingerprint_hash       TEXT NOT NULL,                    -- the cluster key
    frequency              INTEGER NOT NULL,                 -- how many frames in this cluster
    file_count             INTEGER NOT NULL,                 -- distinct Figma files
    stability_score        REAL NOT NULL,                    -- 1.0 - variance(slot_topologies)
    atom_reuse_rate        REAL NOT NULL,                    -- atom INSTANCEs / total descendants
    proposed_name          TEXT,                             -- nullable; reviewer-set
    first_seen             TEXT NOT NULL,
    last_seen              TEXT NOT NULL,
    PRIMARY KEY (tenant_id, fingerprint_hash)
) STRICT;
```

### Plugin verdict request flow (Part B)

```
designer selects FRAME → menu "Check selection against DS"
        │
        ▼
plugin code.ts: POST /v1/audit/organism-match
                Body: { node_id, file_id }
        │
        ▼
ds-service: tenant from JWT
        │
        ▼
SELECT * FROM detected_organism_match
WHERE tenant_id = ? AND frame_id = ?
ORDER BY detected_at DESC LIMIT 1
        │
        ▼
Response:
  - 200 + verdict (kind, suspected_slug, variant, diff_json, confidence)
  - 200 + null  (no import covers this frame yet)
        │
        ▼
plugin ui.html renders verdict card:
  - kind=exact     → "✓ Matches `List on Surface (LI=Yes, RI=Yes, RT=Yes)`"
  - kind=near      → "≈ Looks like `List on Surface (...)`. Diff: [render diff_json]. [Replace with INSTANCE] [Mark as intentional fork]"
  - kind=novel     → "✦ Novel composition (matches no published organism). Used in N frames. [View pattern]"
  - null           → "No verdict — import this file via the Plugin → Audit File menu first"
```

---

## Implementation Units

### Part A — Organism fingerprinting on import

- U1. **Migration 0024 — `detected_organism_match` + `promotion_candidate` tables**

**Goal:** Persist organism verdicts and promotion candidates per tenant. Schema follows the sketch under High-Level Technical Design.

**Requirements:** R2, R8

**Dependencies:** None

**Files:**
- Create: `services/ds-service/migrations/0024_detected_organism_match.up.sql`
- Create: `services/ds-service/migrations/0024_detected_organism_match.down.sql`
- Modify: `services/ds-service/migrations/embed.go` if `embed.go` requires manual registration (check existing pattern)
- Test: `services/ds-service/internal/projects/migrations_test.go` (or wherever migration-up tests live — mirror the 0023 pattern)

**Approach:**
- `STRICT` mode like 0023.
- Tenant-scoped via FK to `tenants(id) ON DELETE CASCADE`.
- Indexes on the three common read patterns: `(tenant_id, match_kind)` for dashboard counts, `(tenant_id, fingerprint_hash) WHERE kind='novel'` for promotion clustering, `(tenant_id, suspected_slug, match_kind)` for per-organism adoption queries.
- `frame_id` is the canonical_tree frame node id (Figma node id format `<page>:<element>` or `I<scope>;<base>`). Not FK'd to anything — canonical_tree frames are not in their own table.
- `parent_frame_id` is nullable for top-level matches.

**Patterns to follow:**
- `services/ds-service/migrations/0023_figma_render_blocklist.up.sql` (most recent + most similar schema shape).
- FK + CASCADE pattern from `0015_tenant_fk_constraints.no_tx.up.sql`.

**Test scenarios:**
- Happy path: applying 0024 from a clean DB creates both tables with all expected columns + indexes.
- Edge case: applying 0024 on a DB that already has rows in `project_versions` + `tenants` — FK constraints validate, no orphans created.
- Edge case: down migration drops both tables cleanly (and any indexes) without affecting other tables.
- Error path: insert with NULL `match_kind` fails the CHECK constraint.
- Error path: insert with `match_kind='bogus'` fails the CHECK constraint.

**Verification:**
- `sqlite3 ds.db ".schema detected_organism_match"` returns the expected structure.
- A test that inserts ~100 rows + queries by the three index patterns completes in O(ms).

---

- U2. **Manifest reader extension — `BuildOrganismSignatures`**

**Goal:** Parse the existing `public/icons/glyph/manifest.json` and derive an in-memory `OrganismSignature[]` catalog for fingerprint comparison. No manifest schema change.

**Requirements:** R1, R3

**Dependencies:** None (independent of U1)

**Files:**
- Modify: `services/ds-service/internal/projects/graph_sources.go` — add `OrganismSignature` struct and `BuildOrganismSignatures(manifestPath string) ([]OrganismSignature, ManifestHash, error)`
- Test: `services/ds-service/internal/projects/graph_sources_test.go` — extend with organism-signature cases

**Approach:**
- Reuse the existing `componentManifest` struct in `graph_sources.go:500`. Each manifest entry with `kind=="component"` and non-empty `composition_refs[]` becomes one `OrganismSignature`:
  - `Slug`: entry's `slug` (e.g., `list-on-surface`)
  - `AtomSlugs`: sorted unique `atom_slug` values from `composition_refs[]`
  - `VariantKeys`: derived from the entry's variants' names (if present) — e.g., `"li=yes,ri=yes,rt=yes"` parsed from variant naming convention
  - `ManifestHash`: sha256 of manifest contents (returned alongside the signature list for storage in U4 rows)
- Signatures are immutable per manifest version. Cache at pipeline-run start; reuse across all screens in the run.

**Patterns to follow:**
- `graph_sources.go::BuildComponentRows` for reader shape + error handling (`os.ErrNotExist` → empty result, not fatal).

**Test scenarios:**
- Happy path: a manifest with 3 `kind=component` entries (one with composition_refs, two without) yields 1 OrganismSignature.
- Edge case: empty manifest → empty signature list + non-empty hash.
- Edge case: manifest file missing → returns nil signatures, nil error (matches existing BuildComponentRows behavior).
- Edge case: manifest with malformed JSON → returns parse error.
- Edge case: an entry whose `composition_refs[]` has empty `atom_slug` values — those entries are skipped (unresolved refs aren't usable for fingerprinting).
- Happy path: ManifestHash is deterministic across runs on the same file.

**Verification:**
- Running against the real `public/icons/glyph/manifest.json` produces at least one `OrganismSignature` for `list-on-surface` containing atoms in the expected set (`left-icon-default`, `right-text`, `right-icon`, etc.).

---

- U3. **Fingerprint walker — `WalkOrganismCandidates` + signature derivation**

**Goal:** Walk a canonical_tree and emit one `OrganismFingerprint` per FRAME that contains ≥2 atom INSTANCEs. Pure function; no DB.

**Requirements:** R1, R7

**Dependencies:** U2 (uses `OrganismSignature` for atom-slug resolution)

**Files:**
- Create: `services/ds-service/internal/projects/pipeline_organism_match.go`
- Test: `services/ds-service/internal/projects/pipeline_organism_match_test.go`
- Test fixture: `services/ds-service/internal/projects/testdata/organism_fixtures/` — canonical_tree JSON snippets mirroring the 11 wild + 3 DS examples from the session probe

**Approach:**
- Walk the canonical_tree depth-first. At each FRAME with `Array(children).length >= 2`, count direct + descendant INSTANCEs that have non-empty `componentId`. If count ≥ 2, emit a candidate.
- For each candidate, build `OrganismFingerprint`:
  - `atom_set`: sorted unique `componentId → atom_slug` resolution (atom_slug comes from a reverse map built off the manifest, U2).
  - `slot_topology`: traverse direct children in bbox-rank order (top-to-bottom, left-to-right when ties), tag each child by its inferred slot kind from name pattern (`LEFT_ICON`, `RIGHT_TEXT`, etc. — same name patterns as TS-side classifier).
  - `hash`: sha256 of canonical JSON `(atom_set, slot_topology)` truncated to 16 bytes.
- Skip the screen-root FRAME (it's always a phone screen, never an organism). Mirror `ExtractClusterIDs`'s screen-root skip.
- Skip flatten-able wrappers (GROUP / BOOLEAN_OPERATION) — descend through them just like `walkClusters`.
- Track `parent_frame_id` for nested organisms — when a candidate is found inside another candidate's subtree, the inner one gets `parent_frame_id = outer.frame_id`.

**Execution note:** Add the 11 wild + 3 DS canonical fixtures from the session probe (under `services/ds-service/internal/projects/testdata/organism_fixtures/`) before writing the walker. Drive walker implementation off the fixtures' expected outputs (test-first for this unit because the heuristic surface is wide).

**Technical design:** *(directional — not implementation spec)*

```
walkOrganismCandidates(node, parentChain, manifest, out []OrganismFingerprint):
    if node.type == "FRAME" and not isScreenRoot(node):
        atomInsts = collectInstancesWithComponentId(node, depth=∞)
        if len(atomInsts) >= 2:
            fp = OrganismFingerprint{
                atom_set: sortedSet(atom_slug(i.componentId) for i in atomInsts),
                slot_topology: orderByBBoxRank(node.children, tag=inferSlotKind),
                hash: sha256_16(canonicalJSON(atom_set, slot_topology)),
                frame_id: node.id,
                parent_frame_id: parentChain.lastFingerprint?.frame_id,
            }
            out = append(out, fp)
            parentChain = parentChain.push(fp)
    for child in node.children:
        walkOrganismCandidates(child, parentChain, manifest, out)
```

**Patterns to follow:**
- `pipeline_cluster_prerender.go::ExtractClusterIDs` for the walker shape + screen-root skip.
- `app/atlas/_lib/leafcanvas-v2/node-classifier.ts` for slot-kind name patterns (`Left Icon`, `Right Text`, `Overline`, etc.). Mirror as `slotKindFromName` in Go.
- `app/atlas/_lib/leafcanvas-v2/visible-filter.ts::bboxKey` for bbox rounding (1 px) so slot ordering is deterministic.

**Test scenarios:**
- Covers AE1. Happy path: `wild-tax-1` fixture (Reliance/List_Full-Width) → 1 candidate with atom_set `{left-icon-default, right-text, right-icon}` + 3-slot topology.
- Happy path: `wild-sav` fixture → 1 candidate with 9-atom atom_set including `overline`, `subtext`, `badges`.
- Edge case: a screen-root FRAME with 5 atom INSTANCEs as children is NOT emitted (screen-root skip).
- Edge case: a FRAME with 1 atom INSTANCE + 5 TEXT children is NOT emitted (atom count below threshold).
- Edge case: nested case — a `List on Card` containing 3 hand-built `List on Surface` rows yields 4 fingerprints; the 3 inner rows have `parent_frame_id` set to the outer card's id.
- Edge case: a FRAME with 2 INSTANCEs whose `componentId` doesn't resolve in the manifest → emitted with `atom_set` containing the raw componentId (not the slug). Surfaces as `novel` downstream because no published signature will match.
- Edge case: deterministic hash — running the same fixture twice yields identical `hash` values.
- Edge case: copy-only diff — same atom_set + same slot_topology with different TEXT character content yield IDENTICAL fingerprint hashes (R7 invariance).

**Verification:**
- Running the walker against the 14 session-probe canonicals yields exactly the expected fingerprint counts: 11 wild candidates + 0 from DS (DS COMPONENT_SETs are tagged COMPONENT_SET not FRAME and are skipped).
- A test that runs the walker 100× on the same input produces identical hash sequences.

---

- U4. **Match classifier — `ClassifyFingerprint`**

**Goal:** Given an `OrganismFingerprint` + the `OrganismSignature[]` catalog, return `MatchKind`, `SuspectedSlug`, `VariantKey`, `Diff`, `Confidence`. Pure function.

**Requirements:** R2, R7

**Dependencies:** U2, U3

**Files:**
- Modify: `services/ds-service/internal/projects/pipeline_organism_match.go` — add `ClassifyFingerprint(fp OrganismFingerprint, sigs []OrganismSignature) MatchVerdict`
- Test: `services/ds-service/internal/projects/pipeline_organism_match_test.go` — extend

**Approach:**
- For each signature, compute Jaccard(`fp.atom_set`, `sig.AtomSlugs`).
- Pick the signature with the highest Jaccard (ties broken by lexicographically-smaller slug).
- If best Jaccard == 1.0:
  - if `fp.slot_topology` hash matches one of the published variant topologies for that slug → `exact`
  - else → `near` (atom set perfect, ordering drift)
- If best Jaccard in [0.7, 1.0) → `near`
- If best Jaccard in [0.5, 0.7) → `near` with lower confidence
- If best Jaccard < 0.5 → `novel`
- `Diff`: a `[]SlotDelta` describing which slots are in fp.atom_set but not sig.AtomSlugs (added) and vice versa (missing). Render as JSON.
- `Confidence`: max(Jaccard, 0.5 * Jaccard + 0.5 * topology_match_score) — favors topology agreement.
- Thresholds live in a top-level Go const struct so retuning is one-line.

**Patterns to follow:**
- Stage-9 classifier dispatch pattern in `pipeline_cluster_prerender.go::isCluster` for "many-condition single-source-of-truth" function.

**Test scenarios:**
- Happy path: fingerprint with atom_set perfectly matching `list-on-surface` + matching slot topology → `exact`, suspected_slug=`list-on-surface`, confidence=1.0, diff=[].
- Happy path: same atom_set but slot order swapped → `near`, diff describes the order swap.
- Edge case: fingerprint atom_set is superset of `list-on-surface` atoms (adds `overline`, `badges`) → `near`, diff lists the additions.
- Edge case: fingerprint atom_set has 1 atom in common with `list-on-surface` (jaccard ~0.2) → `novel`.
- Edge case: empty signature catalog → every fingerprint classifies as `novel`.
- Edge case: signature catalog with two organisms (`list-on-surface`, `list-on-card`) and a fingerprint with Jaccard 0.8 to one + 0.4 to the other → picks the 0.8 match.
- Edge case: tie-breaking — two signatures with identical Jaccard → lex-smallest slug wins, deterministic across runs.

**Verification:**
- The 11 session-probe wild fingerprints classify as expected:
  - tax-1/2/3 → exact (or near depending on right-text color override surfacing as diff)
  - sav → near (composes `Badges` + `Overline` beyond published variant)
  - dash-1..5 → mixture of near + exact
  - eq-1/2 → near (smaller atom set than full `list-on-surface`)
- The 6 portfolio-position-card fingerprints from the prior probe classify as `novel` (no published organism for that shape — Part D will pick them up).

---

- U5. **Pipeline Stage 6.7 integration**

**Goal:** Run U3 + U4 once per project_version inside `pipeline.go`. UPSERT verdicts via U6's repo methods. Stage emits a progress event consumable by the existing progress emitter.

**Requirements:** R1, R3, R9

**Dependencies:** U1, U2, U3, U4, U6

**Files:**
- Modify: `services/ds-service/internal/projects/pipeline.go` — insert Stage 6.7 between Stage 6.5 (cluster prerender spawn) and Stage 7 (audit registry)
- Test: `services/ds-service/internal/projects/pipeline_test.go` — extend with a Stage 6.7 fixture

**Approach:**
- After Stage 6 commits, but before audit rules run, load `screen_canonical_trees_zstd` for every screen in the version.
- For each screen tree: decompress, run U3 walker, run U4 classifier on each candidate.
- Batch UPSERT all rows in one transaction (typical project has 5–500 candidates per version; well within a single tx).
- Emit progress events: `stage_started: organism_match`, `stage_progress: N/Total`, `stage_completed: organism_match{matched, near, novel}`.
- On any error, log + continue. Detection failure should not fail the pipeline (R9).
- Trigger Part D's `RebuildPromotionCandidates(tenant_id)` at the end of Stage 6.7 (U13 covers the aggregation logic).

**Execution note:** Mirror the panic-recovery pattern from `pipeline.go` Stage 9 — wrap the per-screen loop in a recover() so one bad canonical_tree doesn't kill the whole stage.

**Patterns to follow:**
- `pipeline.go` Stage 9 spawn pattern: goroutine guard, in-flight slot, recover().
- `repository.go::UpsertPrototypeLinks` for the batch-UPSERT-in-one-tx pattern.

**Test scenarios:**
- Happy path: a project with 5 screens, each containing 3 organism candidates, completes Stage 6.7 and writes 15 rows to `detected_organism_match`.
- Edge case: a project with 0 screens → Stage completes immediately with `matched=0, near=0, novel=0`.
- Edge case: an unparseable canonical_tree zstd blob → Stage logs the error + skips that screen, continues to the next, completes with the rest.
- Error path: DB write failure during the batch UPSERT → entire batch rolls back; Stage logs + emits `stage_failed` event but doesn't block downstream audit Stage 7.
- Integration: re-running the pipeline on the same `version_id` produces identical rows (R3 idempotency).
- Integration: a fresh manifest with new published organism signatures + re-import → verdicts update (the row's `manifest_hash` reflects the new hash).
- Integration: Stage 6.7 completion triggers `RebuildPromotionCandidates` exactly once per pipeline run.

**Verification:**
- After a Tax Centre re-import, `SELECT COUNT(*) FROM detected_organism_match WHERE tenant_id = 'e09…'` returns a count in the expected order of magnitude (one row per organism-shaped FRAME, ~50–500 for Tax Centre).
- `EXPLAIN QUERY PLAN` on the UPSERT path shows the PRIMARY KEY index is used.

---

- U6. **Repository methods — `UpsertOrganismMatches`, `LookupOrganismMatch`, aggregation queries**

**Goal:** All DB I/O for organism verdicts and promotion candidates lives in `repository.go`. No SQL leaks into pipeline/handlers/admin.

**Requirements:** R2, R8

**Dependencies:** U1

**Files:**
- Modify: `services/ds-service/internal/projects/repository.go` — add the following on TenantRepo:
  - `UpsertOrganismMatches(ctx, rows []DetectedOrganismMatch) error`
  - `LookupOrganismMatchByFrame(ctx, frameID string) (DetectedOrganismMatch, bool, error)`
  - `ListOrganismMatchesForVersion(ctx, versionID string) ([]DetectedOrganismMatch, error)`
  - `CountOrganismMatchesByKind(ctx) (map[string]int, error)`
  - `ListOrganismMatchesBySlug(ctx, slug string, kind string, limit, offset int) ([]DetectedOrganismMatch, error)`
  - `UpsertPromotionCandidates(ctx, rows []PromotionCandidate) error`
  - `ListPromotionCandidates(ctx, sortBy string, limit int) ([]PromotionCandidate, error)`
- Modify: `services/ds-service/internal/projects/types.go` — add `DetectedOrganismMatch`, `PromotionCandidate` structs mirroring the table columns
- Test: `services/ds-service/internal/projects/repository_test.go` — extend

**Approach:**
- All methods tenant-scoped via the existing `TenantRepo` pattern.
- Single-tx batch UPSERTs.
- The 4 list/count methods feed Part C's dashboard endpoints (U10).

**Patterns to follow:**
- `repository.go::UpsertPrototypeLinks` (DELETE-then-INSERT in one tx) vs UPSERT pattern — pick UPSERT here since rows are keyed on `(version_id, frame_id)` which is stable across re-runs.
- `repository.go::GetPrototypeLinks` for the simple SELECT shape.

**Test scenarios:**
- Happy path: UpsertOrganismMatches with 100 rows writes them all in one tx, idempotent on re-run.
- Edge case: tenant isolation — TenantRepo for tenant A cannot read rows written by TenantRepo for tenant B (R8).
- Edge case: CountOrganismMatchesByKind on an empty table returns an empty map without errors.
- Edge case: ListOrganismMatchesBySlug with no matching rows returns empty slice + nil error.
- Edge case: cascading deletes — deleting a `project_versions` row cascades to its `detected_organism_match` rows.
- Error path: passing a row with empty `version_id` fails the FK validation.

**Verification:**
- All repo methods compile under strict-mode + `staticcheck` lint.
- Insert + lookup round-trip preserves all fields including JSON columns (atom_signature_json, diff_json).

---

### Part B — Plugin nudge UI

- U7. **HTTP endpoint — `POST /v1/audit/organism-match`**

**Goal:** Serve a single verdict lookup keyed by Figma node id. Read-only, tenant-scoped, cache-only.

**Requirements:** R4, R9

**Dependencies:** U6

**Files:**
- Modify: `services/ds-service/internal/projects/server.go` — register route + handler
- Modify: `services/ds-service/cmd/server/main.go` — wire the new handler with the standard auth middleware
- Create: `services/ds-service/internal/projects/server_organism_handler.go` — handler implementation
- Test: `services/ds-service/internal/projects/server_organism_handler_test.go`

**Approach:**
- Request body: `{ "node_id": "<figma_node_id>", "file_id": "<figma_file_id>" }`
- Resolve tenant from JWT (existing auth middleware).
- Call `LookupOrganismMatchByFrame(ctx, node_id)`.
- Response shapes:
  - 200 + `{ verdict: {kind, suspected_slug, variant_key, diff, confidence, manifest_hash} }` when found
  - 200 + `{ verdict: null, reason: "no_import_covers_this_frame" }` when not found
  - 401 when JWT missing/invalid (existing middleware)
- No write path. No recomputation. Pure read-through.

**Patterns to follow:**
- `server.go` route registration pattern next to `HandleAssetStream`.
- Bearer-token middleware: `s.requireAuth(...)`.

**Test scenarios:**
- Happy path: a verdict exists for the requested node_id → 200 + verdict payload.
- Happy path: no verdict for the node_id → 200 + `null verdict`.
- Edge case: malformed request body → 400.
- Edge case: missing JWT → 401.
- Edge case: cross-tenant request — designer's JWT for tenant A asks about a node_id whose verdict was written for tenant B → 200 + null verdict (tenant isolation).

**Verification:**
- `curl -X POST -H "Authorization: Bearer dev" -d '{"node_id":"1454:194509"}' http://localhost:8080/v1/audit/organism-match` returns the expected shape.

---

- U8. **Plugin command — "Check selection against DS"**

**Goal:** Add a new menu entry to the Figma plugin. When invoked, post the selected FRAME's node id to U7 and render the verdict in the plugin UI panel.

**Requirements:** R4

**Dependencies:** U7

**Files:**
- Modify: `figma-plugin/manifest.json` — add menu entry `{"name": "Check selection against DS", "command": "checkOrganism"}`
- Modify: `figma-plugin/code.ts` — add `case "checkOrganism"` handler; new message type `organism.check-result`
- Modify: `figma-plugin/ui.html` — add verdict-card UI section
- Modify: `figma-plugin/code.js` — rebuild from `code.ts` (commit both per existing convention)
- Test: there's no automated plugin test harness — verify manually per the existing pattern (call out in plan: see Documentation Plan)

**Approach:**
- Plugin must read the selected FRAME's `id` + the current `figma.fileKey`.
- POST to `${dsURL}/v1/audit/organism-match` with the JWT from `figma.clientStorage.docs_auth_token` (existing pattern).
- Render verdict card in `ui.html`:
  - `exact` → green check + slug/variant label
  - `near` → yellow caution + diff list + action buttons
  - `novel` → blue info + "matches no published organism — used in N frames"
  - null → "No verdict — please import this file first"
- Action buttons in U8: `[View pattern]` (links to atlas admin), `[Mark as intentional fork]` (telemetry-only — U9 wires telemetry). `[Replace with INSTANCE]` is rendered but disabled with "Coming soon" tooltip (deferred to follow-up).

**Patterns to follow:**
- `figma-plugin/code.ts::personas.fetch` handler shape for the fetch-then-post-result pattern.
- `figma-plugin/code.ts::auditSelection` for the existing selection-aware command.

**Test scenarios:**
- *(no automated plugin tests in this repo — manual smoke covered in Documentation Plan)*
- Manual: open plugin in dev mode, select a wild FRAME in Tax Centre, run command → verdict card renders with the expected slug + diff.
- Manual: select a non-organism FRAME (e.g., a header bar) → verdict card renders "no match".
- Manual: ds-service unreachable → verdict card renders error state, doesn't crash plugin UI.

**Verification:**
- Designer can run the command on a Tax Centre frame and see a verdict within ~200 ms.
- The plugin's allowed-domains in manifest.json already cover `indmoney-ds-service.fly.dev` — no manifest network-domain change required.

---

- U9. **Telemetry + intentional-fork marking**

**Goal:** Capture designer choices when they see a verdict. `Mark as intentional fork` writes a row that suppresses the same verdict on future audits.

**Requirements:** R4

**Dependencies:** U7, U8

**Files:**
- Create: `services/ds-service/migrations/0025_organism_fork_mark.up.sql` — table `organism_fork_mark(tenant_id, frame_id, marked_at, marked_by_user_id, reason TEXT)`
- Modify: `services/ds-service/internal/projects/server_organism_handler.go` — add `POST /v1/audit/organism-match/fork` endpoint
- Modify: `figma-plugin/code.ts` — wire the action button
- Modify: `internal/projects/server_organism_handler.go` — verdict lookup checks fork_mark + sets `is_intentional_fork: true` in response when set
- Test: `services/ds-service/internal/projects/server_organism_handler_test.go` — extend

**Approach:**
- "Mark as intentional fork" calls `POST /v1/audit/organism-match/fork` with `{node_id, reason}`.
- On subsequent verdict lookups for the same node_id, the response carries `is_intentional_fork: true` and the dashboard (Part C) sorts these into a separate bucket.
- Telemetry: also write a row to the existing `audit_log` table with `event_type='organism_fork_marked'` for value tracking ("how often are designers marking vs replacing?").

**Patterns to follow:**
- `internal/projects/figma_blocklist.go` for "mark + suppress" pattern (existing blocklist for failed renders).

**Test scenarios:**
- Happy path: fork-mark a frame, subsequent verdict lookup returns `is_intentional_fork: true`.
- Edge case: fork-mark an already-marked frame is idempotent (UPSERT on frame_id).
- Edge case: tenant isolation — fork-mark for tenant A is invisible to tenant B.
- Integration: audit_log receives one `organism_fork_marked` event per fork-mark.

**Verification:**
- After fork-marking a Tax Centre frame, re-running the plugin command renders "Marked as intentional fork on YYYY-MM-DD by <user>".

---

### Part C — Adoption + drift dashboard (atlas admin)

- U10. **Aggregation endpoints**

**Goal:** Serve the dashboard's data needs. Three read endpoints feeding the new admin tab.

**Requirements:** R5

**Dependencies:** U6

**Files:**
- Modify: `services/ds-service/internal/projects/server.go` — register routes
- Create: `services/ds-service/internal/projects/server_organism_admin.go` — handlers
- Test: `services/ds-service/internal/projects/server_organism_admin_test.go`

**Approach:**
- Three endpoints:
  - `GET /v1/admin/organisms/adoption` — returns per-organism counts `{slug, instance_count, exact_match_count, near_match_count, novel_count, fork_count}`. `instance_count` is computed from `graph_index` `uses` edges (atom-level instances of the organism itself, which today is ~0 — see R1 motivation).
  - `GET /v1/admin/organisms/{slug}/matches?kind=...&limit=...&offset=...` — paginated list of matches for one organism + kind.
  - `GET /v1/admin/organisms/matches/{version_id}/{frame_id}` — full detail for one match (atoms, diff, manifest_hash, intentional-fork status).
- All endpoints behind `requireSuperAdmin` middleware (existing pattern — only DS team should see cross-project drift).

**Patterns to follow:**
- `server.go::HandlePrerenderStatus` for admin-only read endpoints.
- `internal/projects/admin/` for routing groups (if it exists; otherwise mirror the blocklist admin pattern).

**Test scenarios:**
- Happy path: adoption endpoint returns counts for every published organism (zero-row entries included so the dashboard can render the full catalog).
- Happy path: matches endpoint returns paginated rows ordered by `detected_at DESC`.
- Edge case: matches endpoint with invalid slug → 404.
- Edge case: detail endpoint with frame_id that doesn't exist → 404.
- Edge case: super-admin auth not present → 403.

**Verification:**
- After Part A runs on Tax Centre, `curl /v1/admin/organisms/adoption` returns at least one row for `list-on-surface` with `near_match_count > 0`.

---

- U11. **Admin tab — adoption table**

**Goal:** New atlas admin route `/atlas/admin/organisms` with a per-organism adoption table.

**Requirements:** R5

**Dependencies:** U10

**Files:**
- Create: `app/atlas/admin/organisms/page.tsx`
- Create: `app/atlas/admin/organisms/_lib/AdoptionTable.tsx`
- Create: `app/atlas/admin/organisms/_lib/types.ts`
- Modify: `app/atlas/admin/_lib/AdminShell.tsx` — add nav entry

**Approach:**
- Fetch `/v1/admin/organisms/adoption` on mount.
- Render a table: one row per published organism, columns `[Slug | Atoms | Instance | Exact | Near | Novel | Forks | Drift signal]`.
- Drift signal is a computed cell: if `near + novel > 0.5 * (instance + exact + near + novel)`, show a red dot. If `near + novel > 0.2 * total`, yellow dot. Else green.
- Click row → navigate to U12 detail.

**Patterns to follow:**
- `app/atlas/admin/figma-blocklist/page.tsx` for layout shell + data-fetch + table.
- `app/atlas/_lib/atlas.tsx` for color tokens.

**Test scenarios:**
- *(component-level — manual until vitest covers admin/* — call out in Documentation Plan)*
- Manual: page loads, table populates with non-zero rows after Tax Centre import.
- Manual: drift signal cell renders correctly for high-drift orgs.
- Manual: click a row navigates to the per-slug detail page (U12).

**Verification:**
- `/atlas/admin/organisms` renders the adoption table with at least `list-on-surface` showing non-zero `near` count after fixtures import.

---

- U12. **Admin drill-in — per-organism matches + per-frame diff**

**Goal:** Drill from the adoption table into a specific organism's matches, then into a specific match's diff descriptor.

**Requirements:** R5

**Dependencies:** U10, U11

**Files:**
- Create: `app/atlas/admin/organisms/[slug]/page.tsx`
- Create: `app/atlas/admin/organisms/[slug]/[frameId]/page.tsx`
- Create: `app/atlas/admin/organisms/_lib/MatchList.tsx`
- Create: `app/atlas/admin/organisms/_lib/MatchDetail.tsx`

**Approach:**
- `/atlas/admin/organisms/list-on-surface` — paginated list of matches grouped by kind (exact → near → novel). Each row: `[Frame thumbnail (from existing screen PNG) | File | Version | Kind | Confidence | Fork?]`.
- `/atlas/admin/organisms/list-on-surface/<frame_id>` — full detail card: rendered atoms, slot topology diagram, diff descriptor as a struct table, "View in atlas" deep-link to `/atlas?focus=...&frame=...`.
- Frame thumbnail reuses the existing screen-PNG asset endpoint with bbox crop params (if supported; otherwise full screen).

**Patterns to follow:**
- Existing per-flow detail pattern in atlas (LeafLabelLayer, leaf-detail panel).
- `LeafFrameRenderer` for the thumbnail rendering — if integration is heavy, fall back to a static crop of the existing screen PNG.

**Test scenarios:**
- *(manual smoke for U12 — vitest doesn't cover Next.js page components in this repo yet)*
- Manual: drilling into a near-match shows the diff descriptor + slot topology visualization.
- Manual: "View in atlas" deep-link opens the right leaf with the right frame highlighted.
- Manual: pagination works for organisms with 50+ matches.

**Verification:**
- After Tax Centre import, the `list-on-surface` detail page lists the 5+ wild-tax matches with diff descriptors visible.

---

### Part D — Promote-to-component recommendations

- U13. **Promotion candidate clustering — `RebuildPromotionCandidates`**

**Goal:** Aggregate novel + near-match-with-low-confidence verdicts across all view_ready versions in a tenant. Group by fingerprint hash; compute frequency, file_count, stability_score, atom_reuse_rate. UPSERT into `promotion_candidate`.

**Requirements:** R6, R8

**Dependencies:** U1, U6 (the corpus must exist)

**Files:**
- Create: `services/ds-service/internal/projects/promotion_candidates.go`
- Test: `services/ds-service/internal/projects/promotion_candidates_test.go`

**Approach:**
- SQL group-by on `detected_organism_match` filtered by `tenant_id` + `match_kind IN ('novel', 'near')` + `confidence < 0.7`.
- Group keys: `fingerprint_hash`. Aggregates per group:
  - `frequency` = COUNT(*)
  - `file_count` = COUNT(DISTINCT file_id) (join through `screens → flows`)
  - `stability_score` = 1.0 - normalized variance of slot_topology hashes within the group (lower variance = higher stability)
  - `atom_reuse_rate` = avg(atom_INSTANCE_count / total_descendant_count) per frame
- UPSERT all clusters into `promotion_candidate`. DELETE rows whose frequency dropped to 0 (no longer represented in any version).
- Triggered from U5 at end of Stage 6.7.
- Ranking: callers sort by `(frequency * stability_score * atom_reuse_rate) DESC`.

**Patterns to follow:**
- `repository.go::UpsertPrototypeLinks` for the DELETE-then-INSERT pattern.
- Stats helpers — write inline; no new dependency.

**Test scenarios:**
- Covers AE2. Happy path: 6 Portfolio Position Card fingerprints (from the prior probe across V4/V5/US Stocks) across 3 files all hash identically → 1 promotion_candidate row with `frequency=6, file_count=3, stability_score≈1.0`.
- Edge case: tenant isolation — running for tenant A doesn't pick up tenant B's novel verdicts.
- Edge case: a cluster of size 1 (single occurrence) is filtered out (K ≥ 3 minimum by default; threshold in config).
- Edge case: a cluster appearing in only 1 file (file_count=1) is filtered out (N ≥ 2 minimum by default).
- Edge case: a previously-promoted cluster (its hash now matches a published organism) is auto-dropped from `promotion_candidate` on the next rebuild.
- Integration: re-running the rebuild on unchanged data yields identical rows (idempotency).

**Verification:**
- After the corpus has the position-card data, `SELECT * FROM promotion_candidate ORDER BY frequency * stability_score DESC LIMIT 5` returns the position-card cluster at the top.

---

- U14. **Promotion candidates endpoint + dashboard surface**

**Goal:** Surface ranked promotion candidates in the same admin tab as Part C. Each card shows: representative thumbnail (from the most-recent frame in the cluster), frequency, file_count, atom_reuse_rate, proposed-name input.

**Requirements:** R6

**Dependencies:** U13, U11

**Files:**
- Modify: `services/ds-service/internal/projects/server_organism_admin.go` — add `GET /v1/admin/organisms/promotion-candidates`
- Modify: `services/ds-service/internal/projects/server_organism_admin.go` — add `PATCH /v1/admin/organisms/promotion-candidates/{hash}` for setting `proposed_name`
- Create: `app/atlas/admin/organisms/_lib/PromotionCandidatesPanel.tsx`
- Modify: `app/atlas/admin/organisms/page.tsx` — render the panel below the adoption table

**Approach:**
- Top 20 candidates by `frequency × stability_score × atom_reuse_rate` desc.
- Each card: thumbnail (from one representative frame), badge `appears 6× across 3 files`, atom list, "[Name this pattern] [Dismiss for now]" actions.
- "Dismiss" sets a `dismissed_at` flag on the candidate (add column in U13 migration if not already present, otherwise extend in this U) — keeps the row but suppresses it from the panel.
- Reviewer-set `proposed_name` is editable inline and stored on the row.

**Patterns to follow:**
- Same data-fetch + table conventions as U11.

**Test scenarios:**
- Happy path: panel renders 5+ candidates after Part A imports + Part D aggregation.
- Edge case: 0 candidates → panel shows "No patterns recur ≥ 3 times across ≥ 2 files yet".
- Edge case: setting `proposed_name` persists on refresh.
- Edge case: dismissing a candidate hides it from the panel.

**Verification:**
- The 6× position-card cluster appears at the top of the panel with a thumbnail and atom list.

---

## System-Wide Impact

- **Interaction graph:**
  - Stage 6.7 is downstream of Stage 6 (canonical_trees committed) and upstream of Stage 7 (audit registry — future `organism_adherence` rule will consume the corpus).
  - Plugin → `POST /v1/audit/organism-match` shares the existing JWT auth + manifest network-allow with no new domains.
  - Atlas admin tab → `GET /v1/admin/organisms/*` uses the existing `requireSuperAdmin` middleware.
- **Error propagation:**
  - Stage 6.7 failures log + continue. Pipeline reaches `view_ready` regardless of detection success. Detection errors do not block render or audit.
  - Plugin verdict lookup failures render an error state but don't break the rest of the plugin.
- **State lifecycle risks:**
  - Re-imports create new `project_version` rows; old detection rows cascade-delete with the old version. No orphan accumulation.
  - Manifest republish: old rows store `manifest_hash`; dashboard surfaces stale-verdict warning when current manifest hash drifts. Re-import triggers re-detection automatically.
  - Promotion candidates can stale-out: re-aggregation deletes rows whose frequency drops to 0.
- **API surface parity:**
  - The plugin's existing menu commands (`auditSelection`, `auditFile`) live alongside the new `checkOrganism`. No existing command behavior changes.
  - The atlas admin shell gains one nav entry; existing tabs unchanged.
- **Integration coverage:**
  - End-to-end: import a fixture file → expect N detected_organism_match rows + M promotion_candidate rows.
  - Plugin → server → DB: select a fixture frame, run command, expect verdict to match what the pipeline wrote.
- **Unchanged invariants:**
  - The atom-level `composition_refs` flow (`cmd/variants` → manifest → `graph_index.edges_uses_json`) is untouched. The mind-graph view continues to render molecule → atom edges exactly as before.
  - The dormant DB `composition_refs` table in `types.go:184` is also untouched. This plan introduces a separate `detected_organism_match` table; we do not repurpose the dormant one.
  - Canonical tree storage and Stage 9 cluster prerender remain unchanged. Detection is a new sibling stage, not a replacement.

---

## Risks & Dependencies

| Risk | Mitigation |
|---|---|
| **Slot-kind inference is too brittle** (designers name a frame "Left Text" but its bbox is on the right) | Slot inference is a soft signal; the hard signal is atom_set Jaccard. Even when slot inference misclassifies, atom_set match still produces correct `near` verdicts. Track slot misclassification rate as a debug metric in U5 and tune patterns. |
| **Detection runtime cost is unbounded for very large projects** (Tax Centre had 102 screens) | The walker is O(N nodes) per screen, single-pass. For 100 screens × 200 nodes = 20K node visits → ~10ms in Go. Confirmed by analogous Stage 9 walker timing. Stage 6.7 budget cap of 60s as a safety valve. |
| **False positives flood the dashboard** (every Card-looking FRAME labeled `near match`) | Conservative confidence thresholds + the `Jaccard ≥ 0.7` minimum for `near`. The `unrelated` bucket absorbs the long tail of accidental matches. Tune thresholds based on real corpus distribution after first ship. |
| **Manifest drift between detection and audit** (manifest publishes mid-pipeline) | Manifest is loaded once at Stage 6.7 start; same signature catalog used for all screens in that run. Manifest-version mismatch between runs is surfaced explicitly via the `manifest_hash` column. |
| **Plugin endpoint becomes a chatty hot path** (designers spamming the command) | Endpoint is pure read from indexed table; ~1ms per lookup. No rate limit needed in v1; add if telemetry shows abuse. |
| **Cross-tenant data leakage** (a tenant sees another's promotion candidates) | Every endpoint resolves tenant from JWT; every query is tenant-scoped. Tested explicitly in U6 + U10. |
| **Promotion candidates feel "AI-suggested" rather than DS-team-curated** (governance concern) | Surface clearly in U14 dashboard copy that recommendations are heuristic. Reviewer must explicitly name + accept any candidate. No automatic publishing to the DS manifest. |

---

## Phased Delivery

### Phase 1: Detection corpus (Part A — U1 through U6)

Lands first; produces the data without any consumer surface.

- Ship migration + walker + classifier + pipeline integration + repo methods.
- Verify with Tax Centre + INDstocks V5 + Dashboard v5 corpora.
- No user-visible behavior change.
- Gate: Tax Centre re-import produces an expected count of `detected_organism_match` rows; classifier output reviewed manually for accuracy.

### Phase 2: Designer surface (Part B — U7, U8, U9) and DS-team surface (Part C — U10, U11, U12) in parallel

Once Part A's corpus is reliable, B and C can ship independently. Either order works; user choice based on which audience needs the value first.

- **B alone**: closes the per-designer feedback loop. Telemetry from U9 informs Part C's drift signals.
- **C alone**: gives the DS team a measurement surface immediately; designers don't see anything new yet.
- **B then C** (recommended): designers' verdicts flow into the dashboard with real-world signal. C launches with non-zero data.

### Phase 3: Promotion engine (Part D — U13, U14)

Builds on the now-stable corpus. Cheapest unit of new value because it reuses everything in Part A.

- Ship clustering + dashboard panel.
- DS team reviews top candidates; promotes 1-2 to validate end-to-end value.

---

## Documentation Plan

- **`docs/plans/2026-05-13-001-feat-organism-pattern-detection-plan.md`** — this plan (active during execution; status=`shipped` on completion).
- **`docs/solutions/`** — write one solution doc per part after ship:
  - `2026-05-XX-organism-fingerprinting.md` — Part A's heuristic choices and threshold tuning history.
  - `2026-05-XX-plugin-organism-verdict.md` — Part B's UX patterns + designer-feedback insights from telemetry.
  - `2026-05-XX-organism-adoption-dashboard.md` — Part C's metric definitions + drift-signal calibration.
- **AGENTS.md / CLAUDE.md update** — add a short paragraph in the design-system docs section: "Organism Pattern Detection — what it is, where verdicts live, how to extend the signature catalog."
- **Plugin user docs** (lives in `figma-plugin/README.md`) — add the new "Check selection against DS" menu entry with screenshots.
- **Atlas admin docs** — add a short walkthrough of `/atlas/admin/organisms` to whatever admin onboarding doc exists (or create one).
- **Manual smoke checklist** — for U8 (plugin) and U11/U12 (admin pages) which have no automated test coverage today, add a short manual smoke sequence in the relevant solution docs so a future operator can re-verify after dependency upgrades.

---

## Sources & References

- Session probe data: `tmp/list-probe/` and `tmp/pattern-probe/` (Figma raw JSON for 14 organism + 6 position-card nodes used to validate the detection heuristic).
- Related shipped work:
  - Atom-level composition: `docs/plans/2026-05-01-003-feat-projects-flow-atlas-phase-7-and-8-plan.md` (Phase 7 U6).
  - Renderer fidelity audit (gives confidence in canonical_tree fidelity): commit `6b2ee73`.
  - Vector-pollution fix (informs Go↔TS classifier parity caution): commit `9cb26f7`.
- Code references:
  - `services/ds-service/internal/projects/pipeline.go` (Stage placement)
  - `services/ds-service/internal/projects/pipeline_cluster_prerender.go::ExtractClusterIDs` (walker pattern)
  - `services/ds-service/internal/projects/graph_sources.go::BuildComponentRows` (manifest reader pattern)
  - `services/ds-service/internal/projects/types.go:184` (dormant `composition_refs` struct — explicitly NOT touched)
  - `app/atlas/_lib/leafcanvas-v2/node-classifier.ts` (slot-kind name patterns — parity reference)
  - `app/atlas/admin/figma-blocklist/page.tsx` (admin tab pattern)
  - `figma-plugin/code.ts` (plugin command pattern)
- External references: none required.
