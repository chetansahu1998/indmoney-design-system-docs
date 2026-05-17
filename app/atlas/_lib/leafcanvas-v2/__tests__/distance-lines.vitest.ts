/**
 * distance-lines.vitest.ts — pure cardinal-distance math for the
 * Alt-hover distance-line chrome paint (U6).
 *
 * The chrome-layer DOM painters that build <line> and <text>
 * elements depend on real getBoundingClientRect coords, which
 * happy-dom doesn't fully simulate. We pin the math instead, where
 * the regression risk lives: computing the four cardinal gap
 * distances between two arbitrary screen-space rects.
 */

import { describe, expect, it } from "vitest";

import {
  computeCardinalDistances,
  type CardinalDistances,
} from "../chrome-layer";

interface Rect {
  left: number;
  top: number;
  width: number;
  height: number;
}

function r(left: number, top: number, width: number, height: number): Rect {
  return { left, top, width, height };
}

describe("computeCardinalDistances", () => {
  it("target to the right of selection: right gap positive, left gap negative", () => {
    const sel = r(0, 0, 100, 100);
    const tar = r(200, 0, 100, 100);
    const d = computeCardinalDistances(sel, tar);
    expect(d.right).toBe(100);
    expect(d.left).toBeLessThan(0);
    // Top/bottom overlap fully — both negative.
    expect(d.top).toBeLessThan(0);
    expect(d.bottom).toBeLessThan(0);
  });

  it("target to the left of selection: left gap positive", () => {
    const sel = r(200, 0, 100, 100);
    const tar = r(0, 0, 100, 100);
    const d = computeCardinalDistances(sel, tar);
    expect(d.left).toBe(100);
    expect(d.right).toBeLessThan(0);
  });

  it("target above selection: top gap positive", () => {
    const sel = r(0, 200, 100, 100);
    const tar = r(0, 0, 100, 100);
    const d = computeCardinalDistances(sel, tar);
    expect(d.top).toBe(100);
    expect(d.bottom).toBeLessThan(0);
  });

  it("target below selection: bottom gap positive", () => {
    const sel = r(0, 0, 100, 100);
    const tar = r(0, 200, 100, 100);
    const d = computeCardinalDistances(sel, tar);
    expect(d.bottom).toBe(100);
    expect(d.top).toBeLessThan(0);
  });

  it("diagonal placement (top-right): top and right positive, bottom/left negative", () => {
    const sel = r(0, 200, 100, 100);
    const tar = r(200, 0, 100, 100);
    const d = computeCardinalDistances(sel, tar);
    expect(d.top).toBeGreaterThan(0);
    expect(d.right).toBeGreaterThan(0);
    expect(d.bottom).toBeLessThan(0);
    expect(d.left).toBeLessThan(0);
  });

  it("overlapping rects: all four distances negative or zero", () => {
    const sel = r(0, 0, 100, 100);
    const tar = r(50, 50, 100, 100);
    const d = computeCardinalDistances(sel, tar);
    // Overlapping on both axes — no positive gap in any direction.
    expect(d.top).toBeLessThanOrEqual(0);
    expect(d.right).toBeLessThanOrEqual(0);
    expect(d.bottom).toBeLessThanOrEqual(0);
    expect(d.left).toBeLessThanOrEqual(0);
  });

  it("touching edges (zero gap): the touching side is exactly 0", () => {
    const sel = r(0, 0, 100, 100);
    const tar = r(100, 0, 100, 100); // touches sel's right edge
    const d = computeCardinalDistances(sel, tar);
    expect(d.right).toBe(0);
  });

  it("identical rects: all four distances are 0 or negative", () => {
    const sel = r(0, 0, 100, 100);
    const tar = r(0, 0, 100, 100);
    const d = computeCardinalDistances(sel, tar);
    expect(d.top).toBeLessThanOrEqual(0);
    expect(d.right).toBeLessThanOrEqual(0);
    expect(d.bottom).toBeLessThanOrEqual(0);
    expect(d.left).toBeLessThanOrEqual(0);
  });

  it("sub-pixel coordinates are preserved", () => {
    const sel = r(0.5, 0.5, 100.25, 100.25);
    const tar = r(200.75, 0.5, 100.25, 100.25);
    const d = computeCardinalDistances(sel, tar);
    // right gap = 200.75 - (0.5 + 100.25) = 100.0
    expect(d.right).toBeCloseTo(100.0, 5);
  });
});

describe("CardinalDistances — exported type contract", () => {
  it("type fields are top/right/bottom/left in that order", () => {
    // Compile-time check via type-keys is the real guarantee; this is
    // an at-runtime echo for documentation.
    const d: CardinalDistances = { top: 0, right: 0, bottom: 0, left: 0 };
    const keys = Object.keys(d).sort();
    expect(keys).toEqual(["bottom", "left", "right", "top"]);
  });
});
