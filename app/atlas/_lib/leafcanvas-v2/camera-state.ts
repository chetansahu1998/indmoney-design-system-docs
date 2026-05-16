/**
 * camera-state.ts — module-level signal for the active leaf canvas's
 * pan/zoom transform `{x, y, z}`. Sister to leaf-zoom-signal (which
 * exposes the scalar zoom only); this module exposes the full camera
 * vector so consumers outside the React tree (chrome-layer, future
 * keymap, future spatial paint loops) can project world-coords into
 * screen-coords without prop-drilling the camera through every node.
 *
 * Producer: `app/atlas/_lib/leafcanvas.tsx` calls `setCamera(value)`
 * inside `applyCameraToDOM` (the RAF-driven flush that writes the
 * world transform). Because the producer already coalesces N wheel
 * events into one RAF tick before calling applyCameraToDOM, the
 * notification rate on this signal is naturally bounded to ~60Hz —
 * even on a high-frequency input device. No extra throttling here.
 *
 * Consumers: chrome-layer's rAF callback reads camera-state on every
 * tick to compute screen-rects for selection rings, hover outlines,
 * padding/gap bands, distance lines, breadcrumb chip, dimension chip.
 * Reads are imperative (getCamera()) — subscribers only fire when
 * the camera changes, not on every frame.
 *
 * Why module-level (not Zustand): camera mutations happen on every
 * wheel/pinch/pan event (~60Hz). Zustand subscribers re-render React
 * components on every state change, which would defeat the existing
 * camRef → RAF → direct DOM transform pattern that keeps the camera
 * fast. Module-level pub/sub bypasses React reconciliation entirely.
 *
 * Why mirror camRef rather than replace it: leafcanvas.tsx is the
 * legacy @ts-nocheck file with the camera as its load-bearing
 * architecture. Replacing camRef wholesale would be a larger
 * refactor than the value of U1 delivers (a chrome-layer that can
 * read camera-vector). The mirror approach: camRef remains the
 * source of truth for the world transform; setCamera() is a
 * one-line addition to applyCameraToDOM that pushes the same value
 * to subscribers. Future work (U2 spring camera) can lift the
 * producer-side primary into this module once the existing flow is
 * proved compatible.
 *
 * Same singleton/useSyncExternalStore + HMR-guard pattern as
 * hover-signal.ts and leaf-zoom-signal.ts.
 */

import { useSyncExternalStore } from "react";

export interface CameraValue {
  /** World x-coord at the world transform's `translate(-x, ...)`. */
  x: number;
  /** World y-coord at the world transform's `translate(..., -y)`. */
  y: number;
  /** Zoom multiplier at `scale(z)`. Always > 0. */
  z: number;
}

const INITIAL: CameraValue = { x: 0, y: 0, z: 1 };

let current: CameraValue = INITIAL;
const listeners = new Set<() => void>();

/**
 * Producer: leafcanvas.tsx pushes the camera vector after every RAF
 * flush. Dedup is by value equality — passing the same {x, y, z}
 * twice is a no-op (no listener re-fire, no chrome-layer repaint).
 */
export function setCamera(next: CameraValue): void {
  if (!Number.isFinite(next.x) || !Number.isFinite(next.y) || !Number.isFinite(next.z)) {
    // Reject NaN / Infinity rather than poison subscribers. The
    // producer side has its own guards but defending here protects
    // the chrome layer from a camRef in a broken state.
    return;
  }
  if (next.z <= 0) return;
  if (current.x === next.x && current.y === next.y && current.z === next.z) {
    return;
  }
  // Snapshot copy so callers can mutate their own object without
  // tearing subscribers mid-frame.
  current = { x: next.x, y: next.y, z: next.z };
  notify();
}

/** Imperative read for non-React consumers (chrome-layer's rAF loop). */
export function getCamera(): CameraValue {
  return current;
}

/** Imperative subscribe; returns unsubscribe. Used by non-React callers. */
export function subscribeCamera(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

/**
 * React hook — re-renders the consumer on camera change. Use sparingly:
 * the chrome layer subscribes imperatively and writes via refs to
 * avoid the per-frame React reconciliation cost.
 */
export function useCamera(): CameraValue {
  return useSyncExternalStore(subscribeCamera, getCamera, () => INITIAL);
}

/**
 * Test reset — clear current to INITIAL and notify subscribers. Lets
 * vitest tests start each describe block from a clean slate without
 * relying on import order to wipe state.
 */
export function __resetCameraStateForTesting(): void {
  current = INITIAL;
  // Don't fire listeners on reset — tests that want to verify the
  // notify path subscribe AFTER calling reset and trigger a real
  // setCamera() to exercise the publish path.
  listeners.clear();
}

function notify(): void {
  // Snapshot so unsubscribe-during-callback doesn't mutate iteration.
  const snapshot = [...listeners];
  for (const fn of snapshot) {
    try {
      fn();
    } catch {
      // Swallow listener errors so a single bad consumer can't sink the rest.
    }
  }
}

// ─── HMR / test re-evaluation guard ────────────────────────────────────
//
// The listener Set + `current` value persist across HMR re-evaluations
// of CONSUMERS — without a global flag, repeated module loads under
// Next.js fast-refresh would accumulate stale state. Tests reset via
// `delete (globalThis as any).__lcCameraStateWired` + re-import.
declare global {
  // eslint-disable-next-line no-var
  var __lcCameraStateWired: boolean | undefined;
}
if (!globalThis.__lcCameraStateWired) {
  globalThis.__lcCameraStateWired = true;
}
