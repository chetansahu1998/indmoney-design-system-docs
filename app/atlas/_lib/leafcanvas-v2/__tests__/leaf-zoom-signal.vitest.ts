/**
 * leaf-zoom-signal.test.ts — exercise the live/settled split. Vitest
 * test, picked up by vitest.config.ts.
 *
 * Scope: synchronous setLeafZoom behavior under different gesturing
 * states. The settle bridge (canvasGestureTracker → settledListeners
 * sync after 150ms debounce) is timing-dependent against the real
 * default tracker; testing it cleanly would require either replacing
 * the singleton with an injected fake (production has no seam) or
 * using vi.useFakeTimers + waiting for the real setTimeout to fire.
 * Skipped here — the gate behavior is what the U4 fix is about.
 */

import { beforeEach, describe, expect, it } from "vitest";

import { canvasGestureTracker } from "../gesture-tracker";
import {
  getLeafZoomLive,
  getLeafZoomSettled,
  setLeafZoom,
} from "../leaf-zoom-signal";

describe("leaf-zoom-signal", () => {
  beforeEach(() => {
    // Reset both signals + gesture tracker between tests so each runs
    // in isolation. Direct module-level state isn't exposed; round-trip
    // via canvasGestureTracker.reset() (clears its listeners — including
    // the module-load settle bridge) plus setLeafZoom(1) to known value.
    canvasGestureTracker.reset();
    setLeafZoom(1);
  });

  it("updates live signal when settled", () => {
    setLeafZoom(0.5);
    expect(getLeafZoomLive()).toBe(0.5);
  });

  it("updates settled signal when not gesturing", () => {
    setLeafZoom(0.75);
    expect(getLeafZoomSettled()).toBe(0.75);
  });

  it("skips settled signal during a gesture (the headline split)", () => {
    setLeafZoom(1.0); // baseline
    canvasGestureTracker.tick(); // flips gesturingFlag = true
    expect(canvasGestureTracker.isGesturing).toBe(true);
    setLeafZoom(0.4);
    expect(getLeafZoomLive()).toBe(0.4);
    expect(getLeafZoomSettled()).toBe(1.0); // skipped during gesture
  });

  it("rejects NaN", () => {
    setLeafZoom(0.5);
    setLeafZoom(Number.NaN);
    expect(getLeafZoomLive()).toBe(0.5);
  });

  it("rejects zero and negative values", () => {
    setLeafZoom(0.5);
    setLeafZoom(0);
    setLeafZoom(-1);
    expect(getLeafZoomLive()).toBe(0.5);
  });

  it("is idempotent on same value", () => {
    setLeafZoom(0.6);
    const before = getLeafZoomLive();
    setLeafZoom(0.6);
    expect(getLeafZoomLive()).toBe(before);
  });
});
