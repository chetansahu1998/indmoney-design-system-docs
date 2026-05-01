/**
 * Phase 7.6 + 7.7 — real shader-based edge pulse on click-and-hold.
 *
 * Phase 6 v1 shipped a "dim non-incident edges" approximation. Phase 7.6
 * replaced it with a sine-wave alpha modulation on incident edges via a
 * shared ShaderMaterial whose `uTime` uniform updates per-frame. Phase
 * 7.7 polish: one ShaderMaterial PER edge class so incident edges keep
 * their semantic colour while pulsing (supersedes pulses orange,
 * binds-to pulses purple, etc.) instead of a fixed neutral.
 *
 * Architecture:
 *   - One ShaderMaterial instance per edge class — `pulseMaterials` map
 *     keyed by GraphEdgeClass. All four share the same `uTime` uniform
 *     value (synchronised pulse) but each has its own `uColor`.
 *   - One static `dimMaterial` (LineBasic) for non-incident edges
 *     during a hold.
 *   - BrainGraph's `linkMaterial` accessor returns one of these when
 *     the user is holding a node, or null (library default) otherwise.
 *   - `advancePulseTime` updates every shader's `uTime` uniform in a
 *     single rAF call. The WebGL renderer evaluates the fragment shader
 *     per-pixel per-frame, so the visual pulse is smooth at native
 *     refresh.
 *
 * Performance: 4 shaders × 1 uniform write per frame = 4 GPU sync calls.
 * Negligible (sub-microsecond on modern hardware).
 */

import * as THREE from "three";

import { EDGE_STYLE } from "./forceConfig";
import type { GraphEdgeClass } from "./types";

const VERTEX = `
  void main() {
    gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
  }
`;

const FRAGMENT = `
  uniform float uTime;
  uniform vec3 uColor;
  uniform float uBaseAlpha;
  void main() {
    // Sine wave at 1Hz: alpha pulses uBaseAlpha → 1.0 → uBaseAlpha over 1s.
    float pulse = uBaseAlpha + 0.4 * sin(uTime * 6.2831853);
    gl_FragColor = vec4(uColor, pulse);
  }
`;

function makePulseMaterial(colorHex: string, baseAlpha: number): THREE.ShaderMaterial {
  return new THREE.ShaderMaterial({
    uniforms: {
      uTime: { value: 0 },
      uColor: { value: new THREE.Color(colorHex) },
      uBaseAlpha: { value: baseAlpha },
    },
    vertexShader: VERTEX,
    fragmentShader: FRAGMENT,
    transparent: true,
    depthWrite: false,
  });
}

/**
 * pulseMaterials — one ShaderMaterial per edge class. Keyed by class so
 * the BrainGraph linkMaterial accessor can pick the right one for each
 * incident edge while still keeping per-class colour fidelity.
 */
export const pulseMaterials: Record<GraphEdgeClass, THREE.ShaderMaterial> = {
  hierarchy: makePulseMaterial(EDGE_STYLE.hierarchy.color, EDGE_STYLE.hierarchy.alpha),
  uses: makePulseMaterial(EDGE_STYLE.uses.color, EDGE_STYLE.uses.alpha),
  "binds-to": makePulseMaterial(EDGE_STYLE["binds-to"].color, EDGE_STYLE["binds-to"].alpha),
  supersedes: makePulseMaterial(EDGE_STYLE.supersedes.color, EDGE_STYLE.supersedes.alpha),
};

/**
 * dimMaterial — assigned to non-incident edges while the user is holding
 * a node. Static low-alpha LineBasicMaterial; no shader work needed.
 * Shared across all classes (the dim hue is intentionally neutral so
 * non-incident edges fade uniformly into the background).
 */
export const dimMaterial = new THREE.LineBasicMaterial({
  color: new THREE.Color("#3D4F7A"),
  transparent: true,
  opacity: 0.18,
  depthWrite: false,
});

/**
 * advancePulseTime — called from BrainGraph's existing rAF loop with
 * the monotonic seconds-since-mount value. Updates every class's uTime
 * uniform so all incident edges pulse in phase.
 */
export function advancePulseTime(seconds: number): void {
  for (const cls of Object.keys(pulseMaterials) as GraphEdgeClass[]) {
    pulseMaterials[cls].uniforms.uTime.value = seconds;
  }
}
