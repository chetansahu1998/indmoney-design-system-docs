"use client";

/**
 * Phase 6 — BrainGraph (U4 + U5 + U6 + interaction wiring for U7–U12).
 *
 * Mounts react-force-graph-3d with the frozen FORCE_CONFIG, applies the
 * brain-view aesthetic (products glow, others dim), wires bloom + DoF
 * postprocessing through the library's exposed EffectComposer, and adds
 * an organic drift loop. Interaction handlers are passed in as props so
 * later units (U10 single-click zoom, U11 click-and-hold, U12 leaf morph)
 * can hook into the same render surface without forking the component.
 *
 * Dynamic-imported by app/atlas/page.tsx with `ssr: false` because three.js
 * touches `window` at module load.
 */

import dynamic from "next/dynamic";
import { useEffect, useMemo, useRef, useState } from "react";
import * as THREE from "three";
import { UnrealBloomPass } from "three/examples/jsm/postprocessing/UnrealBloomPass.js";
import { useSpring } from "@react-spring/three";

import { FilterChips } from "./FilterChips";
import { HoverSignalCard } from "./HoverSignalCard";
import { LeafLabelLayer } from "./LeafLabelLayer";
import { LeafMorphHandoff } from "./LeafMorphHandoff";
import { PlatformToggle } from "./PlatformToggle";
import { SavedViewShareButton } from "./SavedViewShareButton";
import { SearchInput } from "./SearchInput";
import { SignalAnimationLayer } from "./SignalAnimationLayer";
import { advancePulseTime, dimMaterial, pulseMaterials } from "./edgePulseShader";
import {
  BACKGROUND_COLOR,
  EDGE_STYLE,
  FORCE_CONFIG,
  NODE_VISUAL,
} from "./forceConfig";
import { useReducedMotion } from "./reducedMotion";
import { useGraphAggregate } from "./useGraphAggregate";
import { useGraphView } from "./useGraphView";
import { useSignalHold } from "./useSignalHold";
import type {
  GraphEdge,
  GraphFilters,
  GraphNode,
  GraphNodeKind,
  GraphPlatform,
} from "./types";
import { cullVisibleSubset } from "./cull";

// react-force-graph-3d ships a default-export React component. We dynamic-
// import it so the heavy three.js + d3-force-3d bundle stays out of the
// initial route chunk; the parent page is itself dynamic-imported so this
// adds at most one extra fetch.
const ForceGraph3D = dynamic(() => import("react-force-graph-3d"), {
  ssr: false,
});

// ─── Shared-resource caches (module-scoped) ────────────────────────────
// Per the threejs-webgl skill: never create a new Geometry / Material per
// rendered object. We share one SphereGeometry per node type (7 total)
// and one base Material per type that we clone per mesh (so per-node
// opacity + colour state can vary without thrashing GPU resources).
const SHARED_NODE_GEOMETRY = new Map<GraphNodeKind, THREE.SphereGeometry>();
const SHARED_NODE_MATERIAL = new Map<GraphNodeKind, THREE.MeshBasicMaterial>();

function getSharedNodeGeometry(type: GraphNodeKind): THREE.SphereGeometry {
  let g = SHARED_NODE_GEOMETRY.get(type);
  if (!g) {
    const v = NODE_VISUAL[type] ?? NODE_VISUAL.flow;
    g = new THREE.SphereGeometry(v.radius, 16, 16);
    SHARED_NODE_GEOMETRY.set(type, g);
  }
  return g;
}

function getSharedNodeMaterial(type: GraphNodeKind): THREE.MeshBasicMaterial {
  let m = SHARED_NODE_MATERIAL.get(type);
  if (!m) {
    const v = NODE_VISUAL[type] ?? NODE_VISUAL.flow;
    // U8 — Per-type emissive hierarchy. Multiply the base colour by
    // `emissiveIntensity` so the resulting RGB can exceed 1.0 (HDR signal).
    // The bloom pass uses a luminance threshold ≈ 1.0, so only types whose
    // intensity lifts the colour above 1.0 produce visible glow. `toneMapped:
    // false` is mandatory — without it the renderer's ACES filmic tonemap
    // would clamp the HDR signal back below the threshold before the bloom
    // pass samples it, defeating the per-type hierarchy entirely.
    const colour = new THREE.Color(v.color);
    colour.multiplyScalar(v.emissiveIntensity);
    m = new THREE.MeshBasicMaterial({
      color: colour,
      transparent: true,
      opacity: 1.0,
      toneMapped: false,
    });
    SHARED_NODE_MATERIAL.set(type, m);
  }
  return m;
}

interface BrainGraphProps {
  platform: GraphPlatform;
  focusNodeID: string | null;
  /** Hydrate filter chips from a share-link URL. null = defaults. */
  initialFilters?: {
    components: boolean;
    tokens: boolean;
    decisions: boolean;
  } | null;
}

/**
 * Internal force-graph node shape — extends the wire GraphNode with the
 * runtime fields d3-force-3d stamps onto each row (x/y/z + vx/vy/vz).
 */
type FGNode = GraphNode & {
  x?: number;
  y?: number;
  z?: number;
  __originalY?: number;
  __idHash?: number;
};

type FGEdge = GraphEdge;

export default function BrainGraph({
  platform: platformProp,
  focusNodeID,
  initialFilters,
}: BrainGraphProps) {
  const reducedMotion = useReducedMotion();
  const [platform, setPlatform] = useState<GraphPlatform>(platformProp);
  // Keep platform in sync if the URL changes.
  useEffect(() => setPlatform(platformProp), [platformProp]);

  const aggregate = useGraphAggregate(platform);
  const view = useGraphView();
  const hold = useSignalHold();

  // Phase 7.5 — apply share-link filters once on mount. The hook owns the
  // filter state; we just push the URL-derived initial values in.
  useEffect(() => {
    if (!initialFilters) return;
    view.setFilters({
      hierarchy: true,
      components: initialFilters.components,
      tokens: initialFilters.tokens,
      decisions: initialFilters.decisions,
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Hover state for the floating signal card (U9).
  const [hoverNode, setHoverNode] = useState<FGNode | null>(null);
  const [hoverPos, setHoverPos] = useState<{ x: number; y: number } | null>(null);

  // Phase 8 U11 — in-graph search match set. null = no active query (full
  // graph). Non-null = dim non-matching nodes to opacity 0.3.
  const [searchMatches, setSearchMatches] = useState<Set<string> | null>(null);

  // Cull visible subset based on view + filters (U13). The full graph stays
  // in memory; we filter on each render so the d3 simulation operates on
  // the subset.
  const visible = useMemo(() => {
    if (!aggregate.data) return { nodes: [] as FGNode[], edges: [] as FGEdge[] };
    return cullVisibleSubset(aggregate.data, view, view.filters);
  }, [aggregate.data, view]);

  // Stamp deterministic hash + original-Y onto each node once so the drift
  // loop has a per-node phase offset and a base position to oscillate around.
  useEffect(() => {
    visible.nodes.forEach((n) => {
      const node = n as FGNode;
      if (node.__idHash === undefined) {
        node.__idHash = simpleHash(node.id);
      }
    });
  }, [visible.nodes]);

  // ─── force-graph ref + imperative API access ───────────────────────────
  // react-force-graph-3d exposes scene(), camera(), renderer(),
  // postProcessingComposer(), cameraPosition() on the ref.
  type FGRef = {
    cameraPosition: (
      pos: { x?: number; y?: number; z?: number },
      lookAt?: { x: number; y: number; z: number },
      transitionMs?: number,
    ) => void;
    scene: () => THREE.Scene;
    camera: () => THREE.Camera;
    renderer: () => THREE.WebGLRenderer;
    postProcessingComposer: () => {
      addPass: (pass: unknown) => void;
      passes: unknown[];
    };
    d3ReheatSimulation: () => void;
    d3Force: (id: string) => unknown;
  };
  const fgRef = useRef<FGRef | null>(null);

  // ─── U6 + U8 — threshold-driven bloom + organic drift ──────────────────
  // Bloom is added once on mount via the library's postProcessingComposer
  // accessor. Reduced-motion users still get bloom (it's static); we only
  // gate the per-frame drift loop.
  //
  // U8 — The threshold is set to 1.0 so only materials whose
  // `color × emissiveIntensity` exceeds 1.0 in any channel contribute to the
  // bloom. Combined with `toneMapped: false` on the shared node materials,
  // this produces a per-type visual hierarchy: products (intensity 3.5)
  // glow brightest, folders (1.8) and flows (1.2) glow softer, and
  // components / tokens / decisions (≤ 1.0) sit below the threshold and
  // remain crisp / dim — letting the filter-driven opacity gate do the
  // structural visual weighting.
  //
  // Implementation note: react-force-graph-3d owns its own three.js
  // EffectComposer and exposes `postProcessingComposer()` for `addPass`.
  // The `<EffectComposer>` JSX wrap from `@react-three/postprocessing`
  // cannot be mounted into that owned composer (it would create a second
  // composer with an incompatible Pass base class — three.js's
  // `EffectComposer.Pass.render(renderer, writeBuffer, readBuffer, ...)`
  // and postprocessing-lib's `Pass.render(renderer, inputBuffer,
  // outputBuffer, ...)` swap their two buffer arguments). We therefore
  // keep three.js's `UnrealBloomPass` and map the plan's parameters to
  // its (resolution, strength, radius, threshold) signature:
  //   - luminanceThreshold 1.0       → threshold 1.0
  //   - intensity 1.2                → strength  1.2
  //   - mipmapBlur + KernelSize.LARGE → radius   0.85 (UnrealBloomPass
  //     already does a 5-mip downsample / upsample chain; `radius` is the
  //     equivalent dial for kernel breadth).
  useEffect(() => {
    if (!fgRef.current) return;
    const composer = fgRef.current.postProcessingComposer();
    // Avoid double-adding on hot reload. Also reuse / retune an existing
    // pass if one was added by an earlier mount under the old parameters.
    const existing = composer.passes.find(
      (p) => (p as { constructor?: { name?: string } }).constructor?.name === "UnrealBloomPass",
    ) as UnrealBloomPass | undefined;
    if (existing) {
      existing.threshold = 1.0;
      existing.strength = 1.2;
      existing.radius = 0.85;
      return;
    }
    const bloom = new UnrealBloomPass(
      new THREE.Vector2(window.innerWidth, window.innerHeight),
      /* strength */ 1.2,
      /* radius   */ 0.85,
      /* threshold*/ 1.0,
    );
    composer.addPass(bloom);
  }, [aggregate.status]); // re-runs once when graph first becomes ready

  // Apply force config to d3-force-3d on mount + when platform changes.
  useEffect(() => {
    if (!fgRef.current) return;
    const charge = fgRef.current.d3Force("charge") as { strength?: (n: number) => unknown } | null;
    if (charge && typeof charge.strength === "function") charge.strength(FORCE_CONFIG.charge);
    const link = fgRef.current.d3Force("link") as
      | { distance?: (n: number) => unknown; strength?: (n: number) => unknown }
      | null;
    if (link) {
      if (typeof link.distance === "function") link.distance(FORCE_CONFIG.link.distance);
      if (typeof link.strength === "function") link.strength(FORCE_CONFIG.link.strength);
    }
    fgRef.current.d3ReheatSimulation();
  }, [platform, visible.nodes.length]);

  // Organic drift + Phase 7.6 edge-pulse uniform tick. One rAF loop drives
  // both: y-position drift on every visible node, plus the pulseMaterial's
  // uTime uniform that the WebGL fragment shader reads to compute incident-
  // edge alpha. Reduced-motion skips drift but still keeps pulse time
  // ticking — when held under reduced-motion, the static glow at base
  // alpha is what the shader produces with sin(0)=0 → uBaseAlpha (0.6).
  useEffect(() => {
    if (!fgRef.current) return;
    let raf = 0;
    let canceled = false;
    const start = performance.now();
    const loop = () => {
      if (canceled || !fgRef.current) return;
      const t = (performance.now() - start) / 1000;
      advancePulseTime(t);
      if (!reducedMotion) {
        visible.nodes.forEach((node) => {
          const n = node as FGNode;
          if (n.y === undefined) return;
          if (n.__originalY === undefined) n.__originalY = n.y;
          n.y = n.__originalY + Math.sin(t * 0.5 + (n.__idHash ?? 0)) * 0.6;
        });
      }
      raf = requestAnimationFrame(loop);
    };
    raf = requestAnimationFrame(loop);
    return () => {
      canceled = true;
      if (raf) cancelAnimationFrame(raf);
    };
  }, [reducedMotion, visible.nodes]);

  // ─── U5 — node visual factory (memory-safe) ────────────────────────────
  // Geometry is shared module-scope (one per node type → 7 total). Each
  // node gets its own cloned material so we can mutate opacity / colour
  // for dim + held states without re-allocating geometry.
  //
  // The factory itself is STABLE (no state deps) so react-force-graph-3d
  // doesn't re-create node meshes when hold / focus state changes —
  // that work happens in a sibling effect that walks the scene and
  // mutates the existing mesh materials in place.
  //
  // Disposal: tracked-mesh refs collected in `nodeMeshesRef`; the unmount
  // cleanup walks the set + disposes every cloned material. Shared
  // geometries persist for the lifetime of the module (cheap; ~7 of them).
  const nodeMeshesRef = useRef<Set<THREE.Mesh>>(new Set());

  const nodeThreeObject = useMemo(() => {
    return (rawNode: unknown): THREE.Object3D => {
      const node = rawNode as FGNode;
      const type = (node.type as GraphNodeKind) ?? "flow";
      const visual = NODE_VISUAL[type] ?? NODE_VISUAL.flow;

      const geometry = getSharedNodeGeometry(type);
      // Clone the type's base material so this mesh can carry per-node
      // opacity + colour without affecting siblings. The clone shares the
      // GPU shader program; only JS-level state is duplicated.
      const material = getSharedNodeMaterial(type).clone();

      const mesh = new THREE.Mesh(geometry, material);
      mesh.userData.nodeID = node.id;
      mesh.userData.nodeType = type;
      mesh.userData.baseColorHex = visual.color;
      mesh.userData.baseIntensity = visual.emissiveIntensity;
      nodeMeshesRef.current.add(mesh);
      return mesh;
    };
    // Stable across renders — the visual *state* (dim, held) is applied
    // imperatively in the effect below, not by re-running the factory.
  }, []);

  // Apply dim / held / search-match state imperatively whenever any of
  // them changes. Walking the tracked-mesh set is O(visible nodes) per
  // state change, no allocations.
  useEffect(() => {
    const focusedID = view.focusedNodeID;
    const heldID = hold.heldNodeID;
    nodeMeshesRef.current.forEach((mesh) => {
      const id = mesh.userData.nodeID as string | undefined;
      if (!id) return;
      // Three independent dim sources, multiplied. Default 1.0.
      let dimmed = 1.0;
      if (focusedID !== null) {
        const inSubtree = isAncestorOrSelf(
          { id, type: mesh.userData.nodeType, label: "", platform: platform, signal: { severity_counts: { critical: 0, high: 0, medium: 0, low: 0, info: 0 }, persona_count: 0, last_updated_at: "" } } as GraphNode,
          focusedID,
          visible.nodes,
        );
        if (!inSubtree) dimmed = Math.min(dimmed, 0.18);
      }
      if (searchMatches !== null && !searchMatches.has(id)) {
        dimmed = Math.min(dimmed, 0.3);
      }
      const isHeld = heldID === id;
      const intensity =
        (mesh.userData.baseIntensity as number) * (isHeld ? 1.5 : 1.0);
      const mat = mesh.material as THREE.MeshBasicMaterial;
      mat.transparent = dimmed < 1.0;
      mat.opacity = dimmed;
      mat.color.set(mesh.userData.baseColorHex as string);
      mat.color.multiplyScalar(intensity * dimmed);
      mat.needsUpdate = true;
    });
  }, [view.focusedNodeID, hold.heldNodeID, visible.nodes, platform, searchMatches]);

  // Dispose cloned materials on unmount. Shared geometries persist for
  // the lifetime of the module (cheap; ~7 of them).
  useEffect(() => {
    const tracked = nodeMeshesRef.current;
    return () => {
      tracked.forEach((mesh) => {
        const mat = mesh.material as THREE.Material | THREE.Material[];
        if (Array.isArray(mat)) mat.forEach((m) => m.dispose());
        else mat.dispose();
      });
      tracked.clear();
    };
  }, []);

  // Edge color resolver — class → preset.
  const linkColor = (link: unknown): string => {
    const l = link as FGEdge;
    return EDGE_STYLE[l.class]?.color ?? "#3D4F7A";
  };
  const linkWidth = (link: unknown): number => {
    const l = link as FGEdge;
    return EDGE_STYLE[l.class]?.width ?? 0.8;
  };
  const linkOpacity = (link: unknown): number => {
    const l = link as FGEdge;
    return EDGE_STYLE[l.class]?.alpha ?? 0.4;
  };

  // Phase 7.6 — when the user is holding a node, return a custom material
  // for incident edges (sine-wave shader pulse) and a static-dim material
  // for non-incident. Returning null lets the library use its default.
  // The library re-evaluates this accessor whenever the function reference
  // changes — so the deps include `hold.heldNodeID`.
  const linkMaterial = useMemo(() => {
    const heldID = hold.heldNodeID;
    if (!heldID) return null;
    return (raw: unknown): THREE.Material | null => {
      const l = raw as FGEdge & { source: unknown; target: unknown };
      // The library replaces source / target string IDs with the resolved
      // node objects after the first force tick; handle both shapes.
      const sID =
        typeof l.source === "string"
          ? (l.source as string)
          : ((l.source as { id?: string })?.id ?? "");
      const tID =
        typeof l.target === "string"
          ? (l.target as string)
          : ((l.target as { id?: string })?.id ?? "");
      const incident = sID === heldID || tID === heldID;
      if (!incident) return dimMaterial;
      // Phase 7.7 — per-class pulse colour preserves the semantic edge
      // hue (supersedes orange, binds-to purple, uses neutral, hierarchy
      // dim). All four shaders share the same uTime so the pulse is
      // synchronised across edge classes.
      return pulseMaterials[l.class] ?? pulseMaterials.hierarchy;
    };
  }, [hold.heldNodeID]);

  // ─── U10 — single-click recursive zoom (spring-driven) ─────────────────
  // We replace the library's `cameraPosition()` linear tween (mechanical
  // feel) with a `@react-spring/three` spring that imperatively writes
  // the camera position + lookAt every frame. The library owns camera
  // mounting, so we can't wrap it in `<animated.perspectiveCamera>` —
  // the imperative `onChange` pattern is the documented r3f / external-
  // camera escape hatch. See lib/animations/conventions.md.
  //
  // Spring config: tension 170 / friction 26 — react-spring's canonical
  // default; feels mass-spring-y (decelerates naturally, settles without
  // visible overshoot for small distances and a tiny one for large).
  const lookAtTargetRef = useRef(new THREE.Vector3(0, 0, 0));
  const [, cameraSpringApi] = useSpring(() => ({
    px: 0,
    py: 0,
    pz: 600, // matches the library's default initial camera distance
    config: { tension: 170, friction: 26 },
    onChange: ({ value }) => {
      const cam = fgRef.current?.camera();
      if (!cam) return;
      const v = value as { px: number; py: number; pz: number };
      cam.position.set(v.px, v.py, v.pz);
      const t = lookAtTargetRef.current;
      cam.lookAt(t.x, t.y, t.z);
    },
  }));

  const handleNodeClick = (rawNode: unknown) => {
    const node = rawNode as FGNode;
    if (node.type === "flow") {
      // Defer to leaf-morph handoff (U12). The handoff component picks up
      // the node from view state and triggers the route push.
      view.morphTo(node);
      return;
    }
    if (node.type === "product" || node.type === "folder") {
      view.focus(node);
      if (
        fgRef.current &&
        node.x !== undefined &&
        node.y !== undefined &&
        node.z !== undefined
      ) {
        const distance = node.type === "product" ? 200 : 120;
        // Position the camera "behind" the node along its current vector
        // from origin, so the zoom feels continuous rather than orbital.
        const dir = new THREE.Vector3(node.x, node.y, node.z).normalize();
        const targetPx = node.x + dir.x * distance;
        const targetPy = node.y + dir.y * distance;
        const targetPz = node.z + dir.z * distance;
        // The spring's onChange reads from this ref so lookAt tracks the
        // node, not (0,0,0). Update before kicking the spring.
        lookAtTargetRef.current.set(node.x, node.y, node.z);
        // Seed the spring's "from" with the camera's current position so
        // the first onChange tick doesn't snap.
        const cam = fgRef.current.camera();
        if (reducedMotion) {
          // Reduced-motion: skip the spring; jump to the target.
          cameraSpringApi.set({
            px: targetPx,
            py: targetPy,
            pz: targetPz,
          });
          cam.position.set(targetPx, targetPy, targetPz);
          cam.lookAt(node.x, node.y, node.z);
        } else {
          cameraSpringApi.start({
            from: { px: cam.position.x, py: cam.position.y, pz: cam.position.z },
            to: { px: targetPx, py: targetPy, pz: targetPz },
          });
        }
      }
    }
  };

  // ─── U9 — hover card wiring ────────────────────────────────────────────
  const handleNodeHover = (rawNode: unknown | null) => {
    const node = rawNode as FGNode | null;
    setHoverNode(node);
    if (node === null) setHoverPos(null);
  };

  // Track mouse position for the hover card anchor.
  useEffect(() => {
    if (hoverNode === null) return;
    const onMove = (e: MouseEvent) => {
      setHoverPos({ x: e.clientX, y: e.clientY });
    };
    window.addEventListener("mousemove", onMove);
    return () => window.removeEventListener("mousemove", onMove);
  }, [hoverNode]);

  // ─── U11 — click-and-hold pointer-down/up wiring ───────────────────────
  // r3f / react-force-graph-3d expose onNodePointerDown / onNodePointerUp.
  // We dispatch hold-start/hold-end into the SignalAnimationLayer, which
  // owns particles + edge pulse. Camera does NOT move here — that's the
  // user's frozen contract.
  const handleNodePointerDown = (rawNode: unknown) => {
    const node = rawNode as FGNode;
    hold.start(node.id);
  };
  const handleNodePointerUp = () => {
    hold.end();
  };

  // ─── Deep link: focus on mount ─────────────────────────────────────────
  useEffect(() => {
    if (!focusNodeID || !aggregate.data) return;
    const node = aggregate.data.nodes.find((n) => n.id === focusNodeID) as FGNode | undefined;
    if (node) view.focus(node);
  }, [focusNodeID, aggregate.data, view]);

  // ─── Empty + error states ──────────────────────────────────────────────
  if (aggregate.status === "empty") {
    return <EmptyState platform={platform} />;
  }
  if (aggregate.status === "error") {
    return <ErrorState message={aggregate.error ?? "Unknown error"} onRetry={aggregate.refetch} />;
  }

  return (
    <>
      {/* The library types are over-strict (it expects ForceGraphMethods
         on the ref + LinkObject[] for links). We cast around them — at
         runtime our shape matches; at type-time we hand the library a
         loosely-typed component. */}
      <div className="atlas-canvas">
        {/* eslint-disable-next-line @typescript-eslint/no-explicit-any */}
        {(() => {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          const FG = ForceGraph3D as unknown as React.ComponentType<any>;
          return (
            <FG
              ref={fgRef}
              graphData={{ nodes: visible.nodes, links: visible.edges }}
              backgroundColor={BACKGROUND_COLOR}
          // Disable built-in particles & directional arrows — we render
          // ours via SignalAnimationLayer for the hold interaction.
          linkDirectionalParticles={0}
          // Per-node visuals.
          nodeThreeObject={nodeThreeObject}
          // U2a: suppress canvas-sprite labels for flow-type nodes — the
          // DOM overlay (LeafLabelLayer) renders those so they can carry
          // a `view-transition-name` for the cross-route morph (U2b).
          // Non-flow types (folder, product, etc.) keep canvas labels.
          nodeLabel={(n: unknown) => {
            const node = n as FGNode;
            return node.type === "flow" ? "" : node.label;
          }}
          // Per-edge visuals. linkMaterial overrides linkColor/linkWidth/
          // linkOpacity for incident edges during a click-and-hold (Phase
          // 7.6 shader pulse). When no node is held, linkMaterial is null
          // and the library uses the basic-line accessors.
          linkColor={linkColor}
          linkWidth={linkWidth}
          linkOpacity={linkOpacity as unknown as number}
          {...(linkMaterial !== null ? { linkMaterial } : {})}
          // Force config (additional knobs the library exposes directly).
          d3VelocityDecay={FORCE_CONFIG.velocityDecay}
          d3AlphaDecay={FORCE_CONFIG.alphaDecay}
          // Cooldown ticks: stop after 200 ticks so the simulation settles
          // and stops eating CPU.
          cooldownTicks={200}
          warmupTicks={20}
          // Interactions.
          onNodeClick={handleNodeClick}
          onNodeHover={handleNodeHover}
          onNodePointerDown={handleNodePointerDown}
          onNodePointerUp={handleNodePointerUp}
              // Show on screen via 100% width/height of parent.
              width={typeof window !== "undefined" ? window.innerWidth : 1200}
              height={typeof window !== "undefined" ? window.innerHeight : 800}
            />
          );
        })()}
        {/* U11 — particle + edge-pulse layer overlays the same canvas via
            scene-side mutation; the layer subscribes to hold state and
            adds InstancedMesh particles to the force-graph scene. */}
        <SignalAnimationLayer
          fgRef={fgRef as unknown as React.MutableRefObject<{ scene: () => THREE.Scene } | null>}
          heldNodeID={hold.heldNodeID}
          nodes={visible.nodes}
          edges={visible.edges}
          reducedMotion={reducedMotion}
        />
        {/* U2a — DOM-overlay labels for flow leaves. Each label tracks
            its node by projecting the node's world position to screen
            space every frame. Required as the morph SOURCE for the
            View Transitions cross-route morph (U2b) — a WebGL sprite
            cannot serve as a view-transition source because the canvas
            is rasterized as one texture during the snapshot. */}
        <LeafLabelLayer
          nodes={visible.nodes}
          fgRef={fgRef as unknown as React.MutableRefObject<{
            camera: () => THREE.Camera;
            renderer: () => THREE.WebGLRenderer;
          } | null>}
          morphingNode={view.morphingNode}
        />
      </div>

      {/* Top-right Mobile↔Web toggle (U8). */}
      <PlatformToggle
        value={platform}
        onChange={setPlatform}
        reducedMotion={reducedMotion}
      />

      {/* Filter chips above the canvas (U7). */}
      <FilterChips
        filters={view.filters}
        onChange={view.setFilters}
        reducedMotion={reducedMotion}
      />

      {/* Phase 8 U11 — in-graph search input (top-left corner). */}
      <SearchInput
        onMatchChange={setSearchMatches}
        reducedMotion={reducedMotion}
      />

      {/* Phase 7.5 U7 — saved-view share link (bottom-right). */}
      <SavedViewShareButton
        platform={platform}
        focusedNodeID={view.focusedNodeID}
        filters={view.filters}
      />

      {/* Hover signal card (U9). */}
      {hoverNode && hoverPos && (
        <HoverSignalCard node={hoverNode} anchor={hoverPos} />
      )}

      {/* Leaf-morph handoff layer (U12). */}
      {view.morphingNode && (
        <LeafMorphHandoff
          node={view.morphingNode}
          reducedMotion={reducedMotion}
        />
      )}

      <style jsx>{`
        .atlas-canvas {
          position: fixed;
          inset: 0;
          z-index: 0;
        }
      `}</style>
    </>
  );
}

// ─── Helpers ───────────────────────────────────────────────────────────────

function simpleHash(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) {
    h = (h << 5) - h + s.charCodeAt(i);
    h |= 0;
  }
  return Math.abs(h) / 0xffffffff; // [0, 1]
}

/**
 * isAncestorOrSelf — true if `target` is the focused node OR one of its
 * descendants in the hierarchy chain. Used by the focus-dim path so
 * non-focused subtrees recede to opacity 0.18.
 */
function isAncestorOrSelf(
  candidate: GraphNode,
  focusedID: string,
  pool: GraphNode[],
): boolean {
  if (candidate.id === focusedID) return true;
  // Walk parent chain upward; if we hit focusedID we're a descendant.
  const byID = new Map(pool.map((n) => [n.id, n]));
  let cursor: GraphNode | undefined = candidate;
  let depth = 0;
  while (cursor && cursor.parent_id && depth++ < 10) {
    if (cursor.parent_id === focusedID) return true;
    cursor = byID.get(cursor.parent_id);
  }
  return false;
}

function EmptyState({ platform }: { platform: GraphPlatform }) {
  return (
    <div className="empty">
      <h1>No flows yet</h1>
      <p>
        The {platform === "mobile" ? "mobile" : "web"} mind graph is empty.
        Export from the Figma plugin to seed it.
      </p>
      <style jsx>{`
        .empty {
          position: fixed;
          inset: 0;
          display: grid;
          place-items: center;
          color: rgba(255, 255, 255, 0.62);
          font-family: var(--font-sans, "Inter Variable", sans-serif);
          text-align: center;
        }
        h1 {
          font-size: 22px;
          margin: 0 0 12px;
        }
        p {
          margin: 0;
          font-size: 14px;
          color: rgba(255, 255, 255, 0.42);
        }
      `}</style>
    </div>
  );
}

function ErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="err">
      <h1>Couldn&apos;t load the graph</h1>
      <p>{message}</p>
      <button onClick={onRetry}>Retry</button>
      <style jsx>{`
        .err {
          position: fixed;
          inset: 0;
          display: grid;
          place-items: center;
          color: rgba(255, 255, 255, 0.85);
          font-family: var(--font-sans, "Inter Variable", sans-serif);
          text-align: center;
        }
        h1 {
          font-size: 20px;
          margin: 0 0 8px;
        }
        p {
          margin: 0 0 20px;
          color: rgba(255, 175, 175, 0.7);
          font-size: 13px;
        }
        button {
          padding: 8px 16px;
          border: 1px solid rgba(255, 255, 255, 0.18);
          background: rgba(255, 255, 255, 0.04);
          color: inherit;
          border-radius: 8px;
          cursor: pointer;
          font: inherit;
        }
        button:hover {
          background: rgba(255, 255, 255, 0.08);
        }
      `}</style>
    </div>
  );
}
