/**
 * isFlattenedWrapper.vitest.ts — pin the GROUP-cluster boundary fix
 * (2026-05-08).
 *
 * Regression scope: pre-fix `isFlattenedWrapper` returned true for ANY
 * GROUP, causing course-tile illustration GROUPs (180×218, 30 vector
 * leaves, 3 text leaves) to render as `display: contents` passthroughs
 * instead of a single rasterized cluster PNG. Result: 100 thumbnails
 * across the indlearn-learn-revamp leaf rendered as gradient-only
 * placeholders because the FRAME child captured cluster duty but its id
 * was never pre-rendered (Go's walkClusters had captured the GROUP).
 *
 * The fix mirrors the BOOLEAN_OPERATION precedent: only flatten when
 * `isIconCluster` returns false. GROUPs that qualify as illustration
 * clusters fall through to renderClusterPlaceholder with their own id.
 */

import { describe, expect, it } from "vitest";

import { isFlattenedWrapper } from "../nodeToHTML";
import type { AnnotatedNode } from "../types";

// Build a GROUP that satisfies isIconCluster (≥8 shapes, 0 text,
// children with positive depth). Vectors live one level deep so
// treeHeight >= 2.
function buildIllustrationGroup(): AnnotatedNode {
  const vectors: AnnotatedNode[] = [];
  for (let i = 0; i < 8; i++) {
    vectors.push({
      id: `v${i}`,
      type: "VECTOR",
      name: `Vector ${i}`,
      // Each VECTOR is a leaf with no children.
    } as AnnotatedNode);
  }
  return {
    id: "g1",
    type: "GROUP",
    name: "Group 123",
    absoluteBoundingBox: { x: 0, y: 0, width: 180, height: 218 },
    children: [
      {
        id: "inner",
        type: "FRAME",
        name: "inner",
        children: vectors,
      } as AnnotatedNode,
    ],
  } as AnnotatedNode;
}

describe("isFlattenedWrapper", () => {
  it("flattens a plain GROUP whose subtree fails the cluster heuristic", () => {
    // Single TEXT child — classifyNode for the text-heavy GROUP rejects
    // cluster path. Should be flattened so the inner TEXT lays out
    // directly under the parent.
    const node: AnnotatedNode = {
      id: "plain-group",
      type: "GROUP",
      name: "Plain",
      children: [
        { id: "t1", type: "TEXT", name: "label" } as AnnotatedNode,
      ],
    } as AnnotatedNode;
    expect(isFlattenedWrapper(node)).toBe(true);
  });

  it("does NOT flatten a GROUP that qualifies as an illustration cluster", () => {
    // Real production case from indlearn-learn-revamp: course-tile GROUP
    // with shape-heavy subtree. Pre-fix this would have flattened and
    // orphaned Go's pre-rendered cluster PNG.
    const node = buildIllustrationGroup();
    expect(isFlattenedWrapper(node)).toBe(false);
  });

  it("does NOT flatten a BOOLEAN_OPERATION that's an icon cluster (parity precedent)", () => {
    const node: AnnotatedNode = {
      id: "bool-cluster",
      type: "BOOLEAN_OPERATION",
      name: "Combined Shape",
      absoluteBoundingBox: { x: 0, y: 0, width: 24, height: 24 },
      children: [
        { id: "shape", type: "VECTOR", name: "Vector" } as AnnotatedNode,
      ],
    } as AnnotatedNode;
    expect(isFlattenedWrapper(node)).toBe(false);
  });

  it("flattens a BOOLEAN_OPERATION with no shape descendants", () => {
    const node: AnnotatedNode = {
      id: "bool-empty",
      type: "BOOLEAN_OPERATION",
      name: "Wrapper",
      // No children → treeHeight=1 → isIconCluster returns false
      children: [],
    } as AnnotatedNode;
    expect(isFlattenedWrapper(node)).toBe(true);
  });

  it("does not flatten non-wrapper types regardless of cluster heuristic", () => {
    const types = ["FRAME", "INSTANCE", "COMPONENT", "RECTANGLE", "TEXT", "VECTOR"];
    for (const t of types) {
      const node: AnnotatedNode = {
        id: `n-${t}`,
        type: t as AnnotatedNode["type"],
        name: t,
      } as AnnotatedNode;
      expect(isFlattenedWrapper(node), `type=${t} should not flatten`).toBe(false);
    }
  });
});

// ─── collectClusterIDs root-skip (2026-05-08) ─────────────────────────────
//
// Regression scope: a 375×521 overlay/bottomsheet screen whose root FRAME
// passes the FRAME-cluster heuristic (size within 400×600 ceiling +
// shape-heavy due to inner Footer CTA + chart) was rendered as ONE
// cluster <img>. nodeToHTML emitted a single PNG placeholder, hiding all
// inner DOM, atomic-child selectability, and overrides. Live MCP probe
// of insurance-insurance-whatsapp-creative confirmed: 7 screens with
// childCount=1 + clusterImgs=1 + hasContent=0.
//
// The fix lives at two callsites that consume the canonical_tree root:
//  1. nodeToHTML (entry call uses keyHint="root") — never apply
//     classifier or wrapper-flatten when we're at the root.
//  2. collectClusterIDs / collectClusterIDsWithBBox — skip the root so
//     we don't mint a cluster URL for the screen frame and starve real
//     inner clusters of mint slots.

import { collectClusterIDs, collectClusterIDsWithBBox } from "../useIconClusterURLs";
import type { CanonicalNode } from "../types";

describe("collectClusterIDs — screen-root skip", () => {
  it("does NOT collect the root id even when it would otherwise rasterize", () => {
    // Root is a single VECTOR — pre-fix shouldRasterize would tag it.
    const tree = { id: "root-v", type: "VECTOR" } as CanonicalNode;
    expect(collectClusterIDs(tree)).toEqual([]);
  });

  it("collects inner cluster ids underneath a root container", () => {
    const tree = {
      id: "screen",
      type: "FRAME",
      children: [
        { id: "inner-icon", type: "VECTOR" },
        { id: "another-icon", type: "ELLIPSE" },
      ],
    } as unknown as CanonicalNode;
    const ids = collectClusterIDs(tree);
    expect(ids.sort()).toEqual(["another-icon", "inner-icon"]);
    expect(ids).not.toContain("screen");
  });

  it("withBBox variant skips the root for the same reason", () => {
    const tree = {
      id: "screen",
      type: "FRAME",
      absoluteBoundingBox: { x: 0, y: 0, width: 375, height: 521 },
      children: [
        {
          id: "inner",
          type: "VECTOR",
          absoluteBoundingBox: { x: 0, y: 0, width: 24, height: 24 },
        },
      ],
    } as unknown as CanonicalNode;
    const ids = collectClusterIDsWithBBox(tree);
    expect(ids.map((c) => c.id)).toEqual(["inner"]);
  });

  it("returns [] for null tree", () => {
    expect(collectClusterIDs(null)).toEqual([]);
    expect(collectClusterIDsWithBBox(null)).toEqual([]);
  });

  it("returns [] when root has no children (no inner clusters to collect)", () => {
    const tree = { id: "screen", type: "FRAME" } as CanonicalNode;
    expect(collectClusterIDs(tree)).toEqual([]);
  });
});
