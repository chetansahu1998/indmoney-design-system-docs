/**
 * renderText-sizing.vitest.tsx — pin the 2026-05-09 fix that resolves
 * TEXT-node sizing across BOTH Figma fields:
 *
 *   - textAutoResize (legacy, standalone TEXT)
 *   - layoutSizingHorizontal (modern, autolayout TEXT)
 *
 * Empirically (audit of canonical_trees from IND-Learn-v3 + INDstocks-V4):
 * 100% of TEXT nodes inside auto-layout containers (which is most modern
 * production Figma) carry layoutSizingHorizontal but NOT textAutoResize.
 * The pre-fix renderer only honored textAutoResize and applied a
 * fixed-width-wrap default to every modern TEXT — wrapping HUG-sized
 * single-line labels onto two lines in production (INDmoney splash
 * "AES-256 SSL Secured").
 */

import { describe, expect, it } from "vitest";

import { renderToString } from "react-dom/server";
import { nodeToHTML } from "../nodeToHTML";
import type { AnnotatedNode, BoundingBox } from "../types";

const FRAME_BBOX: BoundingBox = { x: 0, y: 0, width: 375, height: 812 };

function renderTextNode(extra: Partial<AnnotatedNode>): string {
  const node: AnnotatedNode = {
    id: "t",
    type: "TEXT",
    name: "label",
    characters: "AES-256 SSL Secured",
    absoluteBoundingBox: { x: 100, y: 200, width: 80, height: 16 },
    ...extra,
  };
  const el = nodeToHTML(node, FRAME_BBOX, "HORIZONTAL", { imageRefs: {}, clusterURLs: new Map() }, "root");
  return el ? renderToString(el) : "";
}

describe("renderText — sizing field resolution", () => {
  it("HUG via layoutSizingHorizontal → width:auto, white-space:nowrap", () => {
    // Production case: INDmoney splash "AES-256 SSL Secured" had layoutSizingHorizontal=HUG
    // and the renderer ignored it, applying fixed-width wrap and breaking onto two lines.
    const html = renderTextNode({ layoutSizingHorizontal: "HUG", layoutSizingVertical: "HUG" });
    expect(html).toContain("width:auto");
    expect(html).toContain("white-space:nowrap");
    // Should NOT have overflow:hidden — HUG text is sized by Figma to fit.
    expect(html).not.toContain("overflow:hidden");
  });

  it("FILL via layoutSizingHorizontal → bbox-width + wrap", () => {
    const html = renderTextNode({ layoutSizingHorizontal: "FILL", layoutSizingVertical: "HUG" });
    // No width:auto — bbox width applies (sizeStyle handled it).
    expect(html).not.toContain("width:auto");
    expect(html).toContain("white-space:pre-wrap");
    expect(html).toContain("word-break:break-word");
  });

  it("FIXED via layoutSizingHorizontal → bbox-width + wrap", () => {
    const html = renderTextNode({ layoutSizingHorizontal: "FIXED", layoutSizingVertical: "HUG" });
    expect(html).toContain("white-space:pre-wrap");
  });

  it("WIDTH_AND_HEIGHT via legacy textAutoResize → width:auto + nowrap", () => {
    const html = renderTextNode({ textAutoResize: "WIDTH_AND_HEIGHT" });
    expect(html).toContain("width:auto");
    expect(html).toContain("white-space:nowrap");
  });

  it("HEIGHT via legacy textAutoResize → wrap", () => {
    const html = renderTextNode({ textAutoResize: "HEIGHT" });
    expect(html).toContain("white-space:pre-wrap");
  });

  it("TRUNCATE via legacy textAutoResize → nowrap + ellipsis", () => {
    const html = renderTextNode({ textAutoResize: "TRUNCATE" });
    expect(html).toContain("white-space:nowrap");
    expect(html).toContain("text-overflow:ellipsis");
    expect(html).toContain("overflow:hidden");
  });

  it("legacy textAutoResize wins over layoutSizingHorizontal when both present", () => {
    // Edge case: a tree carrying BOTH fields. textAutoResize is the
    // explicit non-autolayout signal and should win — Figma only emits it
    // for standalone TEXT, so its presence is informative.
    const html = renderTextNode({
      textAutoResize: "HEIGHT",
      layoutSizingHorizontal: "HUG",
    });
    // HEIGHT → fixed-wrap. Should NOT have width:auto from HUG.
    expect(html).toContain("white-space:pre-wrap");
    expect(html).not.toContain("width:auto");
  });

  it("neither field set → default to size-to-content (Figma's WIDTH_AND_HEIGHT default)", () => {
    // Legacy canonical_trees lacking both fields. We default to fit so
    // anonymous labels don't surprise-wrap.
    const html = renderTextNode({});
    expect(html).toContain("width:auto");
    expect(html).toContain("white-space:nowrap");
  });
});
