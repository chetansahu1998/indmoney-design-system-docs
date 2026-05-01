/**
 * Phase 6 — Mind-graph wire types.
 *
 * Mirrors the Go types in services/ds-service/internal/projects/graph_repo.go.
 * Keep in sync if the backend response shape changes — these are the
 * contract the BrainGraph + hover card + filter chips read.
 */

export type GraphNodeKind =
  | "product"
  | "folder"
  | "flow"
  | "persona"
  | "component"
  | "token"
  | "decision";

export type GraphEdgeClass = "hierarchy" | "uses" | "binds-to" | "supersedes";

export type GraphPlatform = "mobile" | "web";

export interface GraphSeverityCounts {
  critical: number;
  high: number;
  medium: number;
  low: number;
  info: number;
}

export interface GraphSignal {
  severity_counts: GraphSeverityCounts;
  persona_count: number;
  last_updated_at: string;
  last_editor?: string;
  open_url?: string;
}

export interface GraphNode {
  id: string;
  type: GraphNodeKind;
  label: string;
  parent_id?: string;
  platform: GraphPlatform;
  signal: GraphSignal;
  // Force-graph runtime fields (populated by d3-force-3d after settle).
  // Optional because the wire shape doesn't include them.
  x?: number;
  y?: number;
  z?: number;
  vx?: number;
  vy?: number;
  vz?: number;
}

export interface GraphEdge {
  source: string;
  target: string;
  class: GraphEdgeClass;
}

export interface GraphAggregate {
  nodes: GraphNode[];
  edges: GraphEdge[];
  generated_at: string;
  platform: GraphPlatform;
  cache_key: string;
}

/** SSE event payload — matches sse.GraphIndexUpdated from the backend. */
export interface GraphIndexUpdatedEvent {
  tenant_id: string;
  platform: GraphPlatform;
  materialized_at: string;
}

/** Filter chip state. The hierarchy chip is implicitly always-on; toggling
 *  the others reveals satellite node classes per U7. */
export interface GraphFilters {
  hierarchy: true; // intentionally a literal — never disabled in v1
  components: boolean;
  tokens: boolean;
  decisions: boolean;
}

export const DEFAULT_FILTERS: GraphFilters = {
  hierarchy: true,
  components: false,
  tokens: false,
  decisions: false,
};

/** Zoom level — derived from camera.position.z by useGraphView (U13). The
 *  budget per zoom is documented in docs/runbooks/phase-6-mind-graph.md. */
export type GraphZoomLevel = "brain" | "product" | "folder" | "flow";
