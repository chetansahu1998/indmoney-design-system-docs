/**
 * canvas-log.ts — namespaced diagnostic logger for the leaf canvas.
 *
 * Off by default (zero overhead — checks one bool per call). Toggle
 * in DevTools at runtime:
 *
 *   window.__CANVAS_LOG = true            // turn everything on
 *   window.__CANVAS_LOG = ['camera','io'] // selective namespaces
 *   window.__CANVAS_LOG = false           // off again
 *
 * Namespaces in use:
 *
 *   camera       — leafcanvas.tsx auto-fit, onWheel, applyCameraToDOM
 *   gesture      — gesture-tracker tick + settle transitions
 *   io           — LeafFrameRenderer IntersectionObserver fires +
 *                   warm/intersected state transitions
 *   tree         — canonical_tree fetch start / cached / done / failed
 *   image-fill   — useImageRefs HTTP cycle
 *   cluster      — useIconClusterURLs HTTP cycle + tier transitions
 *
 * Why a custom logger rather than `debug` npm: zero deps, zero impact
 * when off, and the format is tuned for the specific bug shape we're
 * chasing — diagnosing why a frame stays on shimmer or why the camera
 * snaps back to landing zone.
 */

type CanvasLogValue = boolean | readonly string[] | undefined;

declare global {
  interface Window {
    __CANVAS_LOG?: CanvasLogValue;
  }
}

function shouldLog(ns: string): boolean {
  if (typeof window === "undefined") return false;
  const flag = window.__CANVAS_LOG;
  if (flag === true) return true;
  if (flag === false || flag == null) return false;
  if (Array.isArray(flag)) return flag.includes(ns);
  return false;
}

/**
 * One-shot log. `ns` is the namespace (filter key). `payload` is any
 * structured data — gets passed straight to console.log so DevTools
 * can expand objects.
 */
export function clog(ns: string, label: string, payload?: unknown): void {
  if (!shouldLog(ns)) return;
  const ts = (performance.now() | 0).toString().padStart(6, " ");
  if (payload !== undefined) {
    // eslint-disable-next-line no-console
    console.log(`%c[${ts}] ${ns}: ${label}`, "color:#7eb8ff", payload);
  } else {
    // eslint-disable-next-line no-console
    console.log(`%c[${ts}] ${ns}: ${label}`, "color:#7eb8ff");
  }
}

/** Same shape but warns instead of logs — for unexpected paths. */
export function cwarn(ns: string, label: string, payload?: unknown): void {
  if (!shouldLog(ns)) return;
  const ts = (performance.now() | 0).toString().padStart(6, " ");
  if (payload !== undefined) {
    // eslint-disable-next-line no-console
    console.warn(`[${ts}] ${ns}: ${label}`, payload);
  } else {
    // eslint-disable-next-line no-console
    console.warn(`[${ts}] ${ns}: ${label}`);
  }
}

/** Imperative test for branches that want to skip work entirely when off. */
export function logEnabled(ns: string): boolean {
  return shouldLog(ns);
}
