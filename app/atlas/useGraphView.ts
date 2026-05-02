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
  /**
   * Phase 9 U3 — reverse-morph entry point.
   *
   * Called when /atlas mounts after a back-navigation from a project view,
   * with the project's slug. Walks the supplied node list looking for the
   * flow node whose `signal.open_url` matches `/projects/<slug>`. If found,
   * applies focus + sets zoomLevel back to "flow" so the leaf is centred.
   * If no match (e.g. the user opened the project via a direct URL and the
   * graph hasn't ever loaded that flow), this is a no-op — callers fall
   * back to the bare /atlas root view (clean default per the plan's edge-
   * case spec).
   *
   * Nodes are passed in (rather than read from internal state) because the
   * aggregation layer (`useGraphAggregate`) lives outside this hook; the
   * caller (BrainGraph) already has the resolved node list and forwarding
   * it keeps this hook pure. The forward-direction `morphTo` works the
   * same way — caller hands in the node, hook stores intent.
   */
  morphFromProject: (slug: string, nodes: GraphNode[]) => void;
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

  // Phase 9 U3 — reverse-morph: resolve slug → flow node, then focus.
  // The node match relies on `signal.open_url` matching `/projects/<slug>`
  // exactly (plus an optional trailing `?…` query string or `#…` hash so
  // share-link variants still resolve). We restrict to `type === "flow"`
  // because only flow nodes carry a project URL in the brainstorm IA.
  const morphFromProject = useCallback(
    (slug: string, nodes: GraphNode[]): void => {
      if (!slug) return;
      const target = `/projects/${slug}`;
      const match = nodes.find((n) => {
        if (n.type !== "flow") return false;
        const url = n.signal.open_url;
        if (!url) return false;
        // Accept `/projects/<slug>`, `/projects/<slug>?v=…`, `/projects/<slug>#tab=…`.
        if (url === target) return true;
        return (
          url.startsWith(target) &&
          (url[target.length] === "?" || url[target.length] === "#")
        );
      });
      if (!match) return;
      setFocusedNode(match);
      setZoomLevel("flow");
    },
    [],
  );

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
      morphFromProject,
    }),
    [
      filters,
      focusedNode,
      focus,
      zoomLevel,
      morphingNode,
      morphTo,
      morphFromProject,
    ],
  );
}
