/**
 * lib/atlas/types.ts — wire-shape types consumed by the ported atlas/leafcanvas
 * modules at app/atlas/_lib/.
 *
 * The reference UI in `INDmoney Docs/` was authored against hand-mocked shapes
 * (DOMAINS / FLOWS / SYNAPSES / LEAVES / FRAMES / violations / decisions /
 * activity / comments). These types preserve those shapes so the ported
 * components don't have to change at all — only the data source does.
 *
 * `data-adapters.ts` is the only place that knows how to map ds-service
 * responses (lib/projects/types.ts) into these shapes. SSE patches go through
 * the same converters so live updates and initial loads produce identical
 * objects.
 */

import type { TextOverride } from "../projects/client";
import type { ViolationSeverity } from "../projects/types";

// ─── Brain graph ─────────────────────────────────────────────────────────────

export type LobeID =
  | "frontalL"     // Markets
  | "frontalR"     // Money Matters
  | "parietalL"    // Platform
  | "parietalR"    // Lending
  | "temporal"     // Recurring Payments
  | "occipital"    // Web Platform
  | "cerebellum";  // (reserved)

export interface Domain {
  /** Stable identifier — used by the canvas hit-test and by SYNAPSES grouping. */
  id: string;
  label: string;
  /** Subtitle shown under the domain label in the sidebar. */
  sub: string;
  /** Which anatomical lobe this domain occupies on the brain silhouette. */
  lobe: LobeID;
}

/**
 * Brain-level node. One per project. Labels are real project names; `count` is
 * the rolled-up screen count used to size the dot; `primary` flips when the
 * project crosses a "this is a major area" threshold so the renderer draws a
 * larger white node and shows its label earlier on zoom-in.
 */
export interface Flow {
  /** Project slug (stable across renames). */
  id: string;
  label: string;
  /** Domain ID (lobe). Resolved via taxonomy.ts. */
  domain: string;
  /** Total screens in latest view_ready version. */
  count: number;
  /** Whether to render larger / surface label sooner. */
  primary: boolean;

  // Auxiliary fields the inspector reads off a flow node. Not in the original
  // mock but harmless to attach (the renderer only touches the four fields
  // above; any extra keys are ignored).
  activeViolations?: number;
  flowCount?: number;
  latestVersionID?: string;
  product?: string;
  /** Wall-clock ms when this node first appeared in the live store. Used by
   *  the bloom animation; 0 for nodes present at first paint. */
  appearedAt?: number;
}

/** [fromFlowId, toFlowId, optional strength 0..1]. */
export type Synapse = [string, string] | [string, string, number];

// ─── Leaf canvas ─────────────────────────────────────────────────────────────

/**
 * Leaf — one of our `flows` table rows, scoped to a parent project (Flow).
 * Reference UI calls this a "leaf" because each one is a sub-area off a
 * brain node.
 */
export interface Leaf {
  /** flows.id (UUID). */
  id: string;
  /** Parent project slug (= Flow.id). */
  flow: string;
  label: string;
  /** Total screens in this flow at the active version. */
  frames: number;
  /** Active violation count. */
  violations: number;
  appearedAt?: number;
}

/**
 * Frame — one screen positioned in the leaf canvas. Coordinates are real
 * x/y from the screens table (Figma-relative). Width/height are the
 * exported PNG dimensions.
 */
export interface Frame {
  /** screens.id. */
  id: string;
  /** Stable index in display order (sorted by y, x). */
  idx: number;
  x: number;
  y: number;
  w: number;
  h: number;
  /** Display label — derived from screen_logical_id or canonical_tree title. */
  label: string;
  /** JWT- or asset-token-signed PNG URL. May be empty if PNG not yet
   *  rendered; renderer shows a placeholder. */
  pngUrl: string;
  appearedAt?: number;
}

/**
 * Edge between two frames. Walked from screen_canonical_trees navigation refs.
 * Kind drives stroke style:
 *   - main   → primary forward arrow (blue solid)
 *   - branch → secondary outgoing arrow (blue dashed)
 *   - back   → reverse / retry arrow (orange dashed)
 */
export type LeafEdgeKind = "main" | "branch" | "back";

export interface LeafEdge {
  from: string;
  to: string;
  kind: LeafEdgeKind;
}

export interface LeafCanvas {
  frames: Frame[];
  edges: LeafEdge[];
  /**
   * Per-screen canonical_tree blobs already pulled as part of the edge
   * inference walk (first 20 screens). The strict-TS LeafFrameRenderer
   * uses these to skip the network round-trip for above-the-fold frames;
   * scrolled-into-view frames lazy-fetch their tree directly.
   *
   * undefined entry = not in this batch; null entry = fetched but no
   * tree available; object = ready-to-walk canonical tree.
   */
  canonicalTreeByScreenID?: Record<string, unknown>;
}

// ─── Inspector overlays ──────────────────────────────────────────────────────

export type DisplayViolationSeverity = "error" | "warning" | "info";

export type DisplayViolationStatus =
  | "active"
  | "acknowledged"
  | "fixed"
  | "dismissed";

/**
 * Violation row as the LeafInspector renders it. Maps from
 * lib/projects/types.ts:Violation via `violationToDisplay()`.
 */
export interface DisplayViolation {
  id: string;
  severity: DisplayViolationSeverity;
  /** Display name — looked up via rules-registry. Falls back to ruleId. */
  rule: string;
  ruleId: string;
  /** Path-style "Frame/Header/Title". */
  layer: string;
  /** Optional pointer to a frame (by idx) so the inspector can highlight. */
  frameIdx?: number;
  status: DisplayViolationStatus;
  /** "Observed → Suggested". */
  detail: string;
  /** Pre-formatted relative time ("2h ago"). */
  ago: string;
  /** Original ISO timestamp; used by the relative-time tick to refresh `ago`. */
  createdAt: string;
  /** Whether the row is currently being updated optimistically. */
  pending?: boolean;
  /** Original raw severity from server, surfaced for advanced filters. */
  rawSeverity: ViolationSeverity;
}

export interface DisplayDecision {
  id: string;
  title: string;
  /** Markdown rationale. */
  body: string;
  author: string;
  ago: string;
  createdAt: string;
  /** Optional frame index this decision is anchored to. */
  linksTo?: number;
}

/** Aligned with the reference UI's CSS palette: .kind-edit, -violation,
 *  -audit, -decision, -sync. Adapter falls back to "edit" for anything
 *  outside the palette so every row has a visible icon. */
export type ActivityKind = "edit" | "audit" | "sync" | "decision" | "violation";

export interface ActivityEntry {
  id: string;
  who: string;
  /** Pre-rendered sentence — "edited DRD", "audit flagged 3 violations", … */
  what: string;
  ago: string;
  ts: string;
  kind: ActivityKind;
}

export interface DisplayComment {
  id: string;
  who: string;
  /** Plain text or markdown with @mentions; renderer applies parser. */
  body: string;
  ago: string;
  createdAt: string;
  /** Number of reactions across all emojis. */
  reactions: number;
  pending?: boolean;
}

export interface DRDDocument {
  /** BlockNote JSON content (stored as opaque string to mirror server). */
  content: string;
  /** Optimistic-concurrency token; PUT requires sending this. */
  revision: number;
  updatedAt: string;
  updatedBy: string;
}

/** Per-leaf bundle pre-fetched together for the inspector. */
export interface LeafOverlays {
  violations: DisplayViolation[];
  decisions: DisplayDecision[];
  activity: ActivityEntry[];
  comments: DisplayComment[];
  drd?: DRDDocument;
  /**
   * U8 — per-screen text-override map. Two-level lookup:
   *   overrides[screenID].get(figma_node_id) → TextOverride row.
   *
   * Populated during cold load by parallel `fetchTextOverrides` calls
   * (one per screen). Empty `Record<string, Map>` when no screens have
   * any overrides; the live store extends this map on PUT/DELETE.
   */
  overrides?: Record<string, Map<string, TextOverride>>;
}

// ─── Tweaks panel ────────────────────────────────────────────────────────────

export interface AtlasTweaks {
  showSidebar: boolean;
  showHints: boolean;
  /** 0..2.5 — multiplier on node drift amplitude. */
  wiggle: number;
}

export const ATLAS_TWEAK_DEFAULTS: AtlasTweaks = {
  showSidebar: true,
  showHints: true,
  wiggle: 0.8,
};

// ─── Top-level atlas state ───────────────────────────────────────────────────

export interface AtlasState {
  domains: Domain[];
  flows: Flow[];
  synapses: Synapse[];
  /** flowId → Leaf[] map. Populated lazily as the user opens leaves. */
  leavesByFlow: Record<string, Leaf[]>;
  /** ETag from the most recent /v1/projects/atlas/brain-nodes fetch. */
  brainNodesETag?: string;
  /** ETag from the most recent /v1/projects/graph fetch (powers SYNAPSES). */
  graphAggregateETag?: string;
  /** Wall-clock ms — reset to Date.now() on every full reload. */
  loadedAt: number;
}

// ─── Live event union ────────────────────────────────────────────────────────

export type AtlasLiveEvent =
  | { type: "GraphIndexUpdated"; tenant: string; platform: string; materializedAt: string }
  | { type: "view_ready"; slug: string; versionID?: string }
  | { type: "audit_complete"; slug: string; versionID?: string }
  | { type: "audit_failed"; slug: string; reason?: string }
  | { type: "audit_progress"; slug: string; pct?: number }
  | { type: "violation_lifecycle_changed"; violationID: string; status: DisplayViolationStatus }
  | { type: "decision.created"; flowID: string; decisionID: string }
  | { type: "decision.superseded"; flowID: string; decisionID: string }
  | { type: "comment.created"; flowID: string; commentID: string };

// ─── Platform alias ──────────────────────────────────────────────────────────

export type Platform = "mobile" | "web";
