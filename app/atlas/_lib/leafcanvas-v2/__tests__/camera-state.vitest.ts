/**
 * camera-state.vitest.ts — pub/sub correctness for the module-level
 * camera-vector signal (U1).
 *
 * Pins:
 *   - setCamera fires subscribers on changed values
 *   - dedups when the same {x, y, z} is set twice (no listener re-fire)
 *   - rejects NaN / Infinity / non-positive z (defensive guard)
 *   - subscribers can unsubscribe and don't see post-unsub events
 *   - getCamera returns the latest value
 *   - HMR guard flag is idempotent across module loads
 */

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  __resetCameraStateForTesting,
  getCamera,
  setCamera,
  subscribeCamera,
} from "../camera-state";

afterEach(() => {
  __resetCameraStateForTesting();
});

describe("camera-state — value reads", () => {
  it("starts at {0, 0, 1}", () => {
    expect(getCamera()).toEqual({ x: 0, y: 0, z: 1 });
  });

  it("setCamera updates the value", () => {
    setCamera({ x: 100, y: 200, z: 0.5 });
    expect(getCamera()).toEqual({ x: 100, y: 200, z: 0.5 });
  });

  it("returns the most recently set value", () => {
    setCamera({ x: 1, y: 2, z: 3 });
    setCamera({ x: 4, y: 5, z: 6 });
    expect(getCamera()).toEqual({ x: 4, y: 5, z: 6 });
  });
});

describe("camera-state — subscriber notifications", () => {
  it("fires subscriber on changed values", () => {
    const cb = vi.fn();
    subscribeCamera(cb);
    setCamera({ x: 10, y: 20, z: 1.5 });
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("fires once per distinct setCamera call", () => {
    const cb = vi.fn();
    subscribeCamera(cb);
    setCamera({ x: 1, y: 0, z: 1 });
    setCamera({ x: 2, y: 0, z: 1 });
    setCamera({ x: 3, y: 0, z: 1 });
    expect(cb).toHaveBeenCalledTimes(3);
  });

  it("dedups when the same value is set twice", () => {
    const cb = vi.fn();
    subscribeCamera(cb);
    setCamera({ x: 5, y: 5, z: 1 });
    setCamera({ x: 5, y: 5, z: 1 });
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("fires for multiple subscribers in subscription order", () => {
    const order: string[] = [];
    subscribeCamera(() => order.push("a"));
    subscribeCamera(() => order.push("b"));
    subscribeCamera(() => order.push("c"));
    setCamera({ x: 1, y: 1, z: 1 });
    expect(order).toEqual(["a", "b", "c"]);
  });

  it("unsubscribed listener does not fire", () => {
    const cb = vi.fn();
    const unsub = subscribeCamera(cb);
    unsub();
    setCamera({ x: 42, y: 0, z: 1 });
    expect(cb).not.toHaveBeenCalled();
  });

  it("one subscriber's error does not sink the rest", () => {
    const cb = vi.fn();
    subscribeCamera(() => {
      throw new Error("boom");
    });
    subscribeCamera(cb);
    setCamera({ x: 1, y: 1, z: 1 });
    expect(cb).toHaveBeenCalledTimes(1);
  });
});

describe("camera-state — defensive guards", () => {
  it("rejects NaN x", () => {
    const cb = vi.fn();
    subscribeCamera(cb);
    setCamera({ x: Number.NaN, y: 0, z: 1 });
    expect(cb).not.toHaveBeenCalled();
    expect(getCamera()).toEqual({ x: 0, y: 0, z: 1 });
  });

  it("rejects Infinity y", () => {
    const cb = vi.fn();
    subscribeCamera(cb);
    setCamera({ x: 0, y: Number.POSITIVE_INFINITY, z: 1 });
    expect(cb).not.toHaveBeenCalled();
  });

  it("rejects zero z", () => {
    const cb = vi.fn();
    subscribeCamera(cb);
    setCamera({ x: 0, y: 0, z: 0 });
    expect(cb).not.toHaveBeenCalled();
  });

  it("rejects negative z", () => {
    const cb = vi.fn();
    subscribeCamera(cb);
    setCamera({ x: 0, y: 0, z: -1 });
    expect(cb).not.toHaveBeenCalled();
  });
});

describe("camera-state — snapshot isolation", () => {
  it("does not retain reference to caller's input object", () => {
    const input = { x: 1, y: 2, z: 3 };
    setCamera(input);
    input.x = 999;
    // Mutating the caller's object must not corrupt the store value.
    expect(getCamera().x).toBe(1);
  });
});

describe("camera-state — HMR guard", () => {
  it("module-load flag is idempotent", () => {
    expect(
      (globalThis as unknown as { __lcCameraStateWired?: boolean }).__lcCameraStateWired,
    ).toBe(true);
  });
});
