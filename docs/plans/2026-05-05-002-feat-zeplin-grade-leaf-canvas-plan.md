---
title: Zeplin-grade interactive leaf canvas
type: feat
status: active
date: 2026-05-05
origin: docs/brainstorms/2026-05-05-zeplin-grade-leaf-canvas-requirements.md
---

# Zeplin-grade interactive leaf canvas

## Overview

Replace the current flat-PNG leaf canvas (one `<img>` per screen, no per-layer identity) with a reconstructed canvas where each Figma frame is rebuilt from its `canonical_tree` JSON as HTML+CSS layers. Designers can hover/inspect/export atomic children (TEXT, icon clusters, image fills) and edit text content inline; surrounding layout reflows automatically when the parent uses Figma autolayout, stays put when it doesn't. Edits persist server-side as keyed overrides that survive the 5-min sheet-sync re-imports via a 3-tier match (`figma_node_id` → `canonical_path` → `last_seen_original_text`).

The brainstorm de-risk validated the architectural core (D1 dual-path renderer + D3a autolayout reflow). This plan sequences the implementation against the existing ds-service patterns: DRD's optimistic-concurrency PUT shape, the canonical-tree fetch endpoint, the Phase 5.2 Figma proxy, the FTS5 search reindex pipeline, and the audit-log activity feed.

---

## Problem Frame

Today's leaf canvas (`app/atlas/_lib/leafcanvas.tsx` + `real-data-bridge.ts:PhoneFrameWrapper`) stamps one flat PNG per screen. Every layer's identity is collapsed into pixels, so:

- Engineers can't read CSS / iOS / Android tokens off the canvas — they round-trip to Figma
- Designers can't iterate on copy in-context — they round-trip to Figma
- Asset extraction (icons, illustrations) is a manual Figma-export-per-node task

The raw material to fix this is already in the database: `screen_canonical_trees.canonical_tree` (gzipped per migration `0016_canonical_tree_gz`) holds the full Figma node tree per screen — `layoutMode`, `itemSpacing`, `padding*`, `fills`, `style`, `characters`, etc. The brainstorm's spike confirmed:

- Browser flexbox maps Figma autolayout 1:1 (D1 dual-path validated)
- Text edit in an autolayout subtree cascades reflow end-to-end with zero JS layout code (D3a validated)

The work is to ship that reconstruction end-to-end with override persistence, asset export, search/activity integration, and the Zeplin-style inspector.

---

## Requirements Trace

- R1. Reconstruct each Figma frame from `canonical_tree` using the dual-path renderer (autolayout → flex; absolute → `position:absolute`); render at the same dimensions Figma exports today.
- R2. Hover, single-click select, double-click edit on atomic children (TEXT, icon-cluster wrappers, image fills); pass-through to nearest atomic on container click; preserve current frame-level click behaviour exactly (per D5).
- R3. Edit text content inline with browser `contenteditable`; surrounding autolayout siblings reflow on edit; non-autolayout siblings don't (matches Figma source-design behaviour).
- R4. Persist text edits as overrides keyed on `(screen_id, figma_node_id)` with secondary `canonical_path` and `last_seen_original_text` anchors; survive 5-min sheet-sync re-imports; mark un-reattachable rows `orphaned` and surface them.
- R5. Single + multi-select bulk asset export (PNG / SVG / 2x / 3x) sourced from Figma `/v1/images?ids=…`, cached server-side, named auto-generated from node label.
- R6. Inspector shows layer info, type styles, tokens panel (CSS / iOS / Android), copy-to-clipboard, export buttons; token snippets reflect the override value, not the original Figma text.
- R7. Override propagates to: ⌘K search index, asset filenames, activity feed, per-leaf "Copy overrides" inspector tab. Does NOT propagate to brain-graph counts or back to Figma.
- R8. CSV bulk export/import for translation / PM review (one row per override, columns `screen | node_path | original | current | last_edited_by | last_edited_at`).
- R9. Honor Figma's `visible: false` flag everywhere (5.4% of nodes in real INDmoney files are hidden states); also handle co-positioned design states (multiple variants stacked at the same coordinates) via a per-frame state picker.
- R10. Font: `@font-face` Basier Circle (Inter fallback while licensing clears); default text color = `#000` when `fills` is missing; ~2 px width tolerance vs Figma's renderer documented as a known limit.
- R11. Performance: viewport virtualization — only frames whose bounding box intersects the visible viewport are mounted; off-screen frames render skeletons at the right size so the canvas grid stays correct. Target ≥55 fps during text edit on a 500-node screen with 4 frames in viewport.

**Origin actors:** designer (primary author of overrides + inspector), engineer (token / asset consumer), PM (CSV reviewer).
**Origin flows:** F1 — Spec & handoff; F2 — Live copy iteration.
**Origin acceptance examples:** AE1–AE13 (covers inspect, edit-with-reflow, single+bulk export, reset-to-original, frame vs atomic click, override survival, override orphan recovery, engineer copies live text, PM bulk-CSV review, search finds override, real-frame rendering, section grid).

---

## Scope Boundaries

- Editing layout / styles on canvas — read-only inspect only.
- Two-way write-back to Figma — overrides live in ds-service; Figma stays source of truth for structure.
- Component-instance override surface (Figma's "Override property" UI).
- Variant / breakpoint switching (handled by separate mode-pair work).
- Free-positioning drag handles — we render Figma's layout, never let the user re-author it.
- Real-time multi-cursor collaboration on the canvas (Yjs already powers DRD; canvas v1 stays plain `contenteditable` + debounced HTTP).

### Deferred to Follow-Up Work

- v2 brainstorm items (per-locale variants, approval/lock state, Slack/Linear webhooks, side-by-side version diff): defer until v1 ships and real demand surfaces.
- Pixel-perfect parity with Figma's renderer on every detail of every frame: iterative — implementation will surface and resolve specific frame-class fidelity bugs over multiple PRs after the v1 architecture lands. Tracked as an ongoing concern, not a single PR.

---

## Context & Research

### Relevant Code and Patterns

**Existing leaf canvas surface** — replaces `<window.PhoneFrame>` rendering only:
- `app/atlas/_lib/leafcanvas.tsx` — outer shell (camera, edges, frame strip, top bar). Keep as-is.
- `app/atlas/_lib/real-data-bridge.ts` — `PhoneFrameWrapper` is the component that renders the flat `<img src={frame.pngUrl}>`. **This is the replacement target.**
- `app/atlas/_lib/leaves.tsx`, `frames.tsx`, `tweaks-panel.tsx`, `atlas.tsx` — sibling shell pieces, untouched by this plan.
- All `app/atlas/_lib/*` files are `// @ts-nocheck` (ported reference UI). New canvas code goes under a new strict-TS subdir `app/atlas/_lib/leafcanvas-v2/`.

**Network surface**:
- `lib/atlas/data-adapters.ts` — `fetchLeafCanvas`, `fetchLeafOverlays`, `screenToFrame`. Extend with override-fetch and asset-export wrappers.
- `lib/projects/client.ts` — already has `fetchDRD`, `putDRD` returning `ApiResult<T>`. New override fetchers mirror exactly.
- `lib/atlas/live-store.ts` — `leafSlots[id]` is the per-leaf zustand cache; add per-leaf `overrides` and `assetCache` slots.

**Backend canonical_tree**:
- Storage: `services/ds-service/migrations/0016_canonical_tree_gz.up.sql` — `screen_canonical_trees(screen_id PK, canonical_tree TEXT, canonical_tree_gz BLOB, hash, updated_at)`. **Sibling table; not on `screens`.**
- Reader: `services/ds-service/internal/projects/canonical_tree.go:ResolveCanonicalTree`.
- Endpoint: `GET /v1/projects/{slug}/screens/{id}/canonical-tree` (`server.go:1172` — `HandleScreenCanonicalTree`). Wire shape `{ "canonical_tree": <raw JSON>, "hash": "<sha256>" }`. `Cache-Control: private, max-age=60`.
- Pipeline writer: `services/ds-service/internal/projects/pipeline.go:328-339` — Stage 6 of every export (manual + sheets-sync) inserts canonical_tree inside the same tx as `InsertScreenModes` and the version-status flip. **Override re-attachment hooks here.**

**DRD optimistic-concurrency PUT (precedent for override PUT)**:
- `services/ds-service/internal/projects/server.go:1082-1163` — `HandlePutDRD`. Body `{ content, expected_revision }`. Returns 200 `{ revision, updated_at }` or 409 `{ error: "revision_conflict", current_revision }`. Body cap `MaxDRDBodyBytes = 1<<20`.
- Frontend: `lib/projects/client.ts:fetchDRD`, `putDRD` returning `ApiResult<DRDPutResult>` discriminated union.
- Editor: `app/atlas/_lib/AtlasDRDEditor.tsx` — 1500ms debounce on `editor.onChange`, save-state machine `idle | saving | saved | conflict | error`. Same machine for override editor.

**Figma proxy precedent (Phase 5.2 P4)**:
- `services/ds-service/internal/projects/figma_proxy.go` — `ParseFigmaURL`, `FigmaPATResolver` (per-tenant PAT decryption via `dbConn.GetFigmaToken` + `cfg.EncryptionKey.Decrypt`), 5-min in-process cache keyed by `(tenant_id, file_key, node_id)`, `figmaProxyLimiter` (per-tenant 5 req/sec token bucket), `errFigmaNotFound` sentinel. Today only PNG thumbnail proxy. **Extend for SVG / per-scale and bulk export.**
- `services/ds-service/internal/projects/pipeline.go:629:renderChunk` — has the 3-attempt 429 + render-timeout retry pattern. Reuse on the asset-export path.
- Asset-token signing: `services/ds-service/internal/projects/png_handler.go:HandleMintAssetToken` — `POST /v1/projects/{slug}/screens/{id}/png-url` returns `{ url, ktx2_url, expires_in }`. Same pattern for icon downloads (so `at=` query param replaces `?token=<JWT>`).

**Search index (FTS5)**:
- `services/ds-service/internal/projects/search_index.go` — `BuildSearchRowsForTenant`, `UpsertSearchIndexRows` (DELETE-then-INSERT), `DeleteSearchIndexBySource`. Existing entity kinds: `flow | drd | decision | persona | component | product | folder`. **Add `text_override` kind.**
- `services/ds-service/internal/projects/search.go:HandleSearch` — FTS5 MATCH + ACL JOIN on `flow_grants`. Reused unchanged.
- Frontend: `components/search/useSearch.ts`, `SearchResultsSection.tsx` — 250 ms debounce + abort-on-stale + cmdk groups + `<mark>` snippets. **Reused unchanged.**

**Activity feed (audit_log)**:
- Backend: `audit_log` table + `services/ds-service/internal/projects/auditlog.go:WriteExport`. New code uses inline `INSERT INTO audit_log` per existing `server.go:1495, 1736` pattern (no central helper for non-export events).
- Endpoint: `GET /v1/projects/{slug}/flows/{flow_id}/activity` filters by `json_extract(details, '$.flow_id')`. **Override audit rows MUST include `flow_id` in details.**
- Frontend mapping: `lib/atlas/data-adapters.ts:auditLogToActivity` (line 662-688) — add `override.text.set` / `.reset` cases to `activityKindOf` + `activitySentenceOf`.

**Tokens registry**:
- `lib/tokens/indmoney/{base,semantic,semantic-dark,text-styles,spacing,motion,effects}.tokens.json` — W3C-DTCG 2024 format.
- Loader: `lib/tokens/loader.ts:colorToHex`, `flattenColorTokens`, `buildSemanticPairs`, `buildBasePalette`. **New helper `buildHexToTokenMap(brand)` returning `Map<hex, semantic-path>` reuses existing flatten functions.**

**Migration discipline**:
- Numbered `NNNN_description.up.sql` in `services/ds-service/migrations/` + idempotent `CREATE TABLE IF NOT EXISTS`, indexes guarded.
- `schema_migrations(version, name, applied_at)` runs migrations in numeric order on ds-service startup.
- ALTER TABLE in SQLite is auto-committed — wrapping a multi-statement migration in a tx does NOT roll back column-add side effects (real lesson from 0017).

### Institutional Learnings

(From `docs/solutions/`; full extracts in research output.)

- **`canonical_tree` is in `screen_canonical_trees`, NOT on `screens`** (`2026-04-30-001-projects-phase-1-learnings.md`). Brainstorm originally said "screens.canonical_tree"; this plan corrects the path. The split exists so list queries don't drag the gzipped blob.
- **SSE single-use ticket auth + flush-driven events** (`2026-04-30-001`, `2026-05-01-001-phase-6-closure.md`). Override-aware events emit AFTER the worker writes materialised search rows, never on the override-write tx — mirrors Phase 6's `GraphIndexUpdated{materialized_at}` fix for read-after-write race.
- **SQLite single-writer deadlock — read source rows BEFORE opening write tx** (`2026-05-01-003-phase-7-8-closure.md`). For CSV bulk import in U12, resolve canonical_tree anchors BEFORE opening the override+audit_log+search write tx.
- **Materialise on write, query on read** (`2026-05-01-001-phase-6-closure.md`). Override-resolved snippets and search rows are materialised on write; never recomputed on every leaf-canvas mount.
- **Search index + activity feed surfaces already exist** (`2026-05-01-003`, `2026-05-02-001-phase-5-collab-learnings.md`). Don't fork; extend.
- **Phase 5.2 Figma proxy precedent** (`2026-05-02-003-phase-5-2-collab-polish.md`). `ParseFigmaURL`, `FigmaPATResolver`, 5-min cache, `errFigmaNotFound`, `(tenant_id, file_key, node_id)` cache key. Extend with `(format, scale, version_index)` for asset cache (OQ-2).
- **0017 sheet_sync_state partial-state migration learning is NOT captured in `docs/solutions/`.** Recommend running `/ce-compound` post-implementation to capture the override-table migration discipline before it joins the gap.
- **Resist v1 collab on the canvas.** Phase 5/5.1 hit BlockNote schema-shape pain; canvas's `contenteditable` + 500 ms debounce is structurally simpler than Hocuspocus integration.

### External References

External research skipped — codebase has solid in-repo patterns for every layer touched (DRD PUT, search index, FTS5, activity feed, Figma proxy, tokens registry, migrations). Browser flexbox + autolayout mapping is documented in W3C CSS specs; not surfaced here because the brainstorm spike already validated the mapping empirically.

---

## Key Technical Decisions

- **Override storage shape**: dedicated `screen_text_overrides` table (NOT a JSONB column on `screens`). Reasons: read independently (search index, activity feed, CSV export, copy-overrides tab); re-import scan is a row scan not JSON path queries; mirrors DRD's `flow_drd` side-table precedent.
- **Override key**: primary `(screen_id, figma_node_id)`; fallbacks `canonical_path` and `last_seen_original_text` for orphan recovery.
- **PUT shape mirrors DRD**: optimistic concurrency via `expected_revision`, 409 returns `{ current_revision }`. Body cap 16 KB (override values are short strings).
- **No zod validators**; struct-unmarshal validation in Go (matches existing PUT/PATCH endpoints).
- **Asset cache table** keyed `(tenant_id, file_id, node_id, format, scale, version_index)`; blobs stored locally under `services/ds-service/data/assets/<tenant>/<version>/<node_id>.<format>` (mirrors `png_storage_key` convention).
- **Per-tenant Figma rate limit** for asset export: extend `figmaProxyLimiter`'s 5 req/sec token-bucket pattern; build into U4 not after.
- **Canvas renderer location**: new strict-TS subdir `app/atlas/_lib/leafcanvas-v2/`. Outer shell (`leafcanvas.tsx`) untouched; only `<window.PhoneFrame>` (in `real-data-bridge.ts`) is replaced via a feature toggle.
- **No BlockNote / Yjs in canvas v1**. Inline text edit is plain `contenteditable` + 500 ms debounce + HTTP PUT. Canvas-side collaboration is deferred to v2 explicitly.
- **Visible:false honoring is a hard requirement**, not a polish item. The walker must filter — confirmed via spike (5.4% of nodes hidden).
- **Co-positioned design state filter**: surface a "state picker" UI per-frame when 2+ children share the same `(x, y)` and `bbox`. Heuristic-driven; not silent.
- **Re-attachment is part of the export pipeline tx**, not a background job. Phase 6's read-after-write race lesson rules out async reconciliation.
- **Activity rows include `details.flow_id`** so they surface in the existing `HandleFlowActivity` query. Required for live update parity with violations/decisions/comments.

---

## Open Questions

### Resolved During Planning

- **Override table vs JSONB column on `screens`** → dedicated `screen_text_overrides` table (research bullet OQ-1, mirrors DRD pattern).
- **Asset cache key shape** → `(tenant_id, file_id, node_id, format, scale, version_index)` (extends Phase 5.2 P4 cache; OQ-2 resolved).
- **Tokens hex→name lookup helper** → `buildHexToTokenMap(brand)` reusing `flattenColorTokens`. New file `lib/tokens/hex-to-token.ts`.
- **canonical_tree fetch path** → reuse existing `GET /v1/projects/{slug}/screens/{id}/canonical-tree`; do NOT fatten the project-list payload.
- **Where override re-attachment runs** → in `pipeline.go` Stage 6, same tx as `InsertCanonicalTree`. NOT a background reconciler.

### Deferred to Implementation

- **Pixel-perfect parity with Figma's renderer on every detail of one specific frame** — iterative. Implementation phase will surface frame-class fidelity bugs (gradient mid-stops, rotation, blendMode, mask layers, complex stroke caps, etc.). Each is a separate PR; v1 ships when the worst-case INDmoney screens render at "designer-acceptable" fidelity (target: pass review on 5 hand-picked complex frames including the ChatBot file's `2882:56419`). Spike artefacts under `/tmp/spike/` are kept for reference.
- **Filtering co-positioned design states** (multiple "Slider Dash2" / "Frame 1321320555" tab variants stacked at the same coords) — U14 introduces the state-picker scaffold; the heuristic for *detecting* candidate state groups (name pattern matching, sibling clustering, designer-named "ALT" variants) is execution-time discovery against real frames.
- **Inter-vs-Basier-Circle font metric drift** — will resolve when the actual `BasierCircle.ttf` family is licensed and embedded. Until then, render uses Inter and accepts ~2 px width tolerance; some text bboxes will visually overflow Figma's. v1 ships with Inter; the swap to Basier Circle is a single-PR follow-up that does not change architecture.
- **Mixed-style text runs** (e.g. one bold word inside a sentence): `characterStyleOverrides` + `styleOverrideTable` rendering as nested `<span>`s. v1 ships uniform-style text; mixed-style is the first follow-up after Inter→Basier swap.
- **Virtualization library choice** — `IntersectionObserver`-driven mount via `lib/use-active-section.ts` pattern is the default; revisit only if perf budget (R11, ≥55 fps) is missed.
- **`docs/solutions/` capture for the override-table migration** — run `/ce-compound` after U1 lands to backfill the partial-state migration learning gap surfaced by Learning #12.

---

## Output Structure

```text
services/ds-service/migrations/
├── 0018_screen_text_overrides.up.sql                # NEW
├── 0019_asset_cache.up.sql                          # NEW

services/ds-service/internal/projects/
├── screen_overrides.go                              # NEW — repo: list/upsert/delete/orphan-mark
├── screen_overrides_test.go                         # NEW
├── screen_overrides_handler.go                      # NEW — HTTP handlers + bulk + CSV
├── asset_export.go                                  # NEW — Figma /v1/images?ids=… proxy + cache
├── asset_export_test.go                             # NEW
├── server.go                                        # MODIFY — add route registrations + wire deps
├── pipeline.go                                      # MODIFY — Stage 6 override re-attachment
├── search_index.go                                  # MODIFY — text_override entity kind
└── figma_proxy.go                                   # MODIFY — extend cache key + format/scale

services/ds-service/cmd/server/main.go               # MODIFY — register new routes

app/atlas/_lib/leafcanvas-v2/                        # NEW — strict-TS replacement renderer
├── LeafFrameRenderer.tsx                            # main: walks canonical_tree, dual-path emit
├── nodeToHTML.ts                                    # pure converter
├── icon-cluster-resolver.ts                         # finds FRAME/GROUP wrappers, fetches via API
├── visible-filter.ts                                # honors visible:false + co-positioned state-picker
├── AtomicChildInspector.tsx                         # type styles + tokens panel + export
├── InlineTextEditor.tsx                             # contenteditable + debounced PUT
├── BulkExportPanel.tsx                              # lasso/multi-select + zip download
├── CopyOverridesTab.tsx                             # active + orphaned overrides list
├── StatePicker.tsx                                  # co-positioned design-state UI (U14)
├── tokens.ts                                        # hex→token snippet generator
└── __tests__/
    ├── nodeToHTML.test.ts                           # dual-path renderer scenarios
    ├── visible-filter.test.ts                       # visibility + state-detection heuristics
    └── reattach.test.ts                             # override re-attachment fixtures (frontend mirror)

lib/projects/client.ts                               # MODIFY — fetchTextOverrides, putTextOverride, etc.
lib/atlas/data-adapters.ts                           # MODIFY — leafSlot.overrides + auditLogToActivity cases
lib/atlas/live-store.ts                              # MODIFY — overrides + assetCache slots
lib/tokens/hex-to-token.ts                           # NEW — buildHexToTokenMap

public/fonts/                                        # NEW — BasierCircle.ttf family (or Inter fallback)
```

---

## Implementation Units

- U1. **Schema + migrations: `screen_text_overrides`, `asset_cache`**

**Goal:** Provision the two new SQLite tables this plan needs and wire migrations into `services/ds-service/migrations/embed.go`.

**Requirements:** R4, R5

**Dependencies:** None

**Files:**
- Create: `services/ds-service/migrations/0018_screen_text_overrides.up.sql`
- Create: `services/ds-service/migrations/0019_asset_cache.up.sql`

**Approach:**
- `screen_text_overrides`: `id` (UUID), `tenant_id`, `screen_id` (FK CASCADE), `figma_node_id`, `canonical_path`, `last_seen_original_text`, `value`, `revision` (int, optimistic-concurrency), `status` (CHECK in `('active','orphaned')`, default `'active'`), `updated_by_user_id`, `updated_at`. Indexes: `(screen_id, figma_node_id)` UNIQUE, `(tenant_id, status)`, `(tenant_id, screen_id)`.
- `asset_cache`: `(tenant_id, file_id, node_id, format, scale, version_index)` PK, `storage_key` TEXT (path under `data/assets/...`), `bytes` INT, `mime` TEXT, `created_at` TEXT. Indexes: `(tenant_id)`, `(file_id, node_id)`.
- Both follow established 0010+ patterns (`CREATE TABLE IF NOT EXISTS`, idempotent indexes, FK to `tenants` and `screens`). Lessons from 0017: avoid splitting transactional and side-effect statements; this migration is pure schema (no ALTER TABLE) so partial-state isn't possible.

**Patterns to follow:**
- `services/ds-service/migrations/0008_decisions_comments_notifications.up.sql` (table + indexes + FK shape).
- `services/ds-service/migrations/0016_canonical_tree_gz.up.sql` (sibling-of-screens table convention).

**Test scenarios:**
- Test expectation: none — pure DDL. Coverage comes from integration tests in U2/U4 that exercise the schema.

**Verification:**
- `services/ds-service/internal/db/db.go:Migrate` runs both migrations cleanly on a fresh DB and on an existing DB with migrations 1-17 applied.
- `schema_migrations` shows entries `18` and `19`.
- `PRAGMA table_info(screen_text_overrides)` and `PRAGMA table_info(asset_cache)` show the columns above.

---

- U2. **Override Go repo + HTTP endpoints (GET/PUT/DELETE/bulk)**

**Goal:** CRUD surface for text overrides, mirroring DRD's optimistic-concurrency PUT shape.

**Requirements:** R4, R7

**Dependencies:** U1

**Files:**
- Create: `services/ds-service/internal/projects/screen_overrides.go` — `TenantRepo` methods: `ListOverridesByScreen`, `ListOverridesByLeaf` (joins through flow→project→screens), `UpsertOverride`, `DeleteOverride`, `MarkOverridesOrphaned`, `BulkUpsertOverrides`.
- Create: `services/ds-service/internal/projects/screen_overrides_handler.go` — HTTP handlers `HandleListOverrides`, `HandlePutOverride`, `HandleDeleteOverride`, `HandleBulkUpsertOverrides`.
- Modify: `services/ds-service/cmd/server/main.go` — register routes:
  - `GET    /v1/projects/{slug}/screens/{id}/text-overrides`
  - `GET    /v1/projects/{slug}/leaves/{leaf_id}/text-overrides`
  - `PUT    /v1/projects/{slug}/screens/{id}/text-overrides/{figma_node_id}`
  - `DELETE /v1/projects/{slug}/screens/{id}/text-overrides/{figma_node_id}`
  - `POST   /v1/projects/{slug}/text-overrides/bulk`
- Test: `services/ds-service/internal/projects/screen_overrides_test.go`

**Approach:**
- PUT body `{ value: string, expected_revision: int, canonical_path: string, last_seen_original_text: string }`.
- 409 returns `{ error: "revision_conflict", current_revision: int, current_value: string }` exactly like DRD.
- `MaxOverrideValueBytes = 16 * 1024` (16 KB; override values are short strings, never long-form content).
- Tenant resolved via `s.resolveTenantID(claims)`. Cross-tenant lookups → 404. Cross-screen mismatches → 404.
- One write tx per PUT/DELETE, encloses: override upsert + audit_log INSERT (`override.text.set` / `.reset`) + search-row upsert (deferred to U10's worker queue, not in tx). Per Phase 6's read-after-write rule, search reindex is the worker; the write itself only fires the materialise-on-write trigger.
- Bulk upsert (`HandleBulkUpsertOverrides`) accepts up to 100 rows in one tx; `bulk_id` shared across audit_log rows (mirrors `HandleBulkAcknowledge`).

**Patterns to follow:**
- `services/ds-service/internal/projects/server.go:1082-1163` (`HandlePutDRD`) — body shape, MaxBytesReader, optimistic concurrency, audit_log inside tx.
- `services/ds-service/internal/projects/server.go:HandleBulkAcknowledge` — bulk-tx + bulk_id pattern.
- `services/ds-service/internal/projects/repository.go:UpsertProject` — repo style: `(t *TenantRepo)` methods, returns `Project` or err, all inside `t.handle().ExecContext(ctx, ...)`.

**Test scenarios:**
- **Happy path** PUT new override: returns 200 `{ revision: 1, updated_at }`; row appears in DB; `details.flow_id` is in the audit_log row. Covers AE2.
- **Happy path** PUT existing override with matching `expected_revision`: revision increments; old `value` not retained (last-write-wins).
- **Edge** PUT with `expected_revision = 0` (treat as create) when row exists: 409 with current revision.
- **Edge** PUT with body > 16 KB: 413 / 400.
- **Error path** PUT with `expected_revision != current`: 409, `current_revision` returned, no DB write.
- **Error path** PUT for cross-tenant `screen_id`: 404 (no oracle).
- **Edge** DELETE on missing override: 204 idempotent.
- **Happy path** DELETE: row removed; `override.text.reset` audit emitted; `details.flow_id` populated. Covers AE5.
- **Bulk** POST 100 rows in one shot: all upserted in one tx; activity feed shows 100 events sharing `bulk_id`. Covers AE10.
- **Bulk** POST 101 rows: 400 invalid_payload.
- **Integration** GET screen overrides returns active + orphaned mixed.

**Verification:**
- `curl -X PUT .../text-overrides/<node-id>` with valid JWT and body returns 200 with revision=1.
- Concurrent PUT with stale `expected_revision` returns 409 deterministically.
- Activity tab in the leaf inspector shows the new event after a PUT (powered by the existing `HandleFlowActivity` query).

---

- U3. **Pipeline: re-attach overrides during canonical_tree re-import**

**Goal:** When the export pipeline rewrites a screen's `canonical_tree`, walk every existing override on that screen and re-anchor it via the 3-tier match (`figma_node_id` → `canonical_path` → `last_seen_original_text`). Mark un-reattachable overrides `orphaned`. All within the same write tx as `InsertCanonicalTree` to preserve atomicity.

**Requirements:** R4

**Dependencies:** U1, U2

**Files:**
- Modify: `services/ds-service/internal/projects/pipeline.go` (extend Stage 6 around line 328-339)
- Modify: `services/ds-service/internal/projects/screen_overrides.go` — add `ReattachOverridesForScreen(ctx, tx, screenID, newTree)` repo method
- Test: `services/ds-service/internal/projects/screen_overrides_test.go` — re-attach scenarios

**Approach:**
- Read existing overrides for the screen BEFORE opening the write tx (per Phase 7+8 deadlock learning).
- Open write tx, do `InsertCanonicalTree`, then `ReattachOverridesForScreen`:
  1. For each override: scan new tree for `figma_node_id` match; if found, update `canonical_path` to new path, ensure `status = active`. Done.
  2. If no `figma_node_id` match: scan by `canonical_path`. If a TEXT node exists at that path, update `figma_node_id` and last_seen_original_text. Done.
  3. If neither: scan by `last_seen_original_text` (full-tree TEXT walk). If a UNIQUE match exists, update `figma_node_id` + `canonical_path`. Done.
  4. If still no match: set `status = 'orphaned'`. Audit-log an `override.text.orphaned` event.
- Step (3) requires uniqueness — multiple TEXT nodes with same `last_seen_original_text` mean ambiguous; mark orphaned in that case too.
- Performance: a screen with N overrides walks the new tree at most 3 times (one per tier). N is small (<10 typical), tree size <1MB; well within tx budget.

**Patterns to follow:**
- `services/ds-service/internal/projects/pipeline.go:Stage6` — existing tx pattern.
- `docs/solutions/2026-05-01-003-phase-7-8-closure.md` — read-source-rows-before-tx rule.

**Test scenarios:**
- Covers AE7. **Happy path** re-attach by `figma_node_id`: override survives a sibling-add (canonical_path shifts), re-points to new path. Verify revision unchanged; `status='active'`.
- Covers AE8. **Edge** re-attach by `last_seen_original_text` after node delete+recreate: override re-anchors to new node-id; new `canonical_path` updated.
- **Error path** ambiguous original text (two text nodes with same content): override marked `orphaned`; `override.text.orphaned` audit row emitted.
- **Edge** Figma file actually deleted the text entirely: override marked `orphaned`.
- **Integration** with the wider Stage 6 tx: pipeline doesn't crash on re-attach errors; the canonical_tree itself still commits.

**Verification:**
- Manual run: edit a CTA in the canvas → "Buy" → "Buy now"; trigger a sheet-sync re-import; override remains active and updated `canonical_path` reflects the latest tree (covers AE7).
- Delete and recreate the CTA in Figma; re-import; override re-attaches via fingerprint (AE8).

---

- U4. **Figma asset export server: extend proxy + cache**

**Goal:** Server-side wrapper for `/v1/images/{file}?ids=<csv>&format={png|svg}&scale={1|2|3}` with per-tenant rate limiting and a cache table. Returns one URL per node ID; clients fetch via signed asset tokens.

**Requirements:** R5

**Dependencies:** U1

**Files:**
- Create: `services/ds-service/internal/projects/asset_export.go`
- Test: `services/ds-service/internal/projects/asset_export_test.go`
- Modify: `services/ds-service/internal/projects/figma_proxy.go` — extend cache key with `(format, scale)` for re-use of `figmaProxyLimiter`
- Modify: `services/ds-service/internal/figma/client/client.go` — add `Client.GetImages(fileKey, nodeIDs, format, scale)` with the existing `IsRateLimit()` 3-attempt backoff

**Approach:**
- Repo method `LookupAsset(tenant, fileID, nodeID, format, scale, versionIndex) (storageKey, bool)` and `StoreAsset(tx, ..., bytes, mime, storageKey)`.
- `RenderAssetsForLeaf(ctx, tenant, leafID, nodeIDs[], format, scale)`: cache lookup → cache miss → batch via `Client.GetImages` (chunk size 80, mirror `pipeline.go:renderChunk`) → download bytes → write to local disk → insert cache row → return `[{node_id, storage_key, mime}]`.
- Per-tenant rate limit: extend `figmaProxyLimiter` to 5 req/sec / 30 burst (URL-fetch calls; bytes downloads count separately at 50 req/sec). `Wait(ctx, tenant)` blocks; on context cancel returns error.
- Mime: PNG → `image/png`, SVG → `image/svg+xml`.

**Patterns to follow:**
- `services/ds-service/internal/projects/figma_proxy.go` — cache + token-bucket pattern.
- `services/ds-service/internal/projects/pipeline.go:renderChunk` (line 629) — 3-attempt 429 backoff + chunked Figma calls.

**Test scenarios:**
- **Happy path** cache miss → fetch → store → return same `storage_key` on next call.
- **Edge** Figma 429 with `Retry-After: 2`: backoff once, succeed on retry; same record stored.
- **Edge** request format=svg for a node Figma can't render as SVG (e.g. only-image fill): handle gracefully, return error with `node_id`.
- **Integration** `version_index` part of cache key: re-export under new version invalidates correctly.
- **Error path** Figma 5xx persistent: returns error; cache not poisoned with garbage.
- **Edge** rate-limit context cancel: returns context error; no stuck goroutines.

**Verification:**
- Two calls for the same `(file_id, node_id, format, scale, version_index)` produce one Figma-side fetch and two local cache hits.
- Per-tenant rate limiter pauses bulk export beyond 5 req/sec on a controlled benchmark.

---

- U5. **Asset download HTTP endpoints (single + bulk → zip)**

**Goal:** Public HTTP surface for designers/engineers to download single assets or bulk-zip selected assets, using signed asset tokens (no JWT in URLs).

**Requirements:** R5

**Dependencies:** U4

**Files:**
- Modify: `services/ds-service/internal/projects/asset_export.go` — add `HandleAssetDownload`, `HandleBulkAssetExport`, `HandleMintAssetExportToken`
- Modify: `services/ds-service/cmd/server/main.go` — register routes:
  - `POST /v1/projects/{slug}/assets/export-url` (mint signed token for one asset)
  - `GET  /v1/projects/{slug}/assets/{node_id}?format=&scale=&at=<token>` (download)
  - `POST /v1/projects/{slug}/assets/bulk-export` (returns `{ download_url, expires_in }` for a generated zip)
  - `GET  /v1/projects/{slug}/assets/bulk/{token}` (zip download)
- Test: `services/ds-service/internal/projects/asset_export_test.go` (extend)

**Approach:**
- Single asset: sign `(tenant, file_id, node_id, format, scale)` via `AssetTokenSigner` (existing); GET endpoint verifies, looks up cache, streams bytes with `Content-Type` and `Content-Disposition: attachment; filename=<flow-slug>__<sanitised-name>.<ext>`.
- Bulk asset: POST `{ node_ids: [...], format, scale }`, server batches `RenderAssetsForLeaf` (U4), assembles a zip in memory (or stream-to-temp if > 50 MB), signs a one-shot URL, returns `{ download_url }`. Client follows the URL; server streams the zip with the same naming convention.
- Token includes `bulk_id` so audit-log rows for the bulk export share an ID (mirrors `HandleBulkAcknowledge`).
- `download_url` expires in 5 min.

**Patterns to follow:**
- `services/ds-service/internal/projects/png_handler.go:HandleMintAssetToken` — token mint shape.
- `services/ds-service/internal/projects/png_handler.go:HandleScreenPNG` — Cache-Control + Content-Type-Options + path-traversal guards.

**Test scenarios:**
- Covers AE3. **Happy path** single SVG: filename matches `<flow-slug>__<name>.svg`; returns binary; mime is `image/svg+xml`.
- **Edge** asset cache miss on download: blocks on synchronous fetch (≤5s) or returns 425 with retry hint.
- Covers AE4. **Bulk** zip with 6 SVGs: all present in archive; names sane; total bytes ≤ size cap.
- **Error path** signed token mismatch: 403.
- **Error path** signed token expired: 410.
- **Integration** with U4 rate limit: bulk export of 200 icons back-pressures correctly without 429s.

**Verification:**
- Designer clicks "Export SVG" on an icon → file downloads with the right name and content.
- Bulk export of 6 nodes returns a zip whose tree matches the brainstorm's AE4 spec.

---

- U6. **Frontend `LeafFrameRenderer` (canvas v2)**

**Goal:** Replace `<window.PhoneFrame>` with a strict-TS renderer that walks `canonical_tree` and emits HTML+CSS per the D1 dual-path spec. Handles autolayout (flex), absolute positioning, GROUP flattening, visibility filtering, image fills, and icon-cluster placeholder until U7 wires real cluster fetching.

**Requirements:** R1, R9

**Dependencies:** U1 (so `assetCache` table exists for cluster URLs even if U7 wires the fetch)

**Files:**
- Create: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx`
- Create: `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts` — pure converter
- Create: `app/atlas/_lib/leafcanvas-v2/visible-filter.ts` — `visible:false` honoring + co-positioned-detect (state-picker scaffold; UI in U14)
- Create: `app/atlas/_lib/leafcanvas-v2/icon-cluster-resolver.ts` — finds FRAME/GROUP wrappers for cluster export
- Modify: `app/atlas/_lib/real-data-bridge.ts` — flag-gate `PhoneFrameWrapper` swap to `<LeafFrameRenderer>` when `canonical_tree` is available; PNG fallback when not
- Modify: `lib/atlas/data-adapters.ts` — `fetchLeafCanvas` now also stashes per-screen canonical_tree on `leafSlots` (lazy-loaded if not present)
- Modify: `lib/atlas/live-store.ts` — extend `LeafSlot` type with `canonicalTreeByScreenID: Record<string, unknown>`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/nodeToHTML.test.ts`
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/visible-filter.test.ts`

**Execution note:** Strict-TS subdir; no `// @ts-nocheck`. Existing `app/atlas/_lib/*` is `// @ts-nocheck`-tagged because of its hand-ported origin — new code does not inherit that.

**Approach:**
- Pure function `nodeToHTML(node, parentBBox, parentLayoutMode)` mirroring the spike's logic from `/tmp/spike/build_html4.py` (translated to TS).
- Path A — `layoutMode` set: emit `display:flex` + `flex-direction` + `gap` + `padding*` + `justify-content` + `align-items`; child sizing per `layoutGrow`/`layoutAlign`.
- Path B — `layoutMode` null: emit `position:absolute` with `left = childX - parentX`, `top = childY - parentY`.
- GROUP / BOOLEAN_OPERATION: flatten (recurse to children with parent passed through unchanged).
- TEXT: `<span>` styled from `style.{fontFamily,fontSize,fontWeight,letterSpacing,lineHeightPx,textAlignHorizontal}` + color from `fills` (default `#000`); `white-space: nowrap; overflow: hidden; text-overflow: clip` until U13 swaps in Basier Circle.
- VECTOR / RECTANGLE / ELLIPSE shapes — render container; cluster wrapper resolution belongs to `icon-cluster-resolver.ts`. Until U7 wires cluster fetch, render placeholder div with dashed border and `data-cluster-placeholder=true`.
- IMAGE fills: pull from `/v1/files/{file}/images` already fetched into `leafSlots.imageRefs`; render as `background-image: url(...)` with `background-size` per `scaleMode`.
- `clipsContent: true` (default for FRAME): add `overflow:hidden`.
- Honor `visible: false` everywhere — filter at walker time, not via CSS `display:none` (perf + DOM count).

**Technical design** *(directional, not code spec):*

```text
nodeToHTML(node, parentBBox, parentLayoutMode):
  if !node.visible: return ""
  if node.type in {GROUP, BOOLEAN_OPERATION}:
    return concat(emit(c, parentBBox, parentLayoutMode) for c in node.children)
  if node is icon-cluster wrapper:
    return <img data-id=... src=clusterURL(node) at parent-scoped coords>
  if node.type == TEXT:
    return <span ...>{characters}</span>  // styled per fills + style
  return <div ...>
    {children.map(c => nodeToHTML(c, node.bbox, node.layoutMode))}
  </div>
```

**Patterns to follow:**
- `lib/atlas/data-adapters.ts:screenToFrame` — existing per-frame data transform; new renderer plugs in alongside.
- `app/atlas/_lib/AtlasDRDEditor.tsx` — strict-TS component pattern in this `_lib` directory.

**Test scenarios:**
- **Happy path** dual-path render of a tiny synthetic tree (2 children: one autolayout container with one TEXT, one absolute-positioned RECTANGLE): produces correct HTML with `display:flex` on container A and `position:absolute` on B.
- **Edge** GROUP flattening: input tree with a GROUP wrapping 2 RECTANGLEs renders as 2 absolutely-positioned divs, no GROUP element in DOM.
- **Edge** `visible:false` on a deeply nested branch: that subtree is absent from DOM.
- **Edge** Co-positioned detection: when 2+ children share `(x, y, width, height)`, the walker tags them with `data-state-group` (UI to consume in U14).
- **Edge** Image fill with `scaleMode=FIT`: emits `background-size: contain`.
- **Edge** clipsContent: parent FRAME has `overflow:hidden`; absolute child positioned outside the bbox doesn't visually leak.
- Covers AE12. **Integration** rendering a synthetic copy of `2882:56419`'s structure produces the same shape (top status bar at top, autolayout cards below, bottom tabs at the bottom) without overflow into chrome.
- **Performance** rendering a 500-node frame produces a DOM tree with ≤1000 elements (after GROUP flattening + visibility filter) and renders in <50 ms initial paint (jsdom benchmark).

**Verification:**
- Manual: open a leaf in atlas with this branch deployed; the canvas shows reconstructed frames with correct layer hierarchy. Hover any TEXT and the hover outline is the actual text bbox, not the whole frame.
- The flat PNG is no longer rendered when `canonical_tree` is present; fallback path activates only when canonical_tree is `null` (acceptable for screens whose audit pipeline hasn't run yet).

---

- U7. **Atomic-child inspector + tokens panel**

**Goal:** Click any atomic child → inspector switches to that child's slot, surfacing layer info, type styles, fills, strokes, radius, opacity, shadows, and CSS / iOS / Android / React snippets generated from real fields. Token mapping (hex → semantic-path) shows alongside raw hex.

**Requirements:** R2, R6

**Dependencies:** U6

**Files:**
- Create: `app/atlas/_lib/leafcanvas-v2/AtomicChildInspector.tsx`
- Create: `app/atlas/_lib/leafcanvas-v2/tokens.ts` — snippet generator using `lib/tokens/hex-to-token.ts`
- Create: `lib/tokens/hex-to-token.ts` — `buildHexToTokenMap(brand)` returning `Map<hex, semantic-path>`
- Modify: `lib/atlas/live-store.ts` — selection includes `selectedAtomicChild: { screenID, figmaNodeID } | null`
- Test: `lib/tokens/__tests__/hex-to-token.test.ts`

**Approach:**
- Single-click on any TEXT / cluster / RECTANGLE / ELLIPSE / VECTOR atomic → emit `selectAtomicChild(screenID, figmaNodeID)`. Frame stays the active context; inspector opens a sub-section.
- Tokens snippet generator: for each color in node, look up via `buildHexToTokenMap`. Render as `color: var(--ds-color-spl-brown);  /* #854236 */` (CSS), `UIColor(named: "Spl/Brown")  // #854236` (iOS), `<color name="spl_brown">#854236</color>` (Android).
- For TEXT atomics, snippet uses **the override's value** when present (engineer copies "Buy now", not "Buy") — implements R6's "engineer copies the live text".

**Patterns to follow:**
- `lib/tokens/loader.ts:flattenColorTokens` — reuse, don't fork.
- `app/atlas/_lib/leafcanvas.tsx:LeafInspector` — existing sidebar shape (tabs, scroll, sticky header).

**Test scenarios:**
- **Happy path** click TEXT atomic: inspector shows font/size/weight/color snippet + token name (e.g. `spl-brown`).
- Covers AE9. **Happy path** TEXT with active override: snippet shows override value, not original.
- **Edge** color hex not in token registry: snippet shows raw hex with `// no token match` comment.
- **Edge** click container (FRAME) inside the screen frame: pass-through to nearest atomic.
- **Integration** with selection store: deselecting via Esc clears the inspector.

**Verification:**
- AE1: click "Buy" CTA → inspector shows Basier Circle Semibold 14px / line-height 20px / color `#0a84ff` (or token name) with copyable CSS / iOS / Android snippets.

---

- U8. **Inline text editor (contenteditable + debounced PUT)**

**Goal:** Double-click a TEXT atomic → activates `contenteditable`; edit triggers autolayout reflow (free, via D3a); blur (or 500 ms debounce) PUTs to U2's override endpoint; save-state UI mirrors DRD's `idle | saving | saved | conflict | error`.

**Requirements:** R3, R4

**Dependencies:** U2, U6, U7

**Files:**
- Create: `app/atlas/_lib/leafcanvas-v2/InlineTextEditor.tsx`
- Modify: `lib/projects/client.ts` — add `fetchTextOverrides`, `putTextOverride`, `deleteTextOverride`, `bulkUpsertTextOverrides`
- Modify: `lib/atlas/live-store.ts` — `LeafSlot.overrides` map + reset action
- Modify: `lib/atlas/data-adapters.ts` — `fetchLeafOverlays` includes overrides

**Approach:**
- Double-click handler upgrades the rendered `<span>` to `contenteditable=true` with cursor at click point. Esc / blur / click-outside commits.
- Save state machine same as `AtlasDRDEditor.tsx`: 500 ms debounce on `oninput`; `idle` → `saving` → `saved` → `idle` (1.2 s flash); 409 → `conflict` (banner: "Server has newer revision; refresh to see").
- Conflict resolution: refresh action calls `fetchTextOverrides` again, replays editor with new revision (last-write-wins).
- Reset-to-original button in inspector: calls `deleteTextOverride`; visually removes the `[overridden]` badge.

**Patterns to follow:**
- `app/atlas/_lib/AtlasDRDEditor.tsx` — save-state machine (do NOT bring BlockNote / Yjs).
- `lib/projects/client.ts:putDRD` — `ApiResult<DRDPutResult>` discriminated union shape.

**Test scenarios:**
- **Happy path** type "Buy" → "Buy now"; blur; PUT fires; activity feed shows event; canvas reflects new text. Covers AE2.
- **Edge** type then Esc: reverts to original. (Keyboard convention.)
- **Edge** blur with no change: no PUT.
- **Integration** with autolayout: parent of edited TEXT has `layoutMode: HORIZONTAL`; sibling icons reflow per browser flexbox (validated by spike). Covers D3a.
- **Error path** PUT 409: state flips to `conflict`; user prompted to refresh.
- **Edge** double-click on RECTANGLE / cluster (non-TEXT): no-op (not editable in v1 per D2).
- **Happy path** Reset to original: override deleted, canvas re-paints with original text. Covers AE5.

**Verification:**
- Edit text → see autolayout siblings reflow on the canvas in real time, exactly like the spike's measured behaviour.
- Save badge briefly shows "Saved" 500 ms after blur.
- 409 banner appears on a controlled stale-revision attempt.

---

- U9. **Bulk asset export UI (lasso + multi-select + zip download)**

**Goal:** Designer can multi-select atomic children (Shift-click or lasso) and export selection as a zip via U5's bulk endpoint. Single export already lives in U7's inspector.

**Requirements:** R5

**Dependencies:** U5, U7

**Files:**
- Create: `app/atlas/_lib/leafcanvas-v2/BulkExportPanel.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` — selection rectangle overlay + lasso state
- Modify: `lib/projects/client.ts` — `mintBulkExportURL`, `triggerBulkDownload`

**Approach:**
- Selection state in `live-store.ts`: `selectedAtomicChildren: Set<{ screenID, figmaNodeID }>`. Shift-click adds; lasso drag overlays a rectangle and adds intersected atomics.
- Floating "Export selection (N)" button when `size > 0`; click triggers `mintBulkExportURL` (POST → `download_url`). Browser navigates / triggers download.
- Filenames preview before zipping (small popover): each row editable; default `<flow-slug>__<name>.<ext>`.

**Patterns to follow:**
- `app/atlas/_lib/leafcanvas.tsx` — pan/zoom selection rectangle pattern.

**Test scenarios:**
- Covers AE4. **Happy path** Shift-click 6 icons; click "Export selection"; zip downloads with 6 SVGs at correct names.
- **Edge** Lasso intersecting non-atomic (FRAME): doesn't include the FRAME, only its atomic descendants visible in the lasso.
- **Edge** 100 selected: bulk endpoint serves; UI doesn't freeze.
- **Edge** Cancel mid-zip: download aborts cleanly.
- **Integration** filename collision (two icons with same name): server appends `-1`, `-2`.

**Verification:**
- AE4 reproduces end-to-end.

---

- U10. **Search-index + activity-feed integration for overrides**

**Goal:** Override writes propagate to `search_index_fts` (so AE-11 works) and emit `audit_log` rows (so the existing flow activity tab surfaces them). All via existing infra; no new index, no new feed.

**Requirements:** R7

**Dependencies:** U2

**Files:**
- Modify: `services/ds-service/internal/projects/search_index.go` — add `text_override` entity-kind builder
- Modify: `services/ds-service/internal/projects/screen_overrides.go` — call `UpsertSearchIndexRows` for the override on commit (deferred via worker queue per Phase 6 flush rule)
- Modify: `services/ds-service/internal/projects/auditlog.go` — small helper `WriteOverrideEvent(tx, ...)` to standardise `details.flow_id` shape
- Modify: `lib/atlas/data-adapters.ts:auditLogToActivity` — add `override.text.set | .reset | .orphaned | .bulk_set` cases to `activityKindOf` + `activitySentenceOf`
- Test: `services/ds-service/internal/projects/search_index_test.go` — new entity scenarios

**Approach:**
- Override-write path enqueues a flush worker (existing `RebuildGraphIndex` infra); worker calls `BuildSearchRowsForTenant` with override-resolved text; `UpsertSearchIndexRows` writes; SSE event `search.reindexed` fires AFTER the write tx commits (mirrors Phase 6's `GraphIndexUpdated` flush).
- Override-resolved text injection: `BuildSearchRowsForTenant` augments TEXT entity rows with the override value when present (priority: override > Figma original).
- `details.flow_id` populated in every audit_log row: pass through from `screen_overrides_handler.go`.
- Activity sentence shape: `Sahaj edited "Buy" → "Buy now" on Order ticket`.

**Patterns to follow:**
- `services/ds-service/internal/projects/search_index.go:BuildSearchRowsForTenant` — entity-kind builders.
- `docs/solutions/2026-05-01-001-phase-6-closure.md` — flush-driven SSE event timing.
- `lib/atlas/data-adapters.ts:auditLogToActivity` — sentence-of mapping pattern.

**Test scenarios:**
- Covers AE11. **Happy path** PUT override → search index has an FTS row with override value; query for the original returns this screen no longer (override beats original).
- **Integration** activity tab shows event with old → new diff after a PUT.
- **Edge** override on screen with no flow context: defensive — emit event without `flow_id` (still appears in tenant-wide audit; just not in flow activity tab).
- **Race** flush event fires AFTER tx commit (per learning #3); test simulates `Search` query right after PUT — must hit indexed row, not stale.

**Verification:**
- ⌘K search for `"Buy now"` finds the screen (AE-11). Searching `"Buy"` no longer surfaces it.
- Activity tab in the leaf inspector shows the new event after a PUT — verified end-to-end via the existing `HandleFlowActivity` query.

---

- U11. **Per-leaf "Copy overrides" inspector tab**

**Goal:** New tab in the leaf inspector listing all active + orphaned overrides for the open leaf. Sortable by who/when. One-click reset. Drag-to-reattach for orphans (manual recovery from AE-8 fallback case).

**Requirements:** R7

**Dependencies:** U2, U7, U8

**Files:**
- Create: `app/atlas/_lib/leafcanvas-v2/CopyOverridesTab.tsx`
- Modify: `app/atlas/_lib/leafcanvas.tsx:LeafInspector` — register the new tab into the existing tabs row

**Approach:**
- Fetches via `fetchTextOverrides(leafID)` (new in U8) returning all overrides scoped to the leaf's screens.
- UI: virtualized list (use existing IntersectionObserver pattern from `lib/use-active-section.ts`); each row shows screen name, original, current, last_edited, status badge.
- Drag-to-reattach: dragging an orphaned row onto an atomic in the canvas calls `putTextOverride` with the dragged-over node's `figma_node_id` and the orphan's value; old override removed.

**Patterns to follow:**
- `app/atlas/_lib/leafcanvas.tsx:LeafInspector` — tab integration.

**Test scenarios:**
- **Happy path** list contains all active + orphaned overrides for the leaf; sort by `updated_at` desc.
- **Happy path** click "Reset" inline → calls DELETE; row disappears.
- **Edge** drag orphan onto a TEXT atomic → re-attach succeeds; orphan disappears, new active row appears.
- **Edge** drag orphan onto a non-TEXT atomic → no-op (cursor shows "no-drop").
- **Integration** live updates via SSE: another user edits an override, this tab refreshes within 1s.

**Verification:**
- PM scenario: open leaf → switch to Copy Overrides tab → see 14 overrides from a recent CSV upload (covers AE10 fan-out).

---

- U12. **CSV bulk export/import for translation / PM review**

**Goal:** Designer/PM exports all leaf copy as CSV; edits in Sheets/Excel; uploads back; conflicts surface in a confirmation dialog before applying.

**Requirements:** R8

**Dependencies:** U2 (bulk endpoint), U10 (audit / search reindex), U11 (UI integration)

**Files:**
- Modify: `services/ds-service/internal/projects/screen_overrides_handler.go` — add `HandleCSVExport`, `HandleCSVImport`
- Modify: `services/ds-service/cmd/server/main.go` — register routes:
  - `GET  /v1/projects/{slug}/leaves/{leaf_id}/text-overrides/csv`
  - `POST /v1/projects/{slug}/leaves/{leaf_id}/text-overrides/csv` (multipart upload)
- Modify: `app/atlas/_lib/leafcanvas-v2/CopyOverridesTab.tsx` — Export / Import buttons + confirmation modal

**Approach:**
- CSV columns: `screen | screen_id | node_path | figma_node_id | original | current | last_edited_by | last_edited_at`.
- Export: server reads canonical_tree per screen, walks TEXT nodes, joins with `screen_text_overrides` to populate `current` (defaulting to `original` if no override), streams CSV.
- Import: parse rows; for each row whose `current` differs from `original`, dispatch as a bulk override (uses U2's `BulkUpsertOverrides`). Resolve `figma_node_id` from CSV first; fall back to canonical_path.
- Conflict detection: if a CSV row's `last_edited_at` is older than the current DB override's `updated_at`, surface in a confirm-before-apply modal listing each conflict.

**Patterns to follow:**
- `services/ds-service/internal/projects/server.go:HandleBulkAcknowledge` — bulk-tx + bulk_id pattern.
- `docs/solutions/2026-05-01-003-phase-7-8-closure.md` — read source rows BEFORE write tx (avoid deadlock under contention).

**Test scenarios:**
- Covers AE10. **Happy path** export → edit 14 rows → import → 14 audit events with shared `bulk_id`; canvas reflects all 14 changes; activity tab shows 14 entries.
- **Edge** import with 1 row that conflicts (DB updated since CSV export): confirm-before-apply lists that row; user can apply / skip per row.
- **Edge** import with malformed CSV: 400 with line-level errors.
- **Edge** import 1000 rows: chunks of 100 per bulk call; transactional integrity per chunk; total time <30s on local.
- **Integration** with search reindex: after import, ⌘K finds all 14 new strings.

**Verification:**
- AE10 reproduces end-to-end.

---

- U13. **Render polish: font, default text color, visible filter follow-up, image fills**

**Goal:** Land the rendering polish that brings the canvas from "structurally correct" to "designer-acceptable". Embed Basier Circle (or document Inter fallback decision), default text color when `fills` is empty, finalise visibility filter edge cases, image fill rendering.

**Requirements:** R10

**Dependencies:** U6 (renderer must exist)

**Files:**
- Create: `public/fonts/BasierCircle-Regular.ttf` (and Medium / Semibold / Bold)
- Modify: `app/atlas/_lib/leafcanvas-v2/__styles__/canvas.css` — `@font-face` declarations
- Modify: `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts` — default `color: #000` when `fills` is empty; image-fill `background-image` resolution
- Modify: `app/atlas/_lib/leafcanvas-v2/visible-filter.ts` — final-pass edge cases (e.g. nodes with `opacity: 0` treated like `visible: false`)

**Approach:**
- Pre-condition: PM / legal sign-off on Basier Circle web-embedding licence. If sign-off not yet, ship Inter fallback and document the ~2 px width tolerance (R10).
- `@font-face` declarations cover Regular (400), Medium (500), Semibold (600), Bold (700) — italics added on demand.
- Default text color when `fills` array missing or empty → `#000`. (Spike confirmed: white-on-white was a fidelity bug.)
- Image fills: lookup from `live-store.leafSlot.imageRefs`; fetch missing refs lazily via `/v1/files/{file}/images` proxied through ds-service.

**Patterns to follow:**
- `app/atlas/_styles/atlas.css` — `@font-face` style co-location.
- `services/ds-service/internal/projects/figma_proxy.go` — image-ref proxy if not already there.

**Test scenarios:**
- **Happy path** TEXT renders in Basier Circle Semibold; falls back to Inter when font isn't loaded yet.
- **Edge** TEXT with no `fills` → renders black (not invisible).
- **Edge** TEXT with `opacity: 0`: filtered like `visible: false`.
- **Integration** image fill on a profile-photo ELLIPSE: loads from cached image-ref, renders as `background-image`.

**Verification:**
- Side-by-side with Figma's official PNG render shows ≤2 px text-width drift on a sample of 20 hand-picked TEXT nodes (acceptable per R10).

---

- U14. **Co-positioned design state filter + state picker UI**

**Goal:** When a frame contains 2+ children at the same `(x, y, w, h)` (spike-found gap: "Slider Dash2" + "Frame 1321320555" stacked at the same coord), surface a per-frame state-picker UI. Default-select one (first in DOM order); user can switch states.

**Requirements:** R9

**Dependencies:** U6 (visible-filter already tags candidate state groups via `data-state-group` attribute)

**Files:**
- Create: `app/atlas/_lib/leafcanvas-v2/StatePicker.tsx`
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` — collects `data-state-group` attributes per frame, renders state picker overlay when set ≥ 2

**Approach:**
- Detection heuristic (in `visible-filter.ts`): two siblings share `(x, y, w, h)` *and* are not flagged hidden → tag `data-state-group=<groupKey>`.
- `groupKey` derived from `(x, y, w, h)` rounded.
- StatePicker overlay: floating chip on the frame top showing each state's name; click switches state.
- Active state stored in `live-store.ts:activeStatesByFrame: Map<frameID, stateGroupKey → activeStateID>`.
- Other states render with `display: none` until picked. (Visibility filter handles this by reading the live-store map.)

**Execution note:** **Heuristic refinement is execution-time discovery.** Initial detection is purely geometric. Real-world data may need name-based heuristics (e.g. "ALT", "Open", "Selected") — defer that until v1 ships and we see real frames.

**Patterns to follow:**
- `app/atlas/_lib/atlas.tsx:Tweaks panel` — floating overlay convention.

**Test scenarios:**
- **Happy path** frame with 2 children at same coords → state picker visible; clicking state 2 hides state 1, shows state 2.
- **Edge** 3 stacked children → 3-option picker.
- **Edge** no co-positioned children → no picker rendered.
- **Edge** state-group key collision across different frames: scoped by frame ID, no cross-talk.

**Verification:**
- The ChatBot file's `2882:56419` (which had multiple stacked tab-row variants in the spike) now shows a state picker; clicking through cycles through them visibly.

---

- U15. **Documentation, deploy, post-implementation `/ce-compound`**

**Goal:** Surface the canvas v2 to designers (changelog, runbook), deploy to Fly + Vercel, capture institutional learnings.

**Requirements:** All R*

**Dependencies:** all U*

**Files:**
- Create: `docs/solutions/2026-05-XX-001-zeplin-canvas-learnings.md` (post-`/ce-compound` from this work)
- Modify: `README.md` — section linking to the new canvas
- Modify: `fly.toml` if any new env vars are required (e.g., `BASIER_CIRCLE_LICENSED=1` toggle)

**Approach:**
- Run `/ce-compound` once U1-U14 ship, capturing learnings on:
  - Override storage & 3-tier re-attachment (closes the 0017 partial-state learning gap).
  - Co-positioned design state heuristics (will inform other rendering work).
  - Asset cache + per-tenant rate-limit for Figma exports.
- Update designer-facing changelog with screenshots + GIF of edit-with-reflow flow.

**Test scenarios:**
- Test expectation: none — pure docs.

**Verification:**
- `docs/solutions/` has a new entry; `README.md` lists the new canvas surface.

---

## System-Wide Impact

- **Interaction graph:** override write → audit_log + search worker; search worker → SSE `search.reindexed`; `HandleFlowActivity` query joins audit_log; canvas-side selection store drives inspector + bulk export. New cross-cutting paths flow through existing surfaces, not parallel ones.
- **Error propagation:** override 409 surfaces in editor save-state; bulk export rate-limit 429 surfaces in BulkExportPanel toast; canonical_tree fetch 404 (no tree built yet) falls back to PNG in `real-data-bridge.ts`.
- **State lifecycle risks:**
  - **Override re-attachment must be in-tx with canonical_tree write** — otherwise re-import races leave overrides stale (Learning #3).
  - **Asset cache invalidation on `version_index` change** — re-keyed entries naturally invalidate.
  - **CSV bulk import deadlock** — read source rows BEFORE opening tx (Learning #4).
- **API surface parity:** new endpoints registered with the same `s.requireAuth(projects.AdaptAuthMiddleware(...))` wrapper as existing routes; tenant scoping uniform; cross-tenant lookups → 404.
- **Integration coverage:** end-to-end scenarios:
  - PUT override → audit_log → search reindex → ⌘K finds → activity tab shows (U2 + U10).
  - Edit text → autolayout reflow on canvas → save → re-import → still attached (U6 + U8 + U3).
  - Multi-select 50 icons → bulk zip → file naming OK (U6 + U9 + U5).
- **Unchanged invariants:**
  - Brain-graph counts and product node aggregation: unchanged. Overrides do NOT affect counts.
  - DRD editor surface: unchanged. (Canvas v2 does NOT use BlockNote/Yjs.)
  - Existing `<window.PhoneFrame>` PNG path: preserved as fallback when canonical_tree is null.
  - Auth, tenant resolution, rate limiter: reused as-is.
  - `lib/tokens/loader.ts`: extended via additive helper; no breaking change.
  - `frame_resolve.go`'s sheet-sync flow: unchanged. Pipeline Stage 6 extension only adds re-attachment logic.

---

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Pixel-perfect parity drift on certain frame classes (gradients, shadows, masks, blendMode) | Iterative; v1 ships when 5 hand-picked complex frames pass designer review. Per-bug PRs after launch. (Deferred to Implementation #1.) |
| Co-positioned state filter mis-detection on edge frames | U14 ships geometric heuristic; refinement defers to execution-time discovery. UI is a state picker (visible), so user can override the heuristic any time. |
| Inter→Basier Circle font metric drift causing layout overflow | R10 + U13: ship Inter fallback first, swap to Basier Circle when licensing clears (single follow-up PR; no architecture change). |
| Override 3-tier re-attachment ambiguity in fingerprint stage | U3 marks ambiguous matches `orphaned`; U11's Copy Overrides tab surfaces them for manual reattachment. |
| Asset bulk export saturating Figma rate limits | U4 builds per-tenant 5 req/sec token-bucket from day one (Learning #8). |
| SQLite single-writer deadlock on CSV bulk import | U12 reads source rows BEFORE opening write tx (Learning #4). |
| SSE event fired before search reindex flushes | U10 emits SSE after worker write commits (Learning #3). |
| `app/atlas/_lib/*` is `// @ts-nocheck` — accidental drift to non-strict TS in new code | New code lives in `app/atlas/_lib/leafcanvas-v2/` strict-TS subdir. CI tsc check covers it (no `// @ts-nocheck`). |
| Overrides leaking into Figma file via plugin write-back | Out of scope for v1 (Scope Boundaries). Plugin code unchanged in this plan. |
| 0017 partial-state migration learning still uncaptured | U15: run `/ce-compound` to backfill. |

---

## Documentation / Operational Notes

- **Designer-facing**: changelog + GIF of edit-with-reflow flow; post in `#design` Slack.
- **Engineer-facing**: README section explaining where overrides live and how `?at=<token>` asset URLs work.
- **Ops**: monitor `audit_log` event_type counts for `override.text.*` to detect anomalies; alert on `override.text.orphaned` rate (sustained spike = re-attachment heuristic regressed).
- **Migration ordering**: 0018 + 0019 are pure-DDL; safe with the existing `schema_migrations` runner. No partial-state risk.
- **Rollout**: feature-flag the canvas v2 swap on a per-tenant basis via `NEXT_PUBLIC_LEAFCANVAS_V2=1` env var until designer sign-off; the PNG fallback keeps existing tenants unaffected.

---

## Sources & References

- **Origin document**: `docs/brainstorms/2026-05-05-zeplin-grade-leaf-canvas-requirements.md`
- Spike artefacts (reference, not promoted): `/tmp/spike/` (Python tree-walker, 4 HTML revisions, side-by-side comparison PNGs)
- DRD precedent: `services/ds-service/internal/projects/server.go:1082-1163`, `app/atlas/_lib/AtlasDRDEditor.tsx`
- Search index: `services/ds-service/internal/projects/search_index.go`, `migrations/0010_admin_acl_search.up.sql:101-109`
- Figma proxy precedent: `services/ds-service/internal/projects/figma_proxy.go`, `pipeline.go:629:renderChunk`
- canonical_tree: `services/ds-service/migrations/0016_canonical_tree_gz.up.sql`, `internal/projects/canonical_tree.go`
- Tokens: `lib/tokens/loader.ts`, `lib/tokens/indmoney/*.tokens.json`
- Solutions: `docs/solutions/2026-04-30-001-projects-phase-1-learnings.md`, `docs/solutions/2026-05-01-001-phase-6-closure.md`, `docs/solutions/2026-05-01-003-phase-7-8-closure.md`, `docs/solutions/2026-05-02-001-phase-5-collab-learnings.md`, `docs/solutions/2026-05-02-003-phase-5-2-collab-polish.md`
