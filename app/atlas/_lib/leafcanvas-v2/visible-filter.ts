/**
 * visible-filter.ts — strip invisible nodes from the canonical_tree before
 * the converter walks it, and tag co-positioned siblings as candidate
 * design-state groups.
 *
 * Why strip up front (rather than `display:none` at render time):
 *   - DOM-element budget: a 500-node frame with 40 % invisible layers blows
 *     past the 1000-element budget the plan sets.
 *   - Hover/click hit-testing: an invisible-but-present element steals
 *     events from the visible one underneath.
 *
 * Exhaustive list of "invisible" conditions handled here (U13):
 *   1. `visible === false`          → standard Figma toggle.
 *   2. `removed === true`           → some plugin-side exports use this
 *                                     for soft-deleted layers.
 *   3. `opacity <= 0.001`           → effectively invisible. Treated like
 *                                     `visible:false` so the node is
 *                                     filtered at walker time, not via CSS
 *                                     (CSS-only opacity:0 still counts
 *                                     toward the DOM-element budget and
 *                                     still steals hit-testing).
 *
 * Anything else (e.g. zero-width bbox, off-screen layout) renders honestly
 * and the canvas-v2 surface relies on `clipsContent: true` on the parent
 * frame to hide it.
 *
 * Co-positioned detection (state-picker scaffold for U14):
 *   When 2+ siblings share `(x, y, width, height)` (rounded to 1 px to
 *   absorb float drift), tag them with `__stateGroup = parentID:hash` so
 *   the renderer can drop `data-state-group` on the emitted div. U14 owns
 *   the picker UI; this layer just exposes the hint.
 *
 * Strict TS: no `// @ts-nocheck`.
 */

import type { AnnotatedNode, BoundingBox, CanonicalNode } from "./types";

/** Treat opacity ≤ 0.001 (effectively invisible) as hidden. */
const OPACITY_HIDDEN_THRESHOLD = 0.001;

export function isVisible(node: CanonicalNode): boolean {
  if (node.visible === false) return false;
  if (node.removed === true) return false;
  if (typeof node.opacity === "number" && node.opacity <= OPACITY_HIDDEN_THRESHOLD) {
    return false;
  }
  return true;
}

/**
 * Recursively prune `visible:false` (and effectively-zero-opacity) subtrees,
 * and tag co-positioned sibling clusters. Returns a fresh tree — the input
 * is never mutated.
 */
export function filterVisible(
  node: CanonicalNode,
  parentID: string = "root",
): AnnotatedNode | null {
  if (!isVisible(node)) return null;

  const children: AnnotatedNode[] = [];
  if (Array.isArray(node.children)) {
    const id = node.id ?? parentID;
    for (const child of node.children) {
      const kept = filterVisible(child, id);
      if (kept) children.push(kept);
    }
    tagCoPositionedSiblings(children, id);
  }

  // Spread the input so we don't mutate the caller's object, then attach
  // the filtered children. Any `__stateGroup` already added by the parent
  // walker survives (we don't overwrite it here).
  const out: AnnotatedNode = { ...node };
  if (children.length > 0) {
    out.children = children;
  } else if (node.children !== undefined) {
    // Preserve the empty children array shape so consumers can rely on
    // `children` being either undefined (leaf) or an array.
    out.children = [];
  }
  return out;
}

/**
 * Round each sibling's bbox to 1 px and group siblings that share a
 * (x, y, width, height) tuple. Members of any group of size ≥ 2 get a
 * stable `__stateGroup` id derived from the parent id and the bbox tuple.
 */
function tagCoPositionedSiblings(
  siblings: AnnotatedNode[],
  parentID: string,
): void {
  if (siblings.length < 2) return;

  const groups = new Map<string, AnnotatedNode[]>();
  for (const s of siblings) {
    const key = bboxKey(s.absoluteBoundingBox);
    if (!key) continue;
    const arr = groups.get(key) ?? [];
    arr.push(s);
    groups.set(key, arr);
  }

  for (const [key, members] of groups) {
    if (members.length < 2) continue;
    const groupID = `${parentID}::${key}`;
    for (const m of members) {
      m.__stateGroup = groupID;
    }
  }
}

function bboxKey(bbox: BoundingBox | null | undefined): string | null {
  if (!bbox) return null;
  const x = Math.round(bbox.x);
  const y = Math.round(bbox.y);
  const w = Math.round(bbox.width);
  const h = Math.round(bbox.height);
  return `${x},${y},${w},${h}`;
}

/** For tests + telemetry: count nodes in a (possibly-filtered) tree. */
export function countNodes(node: CanonicalNode | null | undefined): number {
  if (!node) return 0;
  let n = 1;
  if (Array.isArray(node.children)) {
    for (const c of node.children) n += countNodes(c);
  }
  return n;
}
