/**
 * selection-cycle.ts — pure tree-walk helpers for Enter / Shift+Enter
 * navigation in the selection model (U4).
 *
 * The canonical_tree is a deeply-nested `map[string]any` per-screen
 * structure (Figma exports each frame as a tree of nodes with `id`,
 * `name`, `type`, `children`, etc.). Enter descends one level into
 * the current selection's children; Shift+Enter ascends to the
 * parent. Both operations need:
 *
 *   - the ancestor chain of a node (root → … → current)
 *   - the first usable child of a node (skip purely-cosmetic wrappers)
 *
 * Why pure helpers (no React, no Zustand): these are called from the
 * keymap action handlers in AtlasShellInner. The handler reads the
 * current canonical tree from the live store, calls these helpers,
 * and dispatches a new selection. Keeping the helpers pure means we
 * can pin behavior with cheap unit tests against synthetic trees.
 *
 * Type contract: canonical_tree nodes are loosely typed as
 * `CanonicalLikeNode` to avoid pulling in the heavy AnnotatedNode
 * type from types.ts (which has 50+ optional fields). We only read
 * `id`, `type`, and `children` here.
 */

import type { CanonicalNode } from "./types";

/**
 * Lightweight read-only view of a canonical_tree node. The full
 * canonical type has many fields; these helpers only touch the
 * three needed for ancestor walks.
 */
export interface CanonicalLikeNode {
  id?: string;
  type?: string;
  children?: readonly CanonicalLikeNode[];
}

/**
 * Find the ancestor chain from the root down to the node with the
 * given id (inclusive). Returns null when the id isn't found in the
 * tree. Returns an empty array when id matches root (no ancestors;
 * caller should handle this).
 *
 * Example: tree =
 *   root → section → illustration → icon
 * findAncestorChain(tree, "icon") → [root, section, illustration, icon]
 */
export function findAncestorChain(
  root: CanonicalLikeNode | null | undefined,
  id: string,
): CanonicalLikeNode[] | null {
  if (!root || !id) return null;
  const path: CanonicalLikeNode[] = [];
  if (walkForId(root, id, path)) return path;
  return null;
}

function walkForId(
  node: CanonicalLikeNode,
  id: string,
  path: CanonicalLikeNode[],
): boolean {
  path.push(node);
  if (node.id === id) return true;
  if (Array.isArray(node.children)) {
    for (const c of node.children) {
      if (walkForId(c, id, path)) return true;
    }
  }
  path.pop();
  return false;
}

/**
 * Find the node with `id` in the tree. Returns null when not found.
 * Convenience wrapper over findAncestorChain.
 */
export function findNodeById(
  root: CanonicalLikeNode | null | undefined,
  id: string,
): CanonicalLikeNode | null {
  const chain = findAncestorChain(root, id);
  if (!chain || chain.length === 0) return null;
  return chain[chain.length - 1];
}

/**
 * Return the parent id of the node with `id` in the tree, or null
 * when the node is the root (no parent) or not found.
 *
 * Used by Shift+Enter / `\` ascend.
 */
export function findParentId(
  root: CanonicalLikeNode | null | undefined,
  id: string,
): string | null {
  const chain = findAncestorChain(root, id);
  if (!chain || chain.length < 2) return null;
  const parent = chain[chain.length - 2];
  return typeof parent.id === "string" ? parent.id : null;
}

/**
 * Return the id of the first child of the node with `id`, or null
 * when the node has no children or wasn't found.
 *
 * Used by Enter descend. The "first child" is the first entry in
 * the children array — same canvas-coordinate order Figma uses for
 * tab cycling.
 */
export function findFirstChildId(
  root: CanonicalLikeNode | null | undefined,
  id: string,
): string | null {
  const node = findNodeById(root, id);
  if (!node || !Array.isArray(node.children) || node.children.length === 0) {
    return null;
  }
  const first = node.children[0];
  return typeof first.id === "string" ? first.id : null;
}

/**
 * Return the ids of all siblings of the node with `id`, in order, in
 * the parent's children array. The selected node itself is excluded
 * from the returned list — only true siblings.
 *
 * Used by Tab / Shift+Tab to cycle through siblings of the current
 * selection. Returns an empty array when no siblings exist (only
 * child) or null when the node is the root / not found.
 */
export function findSiblingIds(
  root: CanonicalLikeNode | null | undefined,
  id: string,
): string[] | null {
  const chain = findAncestorChain(root, id);
  if (!chain || chain.length < 2) return null;
  const parent = chain[chain.length - 2];
  if (!Array.isArray(parent.children)) return [];
  return parent.children
    .filter((c) => typeof c.id === "string" && c.id !== id)
    .map((c) => c.id as string);
}

/**
 * Convenience: return the next sibling id of the node with `id`,
 * wrapping around to the first if currently at the last. Used by
 * Tab. Returns null when no siblings exist.
 */
export function findNextSiblingId(
  root: CanonicalLikeNode | null | undefined,
  id: string,
): string | null {
  const chain = findAncestorChain(root, id);
  if (!chain || chain.length < 2) return null;
  const parent = chain[chain.length - 2];
  if (!Array.isArray(parent.children) || parent.children.length === 0) {
    return null;
  }
  const idx = parent.children.findIndex((c) => c.id === id);
  if (idx < 0) return null;
  const next = parent.children[(idx + 1) % parent.children.length];
  return typeof next.id === "string" ? next.id : null;
}

/**
 * Convenience: return the previous sibling id of the node with `id`,
 * wrapping around to the last if currently at the first. Used by
 * Shift+Tab.
 */
export function findPrevSiblingId(
  root: CanonicalLikeNode | null | undefined,
  id: string,
): string | null {
  const chain = findAncestorChain(root, id);
  if (!chain || chain.length < 2) return null;
  const parent = chain[chain.length - 2];
  if (!Array.isArray(parent.children) || parent.children.length === 0) {
    return null;
  }
  const idx = parent.children.findIndex((c) => c.id === id);
  if (idx < 0) return null;
  const prev =
    parent.children[(idx - 1 + parent.children.length) % parent.children.length];
  return typeof prev.id === "string" ? prev.id : null;
}

/**
 * Convenience: collect every node id under the tree where the type
 * matches one of the allowed types. Used by Cmd+A select-all to
 * grab every selectable unit on the current screen. Empty input or
 * no matches returns an empty array.
 */
export function collectIdsByType(
  root: CanonicalLikeNode | null | undefined,
  allowedTypes: ReadonlySet<string>,
): string[] {
  if (!root) return [];
  const out: string[] = [];
  walkCollect(root, allowedTypes, out);
  return out;
}

function walkCollect(
  node: CanonicalLikeNode,
  allowed: ReadonlySet<string>,
  out: string[],
): void {
  if (
    typeof node.id === "string" &&
    typeof node.type === "string" &&
    allowed.has(node.type)
  ) {
    out.push(node.id);
  }
  if (Array.isArray(node.children)) {
    for (const c of node.children) walkCollect(c, allowed, out);
  }
}

// Re-export the canonical type so callers don't need to import from
// the heavier types.ts when they only need the loose shape.
export type { CanonicalNode };
