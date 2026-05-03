/**
 * Phase 6 — Atlas-only WebGL capability gate.
 *
 * Reduced-motion handling now lives exclusively in `@/lib/animations/context`
 * (per atlas runbook §2.8 single-source rule). Atlas callers should import
 * `useReducedMotion` from there directly. This file kept only for the
 * `hasWebGL2()` capability check used by `app/atlas/page.tsx` to decide
 * whether to mount the r3f canvas vs render the EmptyState fallback.
 */

/**
 * WebGL2 capability check. Run on mount before instantiating r3f Canvas.
 * Returns false in two cases the brain view can't recover from:
 *   - SSR (no document) — Next 16 routes Phase 6 through `next/dynamic({ ssr: false })`
 *     so this should never fire in practice; defensive only.
 *   - Browsers without WebGL2 (Safari < 15, headless puppet without GPU).
 *
 * Per plan U3, brain view degrades gracefully to a 2D HTML grid in those
 * cases (Phase 7 polish; v1 ships an EmptyState pointing at /atlas/admin).
 */
export function hasWebGL2(): boolean {
  if (typeof document === "undefined") return false;
  try {
    const canvas = document.createElement("canvas");
    return !!canvas.getContext("webgl2");
  } catch {
    return false;
  }
}
