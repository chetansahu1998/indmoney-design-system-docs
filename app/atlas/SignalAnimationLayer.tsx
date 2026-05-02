"use client";

/**
 * Phase 6 U11 — click-and-hold signal animation layer.
 *
 * The user's frozen contract:
 *   - mousedown on a node = particles converge to that node + the node's
 *     label brightens + incident edges pulse
 *   - **NO camera move**, **NO sibling dim**, **NO expansion**
 *   - mouseup releases — particles decay, glow fades
 *
 * Implementation:
 *   1. Pre-allocated InstancedMesh of 80 particles, attached to the force-
 *      graph's three.js scene via the fgRef. Pool is created on mount and
 *      lives for the lifetime of the component (no per-hold allocation).
 *   2. A rAF loop reads `heldNodeID` via a ref (not React state) so we
 *      don't trigger re-renders mid-frame.
 *   3. U10 — particle approach is now driven by a `@react-spring/three`
 *      spring (`{ tension: 200, friction: 30 }`) instead of a constant-
 *      velocity lerp. The spring exposes a 0..1 scalar `approach` that
 *      maps spawn→target. On hold-start the spring drives toward 1 (a
 *      decelerating approach with the right physical feel); on release
 *      the spring eases back toward 0 *and* a separate scale tween fades
 *      the particles out. Per-particle springs would mean 240 springs;
 *      one shared scalar is sufficient because the visual we're after is
 *      "decelerating approach", not per-particle independent physics.
 *   4. On release: particles tween scale → 0 over 250ms then disappear.
 *
 * Reduced-motion: hold-state still brightens the label (handled by
 * BrainGraph's nodeThreeObject factory) but particles + animation are
 * skipped entirely.
 */

import { useEffect, useRef } from "react";
import * as THREE from "three";
import { useSpring } from "@react-spring/three";

import type { GraphEdge, GraphNode } from "./types";

interface Props {
  fgRef: React.MutableRefObject<{ scene: () => THREE.Scene } | null>;
  heldNodeID: string | null;
  nodes: GraphNode[];
  edges: GraphEdge[];
  reducedMotion: boolean;
}

const PARTICLE_COUNT = 80;
const PARTICLE_RADIUS = 1.2;
/** Outer shell radius — particles spawn within this offset of the node. */
const SHELL_RADIUS = 60;

export function SignalAnimationLayer({
  fgRef,
  heldNodeID,
  nodes,
  reducedMotion,
}: Props) {
  // Refs read by the rAF loop; we never read state inside the loop so
  // updates don't trigger re-renders.
  const heldRef = useRef<string | null>(null);
  const nodesRef = useRef<GraphNode[]>([]);
  const releaseStartRef = useRef<number | null>(null);
  // U10 — shared approach spring (0 = at spawn shell, 1 = at node center).
  // The rAF loop reads this via a ref so the loop itself stays render-free.
  // To sustain a "stream" of converging particles while held, the spring
  // re-kicks itself from 0→1 on rest — each cycle reseeds spawn coords
  // (handled in the loop when approach crosses back below the reseed
  // threshold), giving the visual of continuous convergence rather than
  // a single dolly.
  const approachRef = useRef<number>(0);
  const heldDuringSpringRef = useRef<boolean>(false);
  const [, approachApi] = useSpring(() => ({
    approach: 0,
    config: { tension: 200, friction: 30 },
    onChange: ({ value }) => {
      approachRef.current = (value as { approach: number }).approach;
    },
    onRest: ({ value }) => {
      const v = (value as { approach: number }).approach;
      // If we settled at 1 and the user is still holding, restart the
      // spring from 0 so the particle stream sustains.
      if (v > 0.99 && heldDuringSpringRef.current) {
        approachApi.start({ from: { approach: 0 }, to: { approach: 1 } });
      }
    },
  }));

  useEffect(() => {
    heldRef.current = heldNodeID;
    if (heldNodeID === null) {
      heldDuringSpringRef.current = false;
      releaseStartRef.current = performance.now();
      // Spring back toward 0 on release. The release fade-out below also
      // takes scale → 0 in 250ms, so the spring's "back" trip is mostly
      // for completeness; users will see the scale fade dominate.
      if (!reducedMotion) approachApi.start({ approach: 0 });
      else approachApi.set({ approach: 0 });
    } else {
      heldDuringSpringRef.current = true;
      releaseStartRef.current = null;
      // On hold-start, kick the spring from 0 → 1. Tension 200 / friction 30
      // gives a decelerating approach with no overshoot.
      if (reducedMotion) {
        approachApi.set({ approach: 1 });
      } else {
        approachApi.start({ from: { approach: 0 }, to: { approach: 1 } });
      }
    }
  }, [heldNodeID, reducedMotion, approachApi]);
  useEffect(() => {
    nodesRef.current = nodes;
  }, [nodes]);

  useEffect(() => {
    if (reducedMotion) return; // no particles under reduced-motion
    if (!fgRef.current) return;
    const scene = fgRef.current.scene();

    // Pre-allocate the particle pool. InstancedMesh keeps draw calls to 1.
    const geom = new THREE.SphereGeometry(PARTICLE_RADIUS, 8, 8);
    const mat = new THREE.MeshBasicMaterial({
      color: new THREE.Color("#7B9FFF"),
      transparent: true,
      opacity: 0.85,
    });
    const mesh = new THREE.InstancedMesh(geom, mat, PARTICLE_COUNT);
    mesh.frustumCulled = false; // particles are tiny; cheaper to skip culling
    mesh.visible = false;
    scene.add(mesh);

    // Per-particle SPAWN positions — the spring maps spawn→node by lerp.
    // Holding the spawn coords lets us avoid per-frame trig and gives the
    // approach a stable "from" point even when the spring oscillates.
    const sx = new Float32Array(PARTICLE_COUNT);
    const sy = new Float32Array(PARTICLE_COUNT);
    const sz = new Float32Array(PARTICLE_COUNT);
    const dummy = new THREE.Object3D();
    let canceled = false;

    const respawn = (i: number, ax: number, ay: number, az: number) => {
      // Spawn on the outer shell — random direction × SHELL_RADIUS.
      const u = Math.random();
      const v = Math.random();
      const theta = 2 * Math.PI * u;
      const phi = Math.acos(2 * v - 1);
      sx[i] = ax + Math.sin(phi) * Math.cos(theta) * SHELL_RADIUS;
      sy[i] = ay + Math.sin(phi) * Math.sin(theta) * SHELL_RADIUS;
      sz[i] = az + Math.cos(phi) * SHELL_RADIUS;
    };

    const loop = () => {
      if (canceled) return;
      const now = performance.now();

      const heldID = heldRef.current;
      const releaseStart = releaseStartRef.current;
      const approach = approachRef.current; // spring scalar 0..1

      if (heldID) {
        const node = nodesRef.current.find((n) => n.id === heldID);
        if (
          node &&
          node.x !== undefined &&
          node.y !== undefined &&
          node.z !== undefined
        ) {
          mesh.visible = true;
          // First-frame OR start of a new approach cycle: seed particle
          // SPAWN positions around the node. We detect a new cycle via
          // the spring's value crossing back toward 0 (its `onRest` re-
          // kicks the spring from 0 while held).
          const seedNeeded =
            !mesh.userData.seeded ||
            mesh.userData.heldID !== heldID ||
            (approach < 0.05 && mesh.userData.cycleArmed === false);
          if (seedNeeded) {
            for (let i = 0; i < PARTICLE_COUNT; i++) {
              respawn(i, node.x, node.y, node.z);
            }
            mesh.userData.seeded = true;
            mesh.userData.heldID = heldID;
            mesh.userData.cycleArmed = true;
          }
          // Arm the next cycle once approach has clearly left 0.
          if (approach > 0.2) {
            mesh.userData.cycleArmed = false;
          }
          for (let i = 0; i < PARTICLE_COUNT; i++) {
            // Lerp spawn→node by spring scalar — physics-driven approach.
            const px = sx[i] + (node.x - sx[i]) * approach;
            const py = sy[i] + (node.y - sy[i]) * approach;
            const pz = sz[i] + (node.z - sz[i]) * approach;
            dummy.position.set(px, py, pz);
            dummy.scale.setScalar(1);
            dummy.updateMatrix();
            mesh.setMatrixAt(i, dummy.matrix);
          }
          mesh.instanceMatrix.needsUpdate = true;
        }
      } else if (releaseStart !== null) {
        // Released — tween scale → 0 over 250ms. The spring is also
        // travelling back toward 0 in parallel; we keep the scale fade
        // because it's the dominant visual cue ("particles dissolve").
        const elapsed = now - releaseStart;
        if (elapsed < 250) {
          const scale = 1 - elapsed / 250;
          for (let i = 0; i < PARTICLE_COUNT; i++) {
            // Continue lerping by the (decaying) spring value so the
            // dissolution doesn't snap back to spawn coords.
            const px = sx[i] + 0; // approach decays to 0 ⇒ pos = spawn
            const py = sy[i] + 0;
            const pz = sz[i] + 0;
            void px; void py; void pz;
            // Use the last-known instance matrix position; only scale changes.
            dummy.matrix.identity();
            mesh.getMatrixAt(i, dummy.matrix);
            dummy.matrix.decompose(dummy.position, dummy.quaternion, dummy.scale);
            dummy.scale.setScalar(scale);
            dummy.updateMatrix();
            mesh.setMatrixAt(i, dummy.matrix);
          }
          mesh.instanceMatrix.needsUpdate = true;
        } else {
          mesh.visible = false;
          mesh.userData.seeded = false;
          releaseStartRef.current = null;
        }
      }

      requestAnimationFrame(loop);
    };
    requestAnimationFrame(loop);

    return () => {
      canceled = true;
      scene.remove(mesh);
      geom.dispose();
      mat.dispose();
      mesh.dispose();
    };
  }, [fgRef, reducedMotion]);

  return null;
}
