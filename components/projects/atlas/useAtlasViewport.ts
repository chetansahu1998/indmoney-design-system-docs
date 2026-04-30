"use client";

/**
 * Initial-fit + persisted-zoom hook for the U7 atlas.
 *
 * Responsibilities:
 *   - Compute the bounding box of all screens (in Figma section-relative
 *     coordinates) and derive a camera position + orthographic zoom that
 *     fits the longest axis with 10% padding around the edges.
 *   - Persist the user's zoom-after-pan to `localStorage` keyed by the
 *     `(slug, version)` pair, so re-opening a project preserves the user's
 *     last view. Key composition matches U6's deeplink discipline:
 *     `atlas-viewport:<slug>:<version>` — version-bumping invalidates the
 *     persisted view (intentional — bounding box may have changed).
 *
 * Returned API:
 *   - `initialPosition`: 2D world-space coordinate the camera should look
 *     at (z is fixed; orthographic camera stays orthogonal to the plane).
 *   - `initialZoom`: orthographic zoom factor — larger = more zoomed in.
 *   - `fitToBounds(camera, screens)`: imperative re-fit (used after the
 *     screens prop changes, e.g. after a version flip).
 *   - `persistZoom(zoom)`: writes the current zoom to localStorage.
 *
 * Phase 1 keeps everything in 2D — the orthographic camera looks down the
 * +Z axis; X grows right, Y grows down (matching Figma's coordinate space,
 * but flipped at draw time inside AtlasFrame so the texture isn't mirrored).
 */

import { useMemo } from "react";
import type { Screen } from "@/lib/projects/types";
import type * as THREE from "three";

export interface AtlasViewport {
  initialPosition: [number, number, number];
  initialZoom: number;
  /** Imperative re-fit. The camera is the live r3f `OrthographicCamera`. */
  fitToBounds: (
    camera: THREE.OrthographicCamera,
    screens: Screen[],
    viewportWidthPx: number,
    viewportHeightPx: number,
  ) => void;
  /** Saves the current zoom under the (slug, version) key. */
  persistZoom: (zoom: number) => void;
}

/** 10% breathing room around the section bounds at initial fit. */
const FIT_PADDING = 0.1;

/** Reasonable zoom guard so a degenerate single-frame project still fits. */
const MIN_INITIAL_ZOOM = 0.05;
const MAX_INITIAL_ZOOM = 4.0;

function storageKey(slug: string, versionID: string | undefined): string {
  return `atlas-viewport:${slug}:${versionID ?? "default"}`;
}

interface PersistedView {
  zoom: number;
}

function readPersisted(slug: string, versionID?: string): PersistedView | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(storageKey(slug, versionID));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<PersistedView>;
    if (typeof parsed.zoom !== "number" || !Number.isFinite(parsed.zoom)) {
      return null;
    }
    return { zoom: parsed.zoom };
  } catch {
    return null;
  }
}

function writePersisted(
  slug: string,
  versionID: string | undefined,
  view: PersistedView,
): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(
      storageKey(slug, versionID),
      JSON.stringify(view),
    );
  } catch {
    // Quota exceeded / private browsing — silently ignore. The atlas re-fits
    // to bounds on next mount, which is an acceptable degradation.
  }
}

/**
 * Computes the (x, y) center + width/height of a screen list in Figma
 * coordinate space (positive Y points down). Used by both the initial fit
 * computation and the imperative `fitToBounds` re-fit.
 */
export function computeBounds(screens: Screen[]): {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
  width: number;
  height: number;
  centerX: number;
  centerY: number;
} {
  if (screens.length === 0) {
    return {
      minX: 0,
      minY: 0,
      maxX: 0,
      maxY: 0,
      width: 1,
      height: 1,
      centerX: 0,
      centerY: 0,
    };
  }
  let minX = Number.POSITIVE_INFINITY;
  let minY = Number.POSITIVE_INFINITY;
  let maxX = Number.NEGATIVE_INFINITY;
  let maxY = Number.NEGATIVE_INFINITY;
  for (const s of screens) {
    if (s.X < minX) minX = s.X;
    if (s.Y < minY) minY = s.Y;
    if (s.X + s.Width > maxX) maxX = s.X + s.Width;
    if (s.Y + s.Height > maxY) maxY = s.Y + s.Height;
  }
  const width = Math.max(maxX - minX, 1);
  const height = Math.max(maxY - minY, 1);
  return {
    minX,
    minY,
    maxX,
    maxY,
    width,
    height,
    centerX: minX + width / 2,
    centerY: minY + height / 2,
  };
}

/**
 * Derives an orthographic zoom factor that fits the section bounds in the
 * given viewport at `FIT_PADDING` margin. The orthographic frustum size in
 * world units equals viewport-pixels / zoom, so:
 *
 *   zoom = viewport_px / (world_size * (1 + 2 * padding))
 *
 * We pick the smaller of the two-axis zooms so both axes fit.
 */
function computeFitZoom(
  worldWidth: number,
  worldHeight: number,
  viewportPx: number,
  viewportPy: number,
): number {
  const zx = viewportPx / (worldWidth * (1 + 2 * FIT_PADDING));
  const zy = viewportPy / (worldHeight * (1 + 2 * FIT_PADDING));
  const z = Math.min(zx, zy);
  if (!Number.isFinite(z) || z <= 0) return 1;
  return Math.min(Math.max(z, MIN_INITIAL_ZOOM), MAX_INITIAL_ZOOM);
}

/**
 * Returns a stable {initialPosition, initialZoom, fitToBounds, persistZoom}
 * tuple. Recomputes only when `screens.length` or the section bounding box
 * changes — pan/hover state inside the canvas never re-runs this.
 */
export function useAtlasViewport(
  slug: string,
  screens: Screen[],
  versionID?: string,
): AtlasViewport {
  return useMemo(() => {
    const bounds = computeBounds(screens);

    // Default fit: assume a square-ish viewport at first paint. The Canvas
    // resize observer issues a `fitToBounds` once it knows real pixel size.
    const guessViewportPx = 1200;
    const guessViewportPy = 600;
    const fitZoom = computeFitZoom(
      bounds.width,
      bounds.height,
      guessViewportPx,
      guessViewportPy,
    );

    const persisted = readPersisted(slug, versionID);
    const initialZoom = persisted?.zoom ?? fitZoom;

    return {
      initialPosition: [bounds.centerX, -bounds.centerY, 10],
      initialZoom,
      fitToBounds: (camera, nextScreens, viewportPx, viewportPy) => {
        const b = computeBounds(nextScreens);
        camera.position.set(b.centerX, -b.centerY, 10);
        camera.zoom = computeFitZoom(b.width, b.height, viewportPx, viewportPy);
        camera.updateProjectionMatrix();
      },
      persistZoom: (zoom) => {
        if (!Number.isFinite(zoom) || zoom <= 0) return;
        writePersisted(slug, versionID, { zoom });
      },
    };
    // The bounds depend on screen identities + their positions; serialise
    // those for the dependency array so we don't re-run on unrelated prop churn.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    slug,
    versionID,
    screens.length,
    screens.map((s) => `${s.ID}:${s.X}:${s.Y}:${s.Width}:${s.Height}`).join("|"),
  ]);
}
