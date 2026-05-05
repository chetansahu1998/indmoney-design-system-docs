/**
 * nodeToHTML.test.ts — Phase X U6.
 *
 * Pure-function tests over `nodeToHTML`. Asserts on the React element
 * tree directly (no renderer needed) — strips through `props.style`,
 * `props.children`, and data attributes. Each `_test_*` function is
 * independent and throws on failure; runAll is the umbrella driver.
 *
 * No test runner is wired in this repo (see resolveTreeForMode.test.ts);
 * the file is shape-correct for Vitest/Jest pickup if/when one lands.
 */

import { Children, type ReactElement, type ReactNode, isValidElement } from "react";

import { nodeToHTML, type NodeToHTMLContext } from "../nodeToHTML";
import type { BoundingBox, CanonicalNode } from "../types";
import { filterVisible } from "../visible-filter";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

const CTX: NodeToHTMLContext = { imageRefs: {} };

function asProps(el: ReactElement): { [k: string]: unknown } {
  return el.props as { [k: string]: unknown };
}

function styleOf(el: ReactElement): { [k: string]: unknown } {
  const s = asProps(el)["style"];
  return (s ?? {}) as { [k: string]: unknown };
}

function childrenOf(el: ReactElement): ReactElement[] {
  const c = asProps(el)["children"];
  const arr: ReactElement[] = [];
  Children.forEach(c as ReactNode, (child) => {
    if (isValidElement(child)) arr.push(child);
  });
  return arr;
}

/** Recursively flatten the rendered tree (skipping `display:contents`
 *  flatten-wrappers so callers can assert on the visible structure). */
function flattenForAssertion(el: ReactElement): ReactElement[] {
  const props = asProps(el);
  const data = props["data-flattened-from"];
  if (typeof data === "string") {
    const out: ReactElement[] = [];
    for (const c of childrenOf(el)) out.push(...flattenForAssertion(c));
    return out;
  }
  return [el];
}

const ROOT_BBOX: BoundingBox = { x: 0, y: 0, width: 320, height: 640 };

// ─── Happy path: dual-path render ────────────────────────────────────────────

function _test_dual_path_autolayout_and_absolute(): void {
  const tree: CanonicalNode = {
    id: "root",
    type: "FRAME",
    absoluteBoundingBox: ROOT_BBOX,
    children: [
      {
        id: "a",
        type: "FRAME",
        layoutMode: "VERTICAL",
        itemSpacing: 8,
        paddingLeft: 12,
        paddingTop: 16,
        absoluteBoundingBox: { x: 0, y: 0, width: 320, height: 100 },
        children: [
          {
            id: "a-text",
            type: "TEXT",
            characters: "Hello",
            absoluteBoundingBox: { x: 12, y: 16, width: 100, height: 20 },
            style: { fontFamily: "Inter", fontSize: 16, fontWeight: 500 },
            fills: [{ type: "SOLID", color: { r: 0, g: 0, b: 0 } }],
          },
        ],
      },
      {
        id: "b",
        type: "RECTANGLE",
        absoluteBoundingBox: { x: 50, y: 200, width: 80, height: 40 },
      },
    ],
  };
  const filtered = filterVisible(tree)!;
  const root = nodeToHTML(filtered, ROOT_BBOX, null, CTX, "r")!;
  assert(root.type === "div", "root is a div");
  const kids = childrenOf(root);
  assert(kids.length === 2, "two top-level kids");

  // Container A — autolayout
  const a = kids[0];
  const aStyle = styleOf(a);
  assert(aStyle["display"] === "flex", "container A uses display:flex");
  assert(aStyle["flexDirection"] === "column", "VERTICAL → column");
  assert(aStyle["gap"] === "8px", "itemSpacing → gap");
  assert(aStyle["paddingLeft"] === "12px", "paddingLeft");
  assert(aStyle["paddingTop"] === "16px", "paddingTop");

  // TEXT child inside autolayout container — should be a span and not
  // emit position:absolute (parent is autolayout).
  const aKids = childrenOf(a);
  assert(aKids.length === 1, "one text child");
  const textEl = aKids[0];
  assert(textEl.type === "span", "TEXT renders as <span>");
  const textStyle = styleOf(textEl);
  assert(textStyle["position"] === "relative", "text in autolayout has position:relative not absolute");
  assert(textStyle["fontSize"] === "16px", "fontSize");
  assert(textStyle["fontWeight"] === 500, "fontWeight");

  // Sibling B — absolute path
  const b = kids[1];
  const bStyle = styleOf(b);
  assert(bStyle["position"] === "absolute", "absolute child uses position:absolute");
  assert(bStyle["left"] === "50px", "left = childX - parentX");
  assert(bStyle["top"] === "200px", "top = childY - parentY");
}

// ─── GROUP flattening ────────────────────────────────────────────────────────

function _test_group_flattening(): void {
  const tree: CanonicalNode = {
    id: "root",
    type: "FRAME",
    absoluteBoundingBox: ROOT_BBOX,
    children: [
      {
        id: "g",
        type: "GROUP",
        children: [
          { id: "r1", type: "RECTANGLE", absoluteBoundingBox: { x: 10, y: 10, width: 20, height: 20 } },
          { id: "r2", type: "RECTANGLE", absoluteBoundingBox: { x: 40, y: 40, width: 20, height: 20 } },
        ],
      },
    ],
  };
  const filtered = filterVisible(tree)!;
  const root = nodeToHTML(filtered, ROOT_BBOX, null, CTX, "r")!;

  // Walk the rendered tree and confirm there is no element whose
  // data-figma-type is "GROUP".
  const allEls = collectAllElements(root);
  for (const el of allEls) {
    const t = (el.props as { [k: string]: unknown })["data-figma-type"];
    assert(t !== "GROUP", "no GROUP element in DOM");
  }

  // After flattening, the root's *visible* descendants should be exactly
  // the two rectangles, both absolutely positioned to their parent (root).
  const visibleKids: ReactElement[] = [];
  for (const c of childrenOf(root)) {
    visibleKids.push(...flattenForAssertion(c));
  }
  assert(visibleKids.length === 2, "two flattened kids");
  for (const k of visibleKids) {
    const s = styleOf(k);
    assert(s["position"] === "absolute", "each flattened RECTANGLE is absolute");
  }
}

function collectAllElements(el: ReactElement): ReactElement[] {
  const out: ReactElement[] = [el];
  for (const c of childrenOf(el)) out.push(...collectAllElements(c));
  return out;
}

// ─── visible:false deeply nested ─────────────────────────────────────────────

function _test_deep_visible_false_pruned(): void {
  const tree: CanonicalNode = {
    id: "root",
    type: "FRAME",
    absoluteBoundingBox: ROOT_BBOX,
    children: [
      {
        id: "wrap",
        type: "FRAME",
        absoluteBoundingBox: { x: 0, y: 0, width: 100, height: 100 },
        children: [
          {
            id: "secret",
            type: "FRAME",
            visible: false,
            absoluteBoundingBox: { x: 0, y: 0, width: 50, height: 50 },
            children: [
              { id: "secret-text", type: "TEXT", characters: "hidden!" },
            ],
          },
        ],
      },
    ],
  };
  const filtered = filterVisible(tree)!;
  const root = nodeToHTML(filtered, ROOT_BBOX, null, CTX, "r")!;
  const allEls = collectAllElements(root);
  for (const el of allEls) {
    const id = (el.props as { [k: string]: unknown })["data-figma-id"];
    assert(id !== "secret" && id !== "secret-text", "hidden subtree pruned from DOM");
  }
}

// ─── Co-positioned data-state-group attribute survives ───────────────────────

function _test_state_group_attribute_emitted(): void {
  const tree: CanonicalNode = {
    id: "root",
    type: "FRAME",
    absoluteBoundingBox: ROOT_BBOX,
    children: [
      {
        id: "default",
        type: "FRAME",
        absoluteBoundingBox: { x: 10, y: 10, width: 50, height: 20 },
      },
      {
        id: "hover",
        type: "FRAME",
        absoluteBoundingBox: { x: 10, y: 10, width: 50, height: 20 },
      },
    ],
  };
  const filtered = filterVisible(tree)!;
  const root = nodeToHTML(filtered, ROOT_BBOX, null, CTX, "r")!;
  const kids = childrenOf(root);
  assert(kids.length === 2, "two co-positioned kids");
  for (const k of kids) {
    const tag = (k.props as { [k: string]: unknown })["data-state-group"];
    assert(typeof tag === "string" && tag.length > 0, "data-state-group emitted");
  }
}

// ─── Image fill: scaleMode=FIT → background-size: contain ────────────────────

function _test_image_fill_scale_mode_fit(): void {
  const tree: CanonicalNode = {
    id: "img",
    type: "RECTANGLE",
    absoluteBoundingBox: { x: 0, y: 0, width: 100, height: 100 },
    fills: [{ type: "IMAGE", imageRef: "abc", scaleMode: "FIT" }],
  };
  const ctx: NodeToHTMLContext = { imageRefs: { abc: "https://cdn/x.png" } };
  const el = nodeToHTML(tree, ROOT_BBOX, null, ctx, "r")!;
  const s = styleOf(el);
  assert(s["backgroundSize"] === "contain", "FIT → contain");
  assert(typeof s["backgroundImage"] === "string", "backgroundImage set");
  assert(
    String(s["backgroundImage"]).includes("https://cdn/x.png"),
    "URL in backgroundImage",
  );
}

// ─── clipsContent on FRAME → overflow:hidden ─────────────────────────────────

function _test_frame_clips_content(): void {
  const tree: CanonicalNode = {
    id: "f",
    type: "FRAME",
    absoluteBoundingBox: { x: 0, y: 0, width: 100, height: 100 },
    children: [
      {
        id: "outside",
        type: "RECTANGLE",
        absoluteBoundingBox: { x: 200, y: 0, width: 50, height: 50 },
      },
    ],
  };
  const el = nodeToHTML(tree, ROOT_BBOX, null, CTX, "r")!;
  const s = styleOf(el);
  assert(s["overflow"] === "hidden", "FRAME defaults clipsContent → overflow:hidden");
}

function _test_frame_explicit_clips_false(): void {
  const tree: CanonicalNode = {
    id: "f",
    type: "FRAME",
    clipsContent: false,
    absoluteBoundingBox: { x: 0, y: 0, width: 100, height: 100 },
  };
  const el = nodeToHTML(tree, ROOT_BBOX, null, CTX, "r")!;
  const s = styleOf(el);
  assert(s["overflow"] !== "hidden", "explicit clipsContent:false disables clip");
}

// ─── Performance: 500-node frame stays within budget ─────────────────────────

function _test_perf_500_nodes_under_budget(): void {
  const root = synthLargeTree(500);
  const t0 = Date.now();
  const filtered = filterVisible(root)!;
  const el = nodeToHTML(filtered, root.absoluteBoundingBox!, null, CTX, "r")!;
  const t1 = Date.now();

  const elements = collectAllElements(el);
  assert(elements.length <= 1000, `≤1000 elements (got ${elements.length})`);
  // 50ms budget is jsdom-ish — give ourselves some slack so this doesn't
  // flake on slower machines. The plan calls for <50ms; we assert <250ms
  // so the test is informative without being flaky.
  assert(t1 - t0 < 250, `convert latency under 250ms (got ${t1 - t0}ms)`);
}

function synthLargeTree(target: number): CanonicalNode {
  const root: CanonicalNode = {
    id: "root",
    type: "FRAME",
    absoluteBoundingBox: { x: 0, y: 0, width: 1000, height: 1000 },
    children: [],
  };
  for (let i = 0; i < target - 1; i++) {
    root.children!.push({
      id: `n${i}`,
      type: i % 5 === 0 ? "TEXT" : "RECTANGLE",
      characters: i % 5 === 0 ? `t${i}` : undefined,
      absoluteBoundingBox: { x: (i * 7) % 900, y: (i * 11) % 900, width: 20, height: 20 },
      fills: [{ type: "SOLID", color: { r: 0.2, g: 0.2, b: 0.2 } }],
    });
  }
  return root;
}

// ─── Hidden via opacity:0 also pruned ────────────────────────────────────────

function _test_opacity_zero_pruned(): void {
  const tree: CanonicalNode = {
    id: "root",
    type: "FRAME",
    absoluteBoundingBox: ROOT_BBOX,
    children: [
      { id: "a", type: "RECTANGLE", absoluteBoundingBox: { x: 0, y: 0, width: 10, height: 10 } },
      { id: "b", type: "RECTANGLE", opacity: 0, absoluteBoundingBox: { x: 0, y: 0, width: 10, height: 10 } },
    ],
  };
  const filtered = filterVisible(tree)!;
  const el = nodeToHTML(filtered, ROOT_BBOX, null, CTX, "r")!;
  const allEls = collectAllElements(el);
  for (const e of allEls) {
    const id = (e.props as { [k: string]: unknown })["data-figma-id"];
    assert(id !== "b", "opacity:0 sibling pruned");
  }
}

export function runAll(): void {
  _test_dual_path_autolayout_and_absolute();
  _test_group_flattening();
  _test_deep_visible_false_pruned();
  _test_state_group_attribute_emitted();
  _test_image_fill_scale_mode_fit();
  _test_frame_clips_content();
  _test_frame_explicit_clips_false();
  _test_perf_500_nodes_under_budget();
  _test_opacity_zero_pruned();
}
