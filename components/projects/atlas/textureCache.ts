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
import { KTX2Loader } from "three/examples/jsm/loaders/KTX2Loader.js";

interface CacheEntry {
  texture: THREE.Texture;
  /** Approximate decoded byte count once the image has loaded. */
  bytes: number;
}

const cache = new Map<string, CacheEntry>();
const loader = new THREE.TextureLoader();

// Phase 3.5 follow-up: KTX2 loader for the .ktx2 sidecars the backend
// transcodes via basisu. Lazy-initialised on first KTX2 load attempt
// so non-KTX2 callers don't pay the WASM transcoder bootstrap cost.
//
// The Basis transcoder JS + WASM live in /public/basis/ (copied from
// node_modules/three/examples/jsm/libs/basis/ at build/install time).
// drei's `<KTX2Loader>` wrapper isn't used here — we want a flat
// imperative path that fits the existing getTexture() shape.
let ktx2Loader: KTX2Loader | null = null;
let ktx2RendererAttached = false;

/**
 * Returns a lazily-constructed KTX2Loader. The transcoder path defaults
 * to /basis/; an env override can swap it via NEXT_PUBLIC_BASIS_PATH if
 * a future deploy hosts the WASM elsewhere.
 *
 * detectSupport() needs the WebGL renderer so it can pick the right
 * compression format (BC1, ETC2, ASTC, etc.). The renderer is attached
 * once via attachKTX2Renderer() — until then the loader returns
 * uncompressed RGBA8 textures (still functional, just larger).
 */
function getKTX2Loader(): KTX2Loader {
  if (ktx2Loader) return ktx2Loader;
  ktx2Loader = new KTX2Loader();
  const basisPath =
    (typeof process !== "undefined" &&
      (process as { env?: Record<string, string | undefined> }).env
        ?.NEXT_PUBLIC_BASIS_PATH) ||
    "/basis/";
  ktx2Loader.setTranscoderPath(basisPath);
  return ktx2Loader;
}

/**
 * Hands the live r3f WebGLRenderer to the KTX2Loader so it can detect
 * GPU compression-format support. Called once from AtlasCanvas's onCreated
 * callback. Idempotent.
 */
export function attachKTX2Renderer(renderer: THREE.WebGLRenderer): void {
  if (ktx2RendererAttached) return;
  const l = getKTX2Loader();
  l.detectSupport(renderer);
  ktx2RendererAttached = true;
}

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
 * Phase 3.5 follow-up: try KTX2 first, fall back to PNG.
 *
 * Given a PNG URL like `/v1/projects/welcome/screens/abc/png`, derives
 * the sibling KTX2 URL (`/ktx2`), attempts to load it via the KTX2Loader,
 * and on failure (404 from missing transcode + Basis CLI; or parse
 * error) falls through to the original PNG URL via TextureLoader. The
 * cache key is the FINAL successful URL so subsequent calls go to the
 * same path.
 *
 * Why a separate function instead of routing through getTexture: KTX2
 * loading is async-only (the loader's load() is asynchronous), so the
 * synchronous "create empty Texture upfront for de-dupe" trick that
 * TextureLoader uses doesn't apply. This function returns a Promise<
 * Texture> instead — callers (AtlasFrame) use it inside useEffect.
 */
export async function getTextureKTX2OrPNG(
  pngURL: string,
  onLoad?: (tex: THREE.Texture) => void,
  onError?: (err: unknown) => void,
): Promise<THREE.Texture> {
  // Cache hit on either ktx2 OR png URL — return whichever's there.
  const ktx2URL = pngURLToKTX2(pngURL);
  const cachedKTX2 = cache.get(ktx2URL);
  if (cachedKTX2) {
    if (cachedKTX2.texture.image && onLoad) {
      queueMicrotask(() => onLoad(cachedKTX2.texture));
    }
    return cachedKTX2.texture;
  }
  const cachedPNG = cache.get(pngURL);
  if (cachedPNG) {
    if (cachedPNG.texture.image && onLoad) {
      queueMicrotask(() => onLoad(cachedPNG.texture));
    }
    return cachedPNG.texture;
  }

  // Try KTX2 first.
  try {
    const k = getKTX2Loader();
    const tex = await new Promise<THREE.Texture>((resolve, reject) => {
      k.load(
        ktx2URL,
        (t) => resolve(t),
        undefined,
        (err) => reject(err),
      );
    });
    tex.colorSpace = THREE.SRGBColorSpace;
    const img = (tex.image as { width?: number; height?: number } | null) ?? null;
    const w = img?.width ?? 0;
    const h = img?.height ?? 0;
    cache.set(ktx2URL, { texture: tex, bytes: w * h * 4 });
    onLoad?.(tex);
    return tex;
  } catch (ktx2Err) {
    // Fall through to PNG. Don't surface the KTX2 error to onError —
    // the PNG path is the canonical fallback, not a user-facing failure.
    void ktx2Err;
  }

  // PNG fallback via the existing TextureLoader path. We re-use the
  // existing getTexture() shape so the cache layer dedupes concurrent
  // PNG fetches (e.g. two AtlasFrames hitting the same PNG when KTX2
  // is universally missing).
  return new Promise<THREE.Texture>((resolve, reject) => {
    const tex = getTexture(
      pngURL,
      (t) => {
        onLoad?.(t);
        resolve(t);
      },
      (err) => {
        onError?.(err);
        reject(err);
      },
    );
    // If the texture was already loaded synchronously (cache hit), the
    // onLoad callback above runs in a microtask and resolve happens then.
    // Nothing to do here.
    void tex;
  });
}

/**
 * Suffix swap: <id>@2x.png URLs ↔ <id>@2x.ktx2 URLs. Works on the
 * URL path returned by `lib/projects/client.ts:screenPngUrl()` which
 * is `/v1/projects/<slug>/screens/<id>/png`. Backend ships a sibling
 * /ktx2 route at the same path shape.
 */
function pngURLToKTX2(pngURL: string): string {
  if (pngURL.endsWith("/png")) {
    return pngURL.slice(0, -3) + "ktx2";
  }
  if (pngURL.endsWith(".png")) {
    return pngURL.slice(0, -4) + ".ktx2";
  }
  return pngURL;
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
