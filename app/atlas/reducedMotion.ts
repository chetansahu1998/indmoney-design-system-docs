/**
 * Phase 6 — Reduced-motion gate for the mind graph.
 *
 * Re-exports the existing Phase 1 hook so atlas-specific code can import
 * from a co-located module without crossing the lib/animations boundary
 * for a single hook. The signal-animation layer (U11) and bloom drift (U6)
 * both branch on this.
 *
 * The Phase 1 implementation lives at lib/animations/context.ts — that's
 * the SSR-safe matchMedia subscriber + Lenis singleton. Phase 6 doesn't
 * extend it, just consumes.
 */

export { useReducedMotion, getPrefersReducedMotion } from "@/lib/animations/context";

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
