/**
 * svg-markup-render.vitest.ts — U7 inline-SVG render path pins.
 *
 * Two surfaces:
 *   1. nodeToHTML — when a node carries svg_markup, the renderer
 *      inlines it via dangerouslySetInnerHTML and skips the
 *      cluster-URL / <img> path.
 *   2. useIconClusterURLs.collectClusterIDsWithBBox — same nodes are
 *      excluded from the cluster ID set, so the asset-stream
 *      subscriber doesn't mint a PNG URL that would never get used.
 *
 * Falls through to PNG behavior when svg_markup is absent (R5 silent
 * fallback per the brainstorm Key Decision).
 */

import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";

import { nodeToHTML } from "../nodeToHTML";
import { collectClusterIDsWithBBox } from "../useIconClusterURLs";
import type { AnnotatedNode, CanonicalNode } from "../types";

const SVG_BYTES =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><circle cx="50" cy="50" r="40" fill="red"/></svg>';

function makeIconNode(overrides: Partial<CanonicalNode> = {}): AnnotatedNode {
  return {
    id: "I123:1",
    name: "icon/system/lock",
    type: "INSTANCE",
    absoluteBoundingBox: { x: 0, y: 0, width: 24, height: 24 },
    ...overrides,
  } as AnnotatedNode;
}

function makeIllustrationNode(overrides: Partial<CanonicalNode> = {}): AnnotatedNode {
  // Figma's illustration components are typically COMPONENT/INSTANCE
  // at the cluster boundary — using GROUP triggers the flatten path
  // in renderContainer which drops cluster classification. INSTANCE
  // matches what production canonical_trees actually contain.
  return {
    id: "I456:2",
    name: "illustration/empty-state-watchlist",
    type: "INSTANCE",
    absoluteBoundingBox: { x: 0, y: 0, width: 320, height: 240 },
    ...overrides,
  } as AnnotatedNode;
}

/**
 * Renders a child node inside a synthetic screen-root frame so the
 * cluster classification path actually runs (nodeToHTML skips
 * cluster treatment for the root node — see the isScreenRoot guard
 * in renderContainer). The wrapper bbox is sized to contain the
 * child so layout math works without warnings.
 */
function renderNode(
  node: AnnotatedNode,
  ctxOverrides: Partial<Parameters<typeof nodeToHTML>[3]> = {},
): string {
  const ctx: Parameters<typeof nodeToHTML>[3] = {
    clusterURLs: new Map(),
    clusterFailedIDs: new Set(),
    imageRefs: new Map() as unknown as Parameters<typeof nodeToHTML>[3]["imageRefs"],
    ...ctxOverrides,
  };
  const childBBox = node.absoluteBoundingBox ?? { x: 0, y: 0, width: 100, height: 100 };
  const root: AnnotatedNode = {
    id: "screen-root",
    name: "Screen",
    type: "FRAME",
    absoluteBoundingBox: {
      x: 0,
      y: 0,
      width: Math.max(375, childBBox.width + childBBox.x),
      height: Math.max(812, childBBox.height + childBBox.y),
    },
    children: [node],
  } as AnnotatedNode;
  const el = nodeToHTML(root, null, null, ctx);
  return renderToStaticMarkup(el);
}

describe("nodeToHTML — svg_markup inline branch", () => {
  it("emits a <div data-cluster-svg> with inlined SVG when svg_markup is present", () => {
    const node = makeIllustrationNode({ svg_markup: SVG_BYTES });
    const html = renderNode(node);
    // Inlined markup verbatim.
    expect(html).toContain('viewBox="0 0 100 100"');
    expect(html).toContain('<circle');
    // data-cluster-svg flag for downstream CSS hooks (U5 hover overlays
    // need to know which nodes are inlined vector vs rasterized PNG).
    expect(html).toContain('data-cluster-svg="true"');
    // data-figma-id retained for spatial-store / chrome-layer lookups.
    expect(html).toContain('data-figma-id="I456:2"');
    // a11y role attached.
    expect(html).toContain('role="img"');
    expect(html).toContain('aria-label="illustration/empty-state-watchlist"');
  });

  it("does NOT emit an <img> when svg_markup is present, even if clusterURLs has a URL for the same node", () => {
    // svg_markup wins over a leftover URL — the asset stream may have
    // minted a URL before the canonical_tree refreshed with the
    // inlined markup; the renderer must prefer the inlined path.
    const node = makeIconNode({ svg_markup: SVG_BYTES });
    const clusterURLs = new Map([["I123:1", "https://example.invalid/I123_1.png"]]);
    const html = renderNode(node, { clusterURLs });
    expect(html).toContain("<svg");
    expect(html).not.toContain("<img");
  });

  it("falls through to PNG path when svg_markup is absent (R5 silent fallback)", () => {
    const node = makeIconNode();
    const clusterURLs = new Map([["I123:1", "https://example.invalid/I123_1.png"]]);
    const html = renderNode(node, { clusterURLs });
    expect(html).toContain('<img');
    expect(html).toContain("https://example.invalid/I123_1.png");
    expect(html).not.toContain('data-cluster-svg');
  });

  it("falls through to PNG path when svg_markup is an empty string (treat as absent)", () => {
    const node = makeIconNode({ svg_markup: "" });
    const clusterURLs = new Map([["I123:1", "https://example.invalid/I123_1.png"]]);
    const html = renderNode(node, { clusterURLs });
    expect(html).toContain('<img');
    expect(html).not.toContain('data-cluster-svg');
  });

  it("falls through to the pending placeholder when neither svg_markup nor URL is present", () => {
    const node = makeIconNode();
    const html = renderNode(node);
    expect(html).toContain('data-cluster-pending="true"');
    expect(html).not.toContain('data-cluster-svg');
  });

  it("inlines svg_markup verbatim without HTML-escaping the angle brackets", () => {
    // dangerouslySetInnerHTML must NOT escape the markup; if the
    // renderer accidentally textNodes the string, the SVG won't paint.
    const node = makeIllustrationNode({ svg_markup: SVG_BYTES });
    const html = renderNode(node);
    expect(html).not.toContain('&lt;svg');
    expect(html).toContain('<svg');
  });
});

describe("collectClusterIDsWithBBox — svg_markup skip", () => {
  it("skips a top-level node carrying svg_markup", () => {
    const tree: CanonicalNode = {
      id: "screen-root",
      type: "FRAME",
      absoluteBoundingBox: { x: 0, y: 0, width: 375, height: 812 },
      children: [makeIconNode({ svg_markup: SVG_BYTES })],
    };
    expect(collectClusterIDsWithBBox(tree)).toEqual([]);
  });

  it("collects a regular cluster when svg_markup is absent", () => {
    const tree: CanonicalNode = {
      id: "screen-root",
      type: "FRAME",
      absoluteBoundingBox: { x: 0, y: 0, width: 375, height: 812 },
      children: [makeIconNode()],
    };
    const ids = collectClusterIDsWithBBox(tree);
    expect(ids.length).toBe(1);
    expect(ids[0].id).toBe("I123:1");
  });

  it("does not descend into children of an svg_markup node", () => {
    // svg_markup is the flattened subtree; any nested children would
    // be a stale leftover. Walking into them would mint URLs for
    // shapes that are already painted by the inlined <svg>.
    const tree: CanonicalNode = {
      id: "screen-root",
      type: "FRAME",
      absoluteBoundingBox: { x: 0, y: 0, width: 375, height: 812 },
      children: [
        {
          ...makeIllustrationNode({ svg_markup: SVG_BYTES }),
          children: [makeIconNode({ id: "stale-inner" })],
        } as CanonicalNode,
      ],
    };
    const ids = collectClusterIDsWithBBox(tree);
    expect(ids.find((x) => x.id === "stale-inner")).toBeUndefined();
  });

  it("mixed tree — collects clusters that lack svg_markup, skips those that have it", () => {
    const tree: CanonicalNode = {
      id: "screen-root",
      type: "FRAME",
      absoluteBoundingBox: { x: 0, y: 0, width: 375, height: 812 },
      children: [
        makeIllustrationNode({ id: "inlined", svg_markup: SVG_BYTES }),
        makeIconNode({ id: "needs-png" }),
      ],
    };
    const ids = collectClusterIDsWithBBox(tree);
    expect(ids.map((x) => x.id)).toEqual(["needs-png"]);
  });

  it("empty svg_markup is treated as absent — cluster still collected", () => {
    const tree: CanonicalNode = {
      id: "screen-root",
      type: "FRAME",
      absoluteBoundingBox: { x: 0, y: 0, width: 375, height: 812 },
      children: [makeIconNode({ svg_markup: "" })],
    };
    const ids = collectClusterIDsWithBBox(tree);
    expect(ids.length).toBe(1);
  });
});
