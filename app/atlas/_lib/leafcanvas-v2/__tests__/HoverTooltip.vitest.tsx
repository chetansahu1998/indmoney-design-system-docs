/**
 * HoverTooltip.vitest.ts — Phase 2 U2 hover-tooltip pill rendering.
 *
 * The component renders absolute-positioned content based on:
 *   - useHoveredAtomicChild() returning a {screenID, figmaNodeID}
 *   - the canonical_tree containing a node with that figmaNodeID
 *     and an absoluteBoundingBox
 *
 * We don't mount React here (no DOM environment in this Vitest
 * config beyond happy-dom; React Testing Library would be a
 * heavier setup than U2 needs). Instead, the tests pin two pure
 * surfaces:
 *
 *   1. Tree-walk semantics — the component delegates lookup to a
 *      depth-first walker (`findHovered`) which is colocated in
 *      the file. The walker is deterministic, easy to validate
 *      with the React-tree shape we hand it.
 *
 *   2. Position math — wrapper-local rebase + edge-flip when the
 *      computed top would clip above the wrapper. We test these
 *      via React's `act` + a minimal happy-dom render, asserting
 *      on the rendered `style.left/top` strings.
 */

import { describe, expect, it, afterEach } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { act } from "react";

import { HoverTooltip, type HoverTooltipProps } from "../HoverTooltip";
import { setHoveredAtomicChild } from "../hover-signal";
import type { AnnotatedNode } from "../types";

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(props: HoverTooltipProps): HTMLElement {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(<HoverTooltip {...props} />);
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

const baseTree: AnnotatedNode = {
  id: "root",
  type: "FRAME",
  name: "Screen",
  absoluteBoundingBox: { x: 100, y: 200, width: 375, height: 812 },
  children: [
    {
      id: "header",
      type: "FRAME",
      name: "Header",
      absoluteBoundingBox: { x: 100, y: 200, width: 375, height: 60 },
      children: [
        {
          id: "title",
          type: "TEXT",
          name: "Confirm your address",
          absoluteBoundingBox: { x: 116, y: 220, width: 200, height: 30 },
        } as unknown as AnnotatedNode,
      ],
    } as unknown as AnnotatedNode,
    {
      id: "cta",
      type: "INSTANCE",
      name: "Single Button Set",
      absoluteBoundingBox: { x: 116, y: 700, width: 343, height: 56 },
    } as unknown as AnnotatedNode,
  ],
};

const frameBBox = { x: 100, y: 200, width: 375, height: 812 };

describe("HoverTooltip", () => {
  it("renders nothing when no atomic is hovered", () => {
    setHoveredAtomicChild(null);
    const wrapper = mount({ screenID: "screen-1", frameBBox, tree: baseTree });
    expect(wrapper.querySelector(".leafcv2-hover-tooltip")).toBeNull();
  });

  it("renders nothing when hovered atomic is in a different screen", () => {
    setHoveredAtomicChild({ screenID: "screen-other", figmaNodeID: "cta" });
    const wrapper = mount({ screenID: "screen-1", frameBBox, tree: baseTree });
    expect(wrapper.querySelector(".leafcv2-hover-tooltip")).toBeNull();
  });

  it("renders Name + W×H when the hovered atomic is found", () => {
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "cta" });
    const wrapper = mount({ screenID: "screen-1", frameBBox, tree: baseTree });
    const pill = wrapper.querySelector(".leafcv2-hover-tooltip");
    expect(pill).not.toBeNull();
    expect(pill?.textContent).toContain("Single Button Set");
    expect(pill?.textContent).toContain("343");
    expect(pill?.textContent).toContain("56");
  });

  it("rebases position to wrapper-local coords (cta at world 116,700 → local 16,500)", () => {
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "cta" });
    const wrapper = mount({ screenID: "screen-1", frameBBox, tree: baseTree });
    const pill = wrapper.querySelector<HTMLElement>(".leafcv2-hover-tooltip");
    expect(pill).not.toBeNull();
    // cta absolute is (116, 700). frameBBox origin (100, 200).
    // local origin: (16, 500). Tooltip default sits above bbox top:
    // top = 500 - TOOLTIP_HEIGHT(22) - OFFSET_Y(6) = 472.
    // left = 16 (clamped non-negative).
    expect(pill?.style.left).toBe("16px");
    expect(pill?.style.top).toBe("472px");
  });

  it("edge-flips below the bbox when default position would clip above the wrapper", () => {
    // 'title' is at world y=220, bbox height 30. Wrapper origin y=200.
    // local Y = 20. Default top = 20 - 22 - 6 = -8 → clips → flip below.
    // Flipped top = 20 + 30 + 6 = 56.
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "title" });
    const wrapper = mount({ screenID: "screen-1", frameBBox, tree: baseTree });
    const pill = wrapper.querySelector<HTMLElement>(".leafcv2-hover-tooltip");
    expect(pill?.style.top).toBe("56px");
  });

  it("returns null when the matched node has no bbox", () => {
    const treeNoBBox: AnnotatedNode = {
      ...baseTree,
      children: [
        { id: "noBox", type: "TEXT", name: "x" } as unknown as AnnotatedNode,
      ],
    };
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "noBox" });
    const wrapper = mount({ screenID: "screen-1", frameBBox, tree: treeNoBBox });
    expect(wrapper.querySelector(".leafcv2-hover-tooltip")).toBeNull();
  });

  it("falls back to type when name is empty", () => {
    const treeEmptyName: AnnotatedNode = {
      ...baseTree,
      children: [
        {
          id: "n",
          type: "RECTANGLE",
          name: "",
          absoluteBoundingBox: { x: 100, y: 250, width: 50, height: 50 },
        } as unknown as AnnotatedNode,
      ],
    };
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "n" });
    const wrapper = mount({ screenID: "screen-1", frameBBox, tree: treeEmptyName });
    expect(wrapper.querySelector(".leafcv2-hover-tooltip")?.textContent).toContain(
      "RECTANGLE",
    );
  });

  it("rounds W×H to integers", () => {
    const tree: AnnotatedNode = {
      ...baseTree,
      children: [
        {
          id: "frac",
          type: "RECTANGLE",
          name: "Frac",
          absoluteBoundingBox: { x: 100, y: 250, width: 343.75, height: 55.49 },
        } as unknown as AnnotatedNode,
      ],
    };
    setHoveredAtomicChild({ screenID: "screen-1", figmaNodeID: "frac" });
    const wrapper = mount({ screenID: "screen-1", frameBBox, tree });
    const txt = wrapper.querySelector(".leafcv2-hover-tooltip")?.textContent;
    expect(txt).toContain("344");
    expect(txt).toContain("55");
  });
});
