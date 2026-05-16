/**
 * camera-snap.ts — camera animation helpers.
 *
 * Three pure helpers + a module-level register/request pair so any
 * component can ask the active leaf canvas to scroll-and-zoom to a
 * target bbox without prop-drilling a callback.
 *
 * Design choices:
 *
 *   - **Integrator** (U2 update): critically-damped spring physics
 *     via `spring.ts`. Replaces the original easeInOutCubic lerp.
 *     Stiffness=180, damping=26 (~1% overshoot, settles ~250-300ms
 *     for typical motions). Tunable per side-by-side with Figma —
 *     constants live in `spring.ts` `DEFAULT_SPRING`. The
 *     easeInOutCubic helper remains exported for any future caller
 *     that wants pure cubic, but is no longer used internally.
 *   - **Duration param**: `animateCamera` still accepts `durationMs`
 *     in its signature for back-compat with existing callers, but
 *     spring physics decides termination via settling thresholds.
 *     A durationMs=0 still snaps immediately (instant onTick + onDone)
 *     to preserve that behavior.
 *   - **Padding**: 40px inset on each side (tldraw `inset` default).
 *   - **Zoom math**: `min((vp.w - 2*pad) / bbox.w, (vp.h - 2*pad) /
 *     bbox.h)` clamped to `[MIN_ZOOM, maxZoom]`. Excalidraw's
 *     `actionCanvas.tsx` formula.
 *   - **Max zoom**: cap at 1.0 (100%) so a tiny atomic doesn't fill
 *     the screen — Excalidraw's `fitToViewport: false` pattern.
 *   - **Interrupt on input**: any pointer/wheel event during the
 *     animation calls cancelToken.cancel() and the camera stays at
 *     the last spring-integrated position. No snap-back.
 *
 * The user-decision (2026-05-09) gates the trigger surface:
 * keyboard / inspector-button only, never on canvas click. Clicks
 * shouldn't auto-snap because adjacent atomics get clicked
 * frequently and the disorientation isn't worth it.
 */

import {
  cameraSpringSettled,
  DEFAULT_SPRING,
  springStep,
  type SpringParams,
} from "./spring";

export interface SnapBBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface ViewportSize {
  width: number;
  height: number;
}

export interface CameraState {
  x: number;
  y: number;
  z: number;
}

export interface ComputeFitOptions {
  /** px of inset reserved on each side. Default 40 (tldraw default). */
  padding?: number;
  /** Maximum zoom to apply during snap. Default 1.0 — don't zoom past 100%. */
  maxZoom?: number;
  /** Floor on the computed zoom; default 0.05 to match leafcanvas's
   *  practical minimum. */
  minZoom?: number;
}

const DEFAULT_PADDING = 40;
const DEFAULT_MAX_ZOOM = 1.0;
const DEFAULT_MIN_ZOOM = 0.05;
export const SNAP_DURATION_MS = 320;

/**
 * easeInOutCubic — tldraw's default. Pure JS:
 *   t < 0.5 ? 4t³ : (t-1)(2t-2)² + 1
 *
 * Returns 0..1 for t in 0..1. Symmetric around t=0.5 (slow start,
 * fast middle, slow end). Outside the 0..1 range the function clamps
 * via Math.min/max so over-shoot inputs don't escape.
 */
export function easeInOutCubic(t: number): number {
  if (!Number.isFinite(t)) return 0;
  const tt = Math.max(0, Math.min(1, t));
  if (tt < 0.5) return 4 * tt * tt * tt;
  const u = tt - 1;
  return u * (2 * tt - 2) * (2 * tt - 2) + 1;
}

/**
 * computeFitCamera — given a bbox and viewport, returns the {x, y, z}
 * the camera should land on so the bbox fills the viewport (minus
 * inset) at zoom capped to maxZoom.
 *
 * Returns null when bbox dimensions are zero or negative — caller
 * treats as a no-op snap.
 */
export function computeFitCamera(
  bbox: SnapBBox,
  viewport: ViewportSize,
  opts?: ComputeFitOptions,
): CameraState | null {
  const padding = opts?.padding ?? DEFAULT_PADDING;
  const maxZoom = opts?.maxZoom ?? DEFAULT_MAX_ZOOM;
  const minZoom = opts?.minZoom ?? DEFAULT_MIN_ZOOM;

  if (bbox.width <= 0 || bbox.height <= 0) return null;
  if (viewport.width <= 0 || viewport.height <= 0) return null;

  const effectiveW = Math.max(1, viewport.width - 2 * padding);
  const effectiveH = Math.max(1, viewport.height - 2 * padding);

  const zX = effectiveW / bbox.width;
  const zY = effectiveH / bbox.height;
  let z = Math.min(zX, zY);
  if (z < minZoom) z = minZoom;
  if (z > maxZoom) z = maxZoom;

  // Camera (x, y) is the scene-space point at viewport center. The
  // leaf-canvas transform writes `transform: scale(z) translate(-x,
  // -y)` to .lc-world, so x/y here is scene coords aligned with that
  // contract.
  const x = bbox.x + bbox.width / 2;
  const y = bbox.y + bbox.height / 2;

  return { x, y, z };
}

export interface CancelToken {
  cancel: () => void;
  isCancelled: () => boolean;
}

/**
 * animateCamera — rAF-driven spring integration from `from` to `to`.
 * On each frame, advances three independent springs (x, y, z) toward
 * the target via semi-implicit Euler (see spring.ts) and calls
 * `onTick(state)` so the caller can write the camera ref + flush the
 * DOM transform. Calls `onDone()` once all three axes settle (NOT on
 * cancellation).
 *
 * The `durationMs` parameter is retained in the signature for
 * back-compat with the original easeInOutCubic API. A value of 0
 * preserves the original "snap instantly" semantic: onTick fires once
 * with the target, then onDone fires immediately. Any positive value
 * is treated as a hint — spring physics decides actual termination
 * based on settling thresholds (see spring.ts `springSettled`).
 *
 * Returns a CancelToken; cancel() halts the animation at the next
 * frame boundary, leaving the camera at the last integrated position.
 *
 * Time provider is injectable for tests (defaults to performance.now
 * + rAF). Tests can drive the integrator deterministically by
 * supplying their own now/raf pair.
 */
export function animateCamera(
  from: CameraState,
  to: CameraState,
  durationMs: number,
  onTick: (state: CameraState) => void,
  onDone?: () => void,
  deps?: {
    now?: () => number;
    raf?: (cb: (ts: number) => void) => unknown;
    cancelRaf?: (handle: unknown) => void;
    /** Override spring tuning (test-only — defaults to DEFAULT_SPRING). */
    spring?: SpringParams;
  },
): CancelToken {
  const now = deps?.now ?? (() => performance.now());
  const raf =
    deps?.raf ??
    ((cb: (ts: number) => void) => globalThis.requestAnimationFrame(cb));
  const cancelRaf =
    deps?.cancelRaf ??
    ((handle: unknown) =>
      globalThis.cancelAnimationFrame(handle as number));

  let cancelled = false;
  let handle: unknown = null;

  // durationMs=0 short-circuit preserves the original API contract:
  // the test at camera-snap.vitest.ts pins this behavior, and callers
  // (existing snap-to-fit, future U3 keymap actions) rely on it for
  // "jump without animation" cases.
  if (durationMs === 0) {
    handle = raf(() => {
      if (cancelled) return;
      onTick({ x: to.x, y: to.y, z: to.z });
      onDone?.();
    });
    return {
      cancel: () => {
        cancelled = true;
        if (handle != null) cancelRaf(handle);
      },
      isCancelled: () => cancelled,
    };
  }

  const springParams = deps?.spring ?? DEFAULT_SPRING;

  // Three independent springs (no cross-coupling between axes).
  // Initial velocity is 0 — callers pass start state via `from`, not
  // an in-flight velocity. (If U4's selection-implies-camera ever
  // needs hand-off-with-velocity, the deps interface can grow an
  // `initialVelocity` field.)
  let stateX = { value: from.x, velocity: 0 };
  let stateY = { value: from.y, velocity: 0 };
  let stateZ = { value: from.z, velocity: 0 };

  // Track previous tick time to compute dt per frame. Initialize to
  // `now()` so the first tick has dt=0 (no integration yet — just emit
  // the start state). Subsequent ticks integrate against real elapsed.
  let prevTime = now();
  // Emit the starting position synchronously-ish on the first rAF
  // so consumers see {value === from} before the spring takes over.
  let isFirstTick = true;

  const tick = (): void => {
    if (cancelled) return;

    const t = now();
    const dtSec = Math.max(0, (t - prevTime) / 1000);
    prevTime = t;

    if (isFirstTick) {
      // First tick: emit the start position, accumulate no integration
      // step. Matches the prior lerp behavior where t=0 produced `from`.
      isFirstTick = false;
      onTick({ x: stateX.value, y: stateY.value, z: stateZ.value });
      handle = raf(tick);
      return;
    }

    // Integrate each axis independently.
    stateX = springStep(stateX, to.x, springParams, dtSec);
    stateY = springStep(stateY, to.y, springParams, dtSec);
    stateZ = springStep(stateZ, to.z, springParams, dtSec);

    onTick({ x: stateX.value, y: stateY.value, z: stateZ.value });

    // Use a tighter precision for the zoom axis: z is a multiplier
    // (typical range 0.18-2.0), not pixels, so a "0.5 unit" velocity
    // threshold from the x/y defaults would mean the zoom can settle
    // mid-flight at a visibly-wrong scale. 0.001 unit / 0.01 unit/s
    // is the camera-state precision for z.
    if (
      cameraSpringSettled({ x: stateX, y: stateY, z: stateZ }, to, springParams, {
        value: 0.001,
        velocity: 0.01,
      })
    ) {
      // Snap to exact target on settle so the final tick is precise
      // (spring asymptotes; without this the camera stops at
      // target ± precision rather than at target).
      onTick({ x: to.x, y: to.y, z: to.z });
      onDone?.();
      return;
    }

    handle = raf(tick);
  };

  handle = raf(tick);

  return {
    cancel: () => {
      cancelled = true;
      if (handle != null) cancelRaf(handle);
    },
    isCancelled: () => cancelled,
  };
}

// ─── Module-level snap-request channel ─────────────────────────────────
//
// LeafCanvas registers its snap implementation here on mount; outside
// callers (AtlasShellInner Shift+2 handler, AtomicChildInspector
// "Scroll into view" button) push a bbox via requestCameraSnap. The
// channel is single-target — only the active leaf canvas is mounted at
// any given time, so a single registered callback is sufficient.
//
// HMR-guard with globalThis.__lcCameraSnapWired so re-evaluations don't
// leak the registered slot.

declare global {
  // eslint-disable-next-line no-var
  var __lcCameraSnapWired: boolean | undefined;
}
if (!globalThis.__lcCameraSnapWired) {
  globalThis.__lcCameraSnapWired = true;
}

let registeredSnap: ((bbox: SnapBBox) => void) | null = null;

/**
 * Producer (LeafCanvas) calls this on mount. Returns an unregister
 * fn for the unmount cleanup. Idempotent — re-registering replaces
 * the prior callback, no listener accumulation.
 */
export function registerSnapTarget(snap: (bbox: SnapBBox) => void): () => void {
  registeredSnap = snap;
  return () => {
    if (registeredSnap === snap) registeredSnap = null;
  };
}

/**
 * Consumer (Shift+2 handler / inspector button) calls this with the
 * scene-coords bbox of the node to snap to. No-op when no canvas is
 * registered.
 */
export function requestCameraSnap(bbox: SnapBBox): void {
  if (!registeredSnap) return;
  registeredSnap(bbox);
}

/** Test helper — clears the registered slot between tests. */
export function __resetCameraSnapForTests(): void {
  registeredSnap = null;
}
