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
 *   3. While held: each particle lerps from a random off-node position
 *      toward the held node's coordinates; on arrival, respawns from a
 *      fresh outer-shell offset.
 *   4. On release: particles tween scale → 0 over 250ms then disappear.
 *
 * Reduced-motion: hold-state still brightens the label (handled by
 * BrainGraph's nodeThreeObject factory) but particles + animation are
 * skipped entirely.
 */

import { useEffect, useRef } from "react";
import * as THREE from "three";

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
/** How fast a particle approaches the held node per second (units/s). */
const APPROACH_SPEED = 18;
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

  useEffect(() => {
    heldRef.current = heldNodeID;
    if (heldNodeID === null) {
      releaseStartRef.current = performance.now();
    } else {
      releaseStartRef.current = null;
    }
  }, [heldNodeID]);
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

    // Per-particle state held in a typed array so we don't allocate per frame.
    const px = new Float32Array(PARTICLE_COUNT);
    const py = new Float32Array(PARTICLE_COUNT);
    const pz = new Float32Array(PARTICLE_COUNT);
    const dummy = new THREE.Object3D();
    let lastTime = performance.now();
    let canceled = false;

    const respawn = (i: number, ax: number, ay: number, az: number) => {
      // Spawn on the outer shell — random direction × SHELL_RADIUS.
      const u = Math.random();
      const v = Math.random();
      const theta = 2 * Math.PI * u;
      const phi = Math.acos(2 * v - 1);
      px[i] = ax + Math.sin(phi) * Math.cos(theta) * SHELL_RADIUS;
      py[i] = ay + Math.sin(phi) * Math.sin(theta) * SHELL_RADIUS;
      pz[i] = az + Math.cos(phi) * SHELL_RADIUS;
    };

    const loop = () => {
      if (canceled) return;
      const now = performance.now();
      const dt = Math.min(0.05, (now - lastTime) / 1000); // cap at 20fps for catch-up
      lastTime = now;

      const heldID = heldRef.current;
      const releaseStart = releaseStartRef.current;

      let scale = 1;
      let active = false;

      if (heldID) {
        active = true;
        const node = nodesRef.current.find((n) => n.id === heldID);
        if (node && node.x !== undefined && node.y !== undefined && node.z !== undefined) {
          mesh.visible = true;
          // First-frame: seed particles around the node.
          if (!mesh.userData.seeded || mesh.userData.heldID !== heldID) {
            for (let i = 0; i < PARTICLE_COUNT; i++) {
              respawn(i, node.x, node.y, node.z);
            }
            mesh.userData.seeded = true;
            mesh.userData.heldID = heldID;
          }
          for (let i = 0; i < PARTICLE_COUNT; i++) {
            const dx = node.x - px[i];
            const dy = node.y - py[i];
            const dz = node.z - pz[i];
            const dist = Math.sqrt(dx * dx + dy * dy + dz * dz);
            if (dist < 0.5) {
              respawn(i, node.x, node.y, node.z);
              continue;
            }
            const step = APPROACH_SPEED * dt;
            const t = Math.min(step / dist, 1);
            px[i] += dx * t;
            py[i] += dy * t;
            pz[i] += dz * t;
            dummy.position.set(px[i], py[i], pz[i]);
            dummy.scale.setScalar(scale);
            dummy.updateMatrix();
            mesh.setMatrixAt(i, dummy.matrix);
          }
          mesh.instanceMatrix.needsUpdate = true;
        }
      } else if (releaseStart !== null) {
        // Released — tween scale → 0 over 250ms.
        const elapsed = now - releaseStart;
        if (elapsed < 250) {
          scale = 1 - elapsed / 250;
          active = true;
          for (let i = 0; i < PARTICLE_COUNT; i++) {
            dummy.position.set(px[i], py[i], pz[i]);
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

      // Mark unused under TS strict mode.
      void active;
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
