"use client";

/**
 * AtlasPostprocessing — Phase 3 U1.
 *
 * Wraps the atlas Canvas with `<EffectComposer>` from
 * @react-three/postprocessing and applies a Bloom + ChromaticAberration
 * pass. Effect intensity is React-state-driven so a GSAP timeline
 * (`atlasBloomBuildUp`) can tween it on initial paint and on theme
 * toggles.
 *
 * Design refs (per Phase 3 plan Animation Philosophy):
 *   - Initial mount: bloom builds up over 800ms (luminanceThreshold
 *     1.0 → 0.7, intensity 0 → 0.5). Subtle ChromaticAberration offset
 *     0 → 0.0008.
 *   - Theme toggle: re-runs the build-up (light↔dark transitions
 *     re-bloom).
 *   - prefers-reduced-motion: postprocessing values pinned to final state
 *     instantly; no tween.
 *
 * WebGL context-loss is handled at the Canvas level (the parent's r3f
 * Suspense fallback catches mount failures). Phase 3 U1 deliberately does
 * NOT add a class-based error boundary inside the r3f scene — r3f's
 * reconciler treats mount errors as fatal to the GL canvas, and the
 * Suspense fallback shipped in Phase 1 (`Loading atlas…`) is the right
 * surface for that case. A follow-up unit can add a one-time toast when
 * the canvas falls back.
 *
 * Performance budget: combined Bloom + ChromaticAberration must fit
 * inside 8ms/frame at 1440p on M1 (Phase 3 Risk table). `mipmapBlur=true`
 * keeps Bloom cheap; ChromaticAberration is one full-screen pass.
 */

import {
  EffectComposer,
  Bloom,
  ChromaticAberration,
} from "@react-three/postprocessing";
import { BlendFunction } from "postprocessing";
import { Vector2 } from "three";
import { useReducedMotion } from "@/lib/animations/context";
import {
  ATLAS_BLOOM_FINAL_INTENSITY,
  ATLAS_BLOOM_FINAL_THRESHOLD,
  ATLAS_CHROMA_FINAL_OFFSET,
} from "@/lib/animations/timelines/atlasBloomBuildUp";

/**
 * Public state shape the build-up timeline drives. The atlas root holds
 * these as React state and passes them here so GSAP can update via
 * setState rather than mutating THREE objects directly (which would skip
 * r3f's reconciler).
 */
export interface AtlasPostprocessingState {
  bloomIntensity: number;
  bloomThreshold: number;
  chromaOffsetX: number;
  chromaOffsetY: number;
}

/**
 * Final-frame values. Used as the steady state after the build-up
 * timeline completes, and as the instant value under reduced-motion.
 */
export const POSTPROCESSING_INSTANT: AtlasPostprocessingState = {
  bloomIntensity: ATLAS_BLOOM_FINAL_INTENSITY,
  bloomThreshold: ATLAS_BLOOM_FINAL_THRESHOLD,
  chromaOffsetX: ATLAS_CHROMA_FINAL_OFFSET,
  chromaOffsetY: ATLAS_CHROMA_FINAL_OFFSET,
};

/**
 * Zeroed initial state for the build-up animation: first frame is
 * un-bloomed and the timeline tweens into the final values.
 */
export const POSTPROCESSING_FROM_ZERO: AtlasPostprocessingState = {
  bloomIntensity: 0,
  bloomThreshold: 1.0,
  chromaOffsetX: 0,
  chromaOffsetY: 0,
};

interface Props {
  state: AtlasPostprocessingState;
}

export default function AtlasPostprocessing({ state }: Props) {
  const reduced = useReducedMotion();
  // Under reduced-motion, postprocessing still renders (the user gets the
  // bloom + chromatic look — just no animated arrival). We render with the
  // final-frame values ignoring whatever the live state holds.
  const effective = reduced ? POSTPROCESSING_INSTANT : state;

  return (
    <EffectComposer enableNormalPass={false} multisampling={0}>
      <Bloom
        intensity={effective.bloomIntensity}
        luminanceThreshold={effective.bloomThreshold}
        luminanceSmoothing={0.4}
        mipmapBlur
      />
      <ChromaticAberration
        blendFunction={BlendFunction.NORMAL}
        offset={new Vector2(effective.chromaOffsetX, effective.chromaOffsetY)}
        radialModulation={false}
        modulationOffset={0}
      />
    </EffectComposer>
  );
}
