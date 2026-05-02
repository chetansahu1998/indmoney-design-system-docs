/**
 * Phase 6 U13 — LOD / culling.
 *
 * The full graph stays in memory; we filter to the visible subset on each
 * (zoomLevel, focusedNode, filters) change. The budget per zoom level is
 * documented in docs/runbooks/phase-6-mind-graph.md and is the answer to
 * Origin Q6 (node count budget per zoom).
 *
 * Brain view  : products + folders + flows (~500 nodes)
 * Product zoom: products dim + clicked product's folders/flows + decisions on those flows
 *               + (if Components on) components used by visible flows
 * Folder zoom : same shape, scoped to folder
 * Flow zoom   : single flow + its components/tokens/decisions (~50 nodes)
 *
 * Edge culling: at brain view, only `hierarchy` edges render even if other
 * filter chips are toggled on; satellite edges only render once the user
 * has zoomed at least one level in.
 */

import type {
  GraphAggregate,
  GraphEdge,
  GraphFilters,
  GraphNode,
  GraphZoomLevel,
} from "./types";

interface ViewState {
  zoomLevel: GraphZoomLevel;
  focusedNodeID: string | null;
  focusedNode: GraphNode | null;
  /**
   * Phase 9 U4 — leaf-morph source node. When set, the morph source must
   * stay HOT (i.e. survive culling) for the duration of the View Transition
   * regardless of zoom/focus, so the snapshot the browser captures has a
   * crisp label. Mirrors the Phase 6 closure invariant: "selected/hovered
   * nodes forced HOT regardless of viewport — load-bearing for any
   * cross-surface choreography."
   */
  morphingNode?: GraphNode | null;
}

export function cullVisibleSubset(
  graph: GraphAggregate,
  view: ViewState,
  filters: GraphFilters,
): { nodes: GraphNode[]; edges: GraphEdge[] } {
  const all = graph.nodes;
  const allEdges = graph.edges;

  // Index by id for ancestor walks.
  const byID = new Map(all.map((n) => [n.id, n]));

  // 1. Pick visible NODE TYPES based on zoom + filters.
  const allowed = new Set<GraphNode["type"]>(["product", "folder", "flow"]);
  if (view.zoomLevel !== "brain") {
    if (filters.components) allowed.add("component");
    if (filters.tokens) allowed.add("token");
    if (filters.decisions) allowed.add("decision");
    // Personas always visible past brain view.
    allowed.add("persona");
  }

  // 2. If focused, restrict to focus + descendants + ancestors.
  let inSubtree = (_n: GraphNode) => true;
  if (view.focusedNodeID && view.zoomLevel !== "brain") {
    const focusID = view.focusedNodeID;
    const ancestors = new Set<string>();
    let cursor = byID.get(focusID);
    while (cursor) {
      ancestors.add(cursor.id);
      cursor = cursor.parent_id ? byID.get(cursor.parent_id) : undefined;
    }
    inSubtree = (n: GraphNode) => {
      if (ancestors.has(n.id)) return true;
      // Walk up from n; if we hit focusID, n is a descendant.
      let c: GraphNode | undefined = n;
      let depth = 0;
      while (c && c.parent_id && depth++ < 12) {
        if (c.parent_id === focusID) return true;
        c = byID.get(c.parent_id);
      }
      return false;
    };
  }

  // 3. U4 — pin set: nodes that must always pass culling regardless of
  // zoom/focus/type. Currently only the morph-source node and its ancestor
  // chain (so its parent edges still resolve to visible endpoints — without
  // the chain the leaf would be a stranded node referenced by a hierarchy
  // edge whose other end was filtered out, and the edge filter at step 4
  // would drop the edge anyway). We walk parents up to the same 12-depth
  // limit used in `inSubtree` for symmetry.
  const pinned = new Set<string>();
  if (view.morphingNode) {
    let cursor: GraphNode | undefined = view.morphingNode;
    let depth = 0;
    while (cursor && depth++ < 12) {
      pinned.add(cursor.id);
      cursor = cursor.parent_id ? byID.get(cursor.parent_id) : undefined;
    }
  }

  const nodes = all.filter(
    (n) => pinned.has(n.id) || (allowed.has(n.type) && inSubtree(n)),
  );
  const visibleIDs = new Set(nodes.map((n) => n.id));

  // 4. Edge classes — at brain view only hierarchy; past it, hierarchy +
  // any toggled satellite class. Drop edges to invisible nodes.
  const edges = allEdges.filter((e) => {
    if (!visibleIDs.has(e.source) || !visibleIDs.has(e.target)) return false;
    if (e.class === "hierarchy") return true;
    if (view.zoomLevel === "brain") return false;
    if (e.class === "uses" && filters.components) return true;
    if (e.class === "binds-to" && filters.tokens) return true;
    if (e.class === "supersedes" && filters.decisions) return true;
    return false;
  });

  return { nodes, edges };
}
