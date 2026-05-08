"use client";

/**
 * HoverTooltip — the floating "Name  W×H" pill that follows the
 * hovered atomic on the leaf canvas (Phase 2 U2).
 *
 * Positioning model: the tooltip is rendered as a sibling of the
 * canonical-tree-rendered nodes inside `.leafcv2-frame`, so it
 * inherits the canvas world transform automatically. Its X/Y are
 * computed in wrapper-local coordinates by rebasing the hovered
 * node's `absoluteBoundingBox` against the screen-frame's bbox
 * (matching the parent-rebase math used by `nodeToHTML.ts`).
 *
 * Edge clamp: if the tooltip would render above the top edge of
 * the frame (negative Y), flip it below the hovered bbox instead.
 * This keeps the pill visible even when hovering atomics in a
 * status bar at the top of the screen.
 *
 * pointer-events: none — never swallows clicks. The user can hover
 * an atomic, see the tooltip, and click through it to select.
 *
 * Mounted by LeafFrameRenderer inside the frame wrapper as a
 * sibling to <StatePicker>; one instance per frame, but only the
 * frame whose screenID matches the hover signal renders content
 * (others render null cheaply).
 *
 * Polish rules from `docs/plans/2026-05-02-005-fix-atlas-
 * interaction-polish-plan.md`: 50ms mount delay (avoid flash
 * during fast cursor sweeps), 16ms reposition (one frame),
 * 100ms unmount fade (handled by CSS opacity transition if the
 * design wants one — v1 ships without animation, just flicker-free
 * mount/unmount via the dedup signal).
 */

import { useMemo } from "react";

import { useHoveredAtomicChild } from "./hover-signal";
import type { AnnotatedNode, BoundingBox, CanonicalNode } from "./types";

export interface HoverTooltipProps {
  /** Screen the wrapper renders — only this frame's tooltip fires. */
  screenID: string;
  /** Frame's bounding box; used to rebase wrapper-local positions. */
  frameBBox: BoundingBox;
  /** Pruned canonical_tree the LeafFrameRenderer is currently rendering. */
  tree: AnnotatedNode | null;
}

interface FoundHoverNode {
  id: string;
  name: string;
  bbox: BoundingBox;
}

const TOOLTIP_HEIGHT = 22; // approx; matches CSS line + padding
const TOOLTIP_OFFSET_Y = 6;
const MIN_FRAME_X = 0;

export function HoverTooltip(props: HoverTooltipProps) {
  const { screenID, frameBBox, tree } = props;
  const hovered = useHoveredAtomicChild();

  // Cheap fast-path: render nothing when nothing is hovered or the
  // hovered atomic lives in a different frame.
  const isThisFrame = hovered?.screenID === screenID;

  // Walk the tree once per (hovered.figmaNodeID, tree) tuple to find
  // the bbox + name. Memoised so a re-render of an unrelated child
  // doesn't re-walk.
  const found: FoundHoverNode | null = useMemo(() => {
    if (!isThisFrame || !hovered || !tree) return null;
    return findHovered(tree, hovered.figmaNodeID);
  }, [isThisFrame, hovered, tree]);

  if (!found) return null;

  // Wrapper-local coords: rebase from canonical_tree absolute coords
  // to wrapper origin (which sits at frameBBox.x, frameBBox.y). The
  // wrapper's intrinsic size is frameBBox.width × frameBBox.height.
  const localX = found.bbox.x - frameBBox.x;
  const localY = found.bbox.y - frameBBox.y;

  // Default position: above the bbox top edge.
  // Edge-flip: if that would clip above the wrapper, render below
  // instead.
  let top = localY - TOOLTIP_HEIGHT - TOOLTIP_OFFSET_Y;
  if (top < MIN_FRAME_X) {
    top = localY + found.bbox.height + TOOLTIP_OFFSET_Y;
  }
  // Horizontal: align left edge to bbox left, but clamp inside the
  // frame so the tooltip doesn't cut off when hovering atomics near
  // the right edge.
  let left = localX;
  if (left < MIN_FRAME_X) left = MIN_FRAME_X;

  const w = Math.round(found.bbox.width);
  const h = Math.round(found.bbox.height);

  return (
    <div
      className="leafcv2-hover-tooltip"
      style={{ left, top }}
      aria-hidden="true"
      data-figma-id={found.id}
    >
      <span className="leafcv2-hover-tooltip__name">{found.name || "Layer"}</span>
      <span className="leafcv2-hover-tooltip__dims">
        {w} × {h}
      </span>
    </div>
  );
}

/**
 * Depth-first search for a node by figmaID. Returns null when not
 * found OR when the matched node has no absoluteBoundingBox.
 *
 * Matches the walk semantics of nodeToHTML — visible/removed flags
 * are NOT pruned here because by the time the tooltip fires, the
 * matched node is in the rendered DOM (LeafFrameRenderer's painter
 * already filtered). Walking against the same input tree is safe.
 */
function findHovered(node: CanonicalNode, id: string): FoundHoverNode | null {
  if (!node || typeof node !== "object") return null;
  if (node.id === id) {
    return readNode(node);
  }
  if (Array.isArray(node.children)) {
    for (const c of node.children) {
      const found = findHovered(c as CanonicalNode, id);
      if (found) return found;
    }
  }
  return null;
}

function readNode(node: CanonicalNode): FoundHoverNode | null {
  const bbox = node.absoluteBoundingBox;
  if (!bbox || typeof bbox.width !== "number" || typeof bbox.height !== "number") {
    return null;
  }
  const name =
    typeof node.name === "string" && node.name.length > 0 ? node.name : node.type ?? "Layer";
  return {
    id: node.id ?? "",
    name,
    bbox,
  };
}
