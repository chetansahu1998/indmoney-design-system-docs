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

import { useEffect, useState } from "react";

import { canvasGestureTracker, getIsGesturing } from "./gesture-tracker";
import { useHoveredAtomicChild } from "./hover-signal";
import { useAtlas } from "../../../../lib/atlas/live-store";
import type { AnnotatedNode, BoundingBox } from "./types";

export interface MeasurementOverlayProps {
  /** Screen ID this overlay paints for. Cross-frame state ignored. */
  screenID: string;
  /** Frame's bounding box; rebases wrapper-local coords. */
  frameBBox: BoundingBox;
  /** The pruned canonical_tree the renderer is currently emitting. */
  tree: AnnotatedNode | null;
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

  // U4-U8 land content here. The scaffold returns the empty <svg>
  // root with the styling hook for now.
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
      {/* U4-U8 children land here in subsequent commits. */}
    </svg>
  );
}
