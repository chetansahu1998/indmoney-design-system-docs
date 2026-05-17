---
title: "feat: MCP PM workflow Phase 1 follow-ups (thumbnails, audit, DRD collab, sidecar, autosync absorption)"
status: completed
created: 2026-05-17
completed: 2026-05-17
type: feat
depth: standard
origin: docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md
---

# feat: MCP PM workflow Phase 1 follow-ups (thumbnails, audit, DRD collab, sidecar, autosync absorption)

**Target repo:** indmoney-design-system-docs

Origin plan (`docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md`) shipped Phase 1 (commits `74d0a76` → `d74f288`) with 5 known follow-ups deferred. This plan resolves all five.

---

## Summary

Five focused follow-ups that close gaps surfaced during Phase 1 execution. Each is small-to-medium, reuses Phase 1 patterns, and ships as an atomic commit:

- **U1 — Real frame thumbnails** in the PRD viewer. The U9 wall renders placeholder glyphs because the existing `/v1/projects/{slug}/screens/{id}/png` endpoint can't be reached from `(sub_flow_slug, figma_node_id)`. New endpoint `/v1/figma/frame-png` proxies Figma's `/v1/images` API with a 5-min cache (same TTL as the existing frame-metadata proxy) and signed-URL asset tokens for inline `<img>` use.
- **U2 — Audit thread-through** in `tools_prd.go`. U6b shipped `prd_audit` + `RecordPRDAudit` but no MCP write site emits audit rows yet, so the coverage wall's `last_touched_by` / `last_touched_at` columns are NULL across the board. Threads 11 call sites.
- **U3 — Full DRD collab pane** in the viewer's right rail. Currently read-only because `mintDRDTicket` + `createDRDProvider` are flow_id-keyed. Adds a `sub_flow → flow_id` resolver path and mounts the existing BlockNote + Hocuspocus stack inside the PRD shell.
- **U4 — `prd.export` JSON sidecar**. Current export returns markdown only. Returns a typed JSON sidecar alongside (acceptance criteria as arrays, events with typed properties_schema, etc.) so Storybook / Playwright / Mixpanel / JIRA generators don't re-parse markdown.
- **U5 — Absorb the `/tmp` Python pipeline into Go autosync**. `figma_node_metadata` is populated by manual Python scripts (37,557 rows today). New Go stage in `internal/figma/inventory/` does the equivalent extraction so the running server populates the table without human intervention.

---

## Problem Frame

Phase 1 shipped functional core but with five visible-or-load-bearing gaps:

1. **Wall and FrameGrid show grey-glyph placeholders.** PMs reviewing PRDs see a corkboard with no images — usable, but visually broken. The existing PNG endpoint at `/v1/projects/{slug}/screens/{id}/png` requires a `screen_id` (UUID); the viewer only has `figma_node_id` (e.g. `1:4913`). `screens.screen_logical_id` is also a UUID (not the Figma node id), so no direct join exists in the schema today. The viewer is right to fall back to placeholders — but the right fix is a Figma-node-keyed thumbnail endpoint.

2. **Coverage wall's `last_touched_by` and `last_touched_at` are always NULL.** U6b shipped the `prd_audit` table and the `RecordPRDAudit` method; U6's `tools_prd.go` never calls it. The wall renders the audit columns gracefully (placeholder text), but the value users actually want — "who last touched this state and when?" — is empty.

3. **The DRD pane is a placeholder.** Shows DRD presence + bytes + an "Open in Atlas" link. PMs reviewing a PRD can't actually edit the DRD beside it. Atlas's DRD editor (`AtlasDRDEditor`) is fully working, but its `mintDRDTicket(slug, flowID)` and `createDRDProvider({flowID, ticket})` are keyed on `flow_id`. The viewer has `sub_flow_id`; U3 of the origin plan added `flow_drd.sub_flow_id` but the resolver path (sub_flow_id → flow_id) is the missing piece.

4. **`prd.export` returns markdown only.** Downstream tools (Storybook story generator, Playwright test-stub generator, Mixpanel tracking-plan importer, JIRA story creator) want the typed stems directly. Without a JSON sidecar, every consumer either re-parses markdown (fragile) or hits the MCP `prd.author op:get` separately (extra roundtrip + couples to MCP). A JSON sidecar with the typed shape ships with the markdown and is the canonical structured artifact.

5. **`figma_node_metadata` has no Go writer.** The Phase 1 audit surfaced this: 37,557 rows are populated by manual `/tmp/run_step2_nodes.py` + `/tmp/run_step2_frames.py` runs. Operationally, this means a new file added to Figma doesn't get its node metadata into the DB until someone runs a Python script. The Go autosync's `syncFileDeep` already fetches the section subtree via Figma's API — extending it to flatten + write per-node rows closes the loop.

---

## Scope Boundaries

**In scope (this plan):**
- New `/v1/figma/frame-png` HTTP endpoint that proxies Figma's `/v1/images` API by `(file_key, node_id)` with a 5-min in-memory cache and per-tenant PAT resolution.
- New `FrameThumbnail` React component using the new endpoint; wired into `Wall.tsx` and `CanvasShell`'s `FrameGrid`.
- 11 `RecordPRDAudit` call sites in `internal/mcp/tools_prd.go`, one per `prd.author op:*` write op (skipping `op:get` and `op:export`). Best-effort error handling (log on failure, don't fail the user's write).
- Sub_flow → flow_id resolver: a new helper that looks up `flow_drd.sub_flow_id → flow_drd.flow_id → flows.id`, plus a thin slug-keyed variant of `HandleDRDTicket` so the viewer's `useDRDProvider` hook works against `sub_flow_slug` instead of `flow_id`.
- `DRDPane` upgrade in the viewer: mounts a real BlockNote editor (`@blocknote/mantine`) with the existing Hocuspocus provider, side-by-side with the canvas + wall.
- `prd.author op:export` enhancement: returns `{markdown: string, sidecar: PRDExportSidecar}` instead of a bare string. New typed `PRDExportSidecar` Go struct matching the JSON shape downstream consumers will rely on.
- New Go autosync stage `figma_node_metadata_extractor` that, per file, reads section IDs and calls Figma's `/v1/files/<key>/nodes?ids=<csv>&depth=1` API, flattens direct-child FRAME/INSTANCE/COMPONENT nodes, and upserts `figma_node_metadata` rows. Integrated into the existing `syncFileDeep` pass.

**Deferred to Follow-Up Work:**
- Thumbnail endpoint hardening (signed URL TTL, CDN caching, image format negotiation). v1 uses inline PNG bytes through the existing proxy pattern; production hosting decisions don't block this work.
- Multi-PM concurrent PRD editing (origin plan deferral). DRD collab uses the existing Yjs/Hocuspocus stack for free; PRD typed-stems remain single-writer with optimistic locking.
- Mixpanel verb taxonomy validation (origin plan deferral). The JSON sidecar exposes events as typed but doesn't validate names against a registry.
- Depth=8 figma node extraction for full subtree caching. The `LoadSectionSubtree` blob (mig 0030) already covers this; U5 here only ports the depth=1 direct-children extraction the wall + auto-skeleton consume.
- Hot-reload of `figma_node_metadata` when individual node names change without a section-hash change. Out of v1 — section hash bump triggers re-extraction.

**Out of scope (not this product):**
- Changes to the Yjs schema, BlockNote custom blocks (`lib/drd/customBlocks.tsx`), or the Hocuspocus sidecar protocol. U3 here is wiring, not editor work.
- Replacing the existing extraction pipeline (`pipeline.go`). U5 here populates `figma_node_metadata`; it does not change which sections feed the render pipeline.
- Phase 2 of the origin plan (U10 remote `/mcp`, U11 file-scoped auth). Separate plan.

---

## Key Technical Decisions

### KTD-1. Frame thumbnails use Figma's `/v1/images` API, not a derived lookup

The Phase 1 audit assumed a `figma_node_id → screen_id` lookup existed. Investigation confirmed it doesn't: `screens.screen_logical_id` is a UUID, not the Figma node ID format (`1:4913`). Building a join table + backfill is bigger work for marginal gain — most frames in the wall are direct-child section frames, which the existing extraction pipeline doesn't necessarily turn into `screens` rows.

The plan therefore introduces a thin `/v1/figma/frame-png` proxy: caller passes `(file_key, node_id)` and an asset_token; server resolves the per-tenant Figma PAT, calls Figma's `/v1/images?ids=<node_id>&format=png&scale=1`, redirects to (or proxies through) the returned S3 URL, caches the result for 5 minutes (same TTL as `figma_proxy.go:85` already uses for frame metadata).

This is the canonical Figma-API pattern for "give me a PNG of this node." It costs one Figma API call per cold thumbnail; the cache amortizes for the wall scroll case.

### KTD-2. Audit thread-through is best-effort, not transactional

Every `prd.author op:*` write tool in `tools_prd.go` will, after a successful write, call `deps.Repo.RecordPRDAudit(ctx, stateID, deps.UserID, op)`. The audit is logged on failure but never bubbles up — a successful PRD write must not be rolled back because of an audit insert failure (the audit log is observational, not a constraint).

This matches the existing pattern in `auditlog.go` (the broader audit system) and keeps the wall's `last_touched_*` columns populated without coupling user-facing tools to audit-table availability.

Auto-skeleton writes (U2b in origin plan) deliberately do NOT record audits — `last_touched_by` represents human authorship; auto-skeleton is system-driven.

### KTD-3. DRD collab wires through the existing flow_id-keyed stack, not a parallel sub_flow-keyed one

The Atlas DRD editor (`AtlasDRDEditor.tsx`) + Hocuspocus client (`lib/drd/collab.ts`) + the ticket endpoint (`HandleDRDTicket` at `internal/projects/server.go:2457`) all key on `flow_id`. Rewriting any of these to be sub_flow-keyed is significantly more work than wiring a resolver.

The plan adds: (1) a server-side resolver `(s *Server) resolveFlowIDForSubFlow(ctx, subFlowID) → flow_id` that looks up `flow_drd.sub_flow_id` first and falls back to `BootstrapDRDForSubFlow` if no row exists yet, (2) a new API endpoint `POST /api/projects/[subProduct]/[subFlow]/drd/ticket` that resolves the flow_id and forwards to the existing ticket endpoint, and (3) a `DRDPane` React component that calls the new endpoint, then uses `createDRDProvider({flowID, ticket})` with the resolved flow_id.

The PM never sees `flow_id` — it's all sub_flow_slug at the user-facing seam.

### KTD-4. JSON sidecar mirrors the Go `PRDFull` struct exactly

The sidecar's JSON shape is the snake_case-serialized form of the existing `projects.PRDFull` struct. No transformation, no de-normalization. This gives downstream consumers a stable contract that tracks the Go types directly — schema changes in the typed stems flow through to the sidecar automatically.

The `prd.author op:export` result becomes `{markdown: string, sidecar: PRDExportSidecar, path?: string}`. The bridge / skill writes both files to disk (`<slug>.md` and `<slug>.json`) when invoked from a host that has filesystem access.

### KTD-5. Autosync absorption uses depth=1 only

The existing `/tmp/run_step2_nodes.py` does depth=8 batched fetches against Figma's `/v1/files/<key>/nodes?ids=<csv>&depth=8`. That extracts the full subtree for each section — but the section subtree is **already captured** in `figma_section.subtree_json_zstd` (mig 0030) by the Go autosync. The depth=8 Python output duplicates data that's already in the blob.

The plan ports only the depth=1 extraction (direct children of each section), which is what U5 (`ListSectionFrames`) and U2b (auto-skeleton) actually consume. This keeps Figma API call count down (fewer per-file calls, fewer rate-limit collisions with the existing autosync) and produces the same row count in `figma_node_metadata` that PMs and the wall already work against.

The `/tmp` Python scripts can be deleted once this lands.

---

## Implementation Units

### U1. Figma frame PNG proxy endpoint + viewer integration

**Goal:** Real thumbnails in the wall + FrameGrid via a new `/v1/figma/frame-png` endpoint. Eliminates placeholder glyphs.

**Files:**
- `services/ds-service/internal/projects/figma_proxy.go` (modified — extend existing proxy with a new handler)
- `services/ds-service/internal/projects/figma_proxy_test.go` (modified)
- `services/ds-service/cmd/server/main.go` (modified — one new route registration)
- `app/projects/[subProduct]/[subFlow]/prd/FrameThumbnail.tsx` (new)
- `app/projects/[subProduct]/[subFlow]/prd/Wall.tsx` (modified — replace placeholder with `<FrameThumbnail>`)
- `app/projects/[subProduct]/[subFlow]/prd/CanvasShell.tsx` (modified — same in `FrameGrid`)

**Approach:**
- Server: new `HandleFigmaFramePNG(w, r)` handler. Accepts `?file_key=<key>&node_id=<id>&scale=1`. Resolves tenant from JWT, fetches per-tenant PAT via the existing `figmaPATResolver`, calls Figma's `/v1/images?ids=<node_id>&format=png&scale=<scale>` and either redirects the client to the returned S3 URL (302) or proxies the bytes (preferable — avoids exposing the Figma S3 URL to the browser; 5-min in-memory cache by `(tenant_id, file_key, node_id)`).
- Asset token: same `?at=<token>` query-param fallback as `pathAllowsTokenQueryParam` (so `<img>` tags work without an `Authorization` header).
- Client: `<FrameThumbnail figmaNodeID={...} fileKey={...} alt={frameName} />` renders `<img src="/v1/figma/frame-png?file_key=...&node_id=...&at=...">` with a skeleton placeholder while loading and a graceful 404 fallback (returns to current placeholder glyph).
- The viewer's API layer mints the asset token via the existing `POST /v1/projects/.../screens/.../png-url` pattern — or, simpler, server-component-side renders a signed URL using the existing `mintAssetToken` helper.

**Patterns to follow:**
- `internal/projects/figma_proxy.go:48` (existing `HandleFigmaFrameMetadata`, same shape — input query params, PAT resolve, Figma API call, cache, response).
- `internal/projects/png_handler.go` for the asset-token + `?at=` pattern.
- `lib/drd/customBlocks.tsx:151` (`FigmaLinkRenderer`) for the React component aesthetic and loading/error states.

**Test scenarios:**
- Happy path: valid `(file_key, node_id)` + valid asset token → 200 with PNG bytes.
- Cache hit: second request within 5 minutes returns cached bytes without a Figma API call.
- Edge: invalid `node_id` (e.g. `not-a-node`) → 404 with structured error.
- Edge: missing asset token → 401.
- Edge: cross-tenant asset token → 403.
- Edge: Figma API rate-limit hit → 429 surfaced to the client with a `Retry-After` hint.
- Integration: Wall component renders 8 frames, all thumbnails appear within 2s when Figma is healthy.

**Verification:** Visiting `/prd/wallet/m2m-settlement` shows actual frame PNGs in the wall, not grey glyphs. DevTools Network tab shows one request to `/v1/figma/frame-png` per unique frame, with subsequent reloads hitting the 5-min cache.

---

### U2. Audit thread-through in `tools_prd.go` writes

**Goal:** Every MCP `prd.author op:*` write site records a `prd_audit` row so the coverage wall's `last_touched_by` / `last_touched_at` populate.

**Files:**
- `services/ds-service/internal/mcp/tools_prd.go` (modified — 11 write sites get a post-success `RecordPRDAudit` call)
- `services/ds-service/internal/mcp/tools_prd_test.go` (modified — add audit-assertion tests for representative ops)

**Approach:**
- After each successful write tool returns the new/updated row, call `deps.Repo.RecordPRDAudit(ctx, stateID, deps.UserID, projects.Op<X>)` with the appropriate op constant from `internal/projects/prd_audit.go`.
- Map (`tools_prd.go` op → audit op constant):
  - `prd.upsert_tab` → `OpUpsertTab` (audit row keyed on a representative state in the tab — or skip if the tab has no states yet; document the choice)
  - `prd.add_state` → `OpUpsertState`
  - `prd.add_event` → `OpAddEvent`
  - `prd.add_acceptance_criterion` → `OpAddAcceptanceCriterion`
  - `prd.add_edge_case` → `OpAddEdgeCase`
  - `prd.upsert_copy_string` → `OpUpsertCopyString`
  - `prd.add_a11y_note` → `OpAddA11yNote`
  - `prd.attach_frame` → `OpAttachFrameTag`
  - `prd.detach_frame` → `OpDetachFrameTag`
- `prd.get` and `prd.export` are read-only — no audit recorded.
- Audit is best-effort: wrap the `RecordPRDAudit` call in a deferred-style error log; do not propagate audit failures up to the user's tool result. Use `deps.Log` for the warning.

**Patterns to follow:**
- `internal/projects/auditlog.go` for the "log-and-continue" failure pattern in the broader audit system.
- `RecordPRDAudit` itself is in `internal/projects/prd_audit.go` (U6b shipped).

**Test scenarios:**
- Happy path: `prd.author op:add_state` succeeds → a new `prd_audit` row exists with the right `(prd_state_id, user_id, op="upsert_state")`.
- Happy path: `prd.author op:add_event` after `op:add_state` → two audit rows, `LatestPRDAuditByState` returns the event one as the latest.
- Edge: `RecordPRDAudit` errors (e.g. simulated SQL failure via a stub) → the underlying tool's result is still success; the error is logged.
- Edge: `prd.author op:get` → no audit row is added (read-only).
- Integration: end-to-end — call `prd.author op:add_state` then `section.outline_states` for the same sub_flow; the wall row for that state has populated `last_touched_by` and `last_touched_at`.

**Verification:** Wall in the viewer shows "last touched by `<email>`, `<relative timestamp>`" for any state authored via the MCP path.

---

### U3. DRD collab pane via sub_flow → flow_id resolver

**Goal:** Replace the read-only DRD pane in the PRD viewer with the full BlockNote + Hocuspocus collab editor, keyed on `sub_flow_slug`.

**Files:**
- `services/ds-service/internal/projects/server.go` (modified — add `HandleSubFlowDRDTicket` mirroring `HandleDRDTicket`)
- `services/ds-service/cmd/server/main.go` (modified — register the new route)
- `services/ds-service/internal/projects/drd_collab.go` (modified — add `ResolveFlowIDForSubFlow(ctx, subFlowID) → (flow_id, isNew, error)` helper)
- `app/api/projects/[subProduct]/[subFlow]/drd/ticket/route.ts` (new — Next.js proxy)
- `lib/drd/collab.ts` (modified — add `mintDRDTicketForSubFlow(subProductSlug, subFlowSlug) → DRDTicketResponse`)
- `app/projects/[subProduct]/[subFlow]/prd/DRDPane.tsx` (new — extracts from PRDShell; full editor)
- `app/projects/[subProduct]/[subFlow]/prd/PRDShell.tsx` (modified — replace inline read-only pane with `<DRDPane>`)

**Approach:**
- Server-side resolver: `ResolveFlowIDForSubFlow(ctx, subFlowID)` looks up `flow_drd` by `sub_flow_id`. If found, returns the existing `flow_id`. If not found, calls `BootstrapDRDForSubFlow` (already shipped in U6) to create the project + flow + flow_drd chain and returns the new flow_id.
- New endpoint `POST /v1/projects/{sub_product_slug}/{sub_flow_slug}/drd/ticket`: resolves slug → sub_flow_id → flow_id, then mints a ticket using the existing `HandleDRDTicket` codepath. (Refactor: extract the ticket-issuing core into a private helper both handlers call.)
- Next.js proxy `app/api/projects/[subProduct]/[subFlow]/drd/ticket/route.ts`: forwards `POST` to ds-service with JWT pass-through. Same shape as the existing `/api/projects/[slug]/figma-image-refs/route.ts`.
- Client: `mintDRDTicketForSubFlow` calls the new proxy; result has the same shape as `mintDRDTicket`'s response (just a different way of getting the same ticket).
- `DRDPane` component: on mount, mint ticket → call `createDRDProvider({flowID, ticket})` → wire BlockNote with the collaboration extension. Reuse the editor config from `app/atlas/_lib/AtlasDRDEditor.tsx`. Unmount cleanup destroys the provider.

**Execution note:** Add a characterization test for `HandleDRDTicket` before refactoring out the ticket-issuing core — current behavior includes single-use ticket invalidation, 60s TTL, and tenant + user binding that must be preserved across the refactor.

**Patterns to follow:**
- `HandleDRDTicket` at `internal/projects/server.go:2453` for the ticket flow.
- `BootstrapDRDForSubFlow` at `internal/projects/drd_collab.go` (shipped in U6).
- `AtlasDRDEditor.tsx` for the BlockNote + Hocuspocus mount pattern.
- `lib/drd/collab.ts::createDRDProvider` — unchanged; just called with a resolved flow_id.

**Test scenarios:**
- Happy path: PM opens `/prd/wallet/m2m-settlement`, the DRD pane mounts an editable BlockNote editor that loads the current DRD content via the Hocuspocus provider.
- Happy path: PM types in the DRD pane; the change persists (verify via direct SQL query of `flow_drd.y_doc_state` revision bump after debounce).
- Happy path: Two PMs open the same PRD viewer; both see each other's edits in the DRD pane via the existing Yjs CRDT.
- Edge: First-ever open for a sub_flow with no existing DRD → `ResolveFlowIDForSubFlow` creates the row via `BootstrapDRDForSubFlow`; editor mounts empty; first edit persists.
- Edge: Ticket expires (60s, single-use) → Hocuspocus reconnect mints a fresh ticket via the new endpoint.
- Edge: Cross-tenant request → 403.
- Characterization: `HandleDRDTicket` characterization test passes before AND after the refactor (no behavioral drift).

**Verification:** PM authors a DRD paragraph in the PRD viewer, refreshes the page, sees the paragraph persisted. A second tab on the same viewer shows live collab.

---

### U4. `prd.export` JSON sidecar

**Goal:** Add a typed JSON sidecar to the export result so downstream consumers don't re-parse markdown.

**Files:**
- `services/ds-service/internal/projects/prd.go` (modified — extend `RenderPRDMarkdown` return type)
- `services/ds-service/internal/projects/prd_test.go` (modified — add sidecar shape assertions)
- `services/ds-service/internal/mcp/tools_prd.go` (modified — `prd.export` returns `{markdown, sidecar}` shape)
- `services/ds-service/internal/mcp/tools_prd_test.go` (modified)
- `~/.claude/plugins/ind-suite/mcp-bridge/src/server.ts` (modified, ind-suite repo — write both `.md` and `.json` to disk when invoking `prd.export`)
- `~/.claude/plugins/ind-suite/skills/ind-prd.md` (modified, ind-suite repo — mention the sidecar in Phase 4)

**Approach:**
- Define `PRDExportSidecar` struct in `prd.go` that mirrors `PRDFull` exactly (snake_case JSON tags already exist on `PRDFull`). The sidecar IS a `PRDFull` re-rendered — no de-normalization, no transformation.
- Refactor: replace `RenderPRDMarkdown(ctx, subFlowID) (string, error)` with `RenderPRDExport(ctx, subFlowID) (PRDExport, error)` where `PRDExport struct { Markdown string; Sidecar PRDFull }`. Keep `RenderPRDMarkdown` as a thin wrapper for backwards compat (returns just the markdown field).
- `prd.export` MCP tool response: `{markdown: string, sidecar: object, path?: string}`. The `path` field is informational (the bridge writes the file; ds-service doesn't touch the filesystem per Phase 1's KTD).
- Bridge: when the MCP tool returns `op:export` result, write `<sub_product>/Documents/<sub_flow>.md` AND `<sub_product>/Documents/<sub_flow>.json`. Confirm the directory path via the user's `~/INDmoney/` per CLAUDE.md.

**Patterns to follow:**
- `internal/projects/prd.go::RenderPRDMarkdown` for the existing serialization.
- `internal/projects/types.go` for snake_case JSON tag conventions.

**Test scenarios:**
- Happy path: full PRD with all stem types (criteria, edge cases, copy, events, a11y, frame tags) → sidecar JSON contains all stems with correct shape (array of objects per stem, matching the `PRDFull` types).
- Happy path: markdown rendering unchanged from current `RenderPRDMarkdown` output (regression test).
- Edge: empty PRD (no states yet) → sidecar is `{prd: {...}, tabs: []}`, markdown is the standard "no states yet" template.
- Edge: PRD with soft-deleted state → state is NOT in sidecar (matches `LoadPRD` behavior).
- Integration: invoke `prd.export` via MCP tool → response has both `markdown` and `sidecar` fields; bridge writes both files; round-trip parse of the sidecar JSON matches the `LoadPRD` Go struct exactly.

**Verification:** After PM invokes `/ind-prd <slug>` and runs export, `~/INDmoney/<sub_product>/Documents/` contains both `<sub_flow>.md` and `<sub_flow>.json`. The JSON validates against the `PRDFull` shape.

---

### U5. Autosync absorbs `/tmp` Python pipeline (depth=1 figma_node_metadata extractor)

**Goal:** A Go autosync stage that populates `figma_node_metadata` for every bound section, replacing the manual `/tmp/run_step2_*.py` runs.

**Files:**
- `services/ds-service/internal/figma/inventory/node_metadata_extractor.go` (new)
- `services/ds-service/internal/figma/inventory/node_metadata_extractor_test.go` (new)
- `services/ds-service/internal/figma/inventory/poller.go` (modified — call the new stage during `syncFileDeep`)
- `services/ds-service/internal/figma/client/client.go` (modified, if needed — confirm `GetFileNodes` already accepts a depth parameter; add if not)
- `services/ds-service/internal/projects/repository_figma_autosync.go` (modified — add `UpsertFigmaNodeMetadata([]FigmaNodeMetadataRow)` batch writer)
- `/tmp/run_step2_nodes.py`, `/tmp/run_step2_frames.py`, `/tmp/run_extraction_all.py`, `/tmp/run_extraction_resume.py` — deletion path documented in Verification (not committed; these are local-only scaffolding)

**Approach:**
- `extractNodeMetadataForFile(ctx, fileKey, sectionIDs []string)`:
  - Batch section IDs into Figma-friendly groups (`/v1/files/<key>/nodes?ids=<csv>&depth=1`). Figma's `ids` param can hold many IDs in one call; respect the URL length limit (~2KB).
  - For each returned section, walk its `document.children` array. Keep nodes where `type ∈ {FRAME, INSTANCE, COMPONENT}`. Skip TEXT/VECTOR/RECTANGLE/etc.
  - Build `[]FigmaNodeMetadataRow{ TenantID, FileKey, SectionID, NodeID, ParentID, Depth, OrderIndex, NodeType, Name, AbsX, AbsY, Width, Height, HasBBox, ComponentID, ComponentKey, ... }` (match the existing mig 0034 column set).
  - Call `UpsertFigmaNodeMetadata(rows)` — INSERT ... ON CONFLICT (tenant_id, file_key, node_id) DO UPDATE on the existing PK.
- Integration in `syncFileDeep`: after the section subtree blob is written, kick off `extractNodeMetadataForFile`. Per-file error is log-and-continue (matches existing autosync stages).
- Rate-limit awareness: use the existing `figma/client` rate-limiter (Tier-1 / Tier-2 buckets); the extractor's calls are bounded by section count and respect retry-after.
- Idempotency: re-running on an unchanged file is a no-op (existing rows just get updated to the same values).

**Patterns to follow:**
- `/tmp/run_step2_nodes.py` for the Figma API + INSERT shape (port to Go).
- `internal/figma/inventory/poller.go::syncFileDeep` for the autosync stage integration point.
- `internal/projects/repository_figma_autosync.go::UpsertFigmaPagesAndSections` for the batch upsert pattern.

**Test scenarios:**
- Happy path: file with 5 sections each containing 4 direct-child FRAMEs → 20 `figma_node_metadata` rows after the stage runs.
- Idempotency: second run on the same file → no duplicate rows; updated_at fields refresh.
- Edge: Figma API returns 429 → extractor backs off and retries; eventual success populates rows.
- Edge: section with no direct-child frames → no rows for that section; not an error.
- Edge: TEXT/VECTOR direct children → filtered out, only FRAME/INSTANCE/COMPONENT persist.
- Integration: after autosync runs on a fresh DB, `figma_node_metadata` row count matches what `/tmp/run_step2_nodes.py` would produce for the same input (cross-checked against current 37,557 rows on the dev DB).

**Verification:** Run autosync against the dev DB after dropping `figma_node_metadata`. Row count restores. Wall + auto-skeleton (which read this table) work without manual Python invocation. The `/tmp/run_step2_*.py` scripts can be deleted; document in the commit message.

---

## System-Wide Impact

| Surface | Impact |
|---|---|
| **Figma API rate budget** | U1 thumbnails + U5 node-metadata extraction both add Figma API calls. U1 caches 5 min per thumbnail; U5 runs once per section per autosync cycle. Combined with the existing autosync extractor, peak rate stays well under Tier-1 budget (15 rpm) for the current 49-file corpus. |
| **DB write volume** | U2 audit threads ~1 row per PRD edit (low). U5 extracts ~37k rows once on backfill, then deltas per autosync cycle (low steady-state). |
| **Frontend bundle** | U3 mounts the existing BlockNote stack in the PRD viewer — adds the same ~200KB Atlas already pays. Tree-shaken. |
| **`/tmp` scripts** | U5 obsoletes `/tmp/run_step2_*.py`, `/tmp/run_extraction_*.py`. Document in the commit; user can delete after Phase 2. |
| **MCP wire shape** | U4 changes `prd.export` result from `{markdown: string}` to `{markdown: string, sidecar: PRDFull, path?: string}`. The bridge update is mandatory; existing consumers (only the bridge today) still work because `markdown` field is unchanged. |
| **DRD audit log** | U3 routes DRD edits through the existing `flow_drd` audit (revision increment, last_snapshot_at). No new audit table. |

---

## Risks & Mitigations

- **U1 risk: Figma `/v1/images` rate-limiting under viewer scroll**. Mitigation: 5-min cache shared across viewers in the same tenant. A wall with 20 frames hits the API at most 20 times in 5 min; well under Tier-1 budget.

- **U1 risk: Stale thumbnails when designer updates a frame**. Mitigation: 5-min TTL is acceptable for "review" use cases. Hard-refresh by appending `&v=<content_hash>` query param (existing pattern in figma_proxy.go's metadata cache). Designer hot-reload is out of scope (deferred above).

- **U2 risk: Audit insert errors silently mask data loss**. Mitigation: log with `deps.Log.Warn("prd_audit insert failed", ...)`. The Phase 1 audit infra is the canary — failed audits show up in logs.

- **U3 risk: DRD pane refactor breaks Atlas's existing DRD editor**. Mitigation: Atlas's editor + ticket endpoint stay intact; the new `HandleSubFlowDRDTicket` is additive. Characterization test for `HandleDRDTicket` runs both pre- and post-refactor.

- **U3 risk: `ResolveFlowIDForSubFlow` creates duplicate `BootstrapDRDForSubFlow` rows on race**. Mitigation: the U3 origin plan's idempotency contract (`CreateDRDForSubFlow` returns existing flow_id if row pre-exists) handles this. Add a concurrency test if not present.

- **U4 risk: Sidecar shape divergence from `PRDFull` introduces silent contract drift**. Mitigation: the sidecar IS a `PRDFull` serialized — no separate type. Schema changes to `PRDFull` flow through automatically. A round-trip JSON unmarshal test catches accidental incompatibility.

- **U5 risk: Concurrent autosync runs (per-tenant lease) duplicate Figma API calls**. Mitigation: the existing `figma_autosync_lease` (mig 0033) already serializes per-tenant autosync. The extractor inherits this.

- **U5 risk: Depth=1 misses frames the Python depth=8 captured**. Mitigation: cross-check row count against `/tmp/run_step2_nodes.py` output on the dev corpus during verification. Document the row-count delta in the commit if non-zero (probably zero — direct-child frames are what U5/U2b consume).

---

## Verification

The plan is "done" when:

1. Visiting `/prd/wallet/m2m-settlement` in the viewer shows real Figma frame PNGs in the Wall and FrameGrid — not placeholder glyphs.
2. After authoring a PRD state via `/ind-prd` in Claude Code, the viewer's Wall shows "last touched by `<email>`, `<relative time>`" for that state.
3. The DRD pane in the viewer is an editable BlockNote surface; edits persist (revision increments on `flow_drd`); two browser windows on the same PRD see each other's DRD edits via Yjs CRDT.
4. Invoking `/ind-prd <slug>` and exporting writes both `<slug>.md` AND `<slug>.json` to `~/INDmoney/<sub_product>/Documents/`. The JSON validates against the `PRDFull` Go shape.
5. Dropping `figma_node_metadata` and running autosync repopulates the table without manual Python invocation. `/tmp/run_step2_*.py` scripts can be deleted.

---

## Sequencing

```
U1 (thumbnails) ─────────────┐
U2 (audit thread-through) ───┼─→ visible wall completion (U1+U2 are tightly coupled in UX)
                             │
U3 (DRD collab) ─────────────┘ (parallel to U1+U2)

U4 (JSON sidecar) ─── small, independent. Land any time.

U5 (autosync absorption) ─── operational debt. Land last (no user-visible change).
```

Critical path for "viewer looks done": U1 → U2 → U3. U4 + U5 are parallelizable.

---

## Ship Log

| Date | Commit | Unit | Notes |
|---|---|---|---|
| 2026-05-17 | `adbf2c6` | **U1** — Figma frame-PNG proxy + real thumbnails | 1568 lines. New `/v1/figma/frame-png` (GET, asset-token auth) + `/v1/figma/frame-png-token` (POST, JWT auth) endpoints. Proxies Figma's `/v1/images` API by (file_key, node_id) with 5-min in-memory cache + per-tenant PAT. Reuses `auth.AssetTokenSigner` (packs `figma:<file_key>:<node_id>:<scale>` into resourceID); `Client.GetImages` (plural, already existed); `assetExporter.URLs` for the per-tenant Figma rate-limit budget. Two-endpoint pattern mirrors `HandleMintAssetToken` + `HandleScreenPNG`. Bytes proxied through (not 302'd) to avoid leaking S3 URL. New `FrameThumbnail` React component with skeleton + error fallback; `useFrameThumbToken` hook batches per-Wall token mints. PRDShell threads `figma_file_key` from `section.inspect` down. 8/8 new tests pass. |
| 2026-05-17 | `ec69694` | **U2** — audit thread-through in tools_prd.go | 614 lines. 7 audit insertion sites in `tools_prd.go` (add_state/add_event/add_acceptance_criterion/add_edge_case/upsert_copy_string/add_a11y_note/attach_frame). Best-effort: failed audit logged via `deps.Log` but never propagates. Skipped: `prd.get`/`prd.export` (read-only), `prd.upsert_tab` (structural — no state_id; `prd_audit` is per-state by design), `prd.detach_frame` (would need `frame_tag.id → state_id` lookup, out of instrumentation-only scope). Added `Log *slog.Logger` to `mcp.Deps` (was missing) + `toolLog(deps)` fallback. 10/10 new tests including end-to-end assertion that `last_touched_by` populates on the wall after `prd.add_state`. Cross-tenant returns 404 (no existence oracle — characterized; tighter than plan's 403). |
| 2026-05-17 | `b818d67` | **U3** — DRD collab pane via sub_flow → flow_id resolver | 972 lines. Server: `ResolveFlowIDForSubFlow` (looks up `flow_drd` by `sub_flow_id`; falls back to `BootstrapDRDForSubFlow` for first-time). `BootstrapDRDForSubFlow` refactored via new `ensureDRDChainForSubFlow` helper so resolver uses steps 1-3 without forcing a YDoc snapshot. `issueDRDTicket` private helper extracted from `HandleDRDTicket`; both endpoints now call it. New endpoint `POST /v1/projects/{sub_product_slug}/{sub_flow_slug}/drd/ticket`. Frontend: `mintDRDTicketForSubFlow` in `lib/drd/collab.ts`; Next.js proxy; new `DRDPane.tsx` with 4-state connection machine + editor-only-mounts-after-first-sync rule (per docs/solutions/2026-05-02-002 learning). Uses `"blocknote"` Y.Doc fragment name so Atlas-side + viewer-side edits on same flow_id share the Yjs document. Localized `as any` cast on BlockNote provider (HocuspocusProvider.awareness type friction). 9/9 new tests; characterization test locks pre-refactor behavior. |
| 2026-05-17 | `06bbcd7` | **U4** — `prd.export` JSON sidecar | 433 lines. New `RenderPRDExport(ctx, subFlowID) (PRDExport, error)` returns `{Markdown, Sidecar PRDFull}`. Sidecar IS `PRDFull` serialized — no separate type (KTD-4). Refactored: `renderPRDFullToMarkdown(full PRDFull)` helper shared by both new method + backward-compat `RenderPRDMarkdown` wrapper. Markdown byte-identical to pre-U4 (pinned by test). `prd.author op:export` returns `{markdown, sidecar, sub_flow_full_slug, bytes, sidecar_bytes}`. ind-suite bridge commit `9aad593` writes both `.md` + `.json` to `~/INDmoney/<LOB>/Documents/`. Small `LOB_NAME_OVERRIDES` table (indstocks → INDstocks, etc.). 7 new tests pass. |
| 2026-05-17 | `5cf965f` | **U5** — autosync writes `figma_node_metadata`; /tmp Python obsolete | 1201 lines. New `inventory.NodeMetadataExtractor` runs in `syncFileDeep` after section subtree blob write. Replaces `/tmp/run_step2_*.py` (37,557 rows previously populated by manual scripts). Depth=1 only (KTD-5 — `LoadSectionSubtree` blob already covers depth=8). Filter: FRAME/INSTANCE/COMPONENT direct children. Batched (50 sections/call) through `Client.GetFileNodes` → existing tier-1 rate limiter. New repo method `UpsertFigmaNodeMetadata` with `ON CONFLICT (tenant_id, file_key, node_id) DO UPDATE`. Migration `0034_figma_node_metadata.up.sql` committed alongside (was previously untracked). RepoFactory closure pattern for per-tenant repo. 19/19 new tests pass. `syncFileDeep` gained a `tenantID` parameter (extractor needs raw tenant id for repo factory). |

| 2026-05-17 | _pending_ | **Fix** — extract PM viewer out of `/projects/` namespace | The U9 viewer was originally placed at `/projects/[subProduct]/[subFlow]/prd` and `/v1/projects/{sub_product_slug}/{sub_flow_slug}/drd/ticket` — but `/projects/` is the legacy Atlas namespace keyed by `project_slug` (single identifier per Figma file import). Mashing the new `{sub_product}/{sub_flow}` keys under that prefix collided with Next.js's "same dynamic segment name at the same level" rule (existing `app/projects/[slug]/page.tsx` redirector blocked any sibling like `[subProduct]`). Underlying bug: two namespaces conflated under one URL prefix. Fixed by moving every PM-viewer surface to a dedicated namespace: `app/prd/[subProduct]/[subFlow]/...` for pages, `app/api/prd/[subProduct]/[subFlow]/...` for proxies, and `/v1/sub-flows/{sub_product_slug}/{sub_flow_slug}/drd/ticket` for the ds-service endpoint. Audited the codebase for similar namespace conflations (real bug fix: `tools_resolve.go::ResolveLinks.PRDViewerURL` returned `/projects/<slug>/prd` — now `/prd/<slug>`). Stale doc-comment URLs cleaned up. Legacy `/projects/[slug]/` and `/v1/projects/{slug}/...` routes untouched. |

**Milestone — follow-ups complete.** All five Phase 1 known gaps closed: viewer renders real thumbnails (U1), wall shows last_touched (U2), DRD pane is editable + collab (U3), exports ship typed sidecars (U4), autosync owns `figma_node_metadata` (U5). Plus a fix moving the PM viewer out of the `/projects/` namespace where it was conflating with legacy Atlas project_slug routes. The PM authoring workflow is feature-complete for the local-stdio Phase 1 footprint.

Phase 2 (origin plan's U10 remote `/mcp` + U11 file-scoped auth) remains the next-week follow-up — not in this plan.
