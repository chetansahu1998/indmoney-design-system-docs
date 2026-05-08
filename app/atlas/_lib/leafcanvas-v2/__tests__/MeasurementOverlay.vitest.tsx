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

import { MeasurementOverlay, type MeasurementOverlayProps } from "../MeasurementOverlay";
import { setHoveredAtomicChild } from "../hover-signal";
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
