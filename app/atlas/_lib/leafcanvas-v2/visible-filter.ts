/**
 * visible-filter.ts — strip invisible nodes from the canonical_tree before
 * the converter walks it, and tag co-positioned siblings as candidate
 * design-state groups (consumed by `<StatePicker>` in U14).
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

// ─── State-group collection (U14) ────────────────────────────────────────────

/** A single co-positioned design-state group inside one frame. */
export interface StateGroupVariant {
  /** Figma node id for the variant. Stable across re-fetches. */
  figmaNodeID: string;
  /**
   * Display name for the chip. Falls back to `State N` when the variant
   * has no name (some Figma exports omit it on autogenerated frames).
   */
  name: string;
}

export interface StateGroup {
  /** `groupKey` from `tagCoPositionedSiblings` — `parentID::x,y,w,h`. */
  key: string;
  /**
   * DOM-order list of variants. Per the spike findings, the first entry is
   * what Figma renders by default (DOM-order back-to-front; the visible
   * one is the LAST painted, but the renderer keeps the first to mirror
   * what `<window.PhoneFrame>` does today). Reversing here would surprise
   * designers — see plan §U14 Approach.
   */
  variants: StateGroupVariant[];
  /** Default variant id (first listed). The picker selects this when no
   * explicit user choice exists in `activeStatesByFrame`. */
  defaultVariantID: string;
}

/**
 * Walk a `filterVisible`-annotated tree and collect every state group keyed
 * by the *frame* (= ancestor with `id`) that owns the co-positioned
 * siblings. Returned as `Map<frameID, StateGroup[]>` so the renderer can
 * mount one `<StatePicker>` per frame.
 *
 * Why frame-scoped (not flat by groupKey): two different frames can
 * legitimately produce the same `(x, y, w, h)` rounded coordinates. The
 * UI must keep them isolated — clicking a chip in frame A doesn't shift
 * the variant inside frame B.
 *
 * `frameID` for the top-level call is the root node's `id`. Nested frames
 * (FRAME children of FRAME) re-bind the frameID for their own state
 * groups so each frame owns its own picker.
 */
export function collectStateGroups(
  root: AnnotatedNode | null | undefined,
): Map<string, StateGroup[]> {
  const out = new Map<string, StateGroup[]>();
  if (!root) return out;
  walkForStateGroups(root, root.id ?? "root", out);
  return out;
}

function walkForStateGroups(
  node: AnnotatedNode,
  currentFrameID: string,
  out: Map<string, StateGroup[]>,
): void {
  // Re-bind frameID when descending into a FRAME with a stable id so
  // nested frames each own their own state picker. Non-FRAME containers
  // (GROUP, BOOLEAN_OPERATION, RECTANGLE) keep the parent's frame scope
  // — their state groups bubble up to the nearest enclosing FRAME.
  const ownFrameID =
    node.type === "FRAME" && typeof node.id === "string" && node.id.length > 0
      ? node.id
      : currentFrameID;

  const children = node.children;
  if (Array.isArray(children) && children.length > 1) {
    // Group children by their `__stateGroup` tag (set by
    // `tagCoPositionedSiblings`). DOM-order is preserved by iteration order
    // because Map keys retain insertion order.
    const byKey = new Map<string, AnnotatedNode[]>();
    for (const child of children) {
      const k = child.__stateGroup;
      if (!k) continue;
      const arr = byKey.get(k) ?? [];
      arr.push(child);
      byKey.set(k, arr);
    }
    if (byKey.size > 0) {
      const list = out.get(ownFrameID) ?? [];
      for (const [key, members] of byKey) {
        if (members.length < 2) continue;
        const variants: StateGroupVariant[] = members.map((m, i) => ({
          figmaNodeID: m.id ?? `${key}::${i}`,
          name: variantDisplayName(m, i),
        }));
        list.push({
          key,
          variants,
          defaultVariantID: variants[0].figmaNodeID,
        });
      }
      if (list.length > 0) out.set(ownFrameID, list);
    }
  }

  if (Array.isArray(children)) {
    for (const child of children) {
      walkForStateGroups(child, ownFrameID, out);
    }
  }
}

function variantDisplayName(node: AnnotatedNode, index: number): string {
  const n = node.name;
  if (typeof n === "string" && n.trim().length > 0) return n;
  return `State ${index + 1}`;
}

/**
 * Resolve which variant is currently active for a given group, given
 * (a) the user's explicit pick from the live store and
 * (b) the group's default variant. Centralised so renderer + picker
 * never disagree on what's active.
 */
export function resolveActiveVariantID(
  group: StateGroup,
  pick: string | undefined,
): string {
  if (pick && group.variants.some((v) => v.figmaNodeID === pick)) return pick;
  return group.defaultVariantID;
}

/**
 * Given the per-frame active picks + the collected groups, return the set
 * of figmaNodeIDs that should be HIDDEN (display: none / not rendered) so
 * the renderer can skip them. Variants that are the active one for their
 * group are NOT in the set; everything else inside a state group is.
 */
export function inactiveVariantIDs(
  groupsByFrame: Map<string, StateGroup[]>,
  activePicks: Map<string, Map<string, string>>,
): Set<string> {
  const inactive = new Set<string>();
  for (const [frameID, groups] of groupsByFrame) {
    const picksForFrame = activePicks.get(frameID);
    for (const group of groups) {
      const pick = picksForFrame?.get(group.key);
      const active = resolveActiveVariantID(group, pick);
      for (const v of group.variants) {
        if (v.figmaNodeID !== active) inactive.add(v.figmaNodeID);
      }
    }
  }
  return inactive;
}
