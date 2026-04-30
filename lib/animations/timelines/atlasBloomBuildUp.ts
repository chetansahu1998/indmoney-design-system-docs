/**
 * atlasBloomBuildUp — Phase 3 U1 — atlas postprocessing reveal.
 *
 * Drives the EffectComposer chain in `AtlasPostprocessing.tsx`:
 *   - Bloom: `intensity` 0 → 0.5, `luminanceThreshold` 1.0 → 0.7 over 800ms
 *   - ChromaticAberration: `offset.x` and `offset.y` 0 → 0.0008 over 800ms
 *
 * Used in two places per Animation Philosophy:
 *   1. Initial atlas mount — first paint after Suspense resolves.
 *   2. Theme toggle — re-runs the same build-up so light↔dark transitions
 *      feel cinematic instead of flat (Phase 1 was a plain crossfade;
 *      Phase 3 re-bloom gives it a lift).
 *
 * Reduced-motion: `getPrefersReducedMotion() === true` returns an empty
 * paused timeline. The consumer's React state already holds the
 * final-frame values (POSTPROCESSING_INSTANT) under reduced-motion, so the
 * skipped tween still renders the bloom — just without the animated
 * arrival.
 *
 * Why a state-setter callback instead of `gsap.to(target, …)` directly:
 * the postprocessing values live in React state so r3f's reconciler picks
 * them up. GSAP can't tween React state directly, so we tween a plain
 * `{value: 0}` proxy and call the setter on every frame via `onUpdate`.
 * This costs one extra setState per frame (~16 per second at 60fps for
 * 800ms = ~13 setStates) — well under the budget for r3f's coalescer.
 */

import gsap from "gsap";
import { EASE_PAGE_OPEN } from "../easings";
import { getPrefersReducedMotion } from "../context";

/** Final values the timeline settles on. Exported so AtlasPostprocessing
 *  can also use them as its instant/reduced-motion baseline. */
export const ATLAS_BLOOM_FINAL_INTENSITY = 0.5;
export const ATLAS_BLOOM_FINAL_THRESHOLD = 0.7;
export const ATLAS_CHROMA_FINAL_OFFSET = 0.0008;

/** Total build-up duration in seconds. Mirrors the Phase 3 plan. */
export const BUILD_UP_DURATION_S = 0.8;

/**
 * State-setter shape the timeline expects. The consumer in AtlasCanvas
 * passes a single function that receives the next state and applies it to
 * its React state hook.
 */
export type ApplyPostprocessing = (next: {
  bloomIntensity: number;
  bloomThreshold: number;
  chromaOffsetX: number;
  chromaOffsetY: number;
}) => void;

/**
 * Builds the build-up timeline. Returns a paused GSAP timeline; the caller
 * .play()s it after the EffectComposer reports onReady.
 *
 * Under reduced-motion, returns an empty paused timeline AND immediately
 * calls `apply` with the final-frame values so the consumer's React state
 * lands on POSTPROCESSING_INSTANT without waiting for a tween.
 */
export function atlasBloomBuildUp(
  apply: ApplyPostprocessing,
): gsap.core.Timeline {
  const tl = gsap.timeline({ paused: true });

  if (getPrefersReducedMotion()) {
    apply({
      bloomIntensity: ATLAS_BLOOM_FINAL_INTENSITY,
      bloomThreshold: ATLAS_BLOOM_FINAL_THRESHOLD,
      chromaOffsetX: ATLAS_CHROMA_FINAL_OFFSET,
      chromaOffsetY: ATLAS_CHROMA_FINAL_OFFSET,
    });
    return tl;
  }

  // GSAP-tweened proxy. Only the .progress field matters; we recompute
  // the four output values from it on each frame.
  const proxy = { progress: 0 };

  tl.to(
    proxy,
    {
      progress: 1,
      duration: BUILD_UP_DURATION_S,
      ease: EASE_PAGE_OPEN,
      onUpdate: () => {
        const t = proxy.progress;
        apply({
          bloomIntensity: lerp(0, ATLAS_BLOOM_FINAL_INTENSITY, t),
          // Threshold goes the OPPOSITE direction (1.0 → 0.7), so the
          // bloom-eligible pixel set widens as the timeline progresses.
          bloomThreshold: lerp(1.0, ATLAS_BLOOM_FINAL_THRESHOLD, t),
          chromaOffsetX: lerp(0, ATLAS_CHROMA_FINAL_OFFSET, t),
          chromaOffsetY: lerp(0, ATLAS_CHROMA_FINAL_OFFSET, t),
        });
      },
    },
    0,
  );

  return tl;
}

function lerp(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}
