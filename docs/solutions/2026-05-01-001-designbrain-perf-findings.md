# DesignBrain perf findings — for Projects · Flow Atlas Phase 3.5+

Captured: 2026-04-30
Researched by: Claude (Opus 4.7, 1M ctx) via Claude Code agent

DesignBrain-AI repo lives at `~/DesignBrain-AI/`. Frontend in `web/src/`. There is **no Three.js, no react-three-fiber, no drei, no KTX2/Basis** anywhere in the application code (only mime-type history strings inside `node_modules`). DesignBrain runs a fully custom WebGL2 engine (`web/src/engine/`) with 23 subsystems. ADR-001 (`docs/decisions/adr-001-custom-webgl-engine.md`) documents the call to skip Konva/Fabric/tldraw and own the pipeline.

What that means for Projects: their three.js / r3f integration patterns don't exist here. But they **do** have a production-grade tile pyramid LOD system, a viewport-culling HOT/WARM ring, a global GPU memory budget, a per-frame upload budget, and an instanced-batch architecture that all map cleanly onto our atlas-frame problem. Below is what's worth stealing and what isn't relevant.

## 1. LOD strategy

**They have a Deep Zoom-style tile pyramid for large images, generated server-side at persist time, consumed client-side with viewport-culled per-tile streaming.** This is the strongest finding for our Phase 3.5 LOD tier work.

Backend generator: `/Users/chetansahu/DesignBrain-AI/internal/canvas/image_tile_generator.go`
- 280 lines. `GeneratePyramid(ctx, assetID, originalData, w, h)` produces every level from level-0 (smallest overview, single tile) to `maxLevel` (full res).
- `maxLevel = ceil(log2(maxDim / DefaultTileSize))`, `DefaultTileSize = 512`, `DefaultTileOverlap = 2`, `TilingThreshold = 4096` (only tile if any axis > 4096).
- Per-level scale: `scale = 2^(level - maxLevel)`. Source image is downsampled with `golang.org/x/image/draw.CatmullRom.Scale` (good quality / acceptable cost; better than bilinear, cheaper than Lanczos).
- Per-tile encoding: JPEG quality 90 (line 29, `TileQuality`). Bytes go to GridFS bucket `image_assets` with `metadata.extra.{tile_asset_id, tile_level, tile_col, tile_row}`.
- A `TilePyramid` doc is written to MongoDB collection `image_tile_pyramids` with `{ assetId, width, height, tileSize, overlap, maxLevel, levels: [{level, width, height, cols, rows, scale}] }`. Compound MongoDB indexes on `(tile_asset_id, tile_level, tile_col, tile_row)` for tile fetches (line 263).

Frontend index/manager:
- `/Users/chetansahu/DesignBrain-AI/web/src/engine/materials/ImageTileIndex.ts` (171 lines) — precomputes every tile's normalized AABB at construct time so `getVisibleTiles(level, viewAABB)` is a flat overlap test, no per-frame math. Tile URL is `/api/canvas/images/${assetId}/tiles/${level}/${col}/${row}`. Cache key: `tile:${assetId}:${level}:${col}:${row}`.
- `/Users/chetansahu/DesignBrain-AI/web/src/engine/materials/ImageTileManager.ts` (366 lines) — orchestrator.
  - `selectLevel(zoom, dpr)` picks the smallest level where `levelW >= image.w * zoom * dpr` AND `levelH >= image.h * zoom * dpr` (lines 111-130). Always renders at >= 1:1 pixel density.
  - **Level-0 is always pre-fetched as a fallback** (lines 86-88, 294-319). When a higher-level tile is pending, render falls back to a UV sub-region of the level-0 texture (`getFallbackForTile`, lines 321-339). Eliminates pop-in.
  - Concurrency cap: `MAX_CONCURRENT_TILE_LOADS = 6` (line 65).
  - Hot-path render uses `getRenderTilesSync()` (line 227) — no awaits, just GPU-cache reads + fallback substitution.

**Pros for Projects · Flow Atlas:**
- The size-tier idea (1024 / 2048 / 4096) maps directly onto pyramid levels. We just don't need spatial tiling for atlas frames — each frame is small (a screen). One tile per level per frame is fine.
- The zoom→level selection formula transfers verbatim: `select tier such that tierPx >= screenWorldWidth * zoom * dpr`.
- Level-0 fallback pattern is the right answer for "what do we render before the 4096 tile arrives" — start at 1024, swap up.
- Catmull-Rom downsample in Go via `golang.org/x/image/draw` is a copy-paste solution.
- JPEG q=90 baseline is sane; we'd want PNG for non-photographic UI screens (or quality-90 WebP).

**Cons / mismatch:**
- They tile big single images; our atlas problem is "many small frames." We don't need the col/row spatial partitioning — only the level (1024/2048/4096) ladder. This actually **simplifies** the schema.

## 2. InstancedMesh / texture sharing

**They don't use Three.js InstancedMesh at all — they hand-roll instancing on raw WebGL2 with `drawArraysInstanced` / `drawElementsInstanced`, batched by render state.** Texture sharing is solved in two different ways for two different problems.

Instancing call sites in `/Users/chetansahu/DesignBrain-AI/web/src/engine/render/RenderEngine.ts`:
- L1338: `gl.drawArraysInstanced(...)` for the main shape pass.
- L1646: `gl.drawElementsInstanced(...)` for tessellated vector geometry.
- L2006: instanced TRIANGLE_STRIP for full-screen quads.

How batches are formed: `/Users/chetansahu/DesignBrain-AI/web/src/engine/scene/BatchAssigner.ts` (550 lines).
- Batch key string: `"<shape>|<fillType>|<blendMode>|<clipGroupId>"` plus a gradient-identity hash (`hashGradientFill`, lines 36-50) so each unique gradient gets its own batch (gradient uniforms are global per draw).
- Per-instance attributes: transform matrix, color, corner radius — packed into vertex attribute arrays with `vertexAttribDivisor`.
- Distinct **MaterialRegistry** (line 72) gives O(1) integer-key dedup for batches.

How they share textures across instances:
- **For text glyphs** — one shared MSDF/Canvas2D atlas page with shelf packing + LRU eviction. `/Users/chetansahu/DesignBrain-AI/web/src/engine/text/TextAtlas.ts` (608 lines) and `MSDFTextAtlas`. All glyph quads in a batch sample one atlas texture; per-instance UV rect lives in instance attributes.
- **For images** — they do NOT pack image instances into one atlas. Instead `ImageTileManager` keeps each image as its own GPU texture, and culling + memory budget keep the working set bounded. The result: image-quads are not really instanced across different images; they end up in their own batch each. This is a deliberate trade — atlas packing dynamic user-uploaded photos is its own can of worms (rebakes, eviction, alpha bleeding, format mismatch).

**Pros for Projects · Flow Atlas:**
- The text-atlas pattern (one shared GL texture, per-instance UV in instance attributes) is the textbook answer for our "render >50 frames in one draw call" goal. Each Projects screen frame is small enough that packing 50–200 of them into a 4096×4096 RGBA texture is realistic.
- BatchAssigner's batch-key pattern is good prior art: group frames sharing the same atlas page + blend mode + clip group; emit one instanced draw per group.
- For Projects, given each frame is one quad, plain `drawArraysInstanced(TRIANGLE_STRIP, 0, 4, instanceCount)` over a shared atlas page is enough — no need for full BatchAssigner machinery.

**Cons / mismatch:**
- DesignBrain explicitly chose to **not** atlas user images, and that decision is right for their use case (arbitrary photos, dynamic uploads). For Projects, our atlas frames are a fixed catalog persisted at build time, so server-side packing is cheap. The constraint that pushed them away doesn't apply to us.
- Their batching infra (BatchAssigner, MaterialRegistry, RenderTree) is overkill for our flat list of frames.

## 3. KTX2 / Basis Universal usage

**Zero. They do not use KTX2, Basis Universal, or compressed GPU textures anywhere in product code.** `grep -ril 'ktx2|basisu|basis_universal|KTX2Loader|BasisTextureLoader'` over `web/src`, `internal`, `docs`, `cmd`, `services`, `pkg` returns only `services/ai-sidecar/node_modules/.../HISTORY.md` (mime-type changelogs — not code).

What they ship instead:
- Server: JPEG (q=90) for tile bytes, PNG/JPEG/WebP for thumbnails (`/Users/chetansahu/DesignBrain-AI/internal/thumbnail/thumbnail_service.go`).
- Client: `gl.texImage2D(..., gl.RGBA, gl.UNSIGNED_BYTE, source)` with `gl.generateMipmap(gl.TEXTURE_2D)` and `LINEAR_MIPMAP_LINEAR` (lines 603-614 in `web/src/engine/materials/ImageLoader.ts`). They generate mipmaps at upload time, accept the 33% memory overhead, and call it done.
- They guard against >MAX_TEXTURE_SIZE by downsampling via `OffscreenCanvas` before upload (lines 580-600).

So if Projects wants KTX2, **DesignBrain is no help** — we'd be inventing this for the codebase rather than mirroring an existing pattern. The closest analog is the upload-mipmap-LINEAR_MIPMAP_LINEAR setup, which is fine for raw PNG and would be replaced wholesale by KTX2.

## 4. Other performance patterns worth adopting

These aren't on the original three-item list but are highly applicable:

### 4a. Global GPU memory budget singleton
`/Users/chetansahu/DesignBrain-AI/web/src/engine/render/GPUMemoryBudget.ts` (117 lines).
- Singleton tracks bytes per subsystem (`atlas | images | framebuffers`).
- 1024 MB desktop, 512 MB mobile (auto-detected via `WEBGL_debug_renderer_info` regex matching `mali|adreno|powervr|apple gpu|intel hd`).
- `trackAlloc(subsystem, bytes)` / `trackFree(...)` called from every texture creation/eviction site.
- `MIPMAP_FACTOR = 1.33` — they explicitly account for the mipmap chain in budget math (`estimateTextureBytes(w, h, hasMipmaps)`).
- `onPressure()` callback fires when `utilization > 0.9` — subsystems can self-evict.

This is exactly what Projects needs once we start juggling KTX2 tiers — without budget tracking, the 4096 tier blows up VRAM on low-end devices.

### 4b. Per-frame upload throttling
`/Users/chetansahu/DesignBrain-AI/web/src/engine/render/UploadBudget.ts` (135 lines).
- 200KB/frame default cap on `bufferSubData` writes. At 60fps that's ~12MB/s sustained, well under bus bandwidth, prevents jank spikes.
- Priority lanes P0 (preview drag, never deferred) → P1 (visible) → P2 (nearby) → P3 (deferred, retried next frame).
- Critical insight: "Always allow the first upload to prevent starvation of large batches" (line 69) — without this guard, a single >budget batch never uploads.

We don't currently have an upload budget. When KTX2 lands and 4096 tiers start streaming during pan/zoom, we will want this exact thing.

### 4c. HOT / WARM viewport ring with hysteresis
`/Users/chetansahu/DesignBrain-AI/web/src/engine/scene/ViewportCuller.ts` (234 lines).
- Three zones: **HOT** (viewport + 800px CSS margin → fully rendered), **WARM** (HOT × 2.0 → GPU slot allocated, opacity = 0, instant promote on pan), **outside** (no GPU slot).
- Pinned set: selected/hovered/edited nodes are forced HOT regardless of viewport (selection safety rule).
- Hysteresis: `VIEWPORT_HYSTERESIS = 0.05` — re-query R-tree only when viewport moves > 5% of its own dimension. Stops thrash on sub-pixel pans.
- Reusable `Set` instances kept on the culler so per-frame culling allocates zero (line 60).

This is directly applicable: when Projects pans across 50+ atlas frames, we want HOT/WARM rings around viewport so frames just outside don't pop in visibly.

### 4d. LOD by node screen size, not just zoom
From `docs/architecture/render-pipeline.md` (line 105-116): nodes whose `worldSize * zoom < 1 CSS pixel` are skipped entirely. Below zoom 0.05, all frame/section labels hide. Tiered LOD on zoom thresholds (>=0.5, 0.1–0.5, 0.05–0.1, <0.05).

For atlas: at low zoom, drop label rendering entirely; only paint the texture. At very low zoom, swap to the level-0 (1024) tier even if device DPR demands more — no eye can tell.

### 4e. Adaptive FPS / dirty-driven render loop
`RenderEngine` runs full 60fps only during pointer events / animations. Sleeps when no dirty nodes. `markDirty()` wakes the loop. Camera changes require explicit `notifyCameraChanged() → markDirty()`. (`docs/architecture/render-pipeline.md` lines 149-155.)

We are likely doing rAF-every-frame today. Switching the atlas surface to dirty-driven would cut idle CPU dramatically.

### 4f. Stencil-based clip groups with `colorMask` invariant
Documented invariant in `render-pipeline.md` line 132-134: `gl.colorMask(true, true, true, true)` MUST be active before any visible `drawArraysInstanced()`. Stencil-only writes set colorMask false; both `enableTest()` and `disableTest()` restore it. They flag this as a "critical bug fix" — without restoration, all subsequent draws produce no visible output.

If Projects ever uses stencil for atlas-frame clipping (e.g. rounded-corner masks at scale), copy this invariant verbatim.

## 5. Patterns we should NOT adopt

- **Don't atlas user-uploaded images.** DesignBrain explicitly keeps photos as one-texture-per-image rather than packing them. Their reasoning: dynamic uploads, rebake cost, alpha bleeding. We can ignore this for Projects (our atlas is a fixed catalog), but we should not generalize the pattern to user content later.
- **Don't try to reuse their backend tile-pyramid schema verbatim.** They tile **inside** a single large image (col, row, level). Projects has many small frames; we just need one tier per frame per size (no col/row dimension). Adopting their full schema adds a dimension we'll always set to 0 — wastes index storage and confuses future readers.
- **Don't skip the `colorMask(true,…)` restoration after stencil writes.** Their commit history calls it a "critical bug fix" — it's a known pothole.
- **Don't allow a single oversized upload to bypass the per-frame budget.** Their `UploadBudget` has the "always allow the first upload" exception explicitly because batches > budget would never upload otherwise. We'd want the same exception.
- **Avoid Three.js InstancedMesh for textured frames if you intended one texture per instance.** DesignBrain proves that the right answer for shared-texture instancing is a packed atlas + per-instance UV rect — not a `DataArrayTexture` or per-instance binding. Drei InstancedMesh works fine; the texture-sharing strategy is the decision point.

## 6. Recommended adoption plan for Projects · Flow Atlas

### Backend (Phase 3.5 follow-up #1 — LOD tier generation)

Mirror the Go pyramid generator, simplified to one tile per level (no col/row). New file in our Go service (parallel structure to DesignBrain's `internal/canvas/image_tile_generator.go`):

- Input: per-screen full-res PNG (already produced).
- Levels: emit fixed tiers `[1024, 2048, 4096]` (only tiers <= source size).
- Downsample: `golang.org/x/image/draw.CatmullRom.Scale` (already in stdlib path; no new dep).
- Encode each tier as PNG (preserve UI fidelity) **and** KTX2/UASTC (Phase 3.5 follow-up #3).
- Persist shape: per-screen `lod_tiers: [{ size: 1024, png_url, ktx2_url, bytes }, ...]` with `chosenSize` selection deferred to client. Keep flat — no col/row.

### Frontend (Phase 3.5 follow-up #2 — Instanced rendering)

- Build a server-side "atlas page" packer: take all 1024-tier frames in a project, shelf-pack into 4096×4096 RGBA atlas page(s), emit `{pageUrl, frames: [{frameId, u0, v0, u1, v1, w, h}]}`. Repeat per tier.
- Client-side, per atlas page: one `THREE.InstancedMesh` (or raw `drawArraysInstanced` if we're going non-r3f for this surface) bound to that page. Per-instance attributes: `[modelMatrix, uvRect, opacity]`. One draw call per atlas page.
- Mirror their level-0 fallback pattern: load the 1024 atlas page first, swap individual frame quads up to 2048/4096 as separate (non-atlased) textures when the user zooms in past the threshold.
- Add a `GPUMemoryBudget` singleton to `app/lib/atlas/` (translation of their TS file). Track `atlas | hires | ui`. Default 1024MB desktop, 512MB mobile via the same `WEBGL_debug_renderer_info` sniff.
- Add `UploadBudget` (200KB/frame, P0–P3 lanes) once we start streaming hires tiers during pan.

### Frontend (Phase 3.5 follow-up #3 — KTX2/Basis)

DesignBrain has nothing for us here. Recommendations from outside the repo:
- Drop `basis_transcoder.js` + `basis_transcoder.wasm` into `public/basis/`.
- Use drei `<KTX2Loader>` with `gl.detect()` for compression-format detection (BC7 desktop, ASTC mobile, ETC2 fallback).
- Mark sRGB on color textures (`texture.colorSpace = THREE.SRGBColorSpace`).
- Ship mipmaps **inside** the KTX2 (Basis can do this) so we don't pay `gl.generateMipmap` cost at upload time — the inverse of what DesignBrain does for raw PNG.

### Quick wins independent of LOD/KTX2

These are cheap to lift right now:
1. Translate `GPUMemoryBudget.ts` (117 lines, no deps) → `app/lib/atlas/GPUMemoryBudget.ts`. Wire `trackAlloc/Free` into all texture create/dispose sites.
2. Translate `UploadBudget.ts` (135 lines, no deps) → `app/lib/atlas/UploadBudget.ts`. Gate buffer uploads.
3. Add HOT/WARM ring + hysteresis to atlas viewport (mirror `ViewportCuller.ts`, ~100 lines after stripping the visibility-set tracking we don't need).
4. Adopt their LOD-by-screen-size rule: drop labels at zoom < 0.05, drop everything < 1 CSS pixel.

### File-path mapping cheat-sheet (DesignBrain → Projects)

| DesignBrain (read-only) | Projects (suggested target) |
|-------------------------|-----------------------------|
| `internal/canvas/image_tile_generator.go` | `services/ds-service/internal/atlas/lod_tier_generator.go` (new) |
| `web/src/engine/materials/ImageTileIndex.ts` | not needed — flat tier list, no col/row |
| `web/src/engine/materials/ImageTileManager.ts` | `app/lib/atlas/LodTierManager.ts` (new, ~150 lines after simplification) |
| `web/src/engine/render/GPUMemoryBudget.ts` | `app/lib/atlas/GPUMemoryBudget.ts` (port verbatim) |
| `web/src/engine/render/UploadBudget.ts` | `app/lib/atlas/UploadBudget.ts` (port verbatim) |
| `web/src/engine/scene/ViewportCuller.ts` | `app/lib/atlas/ViewportCuller.ts` (strip visibility-set, keep HOT/WARM + hysteresis) |
| `web/src/engine/scene/BatchAssigner.ts` | not needed — our batches are flat per atlas page |
| `web/src/engine/text/TextAtlas.ts` (shelf packing) | reference for server-side atlas packer (Go side) |

### Risks surfaced from their codebase

1. **Mipmap memory math.** They explicitly multiply texture bytes by 1.33 for mipmaps. If we ship KTX2 with mipmaps and we forget that factor, our budget math is 25% off and we'll over-allocate.
2. **Mobile GPU detection regex.** Their regex is `mali|adreno|powervr|apple gpu|intel (hd|uhd|iris)`. M-series Macs and recent Apple Silicon iGPUs return "apple gpu" — they'll be classified mobile (512MB) on a desktop iMac/MacBook. That may or may not be the right call; review before we copy.
3. **Hysteresis + selection safety.** Their ViewportCuller adds a `pinnedNodes` set so the *currently selected* node never gets culled when the user pans. We need the equivalent for Projects (hovered/clicked frame stays HOT) or the user will see their selection vanish.
4. **`gl.colorMask` restoration after stencil.** Documented as a "critical bug fix" — if Projects ever introduces stencil for clipping, do not skip the restoration. All subsequent `drawArraysInstanced` calls produce zero pixels otherwise.
5. **Single-upload starvation.** Their UploadBudget has an explicit "always allow the first upload" exception; without it, any batch larger than the budget never gets uploaded. Mirror this when we port.
6. **They give up on instancing user-uploaded photos.** Worth understanding *why* before generalizing our atlas-page approach to user-uploaded images later — dynamic uploads + rebakes + alpha bleeding are real costs.
