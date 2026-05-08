/**
 * camera-snap.ts — Phase 2 U7 camera animation helpers.
 *
 * Three pure helpers + a module-level register/request pair so any
 * component can ask the active leaf canvas to scroll-and-zoom to a
 * target bbox without prop-drilling a callback.
 *
 * Design choices, sourced from the camera-snap research (tldraw +
 * Excalidraw + Figma plugin API):
 *
 *   - **Easing**: easeInOutCubic, the tldraw default. Pure JS,
 *     no spring physics. Matches `cubic-bezier(0.645, 0.045,
 *     0.355, 1.000)` if you'd write it in CSS.
 *   - **Duration**: 320ms. tldraw's de-facto default for camera
 *     animations. Long enough to read as "smooth," short enough
 *     to avoid feeling sluggish.
 *   - **Padding**: 40px inset on each side (tldraw `inset` default).
 *   - **Zoom math**: `min((vp.w - 2*pad) / bbox.w, (vp.h - 2*pad) /
 *     bbox.h)` clamped to `[MIN_ZOOM, maxZoom]`. Excalidraw's
 *     `actionCanvas.tsx` formula.
 *   - **Max zoom**: cap at 1.0 (100%) so a tiny atomic doesn't fill
 *     the screen — Excalidraw's `fitToViewport: false` pattern.
 *   - **No spring overshoot**: pure cubic, no bounce. Spring would
 *     fit "playful" surfaces; inspect mode wants "precise."
 *   - **Interrupt on input**: any pointer/wheel event during the
 *     animation calls cancelToken.cancel() and the camera stays at
 *     the last lerped position. No snap-back.
 *
 * The user-decision (2026-05-09) gates the trigger surface:
 * keyboard / inspector-button only, never on canvas click. Clicks
 * shouldn't auto-snap because adjacent atomics get clicked
 * frequently and the disorientation isn't worth it.
 */

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
 * animateCamera — rAF-driven lerp from `from` to `to` over `durationMs`
 * via easeInOutCubic. On each frame, calls `onTick(state)` so the
 * caller can write the camera ref + flush the DOM transform. Calls
 * `onDone()` on natural completion (NOT on cancellation).
 *
 * Returns a CancelToken; cancel() halts the animation at the next
 * frame boundary, leaving the camera at the last lerped position.
 *
 * Pure rAF — no spring physics, no overshoot. Time provider is
 * injectable for tests (defaults to performance.now + rAF).
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
  const start = now();

  const tick = (): void => {
    if (cancelled) return;
    const elapsed = now() - start;
    const t = durationMs > 0 ? Math.min(1, elapsed / durationMs) : 1;
    const e = easeInOutCubic(t);
    const state: CameraState = {
      x: from.x + (to.x - from.x) * e,
      y: from.y + (to.y - from.y) * e,
      z: from.z + (to.z - from.z) * e,
    };
    onTick(state);
    if (t >= 1) {
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
