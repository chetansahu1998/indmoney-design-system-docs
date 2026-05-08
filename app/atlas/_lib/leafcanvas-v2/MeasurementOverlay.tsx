"use client";

/**
 * MeasurementOverlay — Phase 2 U3 scaffold.
 *
 * Owns the canvas-overlay layer for the four Phase 2 measurement
 * surfaces:
 *   - U4: distance lines from selected → hovered atomic
 *   - U5: padding bands on hovered autolayout FRAME
 *   - U6: gap markers between siblings of hovered FRAME
 *   - U8: persistent W×H + (X, Y) chips on selected node
 *
 * Single component instead of four because all four surfaces share
 * the same coordinate system (wrapper-local), the same lookup
 * helpers (find-by-figmaID), the same gesture-end gating, and
 * the same SVG layer. Splitting them would mean four separate
 * gesture subscriptions and four tree walks per pointer-move.
 *
 * Mounted as a sibling of <StatePicker> and <HoverTooltip> inside
 * the .leafcv2-frame wrapper. Inherits the canvas world transform
 * automatically — no separate projection math, no IntersectionObserver
 * (which would deadlock under the transform-parent footgun).
 *
 * Initial render is empty SVG. U4-U8 land content in subsequent
 * commits — each adds a render branch behind a feature gate
 * (hover present, selection present, hovered FRAME with autolayout,
 * etc.) so unrelated surfaces don't pay each other's render cost.
 *
 * Gesture gating: subscribes to canvasGestureTracker.subscribe()
 * for the gesture-end signal. During an active pan/zoom gesture,
 * `getIsGesturing()` returns true and we suppress overlay paint
 * to avoid jitter at 60×/sec. On settle (~150ms after last gesture
 * tick), the subscription forces a re-render so the overlay
 * re-paints at the final camera position.
 *
 * pointer-events: none on the SVG — no overlay element should ever
 * swallow a click meant for an atomic.
 */

import { useEffect, useMemo, useState, type ReactElement } from "react";

import { findByFigmaID } from "./AtomicChildInspector";
import { canvasGestureTracker, getIsGesturing } from "./gesture-tracker";
import { useHoveredAtomicChild } from "./hover-signal";
import { useAtlas } from "../../../../lib/atlas/live-store";
import type { AnnotatedNode, BoundingBox, CanonicalNode } from "./types";

export interface MeasurementOverlayProps {
  /** Screen ID this overlay paints for. Cross-frame state ignored. */
  screenID: string;
  /** Frame's bounding box; rebases wrapper-local coords. */
  frameBBox: BoundingBox;
  /** The pruned canonical_tree the renderer is currently emitting. */
  tree: AnnotatedNode | null;
}

interface DistanceSegment {
  direction: "top" | "bottom" | "left" | "right";
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  distancePx: number;
}

/**
 * Compute up to 4 cardinal distance segments from the selected
 * bbox `S` to the hovered bbox `H`, both in wrapper-local coords.
 *
 * Segment is emitted only when there's a positive gap in that
 * direction — overlapping axes contribute nothing. Lines are
 * drawn perpendicular outward from S's edge midpoints, matching
 * Zeplin's spec ("solid measurement lines from the edges of A to
 * the nearest edges of B in up to 4 cardinal directions").
 *
 * Two no-op cases:
 *   - S === H — selected and hovered are the same node, no lines.
 *     Caller should suppress before invoking. We still defensively
 *     return [] when both bboxes are identical.
 *   - H fully inside S (or vice versa) — no positive gap in any
 *     cardinal, returns [].
 */
export function computeDistanceSegments(s: BoundingBox, h: BoundingBox): DistanceSegment[] {
  const segments: DistanceSegment[] = [];
  const sCenterX = s.x + s.width / 2;
  const sCenterY = s.y + s.height / 2;
  const sRight = s.x + s.width;
  const sBottom = s.y + s.height;
  const hRight = h.x + h.width;
  const hBottom = h.y + h.height;

  // TOP: H entirely above S
  if (hBottom < s.y) {
    segments.push({
      direction: "top",
      x1: sCenterX,
      y1: s.y,
      x2: sCenterX,
      y2: hBottom,
      distancePx: s.y - hBottom,
    });
  }
  // BOTTOM: H entirely below S
  if (h.y > sBottom) {
    segments.push({
      direction: "bottom",
      x1: sCenterX,
      y1: sBottom,
      x2: sCenterX,
      y2: h.y,
      distancePx: h.y - sBottom,
    });
  }
  // LEFT: H entirely left of S
  if (hRight < s.x) {
    segments.push({
      direction: "left",
      x1: s.x,
      y1: sCenterY,
      x2: hRight,
      y2: sCenterY,
      distancePx: s.x - hRight,
    });
  }
  // RIGHT: H entirely right of S
  if (h.x > sRight) {
    segments.push({
      direction: "right",
      x1: sRight,
      y1: sCenterY,
      x2: h.x,
      y2: sCenterY,
      distancePx: h.x - sRight,
    });
  }
  return segments;
}

export function MeasurementOverlay(props: MeasurementOverlayProps) {
  const { screenID, frameBBox, tree } = props;

  const hovered = useHoveredAtomicChild();
  const selected = useAtlas((s) => s.selection.selectedAtomicChild);

  // Force re-render on gesture-end so the overlay paints at the
  // settled camera position. We don't render during an active
  // gesture (jitter would be visible), but we DO render the moment
  // it ends.
  const [, setSettleTick] = useState(0);
  useEffect(() => {
    const off = canvasGestureTracker.subscribe((gesturing) => {
      if (!gesturing) setSettleTick((n) => n + 1);
    });
    return off;
  }, []);

  // Cheap exits.
  if (!tree) return null;
  // Suppress paint mid-gesture; the settle subscriber will trigger
  // the next paint.
  if (getIsGesturing()) return null;

  const hoveredHere = hovered && hovered.screenID === screenID;
  const selectedHere = selected && selected.screenID === screenID;

  // Nothing to draw — render an empty <svg> so React's reconciliation
  // stays cheap and DOM presence is consistent for downstream Playwright
  // queries.
  if (!hoveredHere && !selectedHere) {
    return <svg className="leafcv2-measurement" data-screen-id={screenID} />;
  }

  return (
    <svg
      className="leafcv2-measurement"
      data-screen-id={screenID}
      data-frame-w={Math.round(frameBBox.width)}
      data-frame-h={Math.round(frameBBox.height)}
      width={frameBBox.width}
      height={frameBBox.height}
      viewBox={`0 0 ${frameBBox.width} ${frameBBox.height}`}
      style={{
        position: "absolute",
        left: 0,
        top: 0,
        pointerEvents: "none",
      }}
    >
      {/* U4 — distance lines from selected → hovered. */}
      <DistanceLines
        tree={tree}
        frameBBox={frameBBox}
        hoveredFigmaID={hoveredHere ? hovered.figmaNodeID : null}
        selectedFigmaID={selectedHere ? selected.figmaNodeID : null}
      />
      {/* U5 — padding bands on hovered autolayout FRAME. */}
      <PaddingBands
        tree={tree}
        frameBBox={frameBBox}
        hoveredFigmaID={hoveredHere ? hovered.figmaNodeID : null}
      />
      {/* U6 — gap markers between siblings of hovered autolayout FRAME. */}
      <GapMarkers
        tree={tree}
        frameBBox={frameBBox}
        hoveredFigmaID={hoveredHere ? hovered.figmaNodeID : null}
      />
      {/* U8 lands here in a subsequent commit. */}
    </svg>
  );
}

/**
 * U4 — distance lines + integer-px labels rendered as SVG.
 * Suppresses when selected === hovered (Zeplin's no-op case).
 */
function DistanceLines(props: {
  tree: AnnotatedNode;
  frameBBox: BoundingBox;
  hoveredFigmaID: string | null;
  selectedFigmaID: string | null;
}): ReactElement | null {
  const { tree, frameBBox, hoveredFigmaID, selectedFigmaID } = props;

  // Need both a hover and a selection to draw distance lines.
  if (!hoveredFigmaID || !selectedFigmaID) return null;
  // No-op when same node hovered and selected.
  if (hoveredFigmaID === selectedFigmaID) return null;

  const segments = useMemo<DistanceSegment[]>(() => {
    const sBBox = lookupBBox(tree, selectedFigmaID, frameBBox);
    const hBBox = lookupBBox(tree, hoveredFigmaID, frameBBox);
    if (!sBBox || !hBBox) return [];
    return computeDistanceSegments(sBBox, hBBox);
  }, [tree, frameBBox, selectedFigmaID, hoveredFigmaID]);

  if (segments.length === 0) return null;

  return (
    <g className="leafcv2-measurement__distance-lines">
      {segments.map((seg) => (
        <DistanceLine key={seg.direction} segment={seg} />
      ))}
    </g>
  );
}

/** Render a single line + its midpoint label. */
function DistanceLine(props: { segment: DistanceSegment }): ReactElement {
  const { segment } = props;
  // Midpoint in wrapper-local coords for the label position.
  const midX = (segment.x1 + segment.x2) / 2;
  const midY = (segment.y1 + segment.y2) / 2;
  const label = String(Math.round(segment.distancePx));

  return (
    <g data-direction={segment.direction}>
      <line
        x1={segment.x1}
        y1={segment.y1}
        x2={segment.x2}
        y2={segment.y2}
        stroke="#ef4444"
        strokeWidth={1}
        shapeRendering="crispEdges"
      />
      {/* Tick caps at each end so a 0-distance line is still visible
          (degenerate case — caller filters it). */}
      <DistanceLabel x={midX} y={midY} text={label} />
    </g>
  );
}

/** Pill-shape label with white text on red background. */
function DistanceLabel(props: { x: number; y: number; text: string }): ReactElement {
  const { x, y, text } = props;
  const padX = 4;
  const padY = 2;
  // Approximate text width (monospace digits average ~7px at 11px font).
  const charW = 7;
  const w = text.length * charW + padX * 2;
  const h = 14;
  return (
    <g>
      <rect
        x={x - w / 2}
        y={y - h / 2}
        width={w}
        height={h}
        rx={3}
        ry={3}
        fill="#ef4444"
      />
      <text
        x={x}
        y={y + padY + 1}
        textAnchor="middle"
        dominantBaseline="middle"
        fill="#ffffff"
        fontSize={11}
        fontFamily="-apple-system, BlinkMacSystemFont, Inter, system-ui, sans-serif"
        fontWeight={600}
        style={{ fontVariantNumeric: "tabular-nums" }}
      >
        {text}
      </text>
    </g>
  );
}

/**
 * U5 — padding bands on a hovered autolayout FRAME.
 *
 * When the hovered node is a FRAME with layoutMode "HORIZONTAL" or
 * "VERTICAL", paint up to 4 translucent bands inside its bbox at
 * the top/right/bottom/left edges sized to paddingTop/Right/
 * Bottom/Left from canonical_tree. Skip bands where padding=0.
 *
 * Direct-on-canvas (Figma Dev Mode pattern), not panel-mediated.
 * Lower cognitive load than Zeplin's "hover the panel row to
 * highlight on canvas" model.
 */
function PaddingBands(props: {
  tree: AnnotatedNode;
  frameBBox: BoundingBox;
  hoveredFigmaID: string | null;
}): ReactElement | null {
  const { tree, frameBBox, hoveredFigmaID } = props;

  const data = useMemo(() => {
    if (!hoveredFigmaID) return null;
    const found = findByFigmaID(tree, hoveredFigmaID);
    if (!found) return null;
    const node = found.node as CanonicalNode;
    if (node.type !== "FRAME") return null;
    const layoutMode = node.layoutMode;
    if (layoutMode !== "HORIZONTAL" && layoutMode !== "VERTICAL") return null;
    const bb = node.absoluteBoundingBox;
    if (!bb) return null;
    const localBBox: BoundingBox = {
      x: bb.x - frameBBox.x,
      y: bb.y - frameBBox.y,
      width: bb.width,
      height: bb.height,
    };
    return {
      bbox: localBBox,
      paddingTop: numberOr(node.paddingTop, 0),
      paddingRight: numberOr(node.paddingRight, 0),
      paddingBottom: numberOr(node.paddingBottom, 0),
      paddingLeft: numberOr(node.paddingLeft, 0),
    };
  }, [tree, frameBBox, hoveredFigmaID]);

  if (!data) return null;
  const { bbox, paddingTop, paddingRight, paddingBottom, paddingLeft } = data;
  if (paddingTop === 0 && paddingRight === 0 && paddingBottom === 0 && paddingLeft === 0) {
    return null;
  }

  const fill = "rgba(255, 152, 0, 0.18)";
  const stroke = "rgba(255, 152, 0, 0.45)";

  return (
    <g className="leafcv2-measurement__padding-bands" data-figma-id={hoveredFigmaID}>
      {paddingTop > 0 && (
        <PaddingBand
          x={bbox.x}
          y={bbox.y}
          w={bbox.width}
          h={paddingTop}
          fill={fill}
          stroke={stroke}
          band="paddingTop"
          label={String(Math.round(paddingTop))}
        />
      )}
      {paddingBottom > 0 && (
        <PaddingBand
          x={bbox.x}
          y={bbox.y + bbox.height - paddingBottom}
          w={bbox.width}
          h={paddingBottom}
          fill={fill}
          stroke={stroke}
          band="paddingBottom"
          label={String(Math.round(paddingBottom))}
        />
      )}
      {paddingLeft > 0 && (
        <PaddingBand
          x={bbox.x}
          y={bbox.y + paddingTop}
          w={paddingLeft}
          h={bbox.height - paddingTop - paddingBottom}
          fill={fill}
          stroke={stroke}
          band="paddingLeft"
          label={String(Math.round(paddingLeft))}
        />
      )}
      {paddingRight > 0 && (
        <PaddingBand
          x={bbox.x + bbox.width - paddingRight}
          y={bbox.y + paddingTop}
          w={paddingRight}
          h={bbox.height - paddingTop - paddingBottom}
          fill={fill}
          stroke={stroke}
          band="paddingRight"
          label={String(Math.round(paddingRight))}
        />
      )}
    </g>
  );
}

function PaddingBand(props: {
  x: number;
  y: number;
  w: number;
  h: number;
  fill: string;
  stroke: string;
  band: string;
  label: string;
}): ReactElement {
  const { x, y, w, h, fill, stroke, band, label } = props;
  // Label centered in the band. For thin bands (≤ 12px in the short
  // axis), the label may overflow — that's fine, SVG overflow:visible.
  const labelX = x + w / 2;
  const labelY = y + h / 2;
  return (
    <g data-band={band}>
      <rect x={x} y={y} width={w} height={h} fill={fill} stroke={stroke} strokeWidth={0.5} />
      <text
        x={labelX}
        y={labelY}
        textAnchor="middle"
        dominantBaseline="middle"
        fill="#92400e"
        fontSize={11}
        fontWeight={600}
        fontFamily="-apple-system, BlinkMacSystemFont, Inter, system-ui, sans-serif"
        style={{ fontVariantNumeric: "tabular-nums" }}
      >
        {label}
      </text>
    </g>
  );
}

function numberOr(v: unknown, fallback: number): number {
  return typeof v === "number" && Number.isFinite(v) ? v : fallback;
}

/**
 * U6 — gap markers between siblings of a hovered autolayout FRAME.
 *
 * Direct-on-canvas (Figma Dev Mode pattern, not Zeplin's panel-
 * mediated model). Hover an autolayout parent → pink bands appear
 * automatically between consecutive children with the gap value.
 *
 * Skips:
 *   - Non-FRAME hovered nodes
 *   - layoutMode === NONE
 *   - itemSpacing === 0 (no gap to show)
 *   - Fewer than 2 children (no pairs)
 *   - primaryAxisAlignItems === SPACE_BETWEEN (gap is dynamic; the
 *     itemSpacing field is unused — surface a "Variable gap" hint
 *     so the user understands the gap value isn't fixed)
 */
function GapMarkers(props: {
  tree: AnnotatedNode;
  frameBBox: BoundingBox;
  hoveredFigmaID: string | null;
}): ReactElement | null {
  const { tree, frameBBox, hoveredFigmaID } = props;

  const data = useMemo(() => {
    if (!hoveredFigmaID) return null;
    const found = findByFigmaID(tree, hoveredFigmaID);
    if (!found) return null;
    const node = found.node as CanonicalNode;
    if (node.type !== "FRAME") return null;
    const layoutMode = node.layoutMode;
    if (layoutMode !== "HORIZONTAL" && layoutMode !== "VERTICAL") return null;
    const itemSpacing = numberOr(node.itemSpacing, 0);
    const primary = (node as { primaryAxisAlignItems?: string }).primaryAxisAlignItems;
    const isSpaceBetween = primary === "SPACE_BETWEEN";
    const children = Array.isArray(node.children) ? node.children : [];
    if (children.length < 2) return null;
    if (itemSpacing === 0 && !isSpaceBetween) return null;
    // Convert each child's bbox to wrapper-local coords + filter to
    // ones with valid bboxes.
    const childBoxes: BoundingBox[] = [];
    for (const c of children) {
      const cn = c as CanonicalNode;
      const bb = cn.absoluteBoundingBox;
      if (!bb) continue;
      childBoxes.push({
        x: bb.x - frameBBox.x,
        y: bb.y - frameBBox.y,
        width: bb.width,
        height: bb.height,
      });
    }
    if (childBoxes.length < 2) return null;
    return {
      layoutMode,
      itemSpacing,
      isSpaceBetween,
      childBoxes,
    };
  }, [tree, frameBBox, hoveredFigmaID]);

  if (!data) return null;

  const fill = "rgba(236, 72, 153, 0.22)";
  const stroke = "rgba(236, 72, 153, 0.55)";

  // SPACE_BETWEEN — render a single "Variable gap" badge centered on
  // the parent rather than per-pair (the gap is dynamic).
  if (data.isSpaceBetween) {
    return (
      <g
        className="leafcv2-measurement__gap-markers"
        data-figma-id={hoveredFigmaID}
        data-variable-gap="true"
      >
        {/* No band for SPACE_BETWEEN — emitting a hint via DOM only;
            visual treatment is the inspector's job (U10). */}
      </g>
    );
  }

  return (
    <g className="leafcv2-measurement__gap-markers" data-figma-id={hoveredFigmaID}>
      {data.childBoxes.slice(0, -1).map((curr, i) => {
        const next = data.childBoxes[i + 1];
        const gap = data.itemSpacing;
        if (data.layoutMode === "VERTICAL") {
          // Gap is between curr.bottom and next.top.
          const top = curr.y + curr.height;
          const left = Math.max(curr.x, next.x);
          const right = Math.min(curr.x + curr.width, next.x + next.width);
          if (right <= left) return null; // no horizontal overlap
          return (
            <GapBand
              key={i}
              x={left}
              y={top}
              w={right - left}
              h={gap}
              fill={fill}
              stroke={stroke}
              label={String(Math.round(gap))}
            />
          );
        }
        // HORIZONTAL: gap between curr.right and next.left
        const leftEdge = curr.x + curr.width;
        const top = Math.max(curr.y, next.y);
        const bot = Math.min(curr.y + curr.height, next.y + next.height);
        if (bot <= top) return null;
        return (
          <GapBand
            key={i}
            x={leftEdge}
            y={top}
            w={gap}
            h={bot - top}
            fill={fill}
            stroke={stroke}
            label={String(Math.round(gap))}
          />
        );
      })}
    </g>
  );
}

function GapBand(props: {
  x: number;
  y: number;
  w: number;
  h: number;
  fill: string;
  stroke: string;
  label: string;
}): ReactElement | null {
  const { x, y, w, h, fill, stroke, label } = props;
  // Skip degenerate (zero-area) bands so we don't litter SVG with
  // invisible <rect>s.
  if (w <= 0 || h <= 0) return null;
  return (
    <g data-band="gap">
      <rect x={x} y={y} width={w} height={h} fill={fill} stroke={stroke} strokeWidth={0.5} />
      <text
        x={x + w / 2}
        y={y + h / 2}
        textAnchor="middle"
        dominantBaseline="middle"
        fill="#9d174d"
        fontSize={11}
        fontWeight={600}
        fontFamily="-apple-system, BlinkMacSystemFont, Inter, system-ui, sans-serif"
        style={{ fontVariantNumeric: "tabular-nums" }}
      >
        {label}
      </text>
    </g>
  );
}

/**
 * Look up a node by figmaID and return its wrapper-local bbox.
 * Wrapper-local = absolute - frameBBox origin.
 */
function lookupBBox(
  tree: CanonicalNode,
  figmaID: string,
  frameBBox: BoundingBox,
): BoundingBox | null {
  const found = findByFigmaID(tree, figmaID);
  if (!found) return null;
  const bb = found.node.absoluteBoundingBox;
  if (!bb) return null;
  return {
    x: bb.x - frameBBox.x,
    y: bb.y - frameBBox.y,
    width: bb.width,
    height: bb.height,
  };
}
