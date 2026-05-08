/**
 * hover-signal.ts — module-level pub/sub for the active leaf canvas's
 * "currently hovered atomic child" so multiple consumers can react
 * without forcing a Zustand round-trip on every pointer-move.
 *
 * Two axes are exposed:
 *
 *   hoveredAtomicChild — the atomic the cursor is over right now,
 *                        or null when the cursor is outside any
 *                        recognised atomic. Drives:
 *                          - data-atomic-hovered DOM tag (LeafFrameRenderer)
 *                          - HoverTooltip pill
 *                          - MeasurementOverlay distance/padding/gap
 *                          - inspector row → canvas band binding
 *                            (via the band-hint axis below)
 *
 *   hoveredBandHint   — set by the inspector when the user hovers a
 *                       Layout Widget row (paddingTop, paddingRight,
 *                       gap, etc.) so MeasurementOverlay can boost
 *                       the matching band's outline. Symmetric:
 *                       MeasurementOverlay also pushes the same hint
 *                       when its band is hovered, lighting up the
 *                       inspector row. (Wired in U10.)
 *
 * Why module-level (not Zustand): pointer-move fires up to 60×/sec.
 * Zustand subscribers re-render on every state change, which on a
 * 4250-screen leaf would chew the frame budget. Module-level
 * pub/sub bypasses React reconciliation entirely — only consumers
 * that subscribe re-render, and dedup is by value identity (no
 * re-fire when the hovered atomic doesn't change).
 *
 * Same singleton/useSyncExternalStore pattern as leaf-zoom-signal.ts
 * and canvasFetchQueue. HMR-guarded against module re-evaluation
 * under Next.js fast-refresh + Vitest module-reset.
 */

import { useSyncExternalStore } from "react";

export interface HoveredAtomicChild {
  screenID: string;
  figmaNodeID: string;
}

/**
 * Bands the inspector and overlay can cross-highlight. The string
 * union doubles as a CSS hook (`data-band-hint="paddingTop"`) so
 * the overlay can address the matching band without a translation
 * table.
 */
export type HoveredBandHint =
  | { nodeID: string; band: "paddingTop" | "paddingRight" | "paddingBottom" | "paddingLeft" | "gap" }
  | null;

let hoveredAtomic: HoveredAtomicChild | null = null;
let bandHint: HoveredBandHint = null;

const atomicListeners = new Set<() => void>();
const bandListeners = new Set<() => void>();

/**
 * Producer: pointer-move handler in LeafFrameRenderer calls this
 * with the resolved atomic (via findAtomicTarget) or null when the
 * cursor leaves the wrapper. Dedup is by (screenID, figmaNodeID)
 * identity — passing the same atomic twice is a no-op.
 */
export function setHoveredAtomicChild(next: HoveredAtomicChild | null): void {
  if (next === null && hoveredAtomic === null) return;
  if (
    next !== null &&
    hoveredAtomic !== null &&
    hoveredAtomic.screenID === next.screenID &&
    hoveredAtomic.figmaNodeID === next.figmaNodeID
  ) {
    return;
  }
  hoveredAtomic = next;
  notify(atomicListeners);
}

/** Producer: inspector row hover-enter / hover-leave (U10). */
export function setHoveredBandHint(next: HoveredBandHint): void {
  if (next === null && bandHint === null) return;
  if (
    next !== null &&
    bandHint !== null &&
    bandHint.nodeID === next.nodeID &&
    bandHint.band === next.band
  ) {
    return;
  }
  bandHint = next;
  notify(bandListeners);
}

/** Imperative read for non-React contexts (e.g., MeasurementOverlay paint loop). */
export function getHoveredAtomicChild(): HoveredAtomicChild | null {
  return hoveredAtomic;
}
export function getHoveredBandHint(): HoveredBandHint {
  return bandHint;
}

function subscribeAtomic(cb: () => void): () => void {
  atomicListeners.add(cb);
  return () => {
    atomicListeners.delete(cb);
  };
}
function subscribeBand(cb: () => void): () => void {
  bandListeners.add(cb);
  return () => {
    bandListeners.delete(cb);
  };
}

function notify(set: Set<() => void>): void {
  // Snapshot so unsubscribe-during-callback doesn't mutate iteration.
  const snapshot = [...set];
  for (const fn of snapshot) {
    try {
      fn();
    } catch {
      // Swallow listener errors — one bad consumer must not sink the rest.
    }
  }
}

/** React hook — re-renders on hovered-atomic change. */
export function useHoveredAtomicChild(): HoveredAtomicChild | null {
  return useSyncExternalStore(subscribeAtomic, getHoveredAtomicChild, () => null);
}

/** React hook — re-renders on band-hint change. */
export function useHoveredBandHint(): HoveredBandHint {
  return useSyncExternalStore(subscribeBand, getHoveredBandHint, () => null);
}

// ─── HMR / test re-evaluation guard ────────────────────────────────────
//
// No DOM/event subscriptions live in this module (the gesture-tracker
// pattern doesn't apply here), but listener Sets persist across HMR
// re-evaluations of CONSUMERS — without a global flag, repeated module
// loads accumulate stale listener-Sets. The flag also lets tests reset
// state cleanly via `delete (globalThis as any).__lcHoverSignalWired`.
declare global {
  // eslint-disable-next-line no-var
  var __lcHoverSignalWired: boolean | undefined;
}
if (!globalThis.__lcHoverSignalWired) {
  globalThis.__lcHoverSignalWired = true;
}
