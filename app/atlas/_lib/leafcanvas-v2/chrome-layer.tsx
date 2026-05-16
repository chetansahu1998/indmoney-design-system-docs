"use client";

/**
 * chrome-layer.tsx — screen-space SVG overlay mounted as a sibling of
 * `.lc-world` in `leafcanvas.tsx`. Hosts every non-content visual that
 * should stay crisp at any camera zoom: selection rings, hover outlines,
 * padding bands, gap fills, distance lines, marquee rectangle,
 * breadcrumb chip, dimension labels.
 *
 * Architectural intent (U1 foundation):
 *
 *   - The world layer (`.lc-world`) carries `transform: scale(z) translate(...)`.
 *     Everything inside it gets scaled. A 2px CSS outline becomes a fat 16px
 *     bar at zoom 8× and vanishes near 0.18×.
 *
 *   - The chrome layer is a SIBLING of `.lc-world`, NOT a child. It is
 *     positioned in screen-space (CSS px) and does not inherit the world
 *     transform. Selection rings stay 2px regardless of zoom. Padding
 *     bands stay legible at any zoom. Distance line labels stay readable.
 *
 *   - Coordinates are computed per-paint: read the world-rect from
 *     `spatial-store`, project to screen-coords using the current camera
 *     from `camera-state`. The math is `screen = (world - cam) * cam.z`.
 *
 *   - Updates happen via REF mutations on pre-allocated SVG elements,
 *     never via React re-renders. The component renders the SVG once
 *     with empty <g> groups; subsequent units (U4 selection, U5 hover,
 *     U6 distance, U10 breadcrumb) write into those groups imperatively
 *     on every rAF tick. This is the key to running the chrome layer at
 *     60Hz without contending with React reconciliation.
 *
 * What U1 ships:
 *
 *   - The SVG element + the pre-allocated `<g>` group skeleton.
 *   - The rAF loop driver (subscribes to camera-state + spatial-store
 *     changes; reads them inside one frame; writes nothing yet).
 *   - The component lifecycle (mount/unmount, listener cleanup).
 *   - CSS positioning (`.leafcv2-chrome-layer` in canvas-v2.css).
 *
 * What later units add:
 *
 *   - U4 wires selection ring paint into the `chrome-selection` group.
 *   - U5 wires composed hover (outline + padding bands + gap fills) into
 *     `chrome-hover` / `chrome-padding` / `chrome-gap`. Plus the
 *     breadcrumb chip into `chrome-breadcrumb`.
 *   - U6 wires Alt-hover distance lines into `chrome-distance`.
 *   - The marquee-drag rectangle and dimension chip groups land in U4
 *     and U5 respectively.
 *
 * U1 deliberately does NOT paint anything visible. Tests verify the
 * mount + group structure + rAF loop runs; visible parity with the
 * existing MeasurementOverlay only matters once U5 ships composed
 * hover and the deprecation can proceed.
 */

import { useEffect, useRef } from "react";

import { canvasGestureTracker } from "./gesture-tracker";
import { subscribeCamera } from "./camera-state";
import { subscribeSpatialStore } from "./spatial-store";

/**
 * Pre-allocated SVG group IDs the chrome layer paints into. Each
 * subsequent unit writes into one or two of these groups, never adds
 * new top-level groups — the contract is that adding an affordance is
 * "more <rect>/<path>/<text> elements inside one of these groups",
 * not "more groups at the top level".
 */
export const CHROME_GROUP_IDS = [
  /** U4: selection ring(s) around frame(s) in the current selection. */
  "chrome-selection",
  /** U5: hover ring around the deepest frame under the cursor. */
  "chrome-hover",
  /** U5: translucent padding bands on the containing autolayout. */
  "chrome-padding",
  /** U5: translucent gap fills between siblings of the containing autolayout. */
  "chrome-gap",
  /** U6: Alt-hover red distance lines between selection and hover targets. */
  "chrome-distance",
  /** U4: drag-from-whitespace marquee rectangle. */
  "chrome-marquee",
  /** U5: ancestor-chain breadcrumb chip. */
  "chrome-breadcrumb",
  /** U5: W×H dimension chip on hover target. */
  "chrome-dimension",
] as const;

export type ChromeGroupID = (typeof CHROME_GROUP_IDS)[number];

export interface ChromeLayerProps {
  /**
   * Identifies the active leaf canvas. Used in the `data-leaf-id`
   * attribute so vitest tests can scope assertions to a specific
   * leaf without relying on document order. Subsequent units may
   * scope their paint logic by leafID to support the future
   * atlas-overview parity work (Phase 2 of the initiative).
   */
  leafID: string;
}

/**
 * ChromeLayer — the screen-space SVG overlay sibling of `.lc-world`.
 * Always renders the same JSX (no props-driven content); content is
 * mutated by later units via refs into the pre-allocated groups.
 */
export function ChromeLayer({ leafID }: ChromeLayerProps): React.ReactElement {
  const svgRef = useRef<SVGSVGElement | null>(null);
  const groupRefs = useRef<Partial<Record<ChromeGroupID, SVGGElement | null>>>({});
  const rafPendingRef = useRef<number>(0);
  const needsPaintRef = useRef<boolean>(false);

  useEffect(() => {
    // Schedule a single paint per frame regardless of how many input
    // signals fired. The chrome layer reads (camera, spatial-store,
    // selection, hover) at paint-time, so coalescing N notifications
    // into one rAF tick keeps the read cost bounded to once per frame.
    function schedulePaint(): void {
      needsPaintRef.current = true;
      if (rafPendingRef.current !== 0) return;
      rafPendingRef.current = requestAnimationFrame(paint);
    }

    function paint(): void {
      rafPendingRef.current = 0;
      if (!needsPaintRef.current) return;
      needsPaintRef.current = false;
      // U4/U5/U6/U10 wire actual paint logic here. U1 keeps the loop
      // alive but writes nothing — the goal of the foundation unit is
      // to prove the subscription + rAF plumbing works, not to paint.
      //
      // Subsequent units read:
      //   const cam = getCamera();
      //   const rect = getNodeRect(screenID, nodeID);
      //   const sx = (rect.x - cam.x) * cam.z;
      //   ... mutate group children via groupRefs.current[<id>] ...
    }

    const unsubCamera = subscribeCamera(schedulePaint);
    const unsubSpatial = subscribeSpatialStore(schedulePaint);
    // Subscribe to gesture-tracker too — when a gesture settles, force
    // a paint so any stale chrome from the start of the gesture (e.g.,
    // a marquee mid-drag) updates to the final position.
    const unsubGesture = canvasGestureTracker.subscribe(schedulePaint);

    // Initial paint kick so the rAF loop primes immediately on mount.
    // Subsequent units can rely on at least one paint having occurred
    // before any user interaction.
    schedulePaint();

    return () => {
      unsubCamera();
      unsubSpatial();
      unsubGesture();
      if (rafPendingRef.current !== 0) {
        cancelAnimationFrame(rafPendingRef.current);
        rafPendingRef.current = 0;
      }
    };
  }, [leafID]);

  return (
    <svg
      ref={svgRef}
      className="leafcv2-chrome-layer"
      data-leaf-id={leafID}
      aria-hidden="true"
      // The SVG occupies the full stage; layered above the world's
      // transformed content but below interactive panels (lasso 90,
      // bulk panel 200, inspector etc.). z-index lives in CSS.
    >
      {CHROME_GROUP_IDS.map((id) => (
        <g
          key={id}
          id={id}
          data-group={id}
          ref={(el) => {
            groupRefs.current[id] = el;
          }}
        />
      ))}
    </svg>
  );
}

/**
 * Imperative accessor for non-React subscribers (e.g., a future
 * imperative paint hook in U5 that runs outside the React tree).
 * Returns the active chrome layer's SVG element if one is mounted,
 * or null. Multiple chrome layers active at once is unsupported —
 * we expect exactly one leaf canvas open at a time.
 *
 * Currently unused; included as scaffolding for U5's imperative
 * paint hooks so they can reach the group refs without prop drilling.
 */
let activeSvgRef: SVGSVGElement | null = null;
export function __setActiveChromeLayerForTesting(el: SVGSVGElement | null): void {
  activeSvgRef = el;
}
export function getActiveChromeLayer(): SVGSVGElement | null {
  return activeSvgRef;
}
