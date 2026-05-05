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
  if (hasTextDescendant(node)) return false;
  if (treeHeight(node) < 2) return false;
  // At least one shape descendant — otherwise it's an empty wrapper that
  // shouldn't render as a cluster placeholder.
  return hasShapeDescendant(node);
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
