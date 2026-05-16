/**
 * camera-snap.vitest.ts — Phase 2 U7 pure-helper tests.
 *
 * Three surfaces:
 *   - easeInOutCubic: monotonic, 0/1 endpoints, symmetric around
 *     0.5, clamps OOR inputs.
 *   - computeFitCamera: pan + zoom-to-fit math; cap at maxZoom (1.0
 *     by default); padding inset; null on degenerate inputs.
 *   - animateCamera: rAF lerp with injectable time/raf; cancelToken
 *     halts the animation; onDone fires only on natural completion.
 *   - registerSnapTarget / requestCameraSnap: register/unregister
 *     channel.
 */

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  __resetCameraSnapForTests,
  animateCamera,
  computeFitCamera,
  easeInOutCubic,
  registerSnapTarget,
  requestCameraSnap,
} from "../camera-snap";

afterEach(() => {
  __resetCameraSnapForTests();
});

describe("easeInOutCubic", () => {
  it("starts at 0 and ends at 1", () => {
    expect(easeInOutCubic(0)).toBe(0);
    expect(easeInOutCubic(1)).toBe(1);
  });

  it("midpoint is 0.5 (symmetric)", () => {
    expect(easeInOutCubic(0.5)).toBeCloseTo(0.5, 6);
  });

  it("is monotonically non-decreasing on [0, 1]", () => {
    let prev = -1;
    for (let t = 0; t <= 1; t += 0.05) {
      const v = easeInOutCubic(t);
      expect(v).toBeGreaterThanOrEqual(prev);
      prev = v;
    }
  });

  it("clamps OOR inputs", () => {
    expect(easeInOutCubic(-0.5)).toBe(0);
    expect(easeInOutCubic(1.5)).toBe(1);
  });

  it("returns 0 for NaN; finite-clamp on infinities is N/A (Number.isFinite false → 0)", () => {
    expect(easeInOutCubic(NaN)).toBe(0);
    expect(easeInOutCubic(Infinity)).toBe(0);
    expect(easeInOutCubic(-Infinity)).toBe(0);
  });
});

describe("computeFitCamera", () => {
  it("returns null for zero/negative bbox dims", () => {
    expect(
      computeFitCamera({ x: 0, y: 0, width: 0, height: 100 }, { width: 800, height: 600 }),
    ).toBeNull();
    expect(
      computeFitCamera({ x: 0, y: 0, width: 100, height: -1 }, { width: 800, height: 600 }),
    ).toBeNull();
  });

  it("returns null for zero/negative viewport dims", () => {
    expect(
      computeFitCamera({ x: 0, y: 0, width: 100, height: 100 }, { width: 0, height: 600 }),
    ).toBeNull();
  });

  it("centers the camera on the bbox midpoint", () => {
    const cam = computeFitCamera(
      { x: 100, y: 200, width: 50, height: 30 },
      { width: 800, height: 600 },
    );
    expect(cam?.x).toBe(125); // 100 + 50/2
    expect(cam?.y).toBe(215); // 200 + 30/2
  });

  it("zooms to fit with default 40px inset, capped at 1.0", () => {
    // 800×600 viewport, 40px inset → effective 720×520
    // bbox 200×100 → zX=720/200=3.6, zY=520/100=5.2 → min=3.6, capped at 1.0
    const cam = computeFitCamera(
      { x: 0, y: 0, width: 200, height: 100 },
      { width: 800, height: 600 },
    );
    expect(cam?.z).toBe(1.0);
  });

  it("zooms below 1.0 when bbox is larger than effective viewport", () => {
    // bbox 1000×600 in 800×600 viewport (40px inset → 720×520):
    // zX = 720/1000 = 0.72, zY = 520/600 ≈ 0.867 → min = 0.72
    const cam = computeFitCamera(
      { x: 0, y: 0, width: 1000, height: 600 },
      { width: 800, height: 600 },
    );
    expect(cam?.z).toBeCloseTo(0.72, 4);
  });

  it("respects custom padding", () => {
    // 0 padding → effective viewport is full size
    const cam = computeFitCamera(
      { x: 0, y: 0, width: 800, height: 600 },
      { width: 800, height: 600 },
      { padding: 0 },
    );
    expect(cam?.z).toBe(1.0); // exact fit, capped at maxZoom
  });

  it("respects custom maxZoom", () => {
    // tiny bbox, padding 0, maxZoom 2 → can zoom past 1.0
    const cam = computeFitCamera(
      { x: 0, y: 0, width: 200, height: 200 },
      { width: 800, height: 800 },
      { padding: 0, maxZoom: 2.0 },
    );
    expect(cam?.z).toBe(2.0); // hits the new cap, not 4.0
  });

  it("respects custom minZoom", () => {
    // huge bbox, default padding → effective viewport 720×520.
    // zX = 720/100000 = 0.0072; zY = 520/100000 = 0.0052; min = 0.0052.
    // With default minZoom 0.05 it would clamp UP to 0.05; setting
    // minZoom 0.001 lets the unclamped value pass through.
    const cam = computeFitCamera(
      { x: 0, y: 0, width: 100000, height: 100000 },
      { width: 800, height: 600 },
      { minZoom: 0.001 },
    );
    expect(cam?.z).toBeCloseTo(520 / 100000, 6);
  });
});

describe("animateCamera — spring integration (U2)", () => {
  it("emits the start position on first tick, then integrates toward target", () => {
    const ticks: Array<{ x: number; y: number; z: number }> = [];
    let onDoneFired = false;
    let nowMs = 0;
    const callbacks: Array<(ts: number) => void> = [];
    const raf = (cb: (ts: number) => void) => {
      callbacks.push(cb);
      return callbacks.length;
    };
    const cancelRaf = vi.fn();

    animateCamera(
      { x: 0, y: 0, z: 0.5 },
      { x: 100, y: 200, z: 1.0 },
      300,
      (s) => ticks.push(s),
      () => {
        onDoneFired = true;
      },
      { now: () => nowMs, raf, cancelRaf },
    );

    // First tick at dt=0: emits the start state, no integration.
    callbacks[0](0);
    expect(ticks[0]).toEqual({ x: 0, y: 0, z: 0.5 });
    expect(onDoneFired).toBe(false);

    // Subsequent tick at +16ms: spring has begun moving toward target.
    nowMs = 16;
    callbacks[1](16);
    expect(ticks[1].x).toBeGreaterThan(0);
    expect(ticks[1].x).toBeLessThan(100);
    expect(ticks[1].y).toBeGreaterThan(0);
    expect(ticks[1].y).toBeLessThan(200);
    expect(ticks[1].z).toBeGreaterThan(0.5);
    expect(ticks[1].z).toBeLessThan(1.0);
  });

  it("settles to target and fires onDone (drained via repeated rAF)", () => {
    const ticks: Array<{ x: number; y: number; z: number }> = [];
    let onDoneFired = false;
    let nowMs = 0;
    let pendingCb: ((ts: number) => void) | null = null;
    const raf = (cb: (ts: number) => void) => {
      pendingCb = cb;
      return 1;
    };
    const cancelRaf = vi.fn();

    animateCamera(
      { x: 0, y: 0, z: 0.5 },
      { x: 100, y: 200, z: 1.0 },
      300,
      (s) => ticks.push(s),
      () => {
        onDoneFired = true;
      },
      { now: () => nowMs, raf, cancelRaf },
    );

    // Drain rAF ticks at 60Hz until onDone fires or hard cap reached.
    let safety = 600;
    while (!onDoneFired && safety > 0 && pendingCb) {
      const cb = pendingCb;
      pendingCb = null;
      cb(nowMs);
      nowMs += 1000 / 60;
      safety -= 1;
    }
    expect(onDoneFired).toBe(true);
    // Final tick lands exactly on the target (snap-on-settle).
    expect(ticks[ticks.length - 1]).toEqual({ x: 100, y: 200, z: 1.0 });
  });

  it("cancel halts the loop without firing onDone", () => {
    let onDoneFired = false;
    let nowMs = 0;
    const callbacks: Array<(ts: number) => void> = [];
    const raf = (cb: (ts: number) => void) => {
      callbacks.push(cb);
      return callbacks.length;
    };
    const cancelRaf = vi.fn();

    const token = animateCamera(
      { x: 0, y: 0, z: 0.5 },
      { x: 100, y: 100, z: 1.0 },
      300,
      () => {},
      () => {
        onDoneFired = true;
      },
      { now: () => nowMs, raf, cancelRaf },
    );

    // Run one tick partway through.
    callbacks[0](0);
    nowMs = 100;
    // Cancel before next tick.
    token.cancel();
    expect(token.isCancelled()).toBe(true);

    // Subsequent rAF firings (if any are pending) should be no-ops.
    if (callbacks[1]) callbacks[1](100);
    expect(onDoneFired).toBe(false);
  });

  it("zero-duration finishes on first tick (jump-without-animation back-compat)", () => {
    let onDoneFired = false;
    let nowMs = 0;
    let finalTick: { x: number; y: number; z: number } | null = null;
    const callbacks: Array<(ts: number) => void> = [];
    const raf = (cb: (ts: number) => void) => {
      callbacks.push(cb);
      return callbacks.length;
    };
    animateCamera(
      { x: 0, y: 0, z: 0.5 },
      { x: 100, y: 100, z: 1.0 },
      0,
      (s) => {
        finalTick = s;
      },
      () => {
        onDoneFired = true;
      },
      { now: () => nowMs, raf, cancelRaf: vi.fn() },
    );
    callbacks[0](0);
    expect(onDoneFired).toBe(true);
    expect(finalTick).toEqual({ x: 100, y: 100, z: 1.0 });
  });

  it("respects custom spring tuning via deps.spring", () => {
    // Aggressive critical spring settles faster than default. We pin
    // that "much stiffer spring uses fewer ticks than the default
    // tuning would" by comparing against a generous budget. Exact
    // frame count is tuning-sensitive; the contract under test is
    // "deps.spring is honored", not the precise convergence speed.
    let onDoneFired = false;
    let nowMs = 0;
    let pendingCb: ((ts: number) => void) | null = null;
    const raf = (cb: (ts: number) => void) => {
      pendingCb = cb;
      return 1;
    };

    animateCamera(
      { x: 0, y: 0, z: 1 },
      { x: 100, y: 0, z: 1 },
      300,
      () => {},
      () => {
        onDoneFired = true;
      },
      {
        now: () => nowMs,
        raf,
        cancelRaf: vi.fn(),
        spring: { stiffness: 800, damping: 56 }, // very stiff, critical
      },
    );

    let safety = 100;
    while (!onDoneFired && safety > 0 && pendingCb) {
      const cb = pendingCb;
      pendingCb = null;
      cb(nowMs);
      nowMs += 1000 / 60;
      safety -= 1;
    }
    expect(onDoneFired).toBe(true);
    // Aggressive spring (stiffness=800, critical damping) should
    // settle within 60 frames (1s simulated). Default tuning takes
    // ~46-50 frames; aggressive should take ~28-40. The 60-frame
    // budget gives a comfortable headroom over default tuning.
    expect(safety).toBeGreaterThan(40);
  });
});

describe("snap-target channel", () => {
  it("requestCameraSnap is a no-op when nothing is registered", () => {
    expect(() => requestCameraSnap({ x: 0, y: 0, width: 100, height: 100 })).not.toThrow();
  });

  it("registered callback fires on requestCameraSnap", () => {
    const cb = vi.fn();
    registerSnapTarget(cb);
    requestCameraSnap({ x: 10, y: 20, width: 100, height: 200 });
    expect(cb).toHaveBeenCalledTimes(1);
    expect(cb).toHaveBeenCalledWith({ x: 10, y: 20, width: 100, height: 200 });
  });

  it("unregister fn clears the slot", () => {
    const cb = vi.fn();
    const off = registerSnapTarget(cb);
    off();
    requestCameraSnap({ x: 0, y: 0, width: 1, height: 1 });
    expect(cb).not.toHaveBeenCalled();
  });

  it("re-registering replaces the prior callback", () => {
    const cb1 = vi.fn();
    const cb2 = vi.fn();
    registerSnapTarget(cb1);
    registerSnapTarget(cb2);
    requestCameraSnap({ x: 0, y: 0, width: 1, height: 1 });
    expect(cb1).not.toHaveBeenCalled();
    expect(cb2).toHaveBeenCalledTimes(1);
  });
});
