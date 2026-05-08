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
import { useHoveredAtomicChild, useHoveredBandHint } from "./hover-signal";
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

  // ALL hooks first — React's rule of hooks. No early returns above
  // this block; conditional returns live after every useFoo call.
  const hovered = useHoveredAtomicChild();
  const selected = useAtlas((s) => s.selection.selectedAtomicChild);
  const bandHint = useHoveredBandHint();

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

  // Cheap exits — all hooks have already run.
  if (!tree) return null;
  // Suppress paint mid-gesture; the settle subscriber will trigger
  // the next paint.
  if (getIsGesturing()) return null;

  const hoveredHere = hovered && hovered.screenID === screenID;
  const selectedHere = selected && selected.screenID === screenID;
  // U10 — band-hint can fire bands even without canvas hover/select
  // when the inspector pushes a hint targeting a node in this frame.
  // Cheap pre-check: does the hint reference a node that lives in
  // this frame? PaddingBands and GapMarkers do their own per-frame
  // walks; this gate only decides whether to render the sized <svg>
  // wrapper at all (otherwise the early exit returns the placeholder).
  const hintFramesThisScreen =
    bandHint && bandHint.nodeID && treeContainsNode(tree, bandHint.nodeID);

  // Nothing to draw — render an empty <svg> so React's reconciliation
  // stays cheap and DOM presence is consistent for downstream Playwright
  // queries.
  if (!hoveredHere && !selectedHere && !hintFramesThisScreen) {
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
      {/* U5 — padding bands on hovered autolayout FRAME. U10 also shows
          bands when the inspector pushes a band-hint targeting an
          autolayout FRAME (selected or hovered). */}
      <PaddingBands
        tree={tree}
        frameBBox={frameBBox}
        hoveredFigmaID={hoveredHere ? hovered.figmaNodeID : null}
      />
      {/* U6 — gap markers. Same hint composition as PaddingBands. */}
      <GapMarkers
        tree={tree}
        frameBBox={frameBBox}
        hoveredFigmaID={hoveredHere ? hovered.figmaNodeID : null}
      />
      {/* U8 — persistent W×H + (X,Y) chip on the selected node. */}
      <SelectionChip
        tree={tree}
        frameBBox={frameBBox}
        selectedFigmaID={selectedHere ? selected.figmaNodeID : null}
      />
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

  // Hook FIRST — early returns BEFORE useMemo would violate the
  // rule of hooks across re-renders (when hovered/selected flip
  // from null → set, the second render would call a hook the first
  // render didn't, producing "Rendered fewer hooks than expected"
  // in dev). Move all conditions into the memo's body and return [].
  const segments = useMemo<DistanceSegment[]>(() => {
    if (!hoveredFigmaID || !selectedFigmaID) return [];
    if (hoveredFigmaID === selectedFigmaID) return [];
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
  const hint = useHoveredBandHint();
  // Fallback trigger: if the user hovers a Layout Widget row in the
  // inspector for a node that's NOT the currently-hovered atomic on
  // canvas (e.g. they have a card selected and are reading its
  // Layout Widget without hovering the canvas), render the bands
  // for that hint's nodeID. The visual outcome is identical to
  // canvas-hover: bands appear on the autolayout FRAME they belong to.
  const focusFigmaID = hoveredFigmaID ?? hint?.nodeID ?? null;
  const highlightedBand = hint?.band ?? null;

  const data = useMemo(() => {
    if (!focusFigmaID) return null;
    const found = findByFigmaID(tree, focusFigmaID);
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
      figmaID: focusFigmaID,
      bbox: localBBox,
      paddingTop: numberOr(node.paddingTop, 0),
      paddingRight: numberOr(node.paddingRight, 0),
      paddingBottom: numberOr(node.paddingBottom, 0),
      paddingLeft: numberOr(node.paddingLeft, 0),
    };
  }, [tree, frameBBox, focusFigmaID]);

  if (!data) return null;
  const { bbox, paddingTop, paddingRight, paddingBottom, paddingLeft } = data;
  if (paddingTop === 0 && paddingRight === 0 && paddingBottom === 0 && paddingLeft === 0) {
    return null;
  }

  const baseFill = "rgba(255, 152, 0, 0.18)";
  const baseStroke = "rgba(255, 152, 0, 0.45)";
  // U10 highlight: when the inspector pushes a band hint matching one
  // of these bands, brighten its outline so the eye lands on it. No
  // color change to the fill (the hue is already orange-on-orange).
  const hiStroke = "rgba(234, 88, 12, 1.0)";

  const bandFor = (band: string, props: {
    x: number;
    y: number;
    w: number;
    h: number;
    label: string;
  }) => (
    <PaddingBand
      key={band}
      x={props.x}
      y={props.y}
      w={props.w}
      h={props.h}
      fill={baseFill}
      stroke={highlightedBand === band ? hiStroke : baseStroke}
      strokeWidth={highlightedBand === band ? 1.5 : 0.5}
      band={band}
      label={props.label}
    />
  );

  return (
    <g className="leafcv2-measurement__padding-bands" data-figma-id={data.figmaID}>
      {paddingTop > 0 &&
        bandFor("paddingTop", {
          x: bbox.x,
          y: bbox.y,
          w: bbox.width,
          h: paddingTop,
          label: String(Math.round(paddingTop)),
        })}
      {paddingBottom > 0 &&
        bandFor("paddingBottom", {
          x: bbox.x,
          y: bbox.y + bbox.height - paddingBottom,
          w: bbox.width,
          h: paddingBottom,
          label: String(Math.round(paddingBottom)),
        })}
      {paddingLeft > 0 &&
        bandFor("paddingLeft", {
          x: bbox.x,
          y: bbox.y + paddingTop,
          w: paddingLeft,
          h: bbox.height - paddingTop - paddingBottom,
          label: String(Math.round(paddingLeft)),
        })}
      {paddingRight > 0 &&
        bandFor("paddingRight", {
          x: bbox.x + bbox.width - paddingRight,
          y: bbox.y + paddingTop,
          w: paddingRight,
          h: bbox.height - paddingTop - paddingBottom,
          label: String(Math.round(paddingRight)),
        })}
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
  strokeWidth?: number;
  band: string;
  label: string;
}): ReactElement {
  const { x, y, w, h, fill, stroke, strokeWidth = 0.5, band, label } = props;
  // Label centered in the band. For thin bands (≤ 12px in the short
  // axis), the label may overflow — that's fine, SVG overflow:visible.
  const labelX = x + w / 2;
  const labelY = y + h / 2;
  return (
    <g data-band={band}>
      <rect x={x} y={y} width={w} height={h} fill={fill} stroke={stroke} strokeWidth={strokeWidth} />
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
 * Cheap presence check: does the canonical_tree contain a node with
 * the given figmaID? Used as a per-frame gate on the band-hint
 * fallback so the sized SVG renders only for the frame that owns
 * the hinted node. Walks the tree once; short-circuits on first hit.
 */
function treeContainsNode(tree: AnnotatedNode | null, figmaID: string): boolean {
  if (!tree) return false;
  return findByFigmaID(tree, figmaID) !== null;
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
  const hint = useHoveredBandHint();
  // U10 — hint fallback. Matches PaddingBands: when the inspector
  // pushes a band hint (gap row hovered), render gap markers for
  // that hint's nodeID even if the canvas isn't hovering it.
  const focusFigmaID = hoveredFigmaID ?? hint?.nodeID ?? null;
  const highlighted = hint?.band === "gap";

  const data = useMemo(() => {
    if (!focusFigmaID) return null;
    const found = findByFigmaID(tree, focusFigmaID);
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
      figmaID: focusFigmaID,
      layoutMode,
      itemSpacing,
      isSpaceBetween,
      childBoxes,
    };
  }, [tree, frameBBox, focusFigmaID]);

  if (!data) return null;

  const fill = "rgba(236, 72, 153, 0.22)";
  const stroke = highlighted ? "rgba(190, 24, 93, 1.0)" : "rgba(236, 72, 153, 0.55)";
  const strokeWidth = highlighted ? 1.5 : 0.5;

  // SPACE_BETWEEN — render a single "Variable gap" badge centered on
  // the parent rather than per-pair (the gap is dynamic).
  if (data.isSpaceBetween) {
    return (
      <g
        className="leafcv2-measurement__gap-markers"
        data-figma-id={data.figmaID}
        data-variable-gap="true"
      >
        {/* No band for SPACE_BETWEEN — emitting a hint via DOM only;
            visual treatment is the inspector's job (U10). */}
      </g>
    );
  }

  return (
    <g className="leafcv2-measurement__gap-markers" data-figma-id={data.figmaID}>
      {data.childBoxes.slice(0, -1).map((curr, i) => {
        const next = data.childBoxes[i + 1];
        const gap = data.itemSpacing;
        if (data.layoutMode === "VERTICAL") {
          const top = curr.y + curr.height;
          const left = Math.max(curr.x, next.x);
          const right = Math.min(curr.x + curr.width, next.x + next.width);
          if (right <= left) return null;
          return (
            <GapBand
              key={i}
              x={left}
              y={top}
              w={right - left}
              h={gap}
              fill={fill}
              stroke={stroke}
              strokeWidth={strokeWidth}
              label={String(Math.round(gap))}
            />
          );
        }
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
            strokeWidth={strokeWidth}
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
  strokeWidth?: number;
  label: string;
}): ReactElement | null {
  const { x, y, w, h, fill, stroke, strokeWidth = 0.5, label } = props;
  // Skip degenerate (zero-area) bands so we don't litter SVG with
  // invisible <rect>s.
  if (w <= 0 || h <= 0) return null;
  return (
    <g data-band="gap">
      <rect x={x} y={y} width={w} height={h} fill={fill} stroke={stroke} strokeWidth={strokeWidth} />
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
 * U8 — persistent W×H chip on the selected atomic.
 *
 * Distinct from the hover tooltip (U2) which fires only while the
 * cursor is over an atomic. The selection chip stays visible for
 * the entire duration of the selection so engineers can keep
 * reading dimensions while moving the cursor elsewhere on the
 * canvas (or off-canvas to take notes).
 *
 * Position: bottom-right of the selected bbox + 4px offset. If
 * that would clip outside the frame, flip to top-right (above the
 * bbox + 4px offset) so the chip stays visible in tight cases like
 * footer CTAs at the bottom edge of a screen.
 */
function SelectionChip(props: {
  tree: AnnotatedNode;
  frameBBox: BoundingBox;
  selectedFigmaID: string | null;
}): ReactElement | null {
  const { tree, frameBBox, selectedFigmaID } = props;
  const data = useMemo(() => {
    if (!selectedFigmaID) return null;
    const local = lookupBBox(tree, selectedFigmaID, frameBBox);
    if (!local) return null;
    const found = findByFigmaID(tree, selectedFigmaID);
    if (!found) return null;
    const abs = found.node.absoluteBoundingBox;
    if (!abs) return null;
    return { local, abs };
  }, [tree, frameBBox, selectedFigmaID]);

  if (!data) return null;

  const { local, abs } = data;
  const text = `${Math.round(local.width)} × ${Math.round(local.height)}`;
  // Approximate chip width — same monospace digit width assumption
  // as DistanceLabel; pad slightly for the × glyph.
  const charW = 7;
  const w = text.length * charW + 12;
  const h = 16;
  const offset = 4;

  // Default: bottom-right of the bbox.
  let chipX = local.x + local.width - w;
  let chipY = local.y + local.height + offset;
  // Flip up if it would clip below the frame.
  if (chipY + h > frameBBox.height) {
    chipY = local.y - h - offset;
  }
  // Clamp left edge inside the frame.
  if (chipX < 0) chipX = 0;
  // Clamp right edge inside the frame.
  if (chipX + w > frameBBox.width) chipX = frameBBox.width - w;

  return (
    <g
      className="leafcv2-measurement__selection-chip"
      data-figma-id={selectedFigmaID}
      data-x={Math.round(abs.x)}
      data-y={Math.round(abs.y)}
    >
      <rect
        x={chipX}
        y={chipY}
        width={w}
        height={h}
        rx={3}
        ry={3}
        fill="#0284c7"
      />
      <text
        x={chipX + w / 2}
        y={chipY + h / 2 + 1}
        textAnchor="middle"
        dominantBaseline="middle"
        fill="#ffffff"
        fontSize={11}
        fontWeight={600}
        fontFamily="-apple-system, BlinkMacSystemFont, Inter, system-ui, sans-serif"
        style={{ fontVariantNumeric: "tabular-nums" }}
      >
        {text}
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
