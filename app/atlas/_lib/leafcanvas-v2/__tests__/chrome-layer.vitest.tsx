/**
 * chrome-layer.vitest.tsx — mount + plumbing pins for the screen-space
 * chrome layer (U1).
 *
 * What U1 ships and what these tests assert:
 *   - The component renders an <svg class="leafcv2-chrome-layer">
 *     with `data-leaf-id` and `aria-hidden`.
 *   - The pre-allocated `<g>` group skeleton exists with all the
 *     expected CHROME_GROUP_IDS. Subsequent units (U4-U10) write into
 *     these groups; if any group goes missing, those units silently
 *     skip their paint.
 *   - Mounting subscribes to camera-state and spatial-store.
 *     Unmounting unsubscribes — no listener leak across remounts.
 *   - The rAF callback fires after a subscribed signal changes.
 *
 * What U1 does NOT yet ship (and therefore is NOT tested here):
 *   - Actual paint logic (selection rings, hover outlines, etc.).
 *     Those land in U4/U5/U6/U10.
 *   - Visible-parity with MeasurementOverlay. That's U5's gate.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { act } from "react";

// React 19 + happy-dom: `act` warns if this flag isn't set. The flag
// just opts into act-environment behavior; we always wrap render /
// unmount in act() so the warning is silenceable safely.
(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

/** Drain queued microtasks so rAF callbacks scheduled via queueMicrotask fire. */
async function drainMicrotasks(): Promise<void> {
  await new Promise<void>((resolve) => queueMicrotask(resolve));
}

import {
  CHROME_GROUP_IDS,
  ChromeLayer,
  type ChromeGroupID,
} from "../chrome-layer";
import {
  __resetCameraStateForTesting,
  setCamera,
} from "../camera-state";
import {
  __resetSpatialStoreForTesting,
  setNodeRect,
} from "../spatial-store";

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(leafID: string): HTMLDivElement {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(<ChromeLayer leafID={leafID} />);
  });
  return container;
}

afterEach(() => {
  if (root) {
    act(() => root!.unmount());
    root = null;
  }
  if (container) {
    container.remove();
    container = null;
  }
  __resetCameraStateForTesting();
  __resetSpatialStoreForTesting();
});

beforeEach(() => {
  // happy-dom doesn't drive requestAnimationFrame by default; install
  // a synchronous shim so paint callbacks fire deterministically in
  // tests. The chrome layer doesn't care whether rAF is sync or
  // async — it just expects the callback to run "eventually".
  vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
    queueMicrotask(() => cb(performance.now()));
    return 1;
  });
  vi.stubGlobal("cancelAnimationFrame", (_id: number) => {
    /* no-op for the sync shim — the microtask already fired */
  });
});

describe("ChromeLayer — mount + DOM shape", () => {
  it("renders an svg with the chrome-layer class", () => {
    const root = mount("leaf-1");
    const svg = root.querySelector("svg.leafcv2-chrome-layer");
    expect(svg).not.toBeNull();
  });

  it("carries data-leaf-id from props", () => {
    const root = mount("leaf-abc");
    const svg = root.querySelector("svg.leafcv2-chrome-layer");
    expect(svg?.getAttribute("data-leaf-id")).toBe("leaf-abc");
  });

  it("is aria-hidden (purely visual chrome, no a11y role)", () => {
    const root = mount("leaf-1");
    const svg = root.querySelector("svg.leafcv2-chrome-layer");
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
  });

  it("contains every pre-allocated group with correct id", () => {
    const root = mount("leaf-1");
    for (const id of CHROME_GROUP_IDS) {
      const g = root.querySelector(`g#${id}`);
      expect(g, `expected <g id="${id}"> to exist`).not.toBeNull();
      expect(g?.getAttribute("data-group")).toBe(id);
    }
  });

  it("group skeleton has CHROME_GROUP_IDS.length children inside the svg", () => {
    const root = mount("leaf-1");
    const svg = root.querySelector("svg.leafcv2-chrome-layer");
    expect(svg?.children.length).toBe(CHROME_GROUP_IDS.length);
  });
});

describe("ChromeLayer — CHROME_GROUP_IDS contract", () => {
  it("exposes the full set of groups U4-U10 will paint into", () => {
    const expected: ChromeGroupID[] = [
      "chrome-selection",
      "chrome-hover",
      "chrome-padding",
      "chrome-gap",
      "chrome-distance",
      "chrome-marquee",
      "chrome-breadcrumb",
      "chrome-dimension",
    ];
    expect([...CHROME_GROUP_IDS]).toEqual(expected);
  });
});

describe("ChromeLayer — subscriptions wire up on mount", () => {
  it("subscribes to camera-state (camera change triggers rAF schedule)", async () => {
    const rafSpy = vi.fn();
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
      rafSpy();
      queueMicrotask(() => cb(performance.now()));
      return 1;
    });
    mount("leaf-1");
    // Drain the initial mount paint so rafPendingRef resets to 0 —
    // otherwise the next schedulePaint correctly bails (debounce
    // working as designed) and the test would underestimate the
    // subscription path's behavior.
    await drainMicrotasks();
    const initialCount = rafSpy.mock.calls.length;
    expect(initialCount).toBeGreaterThanOrEqual(1);

    // Camera change should schedule another paint via the subscriber.
    setCamera({ x: 50, y: 50, z: 1.5 });
    expect(rafSpy.mock.calls.length).toBeGreaterThan(initialCount);
  });

  it("subscribes to spatial-store (rect change triggers rAF schedule)", async () => {
    const rafSpy = vi.fn();
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
      rafSpy();
      queueMicrotask(() => cb(performance.now()));
      return 1;
    });
    mount("leaf-1");
    await drainMicrotasks();
    const initialCount = rafSpy.mock.calls.length;

    setNodeRect("screen-1", "node-1", { x: 0, y: 0, w: 10, h: 10 });
    expect(rafSpy.mock.calls.length).toBeGreaterThan(initialCount);
  });

  it("unsubscribes on unmount (post-unmount signal changes do not schedule rAF)", async () => {
    mount("leaf-1");
    await drainMicrotasks();
    act(() => {
      root!.unmount();
    });
    root = null;

    // Replace rAF mock AFTER unmount so we can count post-unmount calls.
    const rafSpyAfter = vi.fn();
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
      rafSpyAfter();
      queueMicrotask(() => cb(performance.now()));
      return 1;
    });
    setCamera({ x: 1, y: 1, z: 1 });
    setNodeRect("s", "n", { x: 0, y: 0, w: 1, h: 1 });
    expect(rafSpyAfter).not.toHaveBeenCalled();
  });
});
