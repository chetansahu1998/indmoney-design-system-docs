# Canvas Figma-Dev-Mode parity — system data-flow reference

Pre-implementation snapshot for the 10-unit plan at `docs/plans/2026-05-17-003-feat-canvas-figma-dev-mode-parity-plan.md`. Captures where state lives, how it moves, and which seams each unit touches. Cite file:line throughout — future-me (or a subagent picked up mid-unit) reads this before changing code.

**Branch:** `main` (parallel with MCP plan; coordination point is `pipeline.go` Stage 9 only).

---

## 1. Frontend data flow (user clicks `/atlas` → sees a leaf)

### Route entry & Zustand store

- `app/atlas/page.tsx:30-44` — parses `?project=…&leaf=…` via `lib/atlas/url-state`.
- `app/atlas/_lib/AtlasShell.tsx:48-237` — orchestrates Zustand hydration, then dynamic-imports `AtlasShellInner`.
- `lib/atlas/live-store.ts:160-297` — `useAtlas` Zustand shape:
  - **Brain-level:** `platform`, `domains`, `flows`, `synapses`, `brainNodesETag`, `graphAggregateETag`, `hydrated`
  - **Per-leaf cache:** `leavesByFlow[flowID]`, `leafSlots[leafID] = LeafSlot { frames, edges, overlays, canonicalTreeByScreenID, textOverrides, loadedAt }`
  - **Selection:** `selection = { flowID, leafID, frameID, selectedAtomicChild, selectedAtomicChildren (Map), activeStatesByFrame }`
  - **In-flight dedup:** module-level `openLeafInFlight: Set<leafID>` (`:156`)
- Window globals (synchronous, before dynamic import): `__ATLAS_DATA_READY`, `__ATLAS_DOMAINS`, `__ATLAS_FLOWS`, `__ATLAS_SYNAPSES`, `LEAVES`, `LEAVES_BY_FLOW` — `AtlasShell.tsx:161-194`.

### Leaf open → canonical_tree fetch

1. `AtlasShellInner.tsx:141-158` — `openLeaf(id)` awaits `live-store.openLeaf(id)` then sets local `leafID`.
2. `live-store.openLeaf` → `fetchLeafOverlays(slug, leafID)` (`lib/atlas/data-adapters.ts`) fetches frames + overlays + **first 20 canonical_trees** for edge inference.
3. Remaining trees: lazy via per-frame `IntersectionObserver` in `LeafFrameRenderer.tsx`. WARM rootMargin=1000px → fetch; HOT rootMargin=200px → render.
4. Mozilla bug 1419339 workaround: subscribe to `canvasGestureTracker` settle → `unobserve(target); observe(target)` so IO recomputes against post-pan transform.
5. Per-frame fetch: `GET /v1/projects/{slug}/screens/{screenID}/canonical-tree`. Cache short-circuit at `useAtlas(s => s.leafSlots[slug]?.canonicalTreeByScreenID[screenID])`.

### Render pipeline

- `AtlasShellInner.tsx:385-391` mounts `<LeafCanvas key={canvas-${leafSticky.id}-${slotVersion}} … />`. `slotVersion = leafSlots[leafID]?.loadedAt` — any SSE-driven overlay bump remounts the canvas.
- `app/atlas/_lib/leafcanvas.tsx:158` — `camRef = useRef({x, y, z})`. **Not** React state.
- `:167-180` — `applyCameraToDOM` writes `transform = scale(z) translate(-x,-y)` to `worldRef.current` + background CSS vars.
- `:184-190` — `scheduleCameraFlush` rAF-coalesces N events into one DOM write.
- `:319-422` — pointer-drag pan (`:319-353`) and wheel/pinch zoom (`:362-422`). Each mutates `camRef` directly + calls `canvasGestureTracker.tick()`.
- DOM hierarchy:
  ```
  .lc-stage (clip)
    .lc-world (transform: scale * translate, transform-origin: 0 0)  ← worldRef
      .lc-frame (absolute, world coords)                              ← per-frame
        .lc-frame-body
          <PhoneFrame>
            <img.ph-screen--v2>  PNG underlay
            .leafcv2-frame [data-status]  ← LeafFrameRenderer mounts here
              (nodeToHTML output | <Skeleton>)
              <MeasurementOverlay>  ← CURRENTLY mounts INSIDE worldRef
              <HoverTooltip>, <StatePicker>, <InlineTextEditor>
  ```

### LeafFrameRenderer (per-frame)

- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` (1180 lines).
- **Selection state:** Zustand `useAtlas`. `selection.selectedAtomicChild` (single) at `:768`; `selection.selectedAtomicChildren: Map<"${screenID}|${figmaID}", string>` for bulk at `:149`.
- **Hover state:** module-level pub/sub `hover-signal.ts` — `setHoveredAtomicChild`, `useHoveredAtomicChild`, plus `setHoveredBandHint`/`useHoveredBandHint` for inspector↔overlay cross-highlight.
- **Click handler** `:151-178` — `findAtomicTarget(e.target, wrapperRef)` (`:1160-1180`) walks up DOM via `data-figma-id` to nearest `data-figma-type` ∈ `ATOMIC_TYPES = {TEXT, RECTANGLE, ELLIPSE, VECTOR, STAR, POLYGON, LINE}` (`:66-74`) **or** `data-cluster="true"`/`data-cluster-pending="true"`. **FRAME/COMPONENT/INSTANCE/GROUP are pass-through today** — this is the seam U4 inverts.
- **Modifier dispatch today:** plain click → single-select; Shift+click → toggle bulk; **no Cmd+click semantic; no Enter/Shift+Enter cycling.**
- **DOM tagging effects:** `data-atomic-selected` painted at `:768-781` (ref-driven, not React-rendered); `data-atomic-hovered` at `:789-802`; `data-bulk-selected` at `:741-759`. Each effect removes stale flags first, then sets new. `cssEscapeAttr` (`:983`) handles Figma's `:` in node IDs.
- **Lasso/marquee:** `onPointerDown` (`:622-642`), `onPointerMove` (`:644-672`), `onPointerUp` (`:681-735`). Rect rendered at `:936-947`. Commit query at `:711-720` selects only `[data-figma-type="…"]`, `[data-cluster="true"]`, `[data-cluster-pending="true"]` — U6 widens this to FRAME types.
- **Status:** IO state machine `idle → loading → ready → error/empty`. `visible-filter.ts` prunes invisible variants. `nodeToHTML(prunedTree, …)` produces React elements.

### nodeToHTML render decision

- `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts:78-149` — single converter, classifies via `classifyNode` → routes.
- **Decision tree:**
  - `isIconCluster(node)` OR `classify.kind ∈ {icon, illustration, shape}` → `renderClusterPlaceholder` (`:256-352`).
  - `node.type === "TEXT"` → `renderText` (`<span>` with style).
  - Zero-axis VECTOR/LINE → `renderLine` (CSS hairline).
  - GROUP / BOOLEAN_OPERATION → `renderFlattenedChildren` (with autolayout-wrapper guard from round-1 audit).
  - Default → `renderContainer` (flex or absolute per `layoutMode`).
- **renderClusterPlaceholder current behavior:**
  - Reads `ctx.clusterURLs.get(node.id)` (Map).
  - URL present → `<img src={url} data-cluster="true" data-figma-id={id} …>` + `onError` retry with 3 attempts + cache-bust.
  - URL absent + `clusterFailedIDs.has(id)` → muted `<div data-cluster-failed>`.
  - Otherwise → teal-pulsing `<div data-cluster-pending="true">`.
- **U7 insertion point:** in `renderClusterPlaceholder` at `:256-263`, ahead of the URL Map lookup, check `node.svg_markup`. If present, emit `<div data-cluster-svg="true" data-figma-id={node.id} dangerouslySetInnerHTML={{__html: node.svg_markup}} style={{positioning}}/>`. **No new file needed; one branch added to one function.**

### Asset stream (cluster URLs)

- `app/atlas/_lib/leafcanvas-v2/leaf-asset-stream.ts` — SSE singleton via `subscribeLeafAssets(slug, leafID, onUpdate)`. First subscriber: `POST /v1/projects/{slug}/leaves/{leafID}/asset-stream/ticket` → opens `EventSource` on `/asset-stream?ticket=…`.
- SSE events: `asset-ready {node_id, format, url}`, `asset-failed {node_id, reason}`, `complete {total, rendered, failed}`.
- Notification coalesced: `notifyScheduled` flag batches N asset-ready events into one subscriber callback per rAF.
- `useIconClusterURLs.ts` — `collectClusterIDsWithBBox(canonicalTree)` walks the tree and collects IDs needing URL minting. **Filter for U7 lives here at `:114-127`:** skip nodes carrying `svg_markup`. `waitForLeafStreamSettled` gates a `POST /v1/projects/{slug}/assets/export-url` residual mint pass.
- `preview-tier.ts` — `pickRasterRender(longestEdgePx, zoom)` maps to PNG tier 128/256/512/2048/1024. Read SETTLED zoom (not live) to avoid mid-zoom URL swaps.

### Image refs (raster fills)

- `useImageRefs.ts` — `GET /v1/projects/:slug/leaves/:leafID/image-refs` → `{image_refs: {[ref]: {url, mime, bytes}}}`. Passed to nodeToHTML as `ctx.imageRefs`; IMAGE-fill nodes emit `<img>` from this map.

### MeasurementOverlay

- `app/atlas/_lib/leafcanvas-v2/MeasurementOverlay.tsx:139-200+` (782 lines).
- **Current mount:** child of `.leafcv2-frame` at `LeafFrameRenderer.tsx:902-908`. Sits INSIDE `worldRef` → inherits world transform. Comment at `:18-23` calls this out as deliberate.
- **Props:** `screenID`, `frameBBox`, `tree` (pruned). Coords are wrapper-local; `lookupBBox` subtracts frame origin.
- **Reads:** `useHoveredAtomicChild()` (`:144`), `useHoveredBandHint()` (`:146`, `:349`, `:530`), `useAtlas(s => s.selection.selectedAtomicChild)` (`:145`).
- **Paints:** distance lines (selected→hovered), padding bands (`paddingTop/Right/Bottom/Left` on hovered autolayout), gap markers (between siblings), selection chip (W×H+(X,Y)).
- **Gesture gating:** `getIsGesturing()` early-return at `:164`; `canvasGestureTracker.subscribe` at `:153-158` force-re-renders on settle.
- **U5 migration:** hoist mount up to `leafcanvas.tsx:523-527` (`.lc-world` sibling). Coords project via camera-state-state on every rAF. `useHoveredBandHint` contract (consumed by `AtomicChildInspector.tsx:556`) needs port to chrome layer — keep band-hint signal as-is, just rewire the consumer.

### AtomicChildInspector

- `app/atlas/_lib/leafcanvas-v2/AtomicChildInspector.tsx:54-196`. Mounted at `AtlasShellInner.tsx:395-400` above the existing `LeafInspector` tabbed pane.
- **Resize plumbing:** `AtlasShellInner.tsx:324-362`, `inspectorWidth` state persisted to localStorage `atlas:inspector:width` (`:327`).
- **Existing tabs:** Layer / Type / Tokens / Export — read selection via `useAtlas`, walk canonical_tree via `findByFigmaID` (also imported by `MeasurementOverlay.tsx:42`).
- **Cross-highlight producer:** inspector hover fires `setHoveredBandHint` (`:556`) → MeasurementOverlay lights matching band.
- **U10 path:** ADD Layout / Typography / Fills sections at the TOP of the same drawer. Existing tabs remain below. New file `inspector-property-groups.tsx` holds `<LayoutGroup>`, `<TypographyGroup>`, `<FillsStrokesGroup>` reading via the same `findByFigmaID` helper.

### Camera + signals

- `leaf-zoom-signal.ts:37-60` — split: `useLeafZoomLive` (per wheel tick) and `useLeafZoomSettled` (gesture-end). `useIconClusterURLs` reads SETTLED so cluster URLs don't remount mid-zoom across tier boundaries.
- `gesture-tracker.ts:46-100` — 150ms settle window. Tick on every wheel/pointer-move; emit settle 150ms after last tick.
- `idle-tracker.ts` — 500ms idle window; detaches IO observers when atlas idle.
- `camera-snap.ts:140-192` — `animateCamera(from, to, onTick, onDone)` currently `easeInOutCubic` 320ms. **U2 swap point:** replace integrator with critically-damped spring; call sites unchanged.
- `:213-235` — `registerSnapTarget`/`requestCameraSnap` channel. LeafCanvas registers via `useEffect` at `leafcanvas.tsx:245-248`.

### Keyboard handlers (scattered, U3 consolidates)

| File:Line | Keys | Owner |
|---|---|---|
| `AtlasShellInner.tsx:124-135` | Shift+2 | Zoom-to-selection snap |
| `AtlasShellInner.tsx:176-195` | Esc | Layered close (hover → atomic → frame → leaf) |
| `leafcanvas.tsx:699-705` | Esc + outside-click | Top-bar dropdowns |
| `atlas.tsx:1327` | Cmd-K | Atlas palette (not leafcanvas-scoped) |
| `InlineTextEditor.tsx:257-258, :381` | Enter, Esc | Text-edit commit/abort |
| `SearchInput.tsx:52-63` | Cmd-K / `/` | Search focus |

**No canvas focus model today** — no `tabindex`. Per security/feasibility review findings, U3 must establish focus contract before hotkeys fire.

### Dev harness

- `app/atlas/dev/canonical-render/page.tsx` + `CanonicalRenderClient.tsx` — `/atlas/dev/canonical-render?file=<abs>&slug=<slug>` route. Loads a canonical_tree from disk and renders through production hooks (`useImageRefs`, `useIconClusterURLs`). Sets `data-render-ready="true"` when stream settles. Used for fidelity audits + Chrome MCP screenshots.

---

## 2. Backend ingestion + storage (Figma → DB → disk)

### Pipeline 9-stage flow

`services/ds-service/internal/projects/pipeline.go:214-859`. Spawned as detached goroutine from `POST /v1/projects/export` (202 returned immediately). Stages:

| # | Stage | Entry | Output |
|---|---|---|---|
| 1 | Heartbeat | `:236-251` | `UPDATE project_versions SET pipeline_heartbeat_at` every 5s |
| 2 | Fetch nodes | `fetchNodesWithRetry:989-1012` | `/v1/files/{key}/nodes?depth=14` → canonical_tree JSON per frame |
| 3 | Render PNGs | `renderPNGsWithRetry:1017-1040` | `/v1/images?format=png&scale=2&ids=…` chunk≤25 → signed URLs |
| 4 | Download + persist | `:450-529` | `data/screens/{tenant}/{version}/{screen}@2x.png` + L1/L2 tiers |
| 5 | Mode-pair detect | `DetectModePairs:546` | `screen_modes` rows |
| 6 | Commit TX | `:602-657` | INSERT screen_canonical_trees + screen_modes, UPDATE status='view_ready', INSERT audit_jobs (all in one tx) |
| 6.7 | Organism detection | `runOrganismDetection:676` | `detected_organism_match` rows (non-blocking) |
| 7 | SSE publish | `:680-686` | `ProjectViewReady` event via broker |
| 8 | Audit enqueue | `:688-691` | `EnqueueAuditJob` (channel + DB row) |
| **9** | **Cluster prerender** | **`:703-856`** | **Goroutine with 30-min budget. Phase 1: PNG pyramid via `RenderAssetsForLeaf`. Phase 2.1: SVG export via `renderSVGClustersForVersion`.** |

Retry hardening (recent uncommitted changes to `pipeline.go`): `PipelineRetryAttempts=5`, `PipelineRetryBaseBackoff=5s`, `isTransientNetErr` (TCP reset / EOF / timeout / connection refused / broken pipe / decode), `downloadPNGWithRetry`, `nextRateLimitDelay` honors `Retry-After` clamped to `[500ms, 60s]`.

### Canonical tree storage

- `internal/projects/canonical_tree.go`:
  - Triple column: `canonical_tree TEXT` (legacy) + `canonical_tree_gz BLOB` (T8, mig 0016) + `canonical_tree_zstd BLOB` (Phase 1, mig 0022).
  - `CompressTreeZstd:144-153` — zstd L19, lazy singleton encoder.
  - `DecompressTreeZstd:157-173` — DecodeAll with **64MB cap at `:43`** (DoS guard).
  - `ResolveCanonicalTree:190-198` — priority: zstd > gz > legacy. **Every read goes through this.**
- `extractCanonicalTree:1213-1231` (pipeline.go) — walks per-frame subtree from `/v1/files/.../nodes` envelope, returns `(jsonString, sha256hex)`. Operates on `map[string]any` — **no Go struct for canonical_tree.**
- `CanonicalTreeFetchDepth = 14` (raised from 10 in May 2026 round-3 audit; covers Tax Centre nested chips, Networth bottomsheet, etc.).

### Asset storage

- **Disk layout:**
  ```
  data/screens/{tenant}/{version}/{screen}@2x.png        (Stage 4)
  data/screens/{tenant}/{version}/{screen}@2x.l1.png     (50%)
  data/screens/{tenant}/{version}/{screen}@2x.l2.png     (25%)
  data/screens/{tenant}/{version}/{screen}@2x.ktx2       (optional)
  data/assets/{tenant}/{file_id}/v{version_index}/{node_id}.{png|svg}  (Stage 9)
  ```
- **`asset_cache` table** (mig 0019, widened 0020-0021):
  - PK: `(tenant_id, file_id, node_id, format, scale, version_index)`
  - `format` CHECK IN: `png`, `svg`, `image-fill`, `preview-128`, `preview-512`, `preview-1024`, `preview-2048`
  - Columns: `storage_key` (relative path), `bytes`, `mime`, `created_at`
  - Indexes: `idx_asset_cache_tenant`, `idx_asset_cache_file_node`
- **persistAssetBytes** (`asset_export.go:393-408`) — atomic `.tmp` → rename to `data/assets/{tenant}/{file}/v{vn}/{sanitizedNodeID}.{format}`. Returns storage key.
- **For U8 read path:** SELECT `storage_key FROM asset_cache WHERE tenant_id=? AND file_id=? AND node_id=? AND format='svg' AND scale=1 AND version_index=?` — safer than path construction.

### SVG eligibility + export

- `internal/projects/svg_eligibility.go` — `IsSVGEligible` blocklist:
  1. `svgRenderableTypes` (`:47-57`): VECTOR, BOOLEAN_OPERATION, ELLIPSE, RECTANGLE, LINE, STAR, POLYGON, REGULAR_POLYGON, TEXT
  2. `svgWrapperTypes` (`:62-68`): FRAME, GROUP, INSTANCE, COMPONENT, COMPONENT_SET
  3. No IMAGE fills (shallow check, `:156`)
  4. No LAYER_BLUR / BACKGROUND_BLUR (visible-flag respected, `:163`)
  5. blendMode ∈ `{"", "NORMAL", "PASS_THROUGH"}` (`:170`)
  6. No `clipsContent=true` with non-cardinal child rotation (`:182`)
- `renderSVGClustersForVersion` (`pipeline_cluster_prerender.go:741-802`) — `AssetExporter.RenderAssetsForLeaf(ctx, tenantID, leafID, svgIDs, "svg", scale=1)`. Chunk size 80 (`AssetExportChunkSize`). Persists `asset_cache` rows with `format="svg"`.
- **U8 mutation point:** after `renderSVGClustersForVersion` returns at `pipeline.go:810`, call `InlineSVGMarkup(ctx, store, tenantID, fileID, versionIndex, svgs)`. For each screen with at least one svgID: `ResolveCanonicalTree` → walk-and-mutate (set `node.svg_markup`) → `CompressTreeZstd` → `UPDATE screen_canonical_trees SET canonical_tree_zstd=? WHERE screen_id=?`.

### HTTP API surface (frontend ↔ backend)

| Method | Path | Handler:line | Used by |
|---|---|---|---|
| POST | `/v1/projects/export` | `HandleExport:855` | extractor/import-figma-url CLI |
| POST | `/v1/projects/{slug}/versions/{version_id}/retry` | `HandleVersionRetry:859` | recovery |
| GET | `/v1/projects/{slug}/screens/{id}/canonical-tree` | `HandleScreenCanonicalTree:975` | LeafFrameRenderer lazy fetch |
| GET | `/v1/projects/{slug}/screens/{id}/png` | `HandleScreenPNG:965` | PNG underlay (`real-data-bridge.ts`) |
| POST | `/v1/projects/{slug}/assets/export-url` | `HandleMintAssetExportToken:1022` | `useIconClusterURLs` residual mint |
| GET | `/v1/projects/{slug}/assets/{node_id}` | `HandleAssetDownload:1045` | direct asset fetch, 30s render budget on miss |
| GET | `/v1/projects/{slug}/leaves/{leaf_id}/image-refs` | `HandleListImageRefs:1026` | `useImageRefs` |
| POST | `/v1/projects/{slug}/leaves/{leaf_id}/asset-stream/ticket` | `HandleAssetStreamTicket:1035` | `leaf-asset-stream` opens SSE |
| GET | `/v1/projects/{slug}/leaves/{leaf_id}/asset-stream` | `HandleAssetStream:1037` | SSE stream of `asset-ready` events |
| GET | `/v1/admin/figma-autosync/state` | `HandleFigmaAutosyncListState:938` | admin dashboard |
| DELETE | `/v1/admin/figma-autosync/state/{file_key}/{page_id}/{section_id}/quarantine` | `HandleFigmaAutosyncClearQuarantine:934` | autosync recovery |

Auth: JWT bearer for most endpoints; HMAC-signed asset tokens (`AssetTokenSigner` at main.go:319-327) bind `(tenant, file_id, node_id, format, scale)`.

### SSE event broker

- `internal/sse/broker.go` (referenced) — `broker.Publish(traceID, event)`. Events the frontend listens for: `ProjectViewReady` (after Stage 6 commit), `ProjectExportFailed`, asset-stream events.
- **U8 SSE consideration:** after `InlineSVGMarkup` UPDATEs `screen_canonical_trees`, the frontend's cache is stale until the next leaf-open or `slotVersion` bump. Two options: (a) publish a new event `ProjectAssetsInlined` that the frontend bumps `loadedAt` on, OR (b) accept that inlining lands at next leaf-open (cleaner; matches the existing "re-import to see changes" mental model). **Default: option (b).** Inlining is bound to import-time, not a live update.

### Autosync

- `internal/projects/repository_figma_autosync.go` — state machine on `figma_auto_sync_state` table.
- `figma_auto_sync_state` columns: `content_hash`, `live_content_hash` (mig 0035), `retry_count` (mig 0032), `leased_by`/`lease_expires_at` (mig 0033), `last_attempt_status ∈ {ok, skipped, error, quarantined}`.
- Auto-quarantine after 5 consecutive failures (`AutoSyncMaxRetries`). Recovery: planner compares `live_content_hash != content_hash` during quarantine and auto-retries.
- Retry loop env: `FIGMA_AUTOSYNC_INTERVAL` (default 15 min; set 0 to disable).
- **Not touched by canvas plan.**

### figma_node_metadata table (mig 0034)

- Uncommitted migration adds flat row-per-node store: `(tenant_id, file_key, page_id, section_id, node_id)` PK with `abs_x/y/width/height`, `rel_to_frame_id/rel_x/rel_y`, `layout_mode (NONE|HORIZONTAL|VERTICAL|GRID)`, `component_id/component_key`, `depth`, `order_index`, `parent_id`.
- **Wiring status: migration file exists, populator unclear.** Likely written by the autosync poller per section.
- **Relevance to canvas plan:** the table is *section*-scoped (Figma sections from `figma_section`), not screen-scoped inside a leaf canvas. Different granularity. Our U1 spatial-store is per-leaf-canvas screen-local. Could *optionally* back-fill server-side spatial data here later, but not for v1.

### Multi-tenancy

- `TenantRepo` wraps `*db.DB` with `tenantID`. Every query injects `tenant_id` filter. Forked at request boundary via `NewTenantRepo(db, tenantID)`.
- Read/write pool split (recent commits ab286d0 / a257298): one write connection (SQLite serializes), multiple read connections. **U8 writes go through write pool; canvas reads through read pool — small lag window possible but acceptable.**

---

## 3. Schema highlights (canvas-plan-relevant)

| Table | Mig | Why we care |
|---|---|---|
| `screen_canonical_trees` | 0001 + 0022 | U8 mutates `canonical_tree_zstd` to inline `svg_markup`. PK: `screen_id`. |
| `asset_cache` | 0019-0021 | U8 reads `format='svg'` rows for the inline bytes. Format already supported. |
| `figma_node_metadata` | 0034 (uncommitted) | NOT touched by canvas plan, but worth knowing exists. |
| `screens` | 0001 | Per-screen layout (x/y/w/h, png_storage_key). PK: `id`. |
| `project_versions` | 0001 | `version_index` is part of asset_cache PK; canvas plan reads it but doesn't write. |

No new migration required for canvas plan. (MCP plan owns mig 0036/0037/0038.)

---

## 4. Dev environment

- **Next.js 16.2.1, React 19.2.4, Vitest 4.1.5 (happy-dom), Playwright 1.59.1, Zustand 5.0.12, Framer Motion 12.38.0.**
- Local ports: frontend `:3001`, ds-service `:8080`/`:8443`, audit-server `:7474`.
- Scripts: `npm run dev` (tz build + next dev), `npm test` (vitest), `npm run test:parity` (Playwright).
- Vitest pattern: `**/*.vitest.ts(x)` (NOT `*.test.ts` — those are skipped per `vitest.config.ts:16-21`).
- Playwright: `tests/*.spec.ts`, base URL `:3001`, `indmoney-light` project.
- Test colocation: `app/atlas/_lib/leafcanvas-v2/__tests__/*.vitest.{ts,tsx}` ← all new client tests land here. Go: `_test.go` siblings.
- No AGENTS.md / CLAUDE.md / STRATEGY.md at repo root (verified).
- Type-only lint (`tsc --noEmit`), no ESLint.
- Already installed spring options: `framer-motion` (React-fiber-bound) or hand-roll. Plan KTD-3 chose hand-roll for rAF-loop control — verified against actual deps.

---

## 5. Cross-stream coordination (MCP plan parallel work)

- MCP plan touches: new `internal/mcp/` dir, new `subflow.go`/`prd.go`/`figma_role.go`, modifies `drd_collab.go` + `repository_figma_autosync.go`, migrations `0036`/`0037`/`0038`, adds `app/projects/[id]/prd/*` routes.
- **Single overlap with canvas plan: `services/ds-service/internal/projects/pipeline.go`.** MCP U2 wires section→sub_flow upsert into the autosync section. Canvas U8 adds a post-Stage 9 mutation pass at `pipeline.go:810`. Different code paths; small commits + rebase from main before each U8 commit.
- Both plans honor "designer name is canonical" doctrine — vocabulary aligned.
- No other file overlaps.

---

## 6. Execution refinements based on inventory

These tweak the plan as written without changing scope:

1. **U8 SVG-bytes read path:** use `SELECT storage_key FROM asset_cache WHERE format='svg' AND …` rather than constructing the disk path from convention. Tenant-scoped lookup is already enforced.
2. **U8 no SSE event needed.** Inlining lands at re-import time; existing `ProjectViewReady` event already covers cache invalidation on the frontend. Avoids a new event type.
3. **U7 cluster-collection filter** lives at `useIconClusterURLs.ts` `collectClusterIDsWithBBox` (`:114-127`) — one-line addition.
4. **U1 chrome-layer mount point:** `leafcanvas.tsx:523-527` near `.lc-world` div (sibling). Camera-state singleton populated from `camRef:158` write sites.
5. **U5 band-hint contract preservation:** the `useHoveredBandHint` push from `AtomicChildInspector.tsx:556` → `MeasurementOverlay.tsx` consumer is load-bearing for inspector↔overlay cross-highlight. Chrome layer must keep the same subscription pattern when MeasurementOverlay is removed.
6. **U5 MeasurementOverlay test port:** `app/atlas/_lib/leafcanvas-v2/__tests__/MeasurementOverlay.vitest.tsx` (~700 lines) must be ported to chrome-layer test files (`composed-hover.vitest.ts`, `distance-lines.vitest.ts`) before removal commit.
7. **Vitest naming:** new test files use `.vitest.ts` extension (not `.test.ts`) per `vitest.config.ts:16-21`.
8. **MeasurementOverlay current parent is INSIDE `worldRef` via `LeafFrameRenderer.tsx:902-908`** — hoisting out means moving the mount up to `leafcanvas.tsx`, NOT adjusting MeasurementOverlay's own JSX. The component itself gets replaced by chrome-layer's painters.
