/**
 * visible-filter.test.ts — Phase X U6.
 *
 * No test runner is wired in this repo (per `lib/projects/resolveTreeForMode.test.ts`),
 * but this file is shape-correct for Vitest/Jest pickup. Each `_test_*`
 * function is independent and asserts via thrown errors. The `runAll`
 * export below lets a future runner drive everything from a single entry.
 */

import { countNodes, filterVisible, isVisible } from "../visible-filter";
import type { CanonicalNode } from "../types";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

function _test_isVisible_default_visible(): void {
  assert(isVisible({}), "node with no visible/opacity should be visible");
  assert(isVisible({ visible: true }), "explicit visible:true is visible");
  assert(isVisible({ opacity: 1 }), "opacity:1 is visible");
  assert(isVisible({ opacity: 0.5 }), "partial opacity is still visible");
}

function _test_isVisible_hidden(): void {
  assert(!isVisible({ visible: false }), "visible:false hides node");
  assert(!isVisible({ opacity: 0 }), "opacity:0 hides node");
  assert(!isVisible({ opacity: 0.0005 }), "near-zero opacity hides node");
}

function _test_filterVisible_drops_hidden_subtree(): void {
  const tree: CanonicalNode = {
    id: "root",
    type: "FRAME",
    children: [
      { id: "a", type: "FRAME", visible: true },
      {
        id: "b",
        type: "FRAME",
        visible: true,
        children: [
          { id: "b1", type: "RECTANGLE", visible: false },
          { id: "b2", type: "RECTANGLE", visible: true },
        ],
      },
      { id: "c", type: "FRAME", visible: false }, // whole subtree pruned
    ],
  };
  const out = filterVisible(tree);
  assert(out !== null, "root visible");
  assert(out!.children!.length === 2, "two visible top-level children");
  const ids = out!.children!.map((c) => c.id);
  assert(!ids.includes("c"), "hidden top-level subtree pruned");
  const b = out!.children!.find((c) => c.id === "b")!;
  assert(b.children!.length === 1, "hidden inner child pruned");
  assert(b.children![0].id === "b2", "remaining inner child is b2");
}

function _test_filterVisible_returns_null_for_hidden_root(): void {
  const out = filterVisible({ id: "x", visible: false });
  assert(out === null, "hidden root returns null");
}

function _test_filterVisible_does_not_mutate_input(): void {
  const tree: CanonicalNode = {
    id: "root",
    children: [{ id: "a", visible: false }, { id: "b" }],
  };
  const before = JSON.stringify(tree);
  filterVisible(tree);
  assert(JSON.stringify(tree) === before, "input unchanged");
}

function _test_copositioned_siblings_get_state_group(): void {
  const tree: CanonicalNode = {
    id: "root",
    type: "FRAME",
    absoluteBoundingBox: { x: 0, y: 0, width: 100, height: 100 },
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
      {
        id: "other",
        type: "FRAME",
        absoluteBoundingBox: { x: 60, y: 10, width: 50, height: 20 },
      },
    ],
  };
  const out = filterVisible(tree)!;
  const dflt = out.children!.find((c) => c.id === "default")!;
  const hover = out.children!.find((c) => c.id === "hover")!;
  const other = out.children!.find((c) => c.id === "other")!;
  assert(typeof dflt.__stateGroup === "string", "default has stateGroup");
  assert(dflt.__stateGroup === hover.__stateGroup, "default and hover share group");
  assert(other.__stateGroup === undefined, "uniquely-positioned sibling has no group");
}

function _test_copositioned_only_when_2plus_share(): void {
  const tree: CanonicalNode = {
    id: "root",
    children: [
      { id: "a", absoluteBoundingBox: { x: 0, y: 0, width: 10, height: 10 } },
      { id: "b", absoluteBoundingBox: { x: 1, y: 1, width: 10, height: 10 } },
    ],
  };
  const out = filterVisible(tree)!;
  for (const c of out.children!) {
    assert(c.__stateGroup === undefined, `sibling ${c.id} should not be tagged`);
  }
}

function _test_countNodes(): void {
  const tree: CanonicalNode = {
    id: "r",
    children: [{ id: "a" }, { id: "b", children: [{ id: "b1" }] }],
  };
  assert(countNodes(tree) === 4, "4 nodes total");
}

export function runAll(): void {
  _test_isVisible_default_visible();
  _test_isVisible_hidden();
  _test_filterVisible_drops_hidden_subtree();
  _test_filterVisible_returns_null_for_hidden_root();
  _test_filterVisible_does_not_mutate_input();
  _test_copositioned_siblings_get_state_group();
  _test_copositioned_only_when_2plus_share();
  _test_countNodes();
}
