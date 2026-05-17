/**
 * inspector-property-groups.vitest.tsx — U10 property-summary pins.
 *
 * What's tested:
 *   - rgbaToHex math: 0..1 floats → #RRGGBB; alpha emits #RRGGBBAA
 *     only when partially transparent.
 *   - LayoutGroup: renders W×H, layoutMode label, padding (compact
 *     when uniform, four-value when not), gap, alignment for
 *     autolayout. Non-autolayout shows "absolute" direction and
 *     hides padding/gap/alignment rows.
 *   - TypographyGroup: always renders (matches Figma Dev Mode);
 *     populated values when TEXT, "—" placeholders otherwise.
 *   - FillsGroup: solid fill → hex swatch; image fill → "Image";
 *     no fills + no strokes → empty hint.
 */

import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";

import {
  FillsGroup,
  LayoutGroup,
  TypographyGroup,
  rgbaToHex,
} from "../inspector-property-groups";
import type { CanonicalNode } from "../types";

function makeNode(overrides: Partial<CanonicalNode> = {}): CanonicalNode {
  return {
    id: "n1",
    type: "FRAME",
    absoluteBoundingBox: { x: 0, y: 0, width: 320, height: 240 },
    ...overrides,
  } as CanonicalNode;
}

// ─── rgbaToHex ──────────────────────────────────────────────────────────

describe("rgbaToHex", () => {
  it("pure red", () => {
    expect(rgbaToHex({ r: 1, g: 0, b: 0 }, undefined)).toBe("#FF0000");
  });

  it("rounds to nearest int", () => {
    expect(rgbaToHex({ r: 0.5, g: 0.5, b: 0.5 }, undefined)).toBe("#808080");
  });

  it("opacity = 1 emits 6-char hex (no alpha)", () => {
    expect(rgbaToHex({ r: 0, g: 0, b: 0 }, 1)).toBe("#000000");
  });

  it("opacity < 1 emits 8-char hex with alpha", () => {
    expect(rgbaToHex({ r: 1, g: 1, b: 1 }, 0.5)).toBe("#FFFFFF80");
  });

  it("color.a < 1 also emits alpha", () => {
    expect(rgbaToHex({ r: 0, g: 0, b: 0, a: 0.5 }, undefined)).toBe("#00000080");
  });

  it("clamps OOR colors", () => {
    // r=1.5 clamped to 1 → 255 (FF); g=-0.5 → 0 (00); b=0.5 → 128 (80)
    expect(rgbaToHex({ r: 1.5, g: -0.5, b: 0.5 }, undefined)).toBe("#FF0080");
  });
});

// ─── LayoutGroup ────────────────────────────────────────────────────────

describe("LayoutGroup", () => {
  it("renders W × H from absoluteBoundingBox", () => {
    const html = renderToStaticMarkup(<LayoutGroup node={makeNode()} />);
    expect(html).toContain("320 × 240");
  });

  it("autolayout HORIZONTAL surfaces direction + padding + gap rows", () => {
    const node = makeNode({
      layoutMode: "HORIZONTAL",
      paddingTop: 8,
      paddingRight: 8,
      paddingBottom: 8,
      paddingLeft: 8,
      itemSpacing: 12,
      primaryAxisAlignItems: "CENTER",
      counterAxisAlignItems: "CENTER",
    });
    const html = renderToStaticMarkup(<LayoutGroup node={node} />);
    expect(html).toContain("horizontal");
    expect(html).toContain("8px"); // uniform padding
    expect(html).toContain("12px"); // gap
    expect(html).toContain("center"); // alignment
  });

  it("non-uniform padding emits four-value form", () => {
    const node = makeNode({
      layoutMode: "VERTICAL",
      paddingTop: 8,
      paddingRight: 16,
      paddingBottom: 8,
      paddingLeft: 16,
    });
    const html = renderToStaticMarkup(<LayoutGroup node={node} />);
    expect(html).toContain("8 / 16 / 8 / 16");
  });

  it("non-autolayout shows 'absolute' direction; hides padding rows", () => {
    const node = makeNode({ layoutMode: "NONE" });
    const html = renderToStaticMarkup(<LayoutGroup node={node} />);
    expect(html).toContain("absolute");
    expect(html).not.toContain("Padding");
  });

  it("missing layoutMode shows '—' direction", () => {
    const node = makeNode({ layoutMode: undefined });
    const html = renderToStaticMarkup(<LayoutGroup node={node} />);
    expect(html).toContain("—");
  });

  it("missing absoluteBoundingBox shows '—' size", () => {
    const node = makeNode({ absoluteBoundingBox: undefined });
    const html = renderToStaticMarkup(<LayoutGroup node={node} />);
    expect(html).toMatch(/Size.*—/);
  });
});

// ─── TypographyGroup ────────────────────────────────────────────────────

describe("TypographyGroup", () => {
  it("TEXT node with style renders full property set", () => {
    const node = makeNode({
      type: "TEXT",
      style: {
        fontFamily: "Inter",
        fontSize: 14,
        fontWeight: 500,
        lineHeightPx: 20,
        letterSpacing: 0.1,
      },
    } as unknown as Partial<CanonicalNode>);
    const html = renderToStaticMarkup(<TypographyGroup node={node} />);
    expect(html).toContain("Inter");
    expect(html).toContain("14px");
    expect(html).toContain("500");
    expect(html).toContain("20px");
    expect(html).toContain("0.10px");
  });

  it("non-TEXT renders all rows with '—' placeholders (matches Figma Dev Mode)", () => {
    const node = makeNode({ type: "FRAME" });
    const html = renderToStaticMarkup(<TypographyGroup node={node} />);
    expect(html).toContain("Typography");
    expect(html).toContain("Family");
    expect(html).toContain("Size");
    expect(html).toContain("—");
  });

  it("TEXT without style still renders with '—' placeholders", () => {
    const node = makeNode({ type: "TEXT" });
    const html = renderToStaticMarkup(<TypographyGroup node={node} />);
    expect(html).toContain("—");
  });
});

// ─── FillsGroup ─────────────────────────────────────────────────────────

describe("FillsGroup", () => {
  it("solid fill renders hex chip", () => {
    const node = makeNode({
      fills: [{ type: "SOLID", color: { r: 0.1, g: 0.55, b: 1, a: 1 } }],
    } as unknown as Partial<CanonicalNode>);
    const html = renderToStaticMarkup(<FillsGroup node={node} />);
    // R=26, G=140, B=255 → #1A8CFF
    expect(html).toContain("#1A8CFF");
  });

  it("image fill renders 'Image' label", () => {
    const node = makeNode({
      fills: [{ type: "IMAGE", imageRef: "abc" }],
    } as unknown as Partial<CanonicalNode>);
    const html = renderToStaticMarkup(<FillsGroup node={node} />);
    expect(html).toContain("Image");
  });

  it("gradient fill renders its kind", () => {
    const node = makeNode({
      fills: [{ type: "GRADIENT_LINEAR" }],
    } as unknown as Partial<CanonicalNode>);
    const html = renderToStaticMarkup(<FillsGroup node={node} />);
    expect(html).toContain("linear");
  });

  it("hidden fill shows '(hidden)'", () => {
    const node = makeNode({
      fills: [
        { type: "SOLID", color: { r: 1, g: 0, b: 0 }, visible: false },
      ],
    } as unknown as Partial<CanonicalNode>);
    const html = renderToStaticMarkup(<FillsGroup node={node} />);
    expect(html).toContain("(hidden)");
  });

  it("solid stroke renders with weight", () => {
    const node = makeNode({
      strokes: [{ type: "SOLID", color: { r: 0, g: 0, b: 0 } }],
      strokeWeight: 2,
    } as unknown as Partial<CanonicalNode>);
    const html = renderToStaticMarkup(<FillsGroup node={node} />);
    expect(html).toContain("#000000");
    expect(html).toContain("2px");
  });

  it("no fills and no strokes shows empty hint", () => {
    const node = makeNode({ fills: [], strokes: [] } as unknown as Partial<CanonicalNode>);
    const html = renderToStaticMarkup(<FillsGroup node={node} />);
    expect(html).toContain("No fills or strokes");
  });

  it("undefined fills array also shows empty hint", () => {
    const node = makeNode();
    const html = renderToStaticMarkup(<FillsGroup node={node} />);
    expect(html).toContain("No fills or strokes");
  });
});
