/**
 * camera-actions.vitest.ts — registry slot tests (U3).
 *
 * Mirrors the registerSnapTarget pattern from camera-snap.ts. The
 * registry is a single slot — re-registering replaces, unregister
 * clears. Tests pin those behaviors.
 */

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  __resetCameraActionsForTesting,
  getCameraActions,
  registerCameraActions,
  type CameraActions,
} from "../camera-actions";

afterEach(() => {
  __resetCameraActionsForTesting();
});

function makeActions(): CameraActions {
  return {
    fitAll: vi.fn(),
    fitSelection: vi.fn(),
    zoom100: vi.fn(),
    zoomIn: vi.fn(),
    zoomOut: vi.fn(),
    nextNamedFrame: vi.fn(),
    prevNamedFrame: vi.fn(),
    listNamedFrames: vi.fn(() => []),
    jumpToFrame: vi.fn(),
  };
}

describe("camera-actions registry", () => {
  it("starts empty (getCameraActions returns null)", () => {
    expect(getCameraActions()).toBeNull();
  });

  it("registerCameraActions stores the actions", () => {
    const actions = makeActions();
    registerCameraActions(actions);
    expect(getCameraActions()).toBe(actions);
  });

  it("re-registering replaces the prior slot", () => {
    const first = makeActions();
    const second = makeActions();
    registerCameraActions(first);
    registerCameraActions(second);
    expect(getCameraActions()).toBe(second);
  });

  it("unregister fn clears the slot (when the same actions object is current)", () => {
    const actions = makeActions();
    const off = registerCameraActions(actions);
    off();
    expect(getCameraActions()).toBeNull();
  });

  it("stale unregister (after replacement) is a no-op", () => {
    const first = makeActions();
    const second = makeActions();
    const offFirst = registerCameraActions(first);
    registerCameraActions(second);
    offFirst(); // calling unregister for `first` AFTER `second` registered
    // should not wipe `second`.
    expect(getCameraActions()).toBe(second);
  });

  it("HMR guard flag is set", () => {
    expect(
      (globalThis as unknown as { __lcCameraActionsWired?: boolean }).__lcCameraActionsWired,
    ).toBe(true);
  });
});
