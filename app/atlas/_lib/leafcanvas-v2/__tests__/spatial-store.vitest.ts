/**
 * spatial-store.vitest.ts — pub/sub + lookup correctness for the
 * module-level nodeId→world-rect store (U1).
 *
 * Pins:
 *   - setNodeRect / getNodeRect round-trip
 *   - dedup when the same rect is set twice (no listener re-fire)
 *   - per-screen isolation (invalidating one screen doesn't touch others)
 *   - setNodeRects bulk path notifies once
 *   - invalidateScreen / invalidateAll clear and notify
 *   - subscribers unsubscribe cleanly
 *   - HMR guard flag is idempotent
 */

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  __resetSpatialStoreForTesting,
  getNodeRect,
  getScreenRects,
  invalidateAll,
  invalidateScreen,
  setNodeRect,
  setNodeRects,
  subscribeSpatialStore,
  type NodeWorldRect,
} from "../spatial-store";

afterEach(() => {
  __resetSpatialStoreForTesting();
});

const RECT_A: NodeWorldRect = { x: 100, y: 200, w: 50, h: 60 };
const RECT_B: NodeWorldRect = { x: 300, y: 400, w: 70, h: 80 };

describe("spatial-store — read / write", () => {
  it("returns undefined for unknown screen", () => {
    expect(getNodeRect("missing", "node")).toBeUndefined();
  });

  it("returns undefined for unknown node within a known screen", () => {
    setNodeRect("s1", "n1", RECT_A);
    expect(getNodeRect("s1", "n2")).toBeUndefined();
  });

  it("setNodeRect / getNodeRect round-trip", () => {
    setNodeRect("s1", "n1", RECT_A);
    expect(getNodeRect("s1", "n1")).toEqual(RECT_A);
  });

  it("getScreenRects returns the screen's rect map", () => {
    setNodeRect("s1", "n1", RECT_A);
    setNodeRect("s1", "n2", RECT_B);
    const screen = getScreenRects("s1");
    expect(screen?.size).toBe(2);
    expect(screen?.get("n1")).toEqual(RECT_A);
    expect(screen?.get("n2")).toEqual(RECT_B);
  });

  it("overwriting a rect updates the stored value", () => {
    setNodeRect("s1", "n1", RECT_A);
    const updated: NodeWorldRect = { x: 0, y: 0, w: 1, h: 1 };
    setNodeRect("s1", "n1", updated);
    expect(getNodeRect("s1", "n1")).toEqual(updated);
  });
});

describe("spatial-store — bulk setNodeRects", () => {
  it("writes multiple rects in one call", () => {
    setNodeRects("s1", [
      ["n1", RECT_A],
      ["n2", RECT_B],
    ]);
    expect(getNodeRect("s1", "n1")).toEqual(RECT_A);
    expect(getNodeRect("s1", "n2")).toEqual(RECT_B);
  });

  it("replaces the screen's rect map atomically (prior entries removed)", () => {
    setNodeRect("s1", "n1", RECT_A);
    setNodeRect("s1", "n2", RECT_B);
    setNodeRects("s1", [["n3", RECT_A]]);
    expect(getNodeRect("s1", "n1")).toBeUndefined();
    expect(getNodeRect("s1", "n2")).toBeUndefined();
    expect(getNodeRect("s1", "n3")).toEqual(RECT_A);
  });

  it("notifies subscribers once for the whole bulk write", () => {
    const cb = vi.fn();
    subscribeSpatialStore(cb);
    setNodeRects("s1", [
      ["n1", RECT_A],
      ["n2", RECT_B],
      ["n3", RECT_A],
    ]);
    expect(cb).toHaveBeenCalledTimes(1);
  });
});

describe("spatial-store — subscriber notifications", () => {
  it("setNodeRect fires subscribers on changed values", () => {
    const cb = vi.fn();
    subscribeSpatialStore(cb);
    setNodeRect("s1", "n1", RECT_A);
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("dedups when the same rect is set twice", () => {
    const cb = vi.fn();
    subscribeSpatialStore(cb);
    setNodeRect("s1", "n1", RECT_A);
    setNodeRect("s1", "n1", { ...RECT_A });
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("fires once per distinct rect change", () => {
    const cb = vi.fn();
    subscribeSpatialStore(cb);
    setNodeRect("s1", "n1", RECT_A);
    setNodeRect("s1", "n1", RECT_B);
    expect(cb).toHaveBeenCalledTimes(2);
  });

  it("unsubscribed listener does not fire", () => {
    const cb = vi.fn();
    const unsub = subscribeSpatialStore(cb);
    unsub();
    setNodeRect("s1", "n1", RECT_A);
    expect(cb).not.toHaveBeenCalled();
  });
});

describe("spatial-store — invalidation", () => {
  it("invalidateScreen clears one screen's rects, leaves others", () => {
    setNodeRect("s1", "n1", RECT_A);
    setNodeRect("s2", "n1", RECT_B);
    invalidateScreen("s1");
    expect(getNodeRect("s1", "n1")).toBeUndefined();
    expect(getNodeRect("s2", "n1")).toEqual(RECT_B);
  });

  it("invalidateScreen notifies subscribers when the screen existed", () => {
    setNodeRect("s1", "n1", RECT_A);
    const cb = vi.fn();
    subscribeSpatialStore(cb);
    invalidateScreen("s1");
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("invalidateScreen for unknown screen is a no-op (no listener fire)", () => {
    const cb = vi.fn();
    subscribeSpatialStore(cb);
    invalidateScreen("ghost");
    expect(cb).not.toHaveBeenCalled();
  });

  it("invalidateAll wipes every screen and notifies once", () => {
    setNodeRect("s1", "n1", RECT_A);
    setNodeRect("s2", "n1", RECT_B);
    const cb = vi.fn();
    subscribeSpatialStore(cb);
    invalidateAll();
    expect(getNodeRect("s1", "n1")).toBeUndefined();
    expect(getNodeRect("s2", "n1")).toBeUndefined();
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("invalidateAll on empty store is a no-op", () => {
    const cb = vi.fn();
    subscribeSpatialStore(cb);
    invalidateAll();
    expect(cb).not.toHaveBeenCalled();
  });
});

describe("spatial-store — snapshot isolation", () => {
  it("stores a copy, not a reference (caller mutation doesn't leak)", () => {
    const input: NodeWorldRect = { x: 1, y: 2, w: 3, h: 4 };
    setNodeRect("s1", "n1", input);
    input.x = 999;
    expect(getNodeRect("s1", "n1")?.x).toBe(1);
  });
});

describe("spatial-store — HMR guard", () => {
  it("module-load flag is idempotent", () => {
    expect(
      (globalThis as unknown as { __lcSpatialStoreWired?: boolean }).__lcSpatialStoreWired,
    ).toBe(true);
  });
});
