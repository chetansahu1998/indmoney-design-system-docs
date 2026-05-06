/**
 * icon-cluster-resolver.ts — detect FRAME/GROUP wrappers that are wholly
 * graphical (no TEXT descendants) and could be exported as a single
 * SVG/PNG asset.
 *
 * Until U7 ships the asset-export client, the renderer emits a placeholder
 * div (`data-cluster-pending="true"`) at the cluster's bbox. When the
 * client lands, the same call site swaps in `<img src={resolveCluster(...)}>`.
 *
 * Heuristic for "graphical wrapper":
 *   - type ∈ {FRAME, GROUP, INSTANCE, COMPONENT, BOOLEAN_OPERATION}
 *   - has at least one descendant
 *   - no descendant has type === "TEXT"
 *   - tree height ≥ 2 (skip single-shape leaves — those render directly)
 *
 * Strict TS: no `// @ts-nocheck`.
 */

import type { CanonicalNode } from "./types";

/**
 * Wrapper types that may collapse into a single icon-cluster export.
 *
 * NB: FRAME is intentionally excluded. FRAMEs are layout containers
 * (status bars, cards, sections) — even if a FRAME has no TEXT
 * descendants today, treating it as a single rasterized icon would
 * destroy autolayout structure and prevent atomic-child inspection.
 * Icon clusters in our codebase always come in via INSTANCE
 * (component-of-icon) or GROUP/BOOLEAN_OPERATION on raw shapes.
 */
const WRAPPER_TYPES: ReadonlySet<string> = new Set([
  "GROUP",
  "INSTANCE",
  "COMPONENT",
  "BOOLEAN_OPERATION",
]);

const SHAPE_TYPES: ReadonlySet<string> = new Set([
  "VECTOR",
  "RECTANGLE",
  "ELLIPSE",
  "STAR",
  "POLYGON",
  "LINE",
  "BOOLEAN_OPERATION",
]);

export function isIconCluster(node: CanonicalNode): boolean {
  const t = node.type;
  if (!t || !WRAPPER_TYPES.has(t)) return false;
  if (!Array.isArray(node.children) || node.children.length === 0) return false;
  if (treeHeight(node) < 2) return false;
  // Pure-icon fast path — no TEXT descendants, just need ≥1 shape leaf.
  if (!hasTextDescendant(node)) return hasShapeDescendant(node);
  // Mixed-content path: illustration frames may embed a few labels
  // (chart axes, "BUY"/"SELL" markers). Mirror the Go classifier's
  // shapeLeaves≥8 && textLeaves≤max(2, shapeLeaves/20) heuristic so the
  // whole illustration clusters as a single PNG instead of fanning out
  // into per-vector placeholder pills.
  const { shapes, texts } = countLeaves(node);
  if (shapes < 8) return false;
  const textBudget = Math.max(2, Math.floor(shapes / 20));
  return texts <= textBudget;
}

export function hasTextDescendant(node: CanonicalNode): boolean {
  if (node.type === "TEXT") return true;
  if (!Array.isArray(node.children)) return false;
  for (const c of node.children) {
    if (hasTextDescendant(c)) return true;
  }
  return false;
}

function hasShapeDescendant(node: CanonicalNode): boolean {
  if (node.type && SHAPE_TYPES.has(node.type)) return true;
  if (!Array.isArray(node.children)) return false;
  for (const c of node.children) {
    if (hasShapeDescendant(c)) return true;
  }
  return false;
}

function countLeaves(node: CanonicalNode): { shapes: number; texts: number } {
  let shapes = 0;
  let texts = 0;
  const walk = (n: CanonicalNode): void => {
    if (n.type === "TEXT") {
      texts += 1;
      return;
    }
    if (n.type && SHAPE_TYPES.has(n.type)) {
      shapes += 1;
      return;
    }
    if (Array.isArray(n.children)) {
      for (const c of n.children) walk(c);
    }
  };
  walk(node);
  return { shapes, texts };
}

function treeHeight(node: CanonicalNode): number {
  if (!Array.isArray(node.children) || node.children.length === 0) return 1;
  let max = 0;
  for (const c of node.children) {
    const h = treeHeight(c);
    if (h > max) max = h;
  }
  return 1 + max;
}

/**
 * Placeholder URL used until U7 wires the asset-export client. Returning
 * `null` signals to the renderer "emit dashed-border placeholder div".
 *
 * Future shape: `(slug, projectVersionID, nodeID) => string | null`.
 */
export function resolveClusterURL(_node: CanonicalNode): string | null {
  return null;
}
