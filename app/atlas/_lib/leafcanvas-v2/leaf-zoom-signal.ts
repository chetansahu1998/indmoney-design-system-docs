/**
 * leaf-zoom-signal.ts — module-level pub/sub for the active leaf
 * canvas's pan/zoom scale. Two signals are exposed:
 *
 *   liveZoom    — updates on every producer call (every wheel tick)
 *                 Use for affordances that should follow the gesture
 *                 (e.g. the zoom-% badge in the bottom toolbar).
 *
 *   settledZoom — updates only when `canvasGestureTracker` reports
 *                 that the gesture has ended (~150 ms after the last
 *                 wheel/pan event). Use for expensive consumers that
 *                 react to viewport change — preview-tier selection,
 *                 image-fill prefetch, canonical_tree hydration.
 *                 Reading settledZoom prevents mid-zoom <img> remounts
 *                 when the user crosses a preview-tier boundary
 *                 (e.g. 0.4 → 0.6 spans the 512→1024 px tier).
 *
 * Producer: `app/atlas/_lib/leafcanvas.tsx` calls `setLeafZoom(z)` on
 * every camera update. The producer doesn't know or care whether a
 * gesture is in flight; this module routes the value to both signals
 * based on gesture-tracker state.
 *
 * Same singleton/useSyncExternalStore pattern as canvasFetchQueue and
 * canvasIdleTracker — one instance per app, lives outside React's
 * render tree.
 *
 * Backwards-compat: `useLeafZoom` is preserved as an alias for
 * `useLeafZoomSettled`, since every existing caller uses it for tier
 * selection and that's exactly what should now be debounced. Direct
 * UI-feedback consumers (zoom badge) should switch to `useLeafZoomLive`.
 */

import { useSyncExternalStore } from "react";

import { canvasGestureTracker } from "./gesture-tracker";

let liveZoom = 1;
let settledZoom = 1;
const liveListeners = new Set<() => void>();
const settledListeners = new Set<() => void>();

/**
 * Producer side: leafcanvas.tsx calls this whenever cam.z changes.
 *
 * Always updates the live signal. The settled signal updates only
 * when the gesture-tracker reports we are NOT mid-gesture; otherwise
 * the gesture-end subscriber (registered below) will sync settled to
 * the latest live value once the gesture finishes.
 */
export function setLeafZoom(z: number): void {
  if (!Number.isFinite(z) || z <= 0) return;
  if (liveZoom !== z) {
    liveZoom = z;
    notify(liveListeners);
  }
  if (!canvasGestureTracker.isGesturing && settledZoom !== z) {
    settledZoom = z;
    notify(settledListeners);
  }
}

/** Imperative read for non-React contexts. */
export function getLeafZoomLive(): number {
  return liveZoom;
}
export function getLeafZoomSettled(): number {
  return settledZoom;
}

/** Back-compat alias — pre-split callers expected the (now-settled) signal. */
export const getLeafZoom = getLeafZoomSettled;

function subscribeLive(cb: () => void): () => void {
  liveListeners.add(cb);
  return () => {
    liveListeners.delete(cb);
  };
}
function subscribeSettled(cb: () => void): () => void {
  settledListeners.add(cb);
  return () => {
    settledListeners.delete(cb);
  };
}

function notify(set: Set<() => void>): void {
  // Snapshot so unsubscribe-during-callback doesn't mutate iteration.
  const snapshot = [...set];
  for (const fn of snapshot) {
    try {
      fn();
    } catch {
      // Swallow listener errors so a single bad consumer can't sink the rest.
    }
  }
}

/**
 * Live zoom — re-renders on every producer call. Use for UI that
 * should track the gesture in real time (zoom-percent badge).
 */
export function useLeafZoomLive(): number {
  return useSyncExternalStore(subscribeLive, getLeafZoomLive, () => 1);
}

/**
 * Settled zoom — re-renders only when zoom changes AND the gesture
 * has ended. Use for tier selection / image mints / canonical-tree
 * hydration triggers. Default export point for prior `useLeafZoom`
 * callers since they all wanted this behaviour.
 */
export function useLeafZoomSettled(): number {
  return useSyncExternalStore(subscribeSettled, getLeafZoomSettled, () => 1);
}

/**
 * Back-compat alias for prior `useLeafZoom` callers — every existing
 * use-site wanted tier-selection (debounced) behaviour, which is exactly
 * what useLeafZoomSettled provides.
 *
 * @deprecated Pick `useLeafZoomLive` (gesture-tracking, zoom badge) or
 * `useLeafZoomSettled` (tier selection, image mints) explicitly. The
 * alias's silent debounce is a footgun for any new caller that wants
 * the live value. Removal will land in a follow-up plan once call sites
 * are renamed.
 */
export const useLeafZoom = useLeafZoomSettled;

// ─── Gesture-end → settle latest live zoom into settledZoom ────────────
//
// Subscribe at module load so the first leaf canvas pickup automatically
// gets the right behaviour. The subscription is permanent — there's only
// ever one gesture-tracker and one zoom-signal pair, both module-level.
//
// HMR / test re-evaluation guard: under Next.js fast-refresh and Vitest
// module-reset, this module can re-evaluate. Without the flag, each
// re-evaluation adds another listener with no path to remove it; over
// a long dev session every gesture-end fires N redundant listeners that
// all write the same settledZoom. The globalThis flag is idempotent.
//
// Note: server-side rendering runs this module too. canvasGestureTracker
// is just a class instance with no DOM hooks of its own (those live in
// leafcanvas.tsx's wheel/pointer handlers), so subscribing here is safe
// in both SSR and CSR.
declare global {
  // eslint-disable-next-line no-var
  var __lcLeafZoomSignalWired: boolean | undefined;
}
if (!globalThis.__lcLeafZoomSignalWired) {
  globalThis.__lcLeafZoomSignalWired = true;
  canvasGestureTracker.subscribe((gesturing) => {
    if (gesturing) return;
    if (settledZoom === liveZoom) return;
    settledZoom = liveZoom;
    notify(settledListeners);
  });
}
