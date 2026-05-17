"use client";

/* eslint-disable react-hooks/exhaustive-deps -- rAF callback closures are
   intentionally created once on mount; deps are mutated via refs. */

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
import {
  getHoveredAtomicChild,
  subscribeHoveredAtomic,
  type HoveredAtomicChild,
} from "./hover-signal";
import { isAltHeld, subscribeHeldKey } from "./keymap";
import { useAtlas } from "../../../../lib/atlas/live-store";

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
      // U5 — pragmatic DOM-driven paint: read getBoundingClientRect
      // of the selected + hovered element via querySelector. Avoids
      // the spatial-store population work U1 deferred; the chrome
      // layer pays one layout read per state change, not per node.
      // For a leaf with ≤200 frames the cost is bounded.
      paintSelectionAndHover(svgRef.current, groupRefs.current);
    }

    const unsubCamera = subscribeCamera(schedulePaint);
    const unsubSpatial = subscribeSpatialStore(schedulePaint);
    // Subscribe to gesture-tracker too — when a gesture settles, force
    // a paint so any stale chrome from the start of the gesture (e.g.,
    // a marquee mid-drag) updates to the final position.
    const unsubGesture = canvasGestureTracker.subscribe(schedulePaint);
    // U5 — hover signal drives the hover outline; selection changes
    // come from the Zustand store (subscribed via useAtlas below).
    const unsubHover = subscribeHoveredAtomic(schedulePaint);
    const unsubSelection = useAtlas.subscribe(schedulePaint);
    // U6 — Alt-held state drives the distance-line paint.
    const unsubHeld = subscribeHeldKey(schedulePaint);

    // Initial paint kick so the rAF loop primes immediately on mount.
    // Subsequent units can rely on at least one paint having occurred
    // before any user interaction.
    schedulePaint();

    return () => {
      unsubCamera();
      unsubSpatial();
      unsubGesture();
      unsubHover();
      unsubSelection();
      unsubHeld();
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

// ─── U5 paint logic ────────────────────────────────────────────────────
//
// Reads the current selection + hover state and writes screen-space
// rects into the chrome-selection / chrome-hover groups. Pure-DOM
// queries avoid the spatial-store population U1 deferred; the cost
// is one getBoundingClientRect per selected + per hovered node, which
// is fine at our current scale (≤200 frames per leaf, ≤1 selected,
// ≤1 hovered at a time).
//
// Selection ring: 2px Figma blue (#0d99ff via --lcv2-selection).
// Hover outline: 2px Figma blue at reduced opacity (0.6) so a
// selected-and-hovered frame reads as "selected" first.
//
// When the chrome layer paints a frame's rect, the rect coords are
// already screen-space (getBoundingClientRect returns viewport-
// relative coords). Since the chrome layer is itself a screen-space
// sibling of .lc-world with `position: absolute; inset: 0`, we need
// to subtract the chrome layer's own bounding-rect origin to get
// chrome-local coords.

function paintSelectionAndHover(
  svg: SVGSVGElement | null,
  groups: Partial<Record<string, SVGGElement | null>>,
): void {
  if (!svg) return;
  const selectionGroup = groups["chrome-selection"];
  const hoverGroup = groups["chrome-hover"];
  const distanceGroup = groups["chrome-distance"];
  if (!selectionGroup || !hoverGroup || !distanceGroup) return;

  // Anchor: chrome-layer's own bounding rect. getBoundingClientRect on
  // any target returns viewport coords; subtract this anchor to land
  // in chrome-local coords (which is also the SVG's coordinate space).
  const anchorRect = svg.getBoundingClientRect();

  clearGroup(selectionGroup);
  clearGroup(hoverGroup);
  clearGroup(distanceGroup);

  // Selection ring (single-select for now; bulk-select reuses the
  // same group when U10's inspector exposes it).
  const sel = readSelection();
  let selRect: NodeRect | null = null;
  if (sel) {
    selRect = lookupRectForNode(sel.screenID, sel.figmaNodeID);
    if (selRect) {
      drawOutline(selectionGroup, selRect, anchorRect, "selection");
    }
  }

  // Hover outline. Suppress when hover target equals selection (a
  // selected-and-hovered frame reads as "selected" first per the
  // brainstorm Key Decision; the dedicated hover overlay would just
  // double-stroke at the same coords).
  const hov = getHoveredAtomicChild();
  let hovRect: NodeRect | null = null;
  if (hov && (!sel || hov.figmaNodeID !== sel.figmaNodeID)) {
    hovRect = lookupRectForNode(hov.screenID, hov.figmaNodeID);
    if (hovRect) {
      drawOutline(hoverGroup, hovRect, anchorRect, "hover");
    }
  }

  // U6 — Alt-hover distance lines. With a node selected AND a
  // different node hovered AND the Alt key held, paint four red
  // distance segments + px labels between the selection rect and
  // the hover rect. Bounds-to-bounds semantics (top/right/bottom/
  // left gap). Suppresses cleanly when any prerequisite is absent.
  if (selRect && hovRect && isAltHeld()) {
    drawDistanceLines(distanceGroup, selRect, hovRect, anchorRect);
  }
}

function clearGroup(g: SVGGElement): void {
  while (g.firstChild) g.removeChild(g.firstChild);
}

interface NodeRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

/**
 * Look up the screen-rect of a figma node by querying the DOM. The
 * canonical_tree renderer tags every node with `data-figma-id`;
 * `document.querySelector` finds it in O(tree depth). We could cache
 * this in spatial-store but at our current scale (≤1 selection,
 * ≤1 hover) it's not load-bearing.
 *
 * Returns null when the node isn't currently rendered (off-screen,
 * skeleton state, or the canonical_tree hasn't hydrated). The chrome
 * layer simply paints nothing for that frame in that case — correct
 * fallback (no stale ring on a frame that's not visible).
 */
function lookupRectForNode(_screenID: string, figmaNodeID: string): NodeRect | null {
  if (typeof document === "undefined") return null;
  // Escape CSS attr values that contain ':' (Figma node ids use it).
  const escaped = cssEscape(figmaNodeID);
  const el = document.querySelector<HTMLElement>(`[data-figma-id="${escaped}"]`);
  if (!el) return null;
  const r = el.getBoundingClientRect();
  return { left: r.left, top: r.top, width: r.width, height: r.height };
}

function cssEscape(s: string): string {
  // Minimal CSS attribute-value escape: prepend backslash to ':' and
  // backslash. Sufficient for Figma node ids ('I12:34', '12:34').
  return s.replace(/([\\:])/g, "\\$1");
}

function drawOutline(
  group: SVGGElement,
  rect: NodeRect,
  anchor: { left: number; top: number },
  kind: "selection" | "hover",
): void {
  const r = document.createElementNS("http://www.w3.org/2000/svg", "rect");
  r.setAttribute("x", String(rect.left - anchor.left));
  r.setAttribute("y", String(rect.top - anchor.top));
  r.setAttribute("width", String(rect.width));
  r.setAttribute("height", String(rect.height));
  r.setAttribute("fill", "none");
  r.setAttribute(
    "stroke",
    kind === "selection" ? "var(--lcv2-selection, #0d99ff)" : "var(--lcv2-hover, #0d99ff)",
  );
  r.setAttribute("stroke-width", "2");
  r.setAttribute("vector-effect", "non-scaling-stroke");
  if (kind === "hover") {
    r.setAttribute("opacity", "0.6");
  }
  // pointer-events:none on the wrapping SVG already; per-rect not needed.
  group.appendChild(r);
}

/**
 * Read the active single-select from the Zustand store. Done
 * imperatively so the chrome-layer paint loop avoids forcing React
 * re-renders for selection changes — useAtlas.getState() pulls the
 * latest snapshot without subscribing the component.
 */
function readSelection(): HoveredAtomicChild | null {
  const sel = useAtlas.getState().selection.selectedAtomicChild;
  if (!sel || !sel.screenID || !sel.figmaNodeID) return null;
  return { screenID: sel.screenID, figmaNodeID: sel.figmaNodeID };
}

// ─── U6 — Alt-hover distance lines ─────────────────────────────────────
//
// Paints four cardinal distance segments + px labels between the
// selection bbox and the hover bbox. Bounds-to-bounds semantics
// (top edge of one to bottom edge of the other, etc.) — matches
// Figma Dev Mode's Alt-hover behavior. Color: vermillion #f24822
// via --lcv2-distance CSS var (defined in canvas-v2.css). Stroke
// stays 2px regardless of camera zoom (chrome layer is screen-space,
// vector-effect non-scaling-stroke on the line).

/**
 * Compute the four cardinal gap distances between two rects. Each
 * value is the screen-px distance from one rect's bounding edge to
 * the other's, in the cardinal direction (top/right/bottom/left).
 * Negative values mean the rects overlap on that axis (no gap to
 * paint — distance line is suppressed for that direction).
 *
 * Exported for testing.
 */
export interface CardinalDistances {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

export function computeCardinalDistances(
  selection: NodeRect,
  target: NodeRect,
): CardinalDistances {
  // top: gap from selection's top edge to target's bottom edge when
  // target is above selection. Otherwise 0 (no top gap).
  const selBottom = selection.top + selection.height;
  const tarBottom = target.top + target.height;
  return {
    top: selection.top - tarBottom,
    right: target.left - (selection.left + selection.width),
    bottom: target.top - selBottom,
    left: selection.left - (target.left + target.width),
  };
}

function drawDistanceLines(
  group: SVGGElement,
  selection: NodeRect,
  target: NodeRect,
  anchor: { left: number; top: number },
): void {
  const distances = computeCardinalDistances(selection, target);
  // Center-axis of the selection rect (used as the line endpoint for
  // top/bottom segments; right/left segments hang off the middle
  // vertical / horizontal of the selection).
  const selCx = selection.left + selection.width / 2 - anchor.left;
  const selCy = selection.top + selection.height / 2 - anchor.top;
  const selL = selection.left - anchor.left;
  const selT = selection.top - anchor.top;
  const selR = selL + selection.width;
  const selB = selT + selection.height;
  const tarCx = target.left + target.width / 2 - anchor.left;
  const tarCy = target.top + target.height / 2 - anchor.top;
  const tarL = target.left - anchor.left;
  const tarT = target.top - anchor.top;
  const tarR = tarL + target.width;
  const tarB = tarT + target.height;

  // Top gap: target sits above selection. Line goes from target's
  // bottom edge to selection's top edge along the selection's
  // horizontal-center axis.
  if (distances.top > 0) {
    drawDistanceSegment(
      group,
      selCx,
      tarB,
      selCx,
      selT,
      distances.top,
      "vertical",
    );
  }
  // Bottom gap: target sits below selection.
  if (distances.bottom > 0) {
    drawDistanceSegment(
      group,
      selCx,
      selB,
      selCx,
      tarT,
      distances.bottom,
      "vertical",
    );
  }
  // Right gap: target sits to the right of selection.
  if (distances.right > 0) {
    drawDistanceSegment(
      group,
      selR,
      selCy,
      tarL,
      selCy,
      distances.right,
      "horizontal",
    );
  }
  // Left gap: target sits to the left of selection.
  if (distances.left > 0) {
    drawDistanceSegment(
      group,
      tarR,
      selCy,
      selL,
      selCy,
      distances.left,
      "horizontal",
    );
  }
  // Suppress: rects overlap on the axis or no gap to display. Future
  // U6b can add "inside" distance (when target is contained within
  // selection) using padding-style chips, but Figma's default behavior
  // is to show nothing in that case.

  // Reference-rect outlines on both target and selection so the user
  // sees what the distances measure between. Light, dashed, distance-
  // color. Suppressed when target is the selection (caught by caller).
  drawReferenceOutline(group, tarL, tarT, tarR - tarL, tarB - tarT);
  void [selCx, selCy, selR, selB]; // referenced above; no-op silencer
}

function drawDistanceSegment(
  group: SVGGElement,
  x1: number,
  y1: number,
  x2: number,
  y2: number,
  distancePx: number,
  orientation: "vertical" | "horizontal",
): void {
  const ns = "http://www.w3.org/2000/svg";
  const line = document.createElementNS(ns, "line");
  line.setAttribute("x1", String(x1));
  line.setAttribute("y1", String(y1));
  line.setAttribute("x2", String(x2));
  line.setAttribute("y2", String(y2));
  line.setAttribute("stroke", "var(--lcv2-distance, #f24822)");
  line.setAttribute("stroke-width", "1");
  line.setAttribute("vector-effect", "non-scaling-stroke");
  group.appendChild(line);

  // px label at the segment midpoint. Small backing rect for
  // readability against busy canvas content.
  const midX = (x1 + x2) / 2;
  const midY = (y1 + y2) / 2;
  const label = `${Math.round(distancePx)}px`;
  const text = document.createElementNS(ns, "text");
  text.setAttribute("x", String(midX));
  text.setAttribute("y", String(midY));
  text.setAttribute("fill", "var(--lcv2-distance, #f24822)");
  text.setAttribute("font-size", "11");
  text.setAttribute("font-family", "Inter, system-ui, sans-serif");
  text.setAttribute("text-anchor", "middle");
  text.setAttribute("dominant-baseline", "central");
  // Offset perpendicular to the segment so the label doesn't sit on
  // top of the line itself.
  if (orientation === "vertical") {
    text.setAttribute("dx", "8");
  } else {
    text.setAttribute("dy", "-4");
  }
  text.textContent = label;
  group.appendChild(text);
}

function drawReferenceOutline(
  group: SVGGElement,
  x: number,
  y: number,
  w: number,
  h: number,
): void {
  const ns = "http://www.w3.org/2000/svg";
  const r = document.createElementNS(ns, "rect");
  r.setAttribute("x", String(x));
  r.setAttribute("y", String(y));
  r.setAttribute("width", String(w));
  r.setAttribute("height", String(h));
  r.setAttribute("fill", "none");
  r.setAttribute("stroke", "var(--lcv2-distance, #f24822)");
  r.setAttribute("stroke-width", "1");
  r.setAttribute("stroke-dasharray", "4 2");
  r.setAttribute("opacity", "0.6");
  r.setAttribute("vector-effect", "non-scaling-stroke");
  group.appendChild(r);
}
