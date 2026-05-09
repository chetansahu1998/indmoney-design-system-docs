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
 * FRAME inclusion update (2026-05-08): the Go side (isCluster in
 * pipeline_cluster_prerender.go) clusters FRAME-typed wrappers when
 * the structural heuristic passes. Excluding FRAME here created a
 * parity drift: Go produced cluster PNGs keyed by the FRAME's id,
 * while this side walked into the FRAME and looked up URLs by the
 * children's ids — none existed → dashed placeholders. Symptom:
 * vector illustrations inside FRAME wrappers (passport doc render,
 * bank-statement check graphic, RM-call robot) showed as gray.
 *
 * Why FRAME used to be excluded: rasterizing a phone-screen FRAME as
 * one PNG destroys autolayout. That concern is now handled by:
 *   1. CLUSTER_MAX_W/H size ceiling — phone-screen FRAMEs (375×1687)
 *      exceed the ceiling and skip the fast path, walking normally.
 *   2. The shape/text budget — form-shaped FRAMEs (with many TEXT
 *      labels) flunk the budget and walk normally.
 * Together these protect the original autolayout case while letting
 * structural illustrations cluster.
 */
const WRAPPER_TYPES: ReadonlySet<string> = new Set([
  "GROUP",
  "INSTANCE",
  "COMPONENT",
  "BOOLEAN_OPERATION",
  "FRAME",
]);

/**
 * Cluster size ceiling — mirror of CLUSTER_MAX_WIDTH/HEIGHT in
 * node-classifier.ts and clusterMaxWidth/Height in the Go
 * pre-renderer. Wrappers larger than this are full-screen surfaces;
 * clustering them as one PNG would lose autolayout structure.
 */
const CLUSTER_MAX_W = 400;
const CLUSTER_MAX_H = 600;

function exceedsClusterSize(node: CanonicalNode): boolean {
  const bb = node.absoluteBoundingBox;
  if (!bb) return false;
  const w = typeof bb.width === "number" ? bb.width : 0;
  const h = typeof bb.height === "number" ? bb.height : 0;
  if (w <= 0 || h <= 0) return false;
  return w > CLUSTER_MAX_W || h > CLUSTER_MAX_H;
}

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
  // FRAME-only safety: phone-screen wrappers and other large layout FRAMEs
  // must NOT cluster as one PNG. Apply the size ceiling so 375×1687 phone
  // screens fall through to the container path. GROUP/INSTANCE/COMPONENT/
  // BOOLEAN_OPERATION historically passed without size gating; preserve
  // that for parity with the pre-2026-05-08 behaviour.
  if (t === "FRAME" && exceedsClusterSize(node)) return false;
  // Autolayout-descendant guard. Pre-2026-05-09 the heuristic looked only
  // at leaf counts, so anything that LOOKED illustration-shaped clustered
  // — including:
  //   - chart screens with time-frame pills (1D/1W/1M/...) inside an
  //     autolayout horizontal FRAME below the chart line. The pills
  //     became unselectable because the whole screen rasterized as one
  //     PNG.
  //   - top-N list cards (ETFs, holdings, watchlist rows) where each
  //     row is an autolayout HORIZONTAL frame containing icon + text +
  //     price. The whole list rasterized; designers couldn't click an
  //     individual row to inspect overrides.
  // Autolayout is the cleanest signal that "this subtree has interactive
  // UI containers, not just illustration shapes" — clustering past it
  // would freeze interactive structure into pixels. Named illustrations
  // and charts (ILLUSTRATION_NAME_RE / CHART_NAME_RE) still cluster via
  // node-classifier's name fast paths; this guard only fires for the
  // structural-heuristic fallback.
  if (hasAutolayoutDescendant(node)) return false;
  // Pure-icon fast path — no TEXT descendants, just need ≥1 shape leaf.
  if (!hasTextDescendant(node)) return hasShapeDescendant(node);
  // Mixed-content path: illustration frames may embed a few labels
  // (chart axes, "BUY"/"SELL" markers, value ticks). Mirror the Go
  // classifier's shapeLeaves≥8 && textLeaves≤max(4, shapes*3/2)
  // heuristic — keeps `services/ds-service/internal/projects/
  // pipeline_cluster_prerender.go::isCluster` and this side in lockstep
  // so the Go pre-renderer never produces a PNG the frontend ignores.
  // Pre-fix the budget was max(2, shapes/20) (5% text). That's right
  // for icons; charts run 100%+ text per shape (every candle has a
  // tick), so they were locked out.
  const { shapes, texts } = countLeaves(node);
  if (shapes < 8) return false;
  const textBudget = Math.max(4, Math.floor((shapes * 3) / 2));
  return texts <= textBudget;
}

/**
 * hasAutolayoutDescendant reports whether the subtree contains any
 * FRAME (or INSTANCE/COMPONENT) that has `layoutMode` set to a non-NONE
 * direction. The screen-root autolayout itself is excluded — we're
 * looking at DESCENDANTS, not the wrapper being classified, because
 * a wrapper that only HAS layout (no inner autolayout subtrees) could
 * still legitimately be an illustration laid out via auto-layout
 * (rare but possible). The descendant signal is what actually reveals
 * "interactive UI containers nested inside".
 */
export function hasAutolayoutDescendant(node: CanonicalNode): boolean {
  if (!Array.isArray(node.children)) return false;
  for (const c of node.children) {
    if (isAutolayoutFrame(c)) return true;
    if (hasAutolayoutDescendant(c)) return true;
  }
  return false;
}

function isAutolayoutFrame(n: CanonicalNode): boolean {
  if (n.type !== "FRAME" && n.type !== "INSTANCE" && n.type !== "COMPONENT") {
    return false;
  }
  return n.layoutMode === "HORIZONTAL" || n.layoutMode === "VERTICAL";
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
