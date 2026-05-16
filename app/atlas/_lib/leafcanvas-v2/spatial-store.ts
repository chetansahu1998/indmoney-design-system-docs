/**
 * spatial-store.ts — module-level `nodeId → world-rect` derived store
 * for the active leaf canvas. Lets the chrome layer (chrome-layer.tsx)
 * paint selection rings, hover outlines, padding bands, gap fills,
 * distance lines, and dimension chips by reading a single map instead
 * of walking the canonical_tree on every paint.
 *
 * Shape:
 *
 *   Map<screenID, Map<figmaNodeID, NodeWorldRect>>
 *
 * The outer key (screenID) matches what hover-signal and the Zustand
 * `selection.selectedAtomicChild` use, so a chrome-layer subscriber
 * with a `{screenID, figmaNodeID}` from the selection store can do an
 * O(1) two-step lookup to get the world-rect.
 *
 * **World-coord convention.** Each NodeWorldRect is in leaf-canvas
 * world coords — the same coordinate space `camera-state` uses. So
 * the chrome layer's screen-projection is just:
 *
 *   screenX = (rect.x - camera.x) * camera.z
 *   screenY = (rect.y - camera.y) * camera.z
 *   screenW = rect.w * camera.z
 *   screenH = rect.h * camera.z
 *
 * To produce a NodeWorldRect, a producer (LeafFrameRenderer once U4
 * wires population) takes the frame's leaf-canvas origin (from the
 * `buildLeafCanvas` layout) and the node's Figma-file-space
 * `absoluteBoundingBox`, then bakes them together:
 *
 *   rect.x = frameOriginX + (node.absBB.x - frameRoot.absBB.x)
 *   rect.y = frameOriginY + (node.absBB.y - frameRoot.absBB.y)
 *   rect.w = node.absBB.width
 *   rect.h = node.absBB.height
 *
 * **Lifecycle.** Producers populate on canonical_tree resolution
 * (deferred to U4 — U1 keeps this module API-only). The store
 * invalidates per-screen when the tree reloads (slotVersion bump on
 * the LeafCanvas key). No invalidation runs on camera tick — rects
 * are world-coord and don't change when the camera does.
 *
 * **Memory.** A 100-screen leaf with depth-14 canonical_trees can
 * easily produce 5,000+ rects total. At 32 bytes per rect (4 floats
 * + object overhead) that's ~160KB — fine for a single leaf, but
 * worth bounding. The `invalidateAll()` path resets the store
 * between leaves (called by LeafCanvas mount/unmount in U1).
 *
 * Why module-level (not Zustand): population fires per-node on
 * tree resolution (potentially thousands of writes for one screen
 * in one tick). Zustand would re-render every subscriber for each
 * setNodeRect call. Module-level + batched-notify (one notify per
 * setNodeRect batch boundary) keeps the chrome layer's rAF cost
 * bounded.
 *
 * Same singleton/useSyncExternalStore + HMR-guard pattern as
 * hover-signal.ts and camera-state.ts.
 */

import { useSyncExternalStore } from "react";

/**
 * World-coord rectangle for one canonical_tree node. Coords are in the
 * leaf-canvas world space (same space as camera-state).
 */
export interface NodeWorldRect {
  /** Leaf-canvas-world x of the rect's top-left corner. */
  x: number;
  /** Leaf-canvas-world y of the rect's top-left corner. */
  y: number;
  /** Width in world pixels (independent of camera zoom). */
  w: number;
  /** Height in world pixels. */
  h: number;
}

// screenID → (figmaNodeID → NodeWorldRect)
const store = new Map<string, Map<string, NodeWorldRect>>();
const listeners = new Set<() => void>();

/**
 * Producer: write one node's rect into the store. Dedup is by value
 * equality — passing the same rect twice is a no-op. The producer
 * is responsible for collapsing tree-walk passes so the chrome
 * layer's rAF callback isn't woken on every node insert in a tight
 * loop. Use `setNodeRects` (plural) for bulk-population.
 */
export function setNodeRect(screenID: string, nodeID: string, rect: NodeWorldRect): void {
  let screen = store.get(screenID);
  if (!screen) {
    screen = new Map();
    store.set(screenID, screen);
  }
  const prev = screen.get(nodeID);
  if (
    prev !== undefined &&
    prev.x === rect.x &&
    prev.y === rect.y &&
    prev.w === rect.w &&
    prev.h === rect.h
  ) {
    return;
  }
  screen.set(nodeID, { x: rect.x, y: rect.y, w: rect.w, h: rect.h });
  notify();
}

/**
 * Bulk-write a screen's rects in one operation. Notifies subscribers
 * once at the end rather than per-node. Use this from canonical_tree
 * walkers where you want to replace an entire screen's rect set
 * atomically.
 */
export function setNodeRects(screenID: string, entries: Iterable<readonly [string, NodeWorldRect]>): void {
  const next = new Map<string, NodeWorldRect>();
  for (const [nodeID, rect] of entries) {
    next.set(nodeID, { x: rect.x, y: rect.y, w: rect.w, h: rect.h });
  }
  store.set(screenID, next);
  notify();
}

/** Imperative read — undefined when the screen / node hasn't been populated. */
export function getNodeRect(screenID: string, nodeID: string): NodeWorldRect | undefined {
  return store.get(screenID)?.get(nodeID);
}

/** Imperative read of an entire screen's rect map. Read-only. */
export function getScreenRects(screenID: string): ReadonlyMap<string, NodeWorldRect> | undefined {
  return store.get(screenID);
}

/**
 * Producer-side invalidation: clear one screen's rects (canonical_tree
 * reloaded; rects may have shifted). Notifies subscribers so the
 * chrome layer paints from the next setNodeRects population.
 */
export function invalidateScreen(screenID: string): void {
  if (!store.has(screenID)) return;
  store.delete(screenID);
  notify();
}

/**
 * Reset the entire store. Called on leaf-canvas mount/unmount so
 * rects from a previous leaf don't leak into the next. Notifies once.
 */
export function invalidateAll(): void {
  if (store.size === 0) return;
  store.clear();
  notify();
}

/** Imperative subscribe; returns unsubscribe. */
export function subscribeSpatialStore(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

/**
 * React hook — re-renders the consumer when ANY screen's rects change.
 * Returns a stable signal (the store generation count). Use sparingly:
 * most consumers should subscribe imperatively and read via getNodeRect.
 */
let generation = 0;
function getGeneration(): number {
  return generation;
}
export function useSpatialStoreGeneration(): number {
  return useSyncExternalStore(subscribeSpatialStore, getGeneration, () => 0);
}

/**
 * Test reset — clear the store and listeners. Lets vitest tests start
 * each describe block from a clean slate.
 */
export function __resetSpatialStoreForTesting(): void {
  store.clear();
  listeners.clear();
  generation = 0;
}

function notify(): void {
  generation += 1;
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
declare global {
  // eslint-disable-next-line no-var
  var __lcSpatialStoreWired: boolean | undefined;
}
if (!globalThis.__lcSpatialStoreWired) {
  globalThis.__lcSpatialStoreWired = true;
}
