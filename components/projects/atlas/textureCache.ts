"use client";

/**
 * Module-level texture cache for the U7 atlas.
 *
 * Why a singleton:
 *   The same screen PNG may be referenced by multiple frames simultaneously
 *   (e.g. a section that re-uses a sub-flow), and theme toggles roundtrip
 *   between the same URL set repeatedly. Without a cache, every theme flip
 *   re-decodes every PNG and three.js re-uploads to GPU memory — wasteful
 *   on both bandwidth and VRAM.
 *
 * Why module-level (not React context):
 *   `<AtlasCanvas>` is dynamic-imported inside a r3f `<Canvas>` whose React
 *   tree is independent from the outer DOM tree. A context would require
 *   re-providing across the boundary; a singleton survives both unmounts and
 *   theme toggles cleanly.
 *
 * Memory budget:
 *   Phase 1 plan caps total texture bytes at ~200 MB. The cache exposes
 *   `totalBytes()` so callers can warn or evict; eviction is manual via
 *   `disposeTexture(url)` for now (Phase 3 ships LRU + LOD).
 *
 * Lifecycle:
 *   `getTexture(url)` returns a cached `THREE.Texture` if present, otherwise
 *   creates one via `TextureLoader` and caches it. Disposed textures are
 *   removed from the cache so a subsequent `getTexture` re-loads them. The
 *   GPU texture is freed via `THREE.Texture#dispose()` — the caller is
 *   responsible for ensuring no live material still references it.
 */

import * as THREE from "three";

interface CacheEntry {
  texture: THREE.Texture;
  /** Approximate decoded byte count once the image has loaded. */
  bytes: number;
}

const cache = new Map<string, CacheEntry>();
const loader = new THREE.TextureLoader();

/**
 * Returns a cached texture or starts loading one.
 *
 * The returned `THREE.Texture` is usable immediately — three.js sets `.image`
 * once decode completes and triggers a re-render via the texture's
 * `needsUpdate` flag, which `TextureLoader` handles internally.
 *
 * `onLoad` and `onError` callbacks are forwarded so frame components can
 * clear their loading state / show error mesh.
 */
export function getTexture(
  url: string,
  onLoad?: (tex: THREE.Texture) => void,
  onError?: (err: unknown) => void,
): THREE.Texture {
  const existing = cache.get(url);
  if (existing) {
    if (existing.texture.image && onLoad) {
      // Image already decoded — fire onLoad on next microtask so callers can
      // treat the path uniformly (always async).
      queueMicrotask(() => onLoad(existing.texture));
    }
    return existing.texture;
  }

  // Synchronously construct an empty texture so the Map has an entry before
  // the loader resolves; this de-dupes concurrent requests for the same URL.
  const tex = loader.load(
    url,
    (loaded) => {
      // Estimate bytes: width × height × 4 (RGBA8). PNG decoder gives us
      // pixel dimensions on `.image` once the load completes.
      const img = loaded.image as HTMLImageElement | undefined;
      const w = img?.naturalWidth ?? img?.width ?? 0;
      const h = img?.naturalHeight ?? img?.height ?? 0;
      const entry = cache.get(url);
      if (entry) entry.bytes = w * h * 4;
      // Color-space hint: PNGs are sRGB-encoded; tagging avoids gamma drift.
      loaded.colorSpace = THREE.SRGBColorSpace;
      onLoad?.(loaded);
    },
    undefined,
    (err) => {
      // On error, evict so a future retry doesn't return the broken texture.
      cache.delete(url);
      onError?.(err);
    },
  );

  cache.set(url, { texture: tex, bytes: 0 });
  return tex;
}

/**
 * Disposes the GPU texture and removes it from the cache. Safe to call with
 * an unknown URL (no-op).
 */
export function disposeTexture(url: string): void {
  const entry = cache.get(url);
  if (!entry) return;
  entry.texture.dispose();
  cache.delete(url);
}

/** Returns the approximate total bytes currently held in cached textures. */
export function totalBytes(): number {
  let total = 0;
  for (const e of cache.values()) total += e.bytes;
  return total;
}

/** Phase 1 budget — plan caps at 200 MB. Exported for tests + warnings. */
export const TEXTURE_BUDGET_BYTES = 200 * 1024 * 1024;

/**
 * Internal escape hatch for tests — clears the cache. Production code should
 * use `disposeTexture(url)` per-URL so half-loaded fetches don't leak.
 */
export function _resetForTests(): void {
  for (const e of cache.values()) e.texture.dispose();
  cache.clear();
}
