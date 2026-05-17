/**
 * dev-mode-state.vitest.ts — global dev mode flag pub/sub (U3 + U9).
 */

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  __resetDevModeForTesting,
  getDevMode,
  setDevMode,
  subscribeDevMode,
  toggleDevMode,
} from "../dev-mode-state";

afterEach(() => {
  __resetDevModeForTesting();
});

describe("dev-mode-state", () => {
  it("starts disabled", () => {
    expect(getDevMode()).toBe(false);
  });

  it("setDevMode updates the value", () => {
    setDevMode(true);
    expect(getDevMode()).toBe(true);
  });

  it("toggleDevMode flips the value", () => {
    toggleDevMode();
    expect(getDevMode()).toBe(true);
    toggleDevMode();
    expect(getDevMode()).toBe(false);
  });

  it("fires subscribers on change", () => {
    const cb = vi.fn();
    subscribeDevMode(cb);
    setDevMode(true);
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("dedups when value doesn't change", () => {
    const cb = vi.fn();
    subscribeDevMode(cb);
    setDevMode(true);
    setDevMode(true);
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("unsubscribed listener does not fire", () => {
    const cb = vi.fn();
    const unsub = subscribeDevMode(cb);
    unsub();
    setDevMode(true);
    expect(cb).not.toHaveBeenCalled();
  });

  it("one subscriber's error does not sink the rest", () => {
    const cb = vi.fn();
    subscribeDevMode(() => {
      throw new Error("boom");
    });
    subscribeDevMode(cb);
    setDevMode(true);
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("HMR guard flag is set", () => {
    expect(
      (globalThis as unknown as { __lcDevModeStateWired?: boolean }).__lcDevModeStateWired,
    ).toBe(true);
  });
});
