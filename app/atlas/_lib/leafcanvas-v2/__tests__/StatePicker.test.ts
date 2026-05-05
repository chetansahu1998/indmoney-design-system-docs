/**
 * StatePicker.test.ts — U14.
 *
 * No jsdom + no zustand: we don't render the React tree. Following the
 * `BulkExportPanel.test.ts` convention, we cover:
 *   - the live-store `setActiveState` reducer logic (pure-fn port),
 *   - end-to-end "pick switches the visible variant" flow via the
 *     `collectStateGroups` + `inactiveVariantIDs` helpers from
 *     `visible-filter.ts`.
 *
 * Strict TS — no `// @ts-nocheck`. Runner shape mirrors the other
 * canvas-v2 tests: each `_test_*` is independent + throws; `runAll` is
 * the umbrella entrypoint executed by `tsx`.
 */

import {
  collectStateGroups,
  filterVisible,
  inactiveVariantIDs,
  resolveActiveVariantID,
  type StateGroup,
} from "../visible-filter";
import type { CanonicalNode } from "../types";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

// ─── live-store `setActiveState` harness ────────────────────────────────────
//
// Pure-fn port of the action implementation in `lib/atlas/live-store.ts`.
// We don't import zustand here so the test stays runner-agnostic and
// keeps the React dep graph out of the typecheck.

type ActiveStates = Map<string, Map<string, string>>;

function setActiveState(
  state: ActiveStates,
  frameID: string,
  groupKey: string,
  variantID: string | null,
): ActiveStates {
  const next = new Map(state);
  const inner = new Map(next.get(frameID) ?? []);
  if (variantID === null) {
    inner.delete(groupKey);
    if (inner.size === 0) next.delete(frameID);
    else next.set(frameID, inner);
    return next;
  }
  if (inner.get(groupKey) === variantID) return state; // idempotent
  inner.set(groupKey, variantID);
  next.set(frameID, inner);
  return next;
}

function _test_setActiveState_records_and_idempotent(): void {
  let s: ActiveStates = new Map();
  s = setActiveState(s, "frame-1", "k1", "v2");
  assert(s.get("frame-1")?.get("k1") === "v2", "stored pick");
  // Idempotent — same input returns the SAME reference (cheap subscribe).
  const s2 = setActiveState(s, "frame-1", "k1", "v2");
  assert(s2 === s, "idempotent re-pick returns same map ref");
  // Different variant updates.
  const s3 = setActiveState(s, "frame-1", "k1", "v3");
  assert(s3.get("frame-1")?.get("k1") === "v3", "switched to v3");
  assert(s3 !== s, "fresh map ref on change");
}

function _test_setActiveState_revert_to_default(): void {
  let s: ActiveStates = new Map();
  s = setActiveState(s, "frame-1", "k1", "v2");
  s = setActiveState(s, "frame-1", "k2", "vA");
  // Drop k1 → frame-1 still present with k2.
  s = setActiveState(s, "frame-1", "k1", null);
  assert(s.get("frame-1")?.get("k1") === undefined, "k1 cleared");
  assert(s.get("frame-1")?.get("k2") === "vA", "k2 retained");
  // Drop k2 → frame-1 entry should be pruned (no empty inner Maps).
  s = setActiveState(s, "frame-1", "k2", null);
  assert(s.get("frame-1") === undefined, "frame entry pruned when emptied");
}

function _test_setActiveState_scoped_per_frame_no_crosstalk(): void {
  // Same groupKey across two different frames must not bleed.
  let s: ActiveStates = new Map();
  s = setActiveState(s, "frame-A", "k", "vA1");
  s = setActiveState(s, "frame-B", "k", "vB1");
  assert(s.get("frame-A")?.get("k") === "vA1", "frame-A pick");
  assert(s.get("frame-B")?.get("k") === "vB1", "frame-B pick");
  s = setActiveState(s, "frame-A", "k", "vA2");
  assert(s.get("frame-A")?.get("k") === "vA2", "frame-A re-pick");
  assert(s.get("frame-B")?.get("k") === "vB1", "frame-B unaffected");
}

// ─── End-to-end: pick → inactive set drives variant gating ─────────────────
//
// The picker renders chips per `StateGroup`; the renderer prunes nodes
// in `inactiveVariantIDs(groups, picks)`. These tests pretend to be the
// renderer and assert the contract holds for the test scenarios listed
// in plan §U14.

function buildHappyPathTree(): CanonicalNode {
  return {
    id: "screen-1",
    type: "FRAME",
    absoluteBoundingBox: { x: 0, y: 0, width: 200, height: 200 },
    children: [
      {
        id: "default",
        name: "Default",
        type: "FRAME",
        absoluteBoundingBox: { x: 10, y: 10, width: 50, height: 20 },
      },
      {
        id: "hover",
        name: "Hover",
        type: "FRAME",
        absoluteBoundingBox: { x: 10, y: 10, width: 50, height: 20 },
      },
    ],
  };
}

function _test_e2e_happy_path_pick_switches_visible(): void {
  const tree = filterVisible(buildHappyPathTree())!;
  const groupsByFrame = collectStateGroups(tree);
  const groups = groupsByFrame.get("screen-1")!;
  assert(groups.length === 1, "1 group for happy path");
  const g = groups[0];

  // No picks → default visible (state 1).
  let picks: ActiveStates = new Map();
  let inactive = inactiveVariantIDs(groupsByFrame, picks);
  assert(inactive.has("hover"), "hover hidden by default");
  assert(!inactive.has("default"), "default visible");

  // Click the second chip → state 2 visible, state 1 hidden.
  picks = setActiveState(picks, "screen-1", g.key, "hover");
  inactive = inactiveVariantIDs(groupsByFrame, picks);
  assert(!inactive.has("hover"), "hover now visible");
  assert(inactive.has("default"), "default now hidden");
}

function _test_e2e_three_stacked_three_chips(): void {
  const tree: CanonicalNode = {
    id: "screen-1",
    type: "FRAME",
    absoluteBoundingBox: { x: 0, y: 0, width: 200, height: 200 },
    children: [
      { id: "a", name: "A", absoluteBoundingBox: { x: 0, y: 0, width: 10, height: 10 } },
      { id: "b", name: "B", absoluteBoundingBox: { x: 0, y: 0, width: 10, height: 10 } },
      { id: "c", name: "C", absoluteBoundingBox: { x: 0, y: 0, width: 10, height: 10 } },
    ],
  };
  const groups = collectStateGroups(filterVisible(tree)!).get("screen-1")!;
  assert(groups.length === 1 && groups[0].variants.length === 3, "3-option group");
  const g = groups[0];
  // Cycle: pick C, then pick A — each cycle hides exactly the other two.
  let picks: ActiveStates = new Map();
  picks = setActiveState(picks, "screen-1", g.key, "c");
  let inactive = inactiveVariantIDs(new Map([["screen-1", groups]]), picks);
  assert(inactive.has("a") && inactive.has("b") && !inactive.has("c"), "C visible");
  picks = setActiveState(picks, "screen-1", g.key, "a");
  inactive = inactiveVariantIDs(new Map([["screen-1", groups]]), picks);
  assert(!inactive.has("a") && inactive.has("b") && inactive.has("c"), "A visible");
}

function _test_e2e_no_copositioned_no_picker(): void {
  const tree: CanonicalNode = {
    id: "screen-1",
    type: "FRAME",
    absoluteBoundingBox: { x: 0, y: 0, width: 200, height: 200 },
    children: [
      { id: "a", absoluteBoundingBox: { x: 0, y: 0, width: 10, height: 10 } },
      { id: "b", absoluteBoundingBox: { x: 100, y: 100, width: 10, height: 10 } },
    ],
  };
  const groupsByFrame = collectStateGroups(filterVisible(tree)!);
  assert(groupsByFrame.size === 0, "no groups → picker won't render");
}

function _test_e2e_groupkey_collision_scoped_per_frame(): void {
  // Two frames carrying state groups at IDENTICAL geometric coords. The
  // groupKeys collide on `(x,y,w,h)` but stay scoped because
  // `tagCoPositionedSiblings` namespaces them by the parent frame id.
  // Verify: clicking inside frame-A doesn't shift the variant in frame-B.
  const tree: CanonicalNode = {
    id: "screen-1",
    type: "FRAME",
    absoluteBoundingBox: { x: 0, y: 0, width: 400, height: 400 },
    children: [
      {
        id: "frame-A",
        type: "FRAME",
        absoluteBoundingBox: { x: 0, y: 0, width: 200, height: 200 },
        children: [
          { id: "a-default", absoluteBoundingBox: { x: 10, y: 10, width: 50, height: 20 } },
          { id: "a-hover", absoluteBoundingBox: { x: 10, y: 10, width: 50, height: 20 } },
        ],
      },
      {
        id: "frame-B",
        type: "FRAME",
        absoluteBoundingBox: { x: 200, y: 0, width: 200, height: 200 },
        children: [
          { id: "b-default", absoluteBoundingBox: { x: 10, y: 10, width: 50, height: 20 } },
          { id: "b-hover", absoluteBoundingBox: { x: 10, y: 10, width: 50, height: 20 } },
        ],
      },
    ],
  };
  const groupsByFrame = collectStateGroups(filterVisible(tree)!);
  const aGroups = groupsByFrame.get("frame-A")!;
  const bGroups = groupsByFrame.get("frame-B")!;
  // groupKeys can be EQUAL across the two frames — that's the collision.
  // Scoping must hold even when keys collide.
  // Pick "a-hover" only on frame-A.
  let picks: ActiveStates = new Map();
  picks = setActiveState(picks, "frame-A", aGroups[0].key, "a-hover");
  const inactive = inactiveVariantIDs(groupsByFrame, picks);
  // frame-A: a-hover visible, a-default hidden.
  assert(!inactive.has("a-hover"), "frame-A a-hover visible");
  assert(inactive.has("a-default"), "frame-A a-default hidden");
  // frame-B: untouched → default (b-default) visible, b-hover hidden.
  assert(!inactive.has("b-default"), "frame-B default still visible (no cross-talk)");
  assert(inactive.has("b-hover"), "frame-B hover still hidden");
}

// ─── Active-chip resolution mirrors picker UI ──────────────────────────────

function _test_resolveActiveVariantID_used_by_picker(): void {
  // The picker calls `resolveActiveVariantID` per-group to decide which
  // chip carries `data-active="true"`. Sanity check the reciprocal of
  // the gating tests above.
  const g: StateGroup = {
    key: "k",
    variants: [
      { figmaNodeID: "x", name: "X" },
      { figmaNodeID: "y", name: "Y" },
    ],
    defaultVariantID: "x",
  };
  assert(resolveActiveVariantID(g, undefined) === "x", "no pick → default chip active");
  assert(resolveActiveVariantID(g, "y") === "y", "explicit pick → that chip active");
}

export function runAll(): void {
  _test_setActiveState_records_and_idempotent();
  _test_setActiveState_revert_to_default();
  _test_setActiveState_scoped_per_frame_no_crosstalk();
  _test_e2e_happy_path_pick_switches_visible();
  _test_e2e_three_stacked_three_chips();
  _test_e2e_no_copositioned_no_picker();
  _test_e2e_groupkey_collision_scoped_per_frame();
  _test_resolveActiveVariantID_used_by_picker();
}
