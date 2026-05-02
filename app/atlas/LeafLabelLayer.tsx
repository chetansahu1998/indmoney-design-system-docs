"use client";

/**
 * Phase 9 U2a — DOM overlay layer for flow-leaf labels.
 *
 * Why this exists: the cross-route morph from /atlas to /projects/[slug]
 * uses the browser-native View Transitions API. The browser snapshots the
 * source page as a raster image during a transition; a WebGL canvas is
 * captured as one flat texture. So a `view-transition-name` placed on a
 * THREE.js sprite would morph the entire canvas, not the leaf label.
 *
 * The morph source must therefore be a real DOM element. This layer
 * renders one `<div>` per visible flow-type node, projecting that node's
 * `THREE.Vector3` world position into screen space on every frame
 * (throttled to ~30fps), and applies a `transform: translate3d(...)` so
 * each label tracks its node pixel-perfectly.
 *
 * What this unit does NOT do: apply the `view-transition-name` CSS
 * property. That's U2b. This file is the substrate U2b layers on top of.
 *
 * Behaviour matrix:
 *   - Visible flow node → DOM label rendered at node screen position.
 *   - Off-screen flow node → DOM label `display: none` (still mounted so
 *     it can morph if it scrolls back; no allocation churn).
 *   - `morphingNode` set (route push in flight) → projection updates
 *     freeze; the source label stays at its last position so the View
 *     Transition snapshot captures it cleanly.
 *   - Window resize → renderer size cache invalidates next frame.
 *   - Reduced motion → projection still runs (labels need to track during
 *     pan/zoom regardless); only `useFrame`-style drift in BrainGraph is
 *     gated on reduced-motion.
 *
 * Library quirk note:
 *   `react-force-graph-3d` exposes `camera()` and `renderer()` on its ref.
 *   `renderer.getSize(new THREE.Vector2())` returns CSS pixels (not device
 *   pixels) by default — verified in three.js r180 source: getSize ignores
 *   pixelRatio and writes back the value last passed to setSize, which the
 *   library calls with CSS dimensions. So we do NOT divide by devicePixelRatio.
 *
 * Reduced-motion source: imported directly from `@/lib/animations/context`
 * per Phase 6 closure correction (do not import from app/atlas/reducedMotion).
 */

import { useSelectedLayoutSegment } from "next/navigation";
import { memo, useEffect, useRef, useState } from "react";
import * as THREE from "three";

import type { GraphNode } from "./types";

interface FGRefShape {
  camera: () => THREE.Camera;
  renderer: () => THREE.WebGLRenderer;
}

interface Props {
  /** The full visible-node set. We filter to flow-type nodes here so the
   *  parent doesn't need to re-allocate on every render. */
  nodes: GraphNode[];
  /** Force-graph ref from BrainGraph. Used to read camera + renderer. */
  fgRef: React.MutableRefObject<FGRefShape | null>;
  /** When set (a flow leaf was just clicked), freeze projection updates
   *  so the View Transition snapshot captures stable label positions. */
  morphingNode: GraphNode | null;
}

interface ProjectedLabel {
  id: string;
  label: string;
  /** Slug derived from `node.signal.open_url` (e.g. `/projects/<slug>?…`).
   *  Used as the discriminator on the `view-transition-name` (U2b) so the
   *  browser-native View Transitions API matches this DOM element to the
   *  project view's title bar across the cross-route morph. `null` when
   *  the node has no open_url (no morph target — the label still renders,
   *  just without a transition name). */
  slug: string | null;
  x: number;
  y: number;
  visible: boolean;
}

/**
 * Extract the project slug from a flow node's `open_url`. Backend writes
 * URLs of the form `/projects/<slug>` or `/projects/<slug>?v=…` (see
 * services/ds-service/internal/projects/graph_repo.go). Returns `null`
 * for any URL that doesn't match — defensive against future backend
 * changes that might emit a different shape.
 *
 * The slug is used as the View Transitions name discriminator
 * (`flow-${slug}-label`) so the browser matches the source DOM label to
 * the destination project title across the route boundary. Both must
 * carry the same name for the same flow. `decodeURIComponent` is NOT
 * applied — the slug is used as a CSS identifier and a path segment;
 * leaving it URL-encoded is correct in both contexts (and round-trips).
 */
function extractSlugFromOpenURL(openURL: string | undefined): string | null {
  if (!openURL) return null;
  // Match `/projects/<slug>` allowing query string + hash but not extra
  // path segments (a future `/projects/<slug>/<sub>` would not match — we
  // want to morph only to the project root for now).
  const match = openURL.match(/^\/projects\/([^/?#]+)/);
  return match ? match[1] : null;
}

// Throttle: 30fps cap means update at most every ~33ms. We use a frame
// counter (skip every other frame at 60fps) which is simpler and stable
// against frame-rate drift than a wall-clock check.
const THROTTLE_FRAME_SKIP = 2;

function LeafLabelLayerImpl({ nodes, fgRef, morphingNode }: Props) {
  // We render labels via React state but updated through a single rAF
  // loop. setState every other frame is acceptable (React batches);
  // we keep label DOM nodes stable by keying on node id so React reuses
  // them across updates — that's load-bearing for U2b's view-transition
  // matching (the same DOM element must persist across the click).
  const [labels, setLabels] = useState<ProjectedLabel[]>([]);

  // Stable refs for the rAF loop so it doesn't re-create on every render.
  const nodesRef = useRef(nodes);
  nodesRef.current = nodes;
  const morphingRef = useRef<GraphNode | null>(morphingNode);
  morphingRef.current = morphingNode;
  const fgRefRef = useRef(fgRef);
  fgRefRef.current = fgRef;

  // Layout segment changes when the route mounts a new segment under /atlas
  // — when it changes, we know a route push completed and projection can
  // resume. We don't actually freeze on segment value; we freeze on
  // morphingNode, but on segment-change we ensure no stale freeze sticks.
  const segment = useSelectedLayoutSegment();

  useEffect(() => {
    let raf = 0;
    let canceled = false;
    let frameCount = 0;

    // Reusable scratch objects — avoid per-frame allocation.
    const v = new THREE.Vector3();
    const size = new THREE.Vector2();

    const loop = () => {
      if (canceled) return;
      frameCount += 1;
      raf = window.requestAnimationFrame(loop);

      // 30fps cap.
      if (frameCount % THROTTLE_FRAME_SKIP !== 0) return;

      // Frozen during morph: keep last-known positions in state.
      if (morphingRef.current) return;

      const ref = fgRefRef.current?.current;
      if (!ref) return;

      // The library's accessors throw if the renderer hasn't mounted yet
      // (graph is still hydrating). Guard with try/catch — we'll pick up
      // on the next frame.
      let camera: THREE.Camera;
      let renderer: THREE.WebGLRenderer;
      try {
        camera = ref.camera();
        renderer = ref.renderer();
      } catch {
        return;
      }

      // getSize writes CSS pixels into `size` (renderer.setSize was called
      // by the library with CSS dimensions; pixelRatio is applied separately
      // by Three.js for the framebuffer). Confirmed three.js r180.
      renderer.getSize(size);
      const halfW = size.x / 2;
      const halfH = size.y / 2;
      if (halfW === 0 || halfH === 0) return;

      const allNodes = nodesRef.current;
      const next: ProjectedLabel[] = [];
      for (let i = 0; i < allNodes.length; i++) {
        const n = allNodes[i];
        if (n.type !== "flow") continue;
        if (n.x === undefined || n.y === undefined || n.z === undefined) {
          continue;
        }
        v.set(n.x, n.y, n.z);
        v.project(camera);
        // After project: x,y in [-1, 1] (NDC); z in [-1, 1] (depth, where
        // > 1 means behind near plane). z > 1 means clipped behind camera.
        const onScreen = v.z >= -1 && v.z <= 1 && v.x >= -1 && v.x <= 1 && v.y >= -1 && v.y <= 1;
        const sx = v.x * halfW + halfW;
        // Three.js NDC y is up; CSS y is down. Flip.
        const sy = -v.y * halfH + halfH;
        next.push({
          id: n.id,
          label: n.label,
          slug: extractSlugFromOpenURL(n.signal.open_url),
          x: sx,
          y: sy,
          visible: onScreen,
        });
      }

      setLabels((prev) => {
        // Cheap diff: if length + every (id, x, y, visible) match within
        // 0.5px, skip setState. Avoids re-render churn during steady state
        // (force simulation settled, camera idle).
        if (prev.length === next.length) {
          let same = true;
          for (let i = 0; i < prev.length; i++) {
            const a = prev[i];
            const b = next[i];
            if (
              a.id !== b.id ||
              a.visible !== b.visible ||
              Math.abs(a.x - b.x) > 0.5 ||
              Math.abs(a.y - b.y) > 0.5
            ) {
              same = false;
              break;
            }
          }
          if (same) return prev;
        }
        return next;
      });
    };

    raf = window.requestAnimationFrame(loop);
    return () => {
      canceled = true;
      if (raf) window.cancelAnimationFrame(raf);
    };
    // segment is intentionally a dep — when the route changes we want a
    // fresh loop (the prior loop was running against an unmounted ref).
  }, [segment]);

  // Window resize: state stays correct because the rAF loop reads
  // `renderer.getSize()` every (un-throttled) tick, but on the very first
  // frame after resize the labels can lag by a frame. Bump a state flag
  // to force one immediate re-projection.
  useEffect(() => {
    const onResize = () => {
      // Empty setState forces a re-render but doesn't reset positions —
      // the rAF loop computes the next ones. We don't actually need this
      // because the loop is always running; this exists as a defensive
      // pump for low-power devices that may have throttled rAF.
      setLabels((prev) => prev.slice());
    };
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  return (
    <div
      data-testid="leaf-label-layer"
      style={{
        position: "absolute",
        inset: 0,
        pointerEvents: "none",
        // Sit above the canvas (z=0 in BrainGraph) but below floating
        // chrome (chips/search/hover card live in their own stacking
        // contexts via `position: fixed`).
        zIndex: 1,
        // Don't catch the WebGL canvas into our compositor layer.
        overflow: "hidden",
      }}
    >
      {labels.map((l) => (
        <div
          key={l.id}
          data-leaf-label-id={l.id}
          data-leaf-label-slug={l.slug ?? undefined}
          className="leaf-label"
          style={{
            position: "absolute",
            left: 0,
            top: 0,
            // translate3d to opt into a GPU layer — labels move every
            // ~33ms so we want them on the compositor, not paint.
            transform: `translate3d(${l.x}px, ${l.y}px, 0)`,
            // -50% / -100% so the label anchors above and centred on the
            // node (matches the default sprite-label convention).
            transformOrigin: "0 0",
            display: l.visible ? "block" : "none",
            // U2b — View Transitions name. The browser matches the
            // source label here to the destination project's title bar
            // (which carries the same `flow-${slug}-label` name) across
            // the cross-route morph. Only set when the node has a slug
            // (i.e. a known project URL); otherwise omitted so the
            // browser doesn't try to match a non-existent destination.
            //
            // Visibility note: `view-transition-name` on a `display:none`
            // element is ignored by the browser (it isn't part of the
            // captured snapshot), so there's no constraint about
            // duplicate names — only on-screen labels participate.
            // Off-screen labels carry the property harmlessly.
            ...(l.slug
              ? { viewTransitionName: `flow-${l.slug}-label` }
              : null),
            // Visual baseline matches the canvas-sprite labels we replace
            // for non-flow nodes — see BrainGraph nodeLabel handling.
            color: "rgba(255, 255, 255, 0.92)",
            fontFamily: "var(--font-sans, 'Inter Variable', sans-serif)",
            fontSize: "11px",
            fontWeight: 500,
            letterSpacing: "0.02em",
            whiteSpace: "nowrap",
            // Lift slightly off the node centre and centre horizontally.
            // Implemented via marginLeft/marginTop so transform stays
            // pure-translate (cheaper for the compositor).
            marginLeft: "-50%",
            marginTop: "-24px",
            textShadow: "0 1px 2px rgba(0, 0, 0, 0.6)",
            userSelect: "none",
          }}
        >
          {l.label}
        </div>
      ))}
    </div>
  );
}

/**
 * Memoised so parent re-renders (e.g. filter chip toggles that don't
 * affect the visible flow-node set) don't force a state reset on the
 * label set.
 */
export const LeafLabelLayer = memo(LeafLabelLayerImpl);
