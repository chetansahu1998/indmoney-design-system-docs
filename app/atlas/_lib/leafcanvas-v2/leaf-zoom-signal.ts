/**
 * leaf-zoom-signal.ts — module-level pub/sub for the active leaf
 * canvas's pan/zoom scale, so deep descendants can read it without
 * prop-drilling through the leaf canvas → real-data-bridge → frame
 * wrapper → renderer chain.
 *
 * Same pattern as canvasFetchQueue and canvasIdleTracker — one
 * singleton per app, lives outside React's render tree, components
 * subscribe via the `useLeafZoom` hook.
 *
 * Producer: `app/atlas/_lib/leafcanvas.tsx` writes `setLeafZoom(cam.z)`
 * inside its pan/zoom effect.
 *
 * Consumers: `LeafFrameRenderer` reads zoom via `useLeafZoom()` and
 * threads it into `useIconClusterURLs` for tier-aware URL minting.
 *
 * Default value `1` covers the SSR / no-leaf case (every consumer
 * sees zoom=1) so cluster mints still pick a sensible tier.
 */

import { useSyncExternalStore } from "react";

let currentZoom = 1;
const listeners = new Set<() => void>();

/** Producer side: leafcanvas.tsx calls this whenever cam.z changes. */
export function setLeafZoom(z: number): void {
  if (!Number.isFinite(z) || z <= 0) return;
  if (currentZoom === z) return;
  currentZoom = z;
  for (const fn of [...listeners]) {
    try {
      fn();
    } catch {
      // Swallow listener errors so a single bad consumer can't sink the rest.
    }
  }
}

/** Imperative read for non-React contexts. */
export function getLeafZoom(): number {
  return currentZoom;
}

/** Module-level subscribe (used by useSyncExternalStore). */
function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

/**
 * React hook — returns the current leaf-canvas zoom and re-renders
 * the consumer when it changes. Uses useSyncExternalStore for
 * concurrent-mode safety; SSR snapshot returns 1.
 */
export function useLeafZoom(): number {
  return useSyncExternalStore(subscribe, getLeafZoom, () => 1);
}
