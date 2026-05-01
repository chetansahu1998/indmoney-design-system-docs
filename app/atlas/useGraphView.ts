"use client";

/**
 * Phase 6 — view-state reducer for the mind graph.
 *
 * Owns: filter chip state (U7), focused node id for the recursive zoom
 * (U10), zoom-level derivation (U13), and morph-target node for the leaf
 * handoff (U12).
 *
 * Camera state is owned by react-force-graph-3d; we keep only the *intent*
 * here so other components (filter chips, signal layer, hover card) can
 * subscribe to the same source of truth.
 */

import { useCallback, useMemo, useState } from "react";

import {
  DEFAULT_FILTERS,
  type GraphFilters,
  type GraphNode,
  type GraphZoomLevel,
} from "./types";

interface GraphView {
  filters: GraphFilters;
  setFilters: (next: GraphFilters) => void;
  /** ID of the currently zoomed-into node. null = brain view. */
  focusedNodeID: string | null;
  /** The full node ref for the focus, exposed for ancestor walks. */
  focusedNode: GraphNode | null;
  focus: (node: GraphNode | null) => void;
  /** Derived from camera distance / focus depth. Used by the cull layer. */
  zoomLevel: GraphZoomLevel;
  setZoomLevel: (level: GraphZoomLevel) => void;
  /** Leaf-morph target. Set when user single-clicks a flow leaf. */
  morphingNode: GraphNode | null;
  morphTo: (node: GraphNode | null) => void;
}

export function useGraphView(): GraphView {
  const [filters, setFilters] = useState<GraphFilters>(DEFAULT_FILTERS);
  const [focusedNode, setFocusedNode] = useState<GraphNode | null>(null);
  const [zoomLevel, setZoomLevel] = useState<GraphZoomLevel>("brain");
  const [morphingNode, setMorphingNode] = useState<GraphNode | null>(null);

  const focus = useCallback((node: GraphNode | null) => {
    setFocusedNode(node);
    if (!node) {
      setZoomLevel("brain");
    } else if (node.type === "product") {
      setZoomLevel("product");
    } else if (node.type === "folder") {
      setZoomLevel("folder");
    } else if (node.type === "flow") {
      setZoomLevel("flow");
    }
  }, []);

  const morphTo = useCallback((node: GraphNode | null) => {
    setMorphingNode(node);
  }, []);

  return useMemo(
    () => ({
      filters,
      setFilters,
      focusedNodeID: focusedNode?.id ?? null,
      focusedNode,
      focus,
      zoomLevel,
      setZoomLevel,
      morphingNode,
      morphTo,
    }),
    [filters, focusedNode, focus, zoomLevel, morphingNode, morphTo],
  );
}
