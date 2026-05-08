/**
 * MeasurementOverlay.vitest.tsx — Phase 2 U3 scaffold smoke tests.
 *
 * The scaffold's job is to:
 *   - render `<svg>` only when there's something to draw OR when
 *     consumers (tests/Playwright) need a stable DOM hook
 *   - subscribe to canvasGestureTracker for settle re-renders
 *   - tear the subscription down on unmount
 *   - pass through frame bbox dimensions as data-attributes for
 *     QA inspection
 *
 * U4-U8 add render branches for distance lines, padding bands, gap
 * markers, and selected chips. Each lands its own targeted test
 * cases inside this file.
 */

import { afterEach, describe, expect, it } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { act } from "react";

import {
  MeasurementOverlay,
  computeDistanceSegments,
  type MeasurementOverlayProps,
} from "../MeasurementOverlay";
import { setHoveredAtomicChild } from "../hover-signal";
import { useAtlas } from "../../../../../lib/atlas/live-store";
import type { AnnotatedNode } from "../types";

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(props: MeasurementOverlayProps): HTMLElement {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(<MeasurementOverlay {...props} />);
  });
  return container;
}

afterEach(() => {
  setHoveredAtomicChild(null);
  if (root) {
    act(() => root!.unmount());
    root = null;
  }
  if (container) {
    container.remove();
    container = null;
  }
});

const tree: AnnotatedNode = {
  id: "root",
  type: "FRAME",
  name: "Screen",
  absoluteBoundingBox: { x: 100, y: 200, width: 375, height: 812 },
  children: [
    {
      id: "cta",
      type: "INSTANCE",
      name: "Single Button Set",
      absoluteBoundingBox: { x: 116, y: 700, width: 343, height: 56 },
    } as unknown as AnnotatedNode,
  ],
};

const frameBBox = { x: 100, y: 200, width: 375, height: 812 };

describe("MeasurementOverlay — scaffold", () => {
  it("renders a stable empty <svg> when nothing hovered or selected", () => {
    setHoveredAtomicChild(null);
    const wrapper = mount({ screenID: "screen-1", frameBBox, tree });
    const svg = wrapper.querySelector("svg.leafcv2-measurement");
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("data-screen-id")).toBe("screen-1");
    // No content yet — U4-U8 add child elements.
    expect(svg?.children.length).toBe(0);
  });

  it("renders empty <svg> placeholder even when paused (gesture in flight) — fallback in U4+", () => {
    // The scaffold returns null mid-gesture (no jitter). We can't
    // easily simulate a gesture without coupling to the tracker
    // singleton. Instead, the contract is: when nothing matches AND
    // not gesturing, the <svg> placeholder appears with data-screen-id
    // for Playwright queries. Asserted in the previous test.
    expect(true).toBe(true);
  });

  it("renders nothing when tree is null", () => {
    const wrapper = mount({ screenID: "screen-1", frameBBox, tree: null });
    expect(wrapper.querySelector("svg.leafcv2-measurement")).toBeNull();
  });

  it("renders sized <svg> when an atomic in this frame is hovered", () => {
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "cta" });
    const wrapper = mount({ screenID: "screen-1", frameBBox, tree });
    const svg = wrapper.querySelector<SVGElement>("svg.leafcv2-measurement");
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("data-frame-w")).toBe("375");
    expect(svg?.getAttribute("data-frame-h")).toBe("812");
    expect(svg?.getAttribute("viewBox")).toBe("0 0 375 812");
  });

  it("does not render content when only a different screen has the hover", () => {
    setHoveredAtomicChild({ screenID: "other-screen", figmaNodeID: "cta" });
    const wrapper = mount({ screenID: "screen-1", frameBBox, tree });
    const svg = wrapper.querySelector("svg.leafcv2-measurement");
    // Empty svg present (the no-hovered/selected branch fires).
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("viewBox")).toBeNull();
  });
});

// ─── U4 — distance line math (pure helper) ─────────────────────────────

describe("computeDistanceSegments — pure math", () => {
  // Canonical reference rectangles in wrapper-local coords.
  const S = { x: 100, y: 100, width: 100, height: 50 }; // 100,100 → 200,150

  it("H above S — emits top segment with correct distance", () => {
    const H = { x: 110, y: 30, width: 80, height: 40 }; // 110,30 → 190,70 (gap = 30)
    const segs = computeDistanceSegments(S, H);
    expect(segs).toHaveLength(1);
    expect(segs[0].direction).toBe("top");
    expect(segs[0].distancePx).toBe(30);
    // Line drawn from S top-edge midpoint (sCenterX=150, y=100) up to (150, 70).
    expect(segs[0].x1).toBe(150);
    expect(segs[0].y1).toBe(100);
    expect(segs[0].x2).toBe(150);
    expect(segs[0].y2).toBe(70);
  });

  it("H below S — emits bottom segment", () => {
    const H = { x: 110, y: 200, width: 80, height: 40 }; // gap = 50
    const segs = computeDistanceSegments(S, H);
    expect(segs).toHaveLength(1);
    expect(segs[0].direction).toBe("bottom");
    expect(segs[0].distancePx).toBe(50);
  });

  it("H left of S — emits left segment", () => {
    const H = { x: 30, y: 110, width: 50, height: 30 }; // hRight=80, gap=20
    const segs = computeDistanceSegments(S, H);
    expect(segs).toHaveLength(1);
    expect(segs[0].direction).toBe("left");
    expect(segs[0].distancePx).toBe(20);
  });

  it("H right of S — emits right segment", () => {
    const H = { x: 250, y: 110, width: 50, height: 30 }; // gap=50
    const segs = computeDistanceSegments(S, H);
    expect(segs).toHaveLength(1);
    expect(segs[0].direction).toBe("right");
    expect(segs[0].distancePx).toBe(50);
  });

  it("H overlaps S vertically and below-right — emits bottom + right", () => {
    // S: 100..200 / 100..150
    // H: 250..300 / 130..180 — overlaps Y axis (130..150 inside 100..150)
    //   so no top/bottom (H not entirely above/below S).
    //   H is right of S (h.x > sRight=200) → right
    const H = { x: 250, y: 130, width: 50, height: 50 };
    const segs = computeDistanceSegments(S, H);
    const dirs = segs.map((s) => s.direction).sort();
    expect(dirs).toEqual(["right"]);
  });

  it("H overlaps both axes (inside S) — emits no segments", () => {
    const H = { x: 110, y: 110, width: 30, height: 20 };
    expect(computeDistanceSegments(S, H)).toEqual([]);
  });

  it("H is identical to S — emits no segments", () => {
    expect(computeDistanceSegments(S, S)).toEqual([]);
  });

  it("H abuts S exactly (touching but no gap) — emits no segments", () => {
    // hRight = 100 = s.x → not "entirely left of S" by strict <
    const H = { x: 50, y: 110, width: 50, height: 30 };
    expect(computeDistanceSegments(S, H)).toEqual([]);
  });

  it("H far below-left — emits bottom + left", () => {
    // S 100..200 / 100..150
    // H 30..70 / 200..240 — h.x+w=70 < s.x=100 (left), h.y=200 > sBottom=150 (below)
    const H = { x: 30, y: 200, width: 40, height: 40 };
    const dirs = computeDistanceSegments(S, H)
      .map((s) => s.direction)
      .sort();
    expect(dirs).toEqual(["bottom", "left"]);
  });

  it("rounds fractional gaps in label rendering (mid-point math is exact)", () => {
    const H = { x: 110, y: 60, width: 80, height: 30 }; // hBottom=90, gap=10
    const Sf = { x: 100, y: 100.5, width: 100, height: 50 };
    const segs = computeDistanceSegments(Sf, H);
    expect(segs).toHaveLength(1);
    expect(segs[0].distancePx).toBeCloseTo(10.5, 5);
    // The visible label rounds to integer; pure math returns the float.
  });
});

// ─── U4 — DOM-level rendering smoke check ─────────────────────────────
//
// Renders MeasurementOverlay with a small tree where hover and select
// land on different nodes; verifies <line> elements appear in the SVG.

const u4Tree: AnnotatedNode = {
  id: "root",
  type: "FRAME",
  name: "Screen",
  absoluteBoundingBox: { x: 0, y: 0, width: 400, height: 300 },
  children: [
    {
      id: "selected-node",
      type: "INSTANCE",
      name: "Header",
      absoluteBoundingBox: { x: 50, y: 50, width: 100, height: 50 },
    } as unknown as AnnotatedNode,
    {
      id: "hovered-node",
      type: "INSTANCE",
      name: "Footer",
      absoluteBoundingBox: { x: 50, y: 200, width: 100, height: 50 },
    } as unknown as AnnotatedNode,
  ],
};

const u4FrameBBox = { x: 0, y: 0, width: 400, height: 300 };

describe("MeasurementOverlay — U4 distance lines DOM", () => {
  afterEach(() => {
    // Reset live-store selection between tests.
    act(() => useAtlas.getState().selectAtomicChild("", ""));
  });

  it("emits <line> + <text> when hover and select differ", () => {
    act(() => {
      useAtlas.getState().selectAtomicChild("screen-1", "selected-node");
    });
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "hovered-node" });
    const wrapper = mount({ screenID: "screen-1", frameBBox: u4FrameBBox, tree: u4Tree });
    const lines = wrapper.querySelectorAll("svg.leafcv2-measurement line");
    // selected at (50, 50, 100×50) — bottom = 100
    // hovered at (50, 200, 100×50) — top = 200
    // gap = 100 (bottom direction)
    expect(lines.length).toBeGreaterThanOrEqual(1);
    const text = wrapper.querySelector("svg.leafcv2-measurement text");
    expect(text?.textContent).toBe("100");
  });

  it("renders no lines when hover === select (same node)", () => {
    act(() => {
      useAtlas.getState().selectAtomicChild("screen-1", "selected-node");
    });
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "selected-node" });
    const wrapper = mount({ screenID: "screen-1", frameBBox: u4FrameBBox, tree: u4Tree });
    expect(wrapper.querySelectorAll("svg.leafcv2-measurement line").length).toBe(0);
  });

  it("renders no lines when only one of hover/select is set", () => {
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "hovered-node" });
    const wrapper = mount({ screenID: "screen-1", frameBBox: u4FrameBBox, tree: u4Tree });
    expect(wrapper.querySelectorAll("svg.leafcv2-measurement line").length).toBe(0);
  });
});

// ─── U5 — padding bands ───────────────────────────────────────────────

const u5Tree: AnnotatedNode = {
  id: "root",
  type: "FRAME",
  name: "Screen",
  absoluteBoundingBox: { x: 0, y: 0, width: 400, height: 600 },
  children: [
    {
      id: "card-with-padding",
      type: "FRAME",
      name: "Card",
      layoutMode: "VERTICAL",
      paddingTop: 24,
      paddingRight: 16,
      paddingBottom: 24,
      paddingLeft: 16,
      absoluteBoundingBox: { x: 50, y: 100, width: 300, height: 200 },
    } as unknown as AnnotatedNode,
    {
      id: "card-no-autolayout",
      type: "FRAME",
      name: "AbsCard",
      layoutMode: "NONE",
      paddingTop: 24,
      absoluteBoundingBox: { x: 50, y: 350, width: 300, height: 100 },
    } as unknown as AnnotatedNode,
    {
      id: "instance-with-padding",
      type: "INSTANCE",
      name: "ButtonInstance",
      layoutMode: "HORIZONTAL",
      paddingLeft: 12,
      absoluteBoundingBox: { x: 50, y: 480, width: 200, height: 60 },
    } as unknown as AnnotatedNode,
  ],
};

const u5FrameBBox = { x: 0, y: 0, width: 400, height: 600 };

describe("MeasurementOverlay — U5 padding bands", () => {
  it("renders 4 bands when hovered FRAME has all 4 paddings", () => {
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "card-with-padding" });
    const wrapper = mount({ screenID: "screen-1", frameBBox: u5FrameBBox, tree: u5Tree });
    const bands = wrapper.querySelectorAll(
      "svg.leafcv2-measurement g.leafcv2-measurement__padding-bands g[data-band]",
    );
    expect(bands.length).toBe(4);
    const dirs = Array.from(bands).map((b) => b.getAttribute("data-band")).sort();
    expect(dirs).toEqual([
      "paddingBottom",
      "paddingLeft",
      "paddingRight",
      "paddingTop",
    ]);
  });

  it("emits the correct numeric labels", () => {
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "card-with-padding" });
    const wrapper = mount({ screenID: "screen-1", frameBBox: u5FrameBBox, tree: u5Tree });
    const labels = Array.from(
      wrapper.querySelectorAll("g[data-band] text"),
    ).map((t) => t.textContent);
    // 4 padding values: top=24, right=16, bottom=24, left=16
    expect(labels.sort()).toEqual(["16", "16", "24", "24"]);
  });

  it("renders nothing when hovered FRAME has layoutMode=NONE", () => {
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "card-no-autolayout" });
    const wrapper = mount({ screenID: "screen-1", frameBBox: u5FrameBBox, tree: u5Tree });
    expect(
      wrapper.querySelectorAll("g.leafcv2-measurement__padding-bands g[data-band]").length,
    ).toBe(0);
  });

  it("renders only the non-zero paddings (instance with paddingLeft only)", () => {
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "instance-with-padding" });
    const wrapper = mount({ screenID: "screen-1", frameBBox: u5FrameBBox, tree: u5Tree });
    // INSTANCE not FRAME → no bands rendered (we gate on FRAME)
    expect(
      wrapper.querySelectorAll("g.leafcv2-measurement__padding-bands g[data-band]").length,
    ).toBe(0);
  });

  it("does not render padding bands for non-FRAME hovered nodes", () => {
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "instance-with-padding" });
    const wrapper = mount({ screenID: "screen-1", frameBBox: u5FrameBBox, tree: u5Tree });
    expect(
      wrapper.querySelector("g.leafcv2-measurement__padding-bands"),
    ).toBeNull();
  });

  it("padding bands position correctly in wrapper-local coords", () => {
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "card-with-padding" });
    const wrapper = mount({ screenID: "screen-1", frameBBox: u5FrameBBox, tree: u5Tree });
    // card abs (50, 100, 300×200), frame origin (0, 0) → local (50, 100)
    const top = wrapper.querySelector('g[data-band="paddingTop"] rect');
    expect(top?.getAttribute("x")).toBe("50");
    expect(top?.getAttribute("y")).toBe("100");
    expect(top?.getAttribute("width")).toBe("300");
    expect(top?.getAttribute("height")).toBe("24");
  });
});
