---
title: "feat: replace figma_node with per-section zstd-compressed subtree blobs"
type: refactor
status: active
date: 2026-05-14
---

# Replace figma_node with per-section zstd-compressed subtree blobs

## Overview

Replace the row-per-Figma-node `figma_node` table (migration 0027, Phase 2C deep-tree crawl) with two columns on `figma_section`: `subtree_json_zstd BLOB` and `subtree_node_count INTEGER`. Both consumers that actually use this data — the U4 subtree hash function and the U10 ExportRequest builder in `docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md` — operate over a single section's descendants at a time, so the row-per-node addressing model and its five indexes are paying ~13GB to support no observed query shape. The blob model mirrors the proven `screen_canonical_trees` pattern from migration 0022 (287 rows × 65KB compressed = 18MB).

The local DB was wiped to 836KB earlier today; this plan ships a cold cutover (no coexistence, no backfill) in a single PR. After re-poll the DB is expected to land at < 100MB total.

This plan **amends** `docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md`:
- **U10** ("AutoSyncPlanner.Execute — full_export path") — its "build ExportRequest from `figma_node` rows" wording is superseded; see U5 below.
- **U13 / U14 admin UI** (already deferred in 001) — permanently removed by U6 below, not re-introduced.

---

## Problem Frame

The Phase 2C deep crawl reached **26.89 million `figma_node` rows × 5 indexes = ~13GB** on the local DB before today's wipe. Per-table size in the pre-wipe backup:

| Source | Size |
|---|---|
| `figma_node` table | 4.8 GB (26.89M rows) |
| `idx_figma_node_parent` | 2.1 GB |
| `sqlite_autoindex_figma_node_1` (PK) | 2.1 GB |
| `idx_figma_node_file_depth` | 1.8 GB |
| `idx_figma_node_name` | 1.4 GB |
| `idx_figma_node_type` | 1.2 GB |

Production scale (more tenants, more files, more depth-14 leaves inside icons) would explode further. The pattern that filled this storage — one row per node at depth ≤ 14 with five secondary indexes — does not match the access pattern callers actually have.

**What callers actually do.** Two real consumers exist:

1. **U4 subtree hash function** (`figma_hash.go::ComputeSubtreeHashes` from plan 001). Operates over the in-memory flat-node list the poller already produces. Does not query `figma_node` row-by-row; the function takes `[]FigmaNodeRow` as a parameter.
2. **U10 ExportRequest builder** (autosync plan, not yet implemented). For a section flagged `full_export`, walks the section's subtree once, filters to FRAME/COMPONENT/INSTANCE leaves ≥ 280×80, emits `frames[]` for `ExportRequest`. Single-section scope, walked in tree order.

**What callers don't do.** The five admin-UI query functions (`ListFigmaNodesForFile`, `ListComponentUsage`, `ListComponentUsageDetail`, `CountFigmaNodesByFile`, plus the unused `GetFigmaInventoryTree` figma_node descent) have zero observed user demand — the user explicitly confirmed they have not opened any of these panels in the admin UI since Phase 2C shipped. The Phase 2C cross-file component-usage analytics was speculative and is being dropped.

The bulk of the rows — VECTOR / RECTANGLE / ELLIPSE / BOOLEAN_OPERATION leaves inside icon and decorative subtrees — are never read individually by the autosync planner (its 280×80 filter stops at the FRAME boundary) and never queried by anyone else. They exist only as inputs to the hash function, which already consumes them in-memory and could continue to do so without their landing in the DB at all.

---

## Requirements Trace

- **R1.** Per-section subtree storage on `figma_section` via two new columns: `subtree_json_zstd BLOB` + `subtree_node_count INTEGER`.
- **R2.** Poller deep-sync writes each section's full descendant list (the same `[]FigmaNodeRow` it already produces in-memory) to the blob column. No write to `figma_node`.
- **R3.** AutoSyncPlanner reads the blob, decompresses, walks in Go to build `ExportRequest`. No SQL recursive walk over figma_node.
- **R4.** The U4 subtree hash function from plan 001 produces **identical** output bytes pre- and post-redesign for any given subtree. Operates on the in-memory struct list, not the blob.
- **R5.** Cold cutover. Single PR. No coexistence period, no feature flag, no backfill. `figma_node` table dropped via second migration in the same release.
- **R6.** All five `figma_node`-consuming admin paths removed: repository methods, HTTP handlers, and React UI panels. No replacement endpoint, no replacement panel.
- **R7.** Post-redesign DB size on the local re-poll lands under 100MB total (down from 13GB).
- **R8.** Tenant-scoped throughout. The blob column carries `tenant_id` on its parent `figma_section` row; no new tenant-isolation surface.

---

## Scope Boundaries

- **No backfill of historical `figma_node` data.** The DB was wiped to 836KB earlier today. The next poll cycle re-populates everything in the new shape.
- **No new admin UI.** The autosync planner remains the only consumer of section subtree data. If cross-file component-usage analytics is ever requested by a real user, a much smaller `figma_component_master` table (COMPONENT + COMPONENT_SET rows only, a few thousand max) can reconstruct it — not in scope here.
- **No change to `figma_section`'s existing `content_hash` / `position_hash` columns** added by migration 0028. The hash function's inputs and output bytes do not change.
- **No coexistence of figma_node and the blob.** Both migrations land in the same PR in strict order: 0030 adds blob columns before any code reads them; 0031 drops `figma_node` after all callers are gone.
- **No new compression scheme.** Reuse the existing `CompressTreeZstd` / `DecompressTreeZstd` helpers from `services/ds-service/internal/projects/canonical_tree.go` (zstd via `github.com/klauspost/compress/zstd`). No new dependency.
- **No change to `FileDeepTree.Flatten()` in `services/ds-service/internal/figma/client/client.go`.** The poller still receives the same flat node list; only the *write path* downstream of it changes.
- **No change to webhook handling, page classification, persona derivation, project mapping, or any other autosync-plan-001 concern outside U10.**

---

## Context & Research

### Relevant Code and Patterns

- **zstd helpers** — `services/ds-service/internal/projects/canonical_tree.go`: `CompressTreeZstd(raw string) ([]byte, error)` and `DecompressTreeZstd(blob []byte) (string, error)`. Used today for `screen_canonical_trees.canonical_tree_zstd`. **Direct reuse** — they take/return string, so the per-section serializer marshals JSON to string, compresses, and the reader does the inverse.
- **zstd blob migration pattern** — `services/ds-service/migrations/0022_canonical_tree_zstd.up.sql`. Adds a `BLOB` column alongside the existing TEXT column, no `NOT NULL` (population happens post-migration on the next write). Mirror exactly for migration 0030.
- **Per-section flat-node source** — `services/ds-service/internal/figma/inventory/poller.go:467` `resp.Flatten()` returns the flat node list. **No change to this call site** — only the downstream write changes.
- **Flat-node type** — `services/ds-service/internal/figma/client/client.go` `FileDeepTree.Flatten()` returns `[]FlatNode`. The poller maps it to `[]FigmaNodeRow` (the repository's type). Both shapes are already in use by the hash function (plan 001 U4).
- **Existing `UpsertFigmaPagesAndSections` write path** — `services/ds-service/internal/projects/repository_figma_inventory.go:604`. Takes the in-memory flat-node list and writes `figma_page` + `figma_section` rows in one tx. Extends naturally with a per-section blob write inside the same tx.
- **Existing `UpsertFigmaNodes`** — `services/ds-service/internal/projects/repository_figma_inventory.go:951`. **Deleted** by U4 below.
- **Dead admin code (deleted by U6)**:
  - `services/ds-service/internal/projects/repository_figma_inventory.go`: `ListFigmaNodesForFile` (1131), `ListComponentUsage` (1281), `ListComponentUsageDetail` (1342), `CountFigmaNodesByFile` (1392).
  - `services/ds-service/internal/projects/server_figma_inventory_admin.go`: handlers at 215, 247, 282 plus their route registrations.
  - `app/atlas/figma-inventory/_components/ComponentsPanel.tsx` and the "Nodes" column in `FilesTable.tsx`.
- **AutoSyncPlanner location** — `services/ds-service/internal/figma/inventory/autosync_planner.go` (created by plan 001 Phase B U7; Execute path U10 not yet implemented).

### Institutional Learnings

- **Read source rows BEFORE opening the SQLite write tx** — `docs/solutions/2026-05-01-003-phase-7-8-closure.md`. Applies to U5 (planner reads blob before any tx) but the poller write path (U4) is unchanged in shape — still one tx per file.
- **Tenant-scoped denormalized `tenant_id` on every table** — same source. The blob lives on `figma_section`, which already carries `tenant_id`. No new tenant-isolation surface.
- **`schema_migrations` records each `.up.sql` independently** — `services/ds-service/internal/db/migrations.go`. The runner sorts by version prefix and applies any not yet recorded. 0030 and 0031 land sequentially on next process start.

### External References

- [klauspost/compress/zstd](https://pkg.go.dev/github.com/klauspost/compress/zstd) — the dependency `canonical_tree.go` already uses. No version bump.
- [SQLite ALTER TABLE](https://www.sqlite.org/lang_altertable.html) — `ADD COLUMN` is supported and additive; `DROP TABLE` cascades drop of all indexes on that table.

---

## Key Technical Decisions

- **Hash inputs are unchanged.** The U4 hash function from plan 001 reads `[]FigmaNodeRow` from memory. The flat-node list it receives is the *same list* the poller already produces via `FileDeepTree.Flatten()`. We do not hash the blob bytes; we hash the struct list. This guarantees R4 (identical hash output bytes pre/post-redesign) and is cheaper than serialize-then-hash. The blob is a sibling output, not a hash input.
- **Two migrations, single PR, strict order.** Migration 0030 adds the columns; the same PR's code changes start writing blobs; migration 0031 drops `figma_node`. The runner applies them in sequence on next startup. No code path between 0030 and 0031 reads `figma_node`, so the drop is safe.
- **JSON, not Protobuf or msgpack.** The per-section payload is small (~5KB compressed per section); JSON serialization cost is negligible against the network and DB write cost upstream. Matches `screen_canonical_trees` precedent — same zstd dictionary advantages apply.
- **Reuse `CompressTreeZstd` / `DecompressTreeZstd` directly.** These take/return `string`. The serializer is `json.Marshal(nodes) -> string -> CompressTreeZstd`. The reader is `DecompressTreeZstd -> string -> json.Unmarshal -> []FigmaNodeRow`. No new helper file required; a thin wrapper lives alongside the repository methods.
- **Drop `figma_node` in one motion, not in two releases.** A "deprecate first, drop later" plan has carrying cost (two more migrations, two more PRs, monitoring dashboard for "is anyone still reading figma_node?"). Given the DB was just wiped, the local cost of a clean cutover is zero. Fly is already wiped from earlier in the session. The cold-cutover safety case rests on these two empty databases — the only environments where `figma_node` could carry orphaned consumer-data is in environments we know are empty.
- **Reject the "structural-rows-only" hybrid.** Considered keeping `figma_node` for FRAME/COMPONENT/COMPONENT_SET/INSTANCE/SECTION/GROUP/TEXT rows only and dropping VECTOR/RECTANGLE/etc. Estimated ~2GB instead of 13GB. Rejected because (a) the index overhead persists, (b) row-per-node schemas invite future column-creep (`fills_json`, `effects_json`), and (c) the hash function needs the *full* descendant set including shape leaves to detect icon-internal changes — splitting the write set from the hash set adds asymmetry. Blob storage is symmetric and bounded by section count, not node count.
- **No partial UNIQUE index needed on the new columns.** `figma_section` PK is `(tenant_id, file_key, section_id)` and is already enforced. The blob column has no uniqueness or NULL semantics worth indexing.
- **Schema version 0030 + 0031 land in the same release branch.** The reviewer sees one PR; the runner applies both on first startup after deploy. No "0030-only" intermediate state where `figma_node` exists alongside a populated blob column — the code that populates the blob also stops writing `figma_node` in the same commit set.

---

## Open Questions

### Resolved During Planning

- *Hash inputs — struct list or blob?* — Struct list. The blob is purely an output. Hash function signature does not change.
- *Single PR or staged rollout?* — Single PR with two migrations in strict order. Both target DBs (local + Fly) are empty, eliminating the usual coexistence-risk argument.
- *Compression library / level?* — Existing `CompressTreeZstd` (level not specified at call sites — uses the package default). No new dep, no new tuning.
- *Backfill?* — None. Next poll re-populates from Figma.
- *Component analytics replacement?* — None ships speculatively. If a real feature request lands, a tiny `figma_component_master` (COMPONENT + COMPONENT_SET rows, ~thousands) becomes a separate plan.

### Deferred to Implementation

- *Exact JSON field-name casing for the serialized FigmaNodeRow.* — Mirror the existing `FigmaNodeRow` Go struct's json tags. Verify no consumer of the blob assumes a different shape (there are no existing consumers yet — U5 is the first).
- *Whether to include `first_seen_at` / `last_seen_at` / `deleted_at` in the blob.* — Probably no — those are inventory-tracking metadata, not subtree-structural data. The autosync planner doesn't need them and excluding them shrinks the blob. Decide at implementation time after eyeballing the FigmaNodeRow type.
- *Whether to assert blob size limits.* — A pathological section (entire phone-screen background full of vectors) could produce a multi-MB blob even after zstd. Implementer adds a soft warning log if compressed size exceeds say 256KB; no hard limit in v1.

---

## Implementation Units

- U1. **Migration 0030 — add `subtree_json_zstd` BLOB + `subtree_node_count` INTEGER on `figma_section`**

**Goal:** Add two nullable columns to `figma_section`. Population happens on next poll (U4); the migration itself is a pure schema additive.

**Requirements:** R1

**Dependencies:** None.

**Files:**
- Create: `services/ds-service/migrations/0030_figma_section_subtree_blob.up.sql`

**Approach:**
- Two `ALTER TABLE figma_section ADD COLUMN` statements. Both nullable. No `DEFAULT`. No new index.
- Comment block at the top of the migration documenting the rationale (mirror autosync plan 001 + this plan reference), why no `NOT NULL`, why no index.

**Patterns to follow:**
- `services/ds-service/migrations/0022_canonical_tree_zstd.up.sql` — additive BLOB column pattern.

**Test scenarios:**
- *Happy path:* Migration applies cleanly on a fresh DB; `figma_section` has the two new columns after apply; existing rows have NULL in both.
- *Edge case:* Migration applies on a DB with non-empty `figma_section` rows (re-poll state) without rewriting them; existing rows keep NULL until next write.
- *Integration:* `schema_migrations` records version 29 after apply; `db.AppliedMigrations(ctx)` includes 29.

**Verification:** `sqlite3 ds.db .schema figma_section` shows the new columns after migration runs; existing `figma_section` rows preserved.

---

- U2. **Per-section subtree serialization + zstd compression helper**

**Goal:** Two functions: `EncodeSubtreeBlob(nodes []FigmaNodeRow) ([]byte, error)` and `DecodeSubtreeBlob(blob []byte) ([]FigmaNodeRow, error)`. Thin wrappers over JSON marshal/unmarshal + `CompressTreeZstd` / `DecompressTreeZstd`. Pure, no DB I/O.

**Requirements:** R2, R3

**Dependencies:** None (uses existing zstd helpers).

**Files:**
- Create: `services/ds-service/internal/projects/figma_section_subtree.go`
- Create: `services/ds-service/internal/projects/figma_section_subtree_test.go`

**Approach:**
- `EncodeSubtreeBlob`: `json.Marshal(nodes) -> string -> CompressTreeZstd(string) -> []byte`. Return any error from marshal/compress unwrapped.
- `DecodeSubtreeBlob`: inverse. Tolerate nil/empty blob → return empty slice, no error (an empty section is valid).
- Decide implementation-time whether to strip inventory-tracking fields (`first_seen_at`, `last_seen_at`, `deleted_at`) from the serialized struct via a stripped sibling type with selective json tags. If yes, document the contract in the file comment.

**Patterns to follow:**
- `services/ds-service/internal/projects/canonical_tree.go` — `CompressTreeZstd` / `DecompressTreeZstd` call style.

**Test scenarios:**
- *Happy path:* Round-trip: encode 100 mixed-type nodes, decode, get identical `[]FigmaNodeRow`. Field-by-field equality.
- *Happy path:* Determinism: encoding the same slice twice produces byte-identical blobs (depends on json.Marshal's key ordering — Go's encoding/json sorts map keys but our type is a struct slice, so deterministic by construction).
- *Edge case:* Empty slice → encode produces a non-zero-length blob (the JSON `[]` zstd-compressed); decode returns empty slice with no error.
- *Edge case:* Nil slice → encode treats as empty; decode of resulting blob returns non-nil empty slice.
- *Edge case:* Nil/zero-length input to `DecodeSubtreeBlob` → returns empty slice, no error.
- *Error path:* Corrupted blob bytes → `DecodeSubtreeBlob` returns an error (zstd decompression failure surfaces).
- *Error path:* Valid zstd blob whose decompressed payload is not valid JSON → returns an unmarshal error.
- *Integration:* A real section's subtree (~50 nodes from a fixture file) round-trips identically; compressed size is in the expected single-digit KB range.

**Verification:** Tests pass; package builds clean.

---

- U3. **Hash-invariance regression test (characterization-first)**

**Goal:** Lock in U4's hash output bytes for a representative subtree BEFORE U4's poller changes ship, so any accidental serializer drift in U2 or write-path change in U4 surfaces as a test failure rather than as silent corruption of `figma_section.content_hash`.

**Requirements:** R4

**Dependencies:** None on this plan (depends only on the existing `ComputeSubtreeHashes` from plan 001 U4, already shipped per memory observation 4506).

**Execution note:** Characterization-first. Capture the existing hash output on a representative `[]FigmaNodeRow` fixture into a `wantHash` constant. The test fails fast on the next iteration if anyone (including U4 of this plan) inadvertently changes hash inputs.

**Files:**
- Create: `services/ds-service/internal/projects/figma_hash_regression_test.go`
- Create: `services/ds-service/internal/projects/testdata/figma_section_subtree_fixture.json` (optional — can inline in the test source)

**Approach:**
- Build a synthetic `[]FigmaNodeRow` representing a realistic section subtree: ~30 nodes including FRAME, INSTANCE, TEXT, RECTANGLE, VECTOR; varied depths; varied order_index; some with non-nil componentId/component_key.
- Call `ComputeSubtreeHashes(rootNodeID, nodes)`; capture the returned content_hash + position_hash as constants in the test file.
- Assert: a re-run on the same fixture produces those exact bytes.
- Add a negative assertion: tweak one TEXT node's `name`, re-run, assert hash *changes*. (Sanity: the function is actually responsive to inputs.)

**Patterns to follow:**
- `services/ds-service/internal/projects/canonical_tree_test.go` if it has a similar zstd round-trip pattern.
- Existing hash test from plan 001 U4 (`figma_hash_test.go` — verify it exists and what it covers; this plan's regression test is *complementary*, not duplicative).

**Test scenarios:**
- *Happy path:* Fixture subtree → expected content_hash bytes (constant in test source); expected position_hash bytes (constant in test source).
- *Edge case:* Removing the deepest node from the fixture flips content_hash. Documented as a sanity check.
- *Edge case:* Changing only the root's x/y leaves content_hash untouched (the U4 contract says the section's own bbox is excluded from its content_hash); position_hash flips. Documented as a sanity check.

**Verification:** Test passes on current main (before any of this plan's other units land). The test is the canary — if U4 of this plan or any future change breaks hash invariance, this test fails first.

---

- U4. **Poller write path: serialize per-section subtree blob, stop writing `figma_node`**

**Goal:** Modify the poller's deep-sync write path to (a) group the in-memory flat-node list by nearest-section-ancestor, (b) for each section, encode via U2's `EncodeSubtreeBlob` and write the blob + node_count to the existing `figma_section` row in the same tx as the existing page/section upsert, (c) stop calling `UpsertFigmaNodes`. Delete `UpsertFigmaNodes` from the repository (it has no other caller).

**Requirements:** R2, R5

**Dependencies:** U1 (column must exist before write), U2 (serializer).

**Files:**
- Modify: `services/ds-service/internal/projects/repository_figma_inventory.go` — extend `UpsertFigmaPagesAndSections` to accept the per-section subtree map and write the blob columns; delete `UpsertFigmaNodes`, `SweepFigmaNodes` if present, and the `FigmaNodeRow` type if no consumer remains after U6.
- Modify: `services/ds-service/internal/figma/inventory/poller.go` — at the call site (line ~467 area), group `resp.Flatten()` output by nearest SECTION ancestor (walk parent_id chain; if a node has no SECTION ancestor inside the file, it belongs to its page only and is not blob-serialized). Pass the resulting `map[sectionID][]FigmaNodeRow` into the extended `UpsertFigmaPagesAndSections`.
- Modify: `services/ds-service/internal/projects/repository_figma_inventory_test.go` — update existing tests for the new `UpsertFigmaPagesAndSections` signature.

**Approach:**
- Build a `map[string][]FigmaNodeRow` keyed by section_id during a single pass over the flat list. A node's section ancestor is found by walking `parent_id` upward via an `id -> parent_id, type` index built once per file. Nodes whose ancestor chain hits a PAGE (`CANVAS` in Figma terms) without crossing a SECTION are dropped (they belong to the page level, not section level, and have no subtree consumer).
- Inside the existing `UpsertFigmaPagesAndSections` tx, after the existing section UPSERT, run a second statement per section: `UPDATE figma_section SET subtree_json_zstd = ?, subtree_node_count = ? WHERE tenant_id = ? AND file_key = ? AND section_id = ?`.
- Logging: warn-log (no abort) if a section's compressed blob exceeds 256KB. Counted into metrics so we can spot bloated sections.
- After this unit ships, no code path calls `UpsertFigmaNodes`. Delete it + its companion sweep + its `FigmaNodeRow` type (if U6's deletions remove the last reader). Cross-check before committing.

**Patterns to follow:**
- `services/ds-service/internal/projects/repository_figma_inventory.go::UpsertFigmaPagesAndSections` — existing tx shape, prepared statements, tenant_id injection.
- `docs/solutions/2026-05-01-003-phase-7-8-closure.md` — read-before-tx pattern (in U5; the poller write path is still one-tx-per-file).

**Test scenarios:**
- *Happy path:* Poll fixture file → `figma_section.subtree_json_zstd` populated for every section, `subtree_node_count` matches the descendant count, `figma_node` table is not written to (count remains 0 in the test DB).
- *Happy path:* Re-poll same fixture → blob is overwritten in place (no row count growth on figma_section).
- *Edge case:* Section with zero descendants → blob is the encoded-empty-slice blob (non-empty bytes), node_count = 0.
- *Edge case:* Node whose parent chain never hits a SECTION (page-level node) → not included in any section's blob; silently dropped from the per-section map. Documented in code comment.
- *Edge case:* Section's compressed blob > 256KB → warning logged with section_id + size; no abort.
- *Integration:* End-to-end poller run on a fixture file populates the blob; a downstream call to `DecodeSubtreeBlob` returns the same `[]FigmaNodeRow` that the in-memory walker built.
- *Integration:* Tenant isolation — poller for tenant A populates only tenant A's `figma_section` rows.

**Verification:** Tests pass. After a poll cycle on a real file, `SELECT COUNT(*) FROM figma_node` returns 0; `SELECT COUNT(*) FROM figma_section WHERE subtree_json_zstd IS NOT NULL` matches the number of sections in the file.

---

- U5. **AutoSyncPlanner section-subtree reader (amends plan 001 U10)**

**Goal:** Provide a function that the AutoSyncPlanner's Execute path (plan 001 U10) calls to materialize a section's `[]FigmaNodeRow` for ExportRequest construction. Replaces the SQL recursive walk over `figma_node` that plan 001 U10's approach text describes.

**Requirements:** R3

**Dependencies:** U1 (column), U2 (decoder), U4 (blob populated).

**Files:**
- Modify: `services/ds-service/internal/projects/repository_figma_autosync.go` — add `LoadSectionSubtree(ctx, fileKey, sectionID) ([]FigmaNodeRow, error)` on `TenantRepo`. Single SELECT against `figma_section.subtree_json_zstd`; call `DecodeSubtreeBlob`. Tenant-scoped.
- Modify: `services/ds-service/internal/projects/repository_figma_autosync_test.go`
- Modify: `docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md` U10 — update the **Approach** section's wording from "Build `ExportRequest` from `figma_node` rows (DB-only, no Figma API): walk the section subtree…" to reference `LoadSectionSubtree` instead. Pure documentation amendment; no code in plan 001 has shipped this part yet.

**Approach:**
- `LoadSectionSubtree` returns `(nodes, ErrNotFound)` when the section row exists but `subtree_json_zstd IS NULL` (a section that hasn't been deep-polled yet). The caller (AutoSyncPlanner.Execute) treats this as `skip_reason='subtree_not_synced'` — same shape as the autosync plan's existing skip semantics.
- `LoadSectionSubtree` returns `(nil, ErrNotFound)` when no `figma_section` row exists at all. Distinct from the unsynced case; surfaced via a different skip_reason at the planner layer (`section_not_found` — same as plan 001 already specifies for the `file_not_found` analogue).
- The function is the ONLY new read path. Plan 001 U10's Approach gets its wording updated to call this instead of "SELECT FROM figma_node WHERE …".

**Patterns to follow:**
- `services/ds-service/internal/projects/repository_figma_inventory.go::LookupFigmaFile` — TenantRepo single-row lookup style, ErrNotFound sentinel, force tenant_id.

**Test scenarios:**
- *Happy path:* Section row exists with a populated blob → returns the decoded `[]FigmaNodeRow`, same shape as the upstream encoder produced.
- *Happy path:* Round-trip with U4 — populate via the poller, read via `LoadSectionSubtree`, assert byte-identical slice contents (modulo field ordering preserved by encoder determinism).
- *Edge case:* Section row exists but blob is NULL (deep-sync hasn't happened yet) → returns `ErrNotFound` (or a specific sentinel — implementer chooses; document in code).
- *Edge case:* No section row at all (unknown section_id) → returns `ErrNotFound`.
- *Error path:* Blob is non-NULL but corrupted (manual DB mutation) → returns decompression/unmarshal error; caller logs at warn level.
- *Error path:* Empty tenant_id → returns error before any SQL runs.
- *Integration:* Tenant isolation — tenant A's `LoadSectionSubtree` for a section in tenant B's file returns `ErrNotFound`.

**Verification:** Tests pass. The function compiles into `repository_figma_autosync.go` and is callable from `internal/figma/inventory/autosync_planner.go` (no new package boundaries to wire).

---

- U6. **Delete dead admin-UI code: repository methods, HTTP handlers, React panels**

**Goal:** Permanently remove the five `figma_node`-consuming admin paths and their tests. After this unit, no code in `services/ds-service` or `app/atlas` references `figma_node`.

**Requirements:** R6

**Dependencies:** None on other units in this plan (U1-U5 don't depend on this code existing or being removed). Ordering relative to U4: U6 must complete *before* U7 (drop table) because U7's verification needs zero readers; ordering relative to the rest is flexible.

**Files:**
- Delete from `services/ds-service/internal/projects/repository_figma_inventory.go`: `ListFigmaNodesForFile`, `ListComponentUsage`, `ListComponentUsageDetail`, `CountFigmaNodesByFile`, and the `FigmaNodeView` + `ComponentUsageRow` + `ComponentUsageDetail` types if no other consumer.
- Delete from `services/ds-service/internal/projects/server_figma_inventory_admin.go`: the three handlers around lines 215, 247, 282 plus their route registrations and any imports they uniquely required.
- Delete file: `app/atlas/figma-inventory/_components/ComponentsPanel.tsx`.
- Modify: `app/atlas/figma-inventory/_components/FilesTable.tsx` — remove the "Nodes" column + its sort handler.
- Modify: `app/atlas/figma-inventory/page.tsx` — remove the ComponentsPanel render.
- Modify: any test files exercising the removed methods/endpoints/panels. Delete (don't skip) tests of code that no longer exists.
- Modify: `services/ds-service/cmd/server/main.go` — remove route registrations for the deleted handlers.

**Approach:**
- Single search-and-destroy pass. `grep -rn "ListFigmaNodesForFile\|ListComponentUsage\|ListComponentUsageDetail\|CountFigmaNodesByFile\|ComponentsPanel"` finds every reference; delete each in dependency order (test → handler → repo method → type).
- After deletion: `go build ./...` and `next build` (or the equivalent) must pass. Any remaining unused imports get removed.
- No replacement endpoints. The admin UI tab for "Components" stops existing entirely; if the team wants it back later, see this plan's Key Technical Decisions note about `figma_component_master`.

**Patterns to follow:** N/A — pure deletion.

**Test scenarios:**
- Test expectation: none — pure removal of code with no observed user demand. Verified by build success + by U7's `DROP TABLE figma_node` not surfacing any "table referenced by …" error.

**Verification:** `go build ./...` clean; `next build` clean; `grep -rn "figma_node\b" services/ds-service --include='*.go'` returns matches only in poller write-path internal helpers (which U4 either deleted or replaced) and migrations 0027 + 0031.

---

- U7. **Migration 0031 — DROP TABLE `figma_node`**

**Goal:** Remove the table and all its indexes. Run in the same release as 0030 and U4-U6.

**Requirements:** R5, R7

**Dependencies:** U1, U4, U5, U6 (no caller may remain).

**Files:**
- Create: `services/ds-service/migrations/0031_drop_figma_node.up.sql`

**Approach:**
- Single statement: `DROP TABLE IF EXISTS figma_node;`. SQLite cascades the drop of all 5 indexes automatically. No explicit `DROP INDEX` needed.
- Comment block at the top of the migration referencing this plan and the rationale (per-section blob replaces row-per-node; see migration 0030 + `figma_section.subtree_json_zstd`).
- No data backfill — figma_node was emptied during the pre-plan wipe.

**Patterns to follow:**
- `services/ds-service/migrations/0027_figma_node.up.sql` — same migration filename convention, header comment style.
- SQLite `DROP TABLE` semantics — cascades indexes; FK checks: `figma_auto_sync_state` (migration 0028) does NOT have an FK to figma_node (verified — autosync_state references file_key, not row IDs). No other table FKs into figma_node.

**Test scenarios:**
- *Happy path:* Migration applies on a DB with a populated `figma_section.subtree_json_zstd` set; `figma_node` is gone after apply; the 5 indexes are gone; existing `figma_section` rows + blob columns are untouched.
- *Integration:* `db.AppliedMigrations(ctx)` includes 29 and 30 after the release ships. `.tables` no longer lists `figma_node`. DB file size after `VACUUM` drops by ~13GB on any environment that still had the pre-wipe data (not the local DB which is already wiped, but documents the expected production behavior on first deploy).

**Verification:** `sqlite3 ds.db .tables` after migration shows no `figma_node`; `.indices` shows no `idx_figma_node_*`; `figma_section` blob columns intact.

---

## System-Wide Impact

- **Interaction graph:**
  - Upstream of figma_node writes: `internal/figma/inventory/poller.go::pollOne` calls `FileDeepTree.Flatten` then `UpsertFigmaPagesAndSections` (modified by U4). No other producer.
  - Downstream of figma_node reads (current): four admin paths (all deleted by U6).
  - Downstream of figma_node reads (future per plan 001 U10): AutoSyncPlanner.Execute — now reads the blob via U5's `LoadSectionSubtree`.
- **Error propagation:** Blob decode failures (corrupted bytes, unmarshal error) surface as `LoadSectionSubtree` errors. AutoSyncPlanner treats them as a skip with `error_message` preserved on `figma_auto_sync_state`, same shape as the existing autosync-state error path. Poller-side encode failures (which would only happen on json.Marshal exhaustion or zstd failure — neither expected on well-formed input) abort the per-file tx with a logged error; the rest of the poller loop continues to the next file.
- **State lifecycle risks:**
  - Re-poll partial failure: if the poller commits `figma_section` rows but crashes before the next file's batch, the surviving `figma_section.subtree_json_zstd` values are correct for the sections that wrote, and stale-but-correct for the sections that didn't (their last-good blob is still readable). The U4 hash function detects content change on the next successful poll and reconverges.
  - Migration crash between 0030 and 0031: SQLite migrations are per-tx, so 0030 either fully applied or not. If 0030 applied but 0031 didn't (process crash mid-deploy), the next start re-runs 0031. `DROP TABLE IF EXISTS` is idempotent. Safe.
- **API surface parity:** The deleted admin endpoints (`GET /v1/admin/figma-inventory/files/{key}/nodes`, `/components`, `/components/{key}/usage`) have no analog elsewhere; they were specific to the deleted UI panels. No other API exposes per-node data.
- **Integration coverage:** End-to-end poll → store → planner-read covered by U4 + U5 integration tests. The U10-implementation in plan 001 will exercise the full chain (planner triggers re-export, audit pipeline ingests) — out of scope for this plan but unlocked by it.
- **Unchanged invariants:**
  - The U4 hash function from plan 001 produces identical output bytes (R4, locked by U3).
  - `screen_canonical_trees` storage is untouched.
  - `figma_section.content_hash` + `position_hash` semantics from plan 001 U4 are preserved exactly.
  - The HTTP API surface for autosync admin (plan 001 U11/U17) is unchanged.
  - All existing routes for the audit pipeline (`POST /v1/projects/export` etc.) are unchanged.

---

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| **Hash drift** — U4 of this plan or any future serializer change accidentally feeds different bytes into `ComputeSubtreeHashes`, flipping `figma_section.content_hash` on every existing row and causing false-positive "everything changed" reports from AutoSyncPlanner. | U3 captures the hash output bytes for a fixture into a test constant *before* U4 lands. Any drift surfaces as a CI failure. Additionally, the Key Technical Decisions note explicitly states: hash from struct list, NOT from blob. |
| **Orphaned consumers of `figma_node`** — a code path the grep audit misses still queries the table, breaks at runtime after 0031. | U6's verification step grep is comprehensive (`services/ds-service` + `app/atlas`). `go build ./...` and `next build` catch any unresolved Go references. The remaining grep-only risk is dynamic SQL string-construction — none exists in this codebase per the U6 audit. |
| **Migration ordering** — 0030 must apply before any code reads the blob; 0031 must apply only after no caller queries `figma_node`. | Both migrations land in the same PR. The migration runner (`internal/db/migrations.go`) applies them in version order on next startup; code that reads the blob does not exist on the prior release. The PR is the unit of atomicity at the team's deploy granularity. |
| **Blob bloat on pathological sections** — a designer's "all in one canvas" section could compress to multi-MB. | U4 logs a warning when compressed size exceeds 256KB. The current poller already pulls these node trees via `FileDeepTree.Flatten` and holds them in memory — the blob doesn't change the memory profile, only the persistence cost. If a real bloat case shows up, U4's warning surfaces it and we can iterate (e.g., chunked storage or a per-section size cap). |
| **`figma_auto_sync_state` FK references `figma_node`** | Audited and false: `figma_auto_sync_state` references `file_key` (TEXT), not figma_node row IDs. Migration 0028 introduced no FK into figma_node. Safe. |
| **Plan 001 U10 still says "build ExportRequest from figma_node rows"** — implementer of U10 might wire the wrong call. | U5 explicitly amends plan 001 U10's Approach text. The amendment lands in the same PR as this plan's U5 implementation. Plan 001's U10 unit is not yet implemented per memory observation 4506 (Phase A shipped, Phases B and C pending). |
| **Production deploy with non-empty figma_node** | The local DB and Fly DB were both wiped as part of the prior conversation. Verify Fly is wiped before deploying this PR. If a third environment has data, a one-line wipe runs before deploy. |

---

## Documentation / Operational Notes

- **Migration applies on next startup**, no operator step required.
- **Local verification after merge:**
  1. Trigger one poll cycle on a real file (admin "Sync now" or wait for the next scheduled poll).
  2. `sqlite3 services/ds-service/data/ds.db "SELECT COUNT(*) FROM figma_section WHERE subtree_json_zstd IS NOT NULL"` — expect > 0.
  3. `sqlite3 services/ds-service/data/ds.db ".tables" | grep figma_node` — expect empty.
  4. `du -h services/ds-service/data/ds.db` — expect under 100MB after full re-poll of the seeded team.
- **Plan 001 U10 reference update is documentation-only.** The same PR's U5 commit edits the U10 unit of plan 001 in place, replacing the figma_node-walking approach text with a reference to `LoadSectionSubtree`. No code in plan 001 has shipped this part yet.
- **No rollback plan beyond `git revert`.** The cutover is clean and the migration is one-way (no `.down.sql` files used in this repo). If a critical post-deploy bug surfaces, revert the PR; the next deploy re-creates `figma_node` via 0027 (still present in migration history), and the next poll re-populates rows. The blob columns persist as NULL on `figma_section`, harmless.

---

## Sources & References

- **Sibling plan being amended:** [docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md](2026-05-14-001-feat-figma-db-autosync-bridge-plan.md) — U10 (Execute path) wording updated by this plan's U5.
- **Phase 2C plan that introduced `figma_node`:** [docs/plans/2026-05-13-002-feat-figma-db-phase-2-plan.md](2026-05-13-002-feat-figma-db-phase-2-plan.md).
- **Migration the new schema mirrors:** `services/ds-service/migrations/0022_canonical_tree_zstd.up.sql`.
- **Existing zstd helpers (reused unchanged):** `services/ds-service/internal/projects/canonical_tree.go`.
- **Flat-node producer (unchanged):** `services/ds-service/internal/figma/client/client.go` `FileDeepTree.Flatten`.
- **Poller call site (modified by U4):** `services/ds-service/internal/figma/inventory/poller.go` around line 467.
- **Pre-wipe evidence:** 13GB local DB dominated by `figma_node` table (4.8GB) + 5 indexes (~8.6GB); pre-wipe backup at `services/ds-service/data/ds.db.pre-wipe-20260514-023830.bak` (delete after this plan ships and verification passes).
