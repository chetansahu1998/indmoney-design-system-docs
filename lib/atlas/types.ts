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
 * SubFlow lifecycle states (plan 005 KTD-4 / plan 002 KTD-8). Drives the
 * Atlas center-pane render branch:
 *   - empty / proto-only / proto-wip → render PrototypeCanvas iframe (U6).
 *   - design-shipped → render leafcanvas as today (U6).
 *
 * Mirror of the server-side computeCanvasLifecycle() switch in
 * services/ds-service/internal/projects/server.go.
 */
export type SubFlowCanvasLifecycle =
  | "empty"
  | "proto-only"
  | "proto-wip"
  | "design-shipped";

/**
 * SubFlowSummary — the PM-authoring context attached to a Leaf when its
 * underlying flow has been bound to a sub_flow row (via the
 * flows.section_id → sub_flow.figma_section_id binding written by autosync).
 *
 * Populated by:
 *   1. Derivation: data-adapters.fetchSubFlowForLeaf() calls
 *      GET /v1/projects/{slug}/flows/{flow_id}/sub-flow during cold load.
 *   2. URL override: AtlasShell reads ?subFlow=<full_slug> and calls the
 *      MCP `resolve(slug)` proxy at /api/resolve/<sub_product>/<sub_flow>.
 *
 * Absent on legacy leaves that pre-date the sub_flow taxonomy or whose
 * Figma section hasn't been authored yet. Every downstream consumer
 * (U2 PRD tab, U3 tab list, U6 prototype canvas, U7 coverage wall)
 * tolerates `leaf.subFlow === undefined` by falling back to legacy behaviour.
 */
export interface SubFlowSummary {
  /** sub_flow.id (UUID). */
  id: string;
  /** Universal join key — "{sub_product.slug}/{sub_flow.slug}". */
  fullSlug: string;
  /** Display name preserving first-writer casing (e.g. "M2M Settlement"). */
  name: string;
  canvasLifecycle: SubFlowCanvasLifecycle;
  /** HTML prototype URL when a PM has attached one (proto-* lifecycle). */
  prototypeURL?: string;
  prototypeTitle?: string;
  /** Bound Figma section id; null until autosync links the section. */
  figmaSectionID?: string;
  /**
   * Figma file_key the bound section lives in. Populated server-side by
   * HandleSubFlowForLeaf via LookupFigmaSectionFileKey when
   * figma_section_id is set. Required by <FrameThumbnail> to construct
   * the /api/figma/frame-png request; absent for legacy / unbound leaves.
   */
  figmaFileKey?: string;
}

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
  /**
   * Sub_flow context populated when the leaf's underlying flow is bound
   * to a sub_flow taxonomy row. Drives the PM-authoring tabs (PRD,
   * Activity, Comments) and the center-pane prototype/canvas swap.
   *
   * Optional by design — legacy leaves render the 6-tab inspector as today.
   */
  subFlow?: SubFlowSummary;
  /**
   * Figma file key — the file this section lives in. Used to disambiguate
   * same-named sections across files (Wallet in V3 vs Wallet in V4).
   */
  fileKey?: string;
  /**
   * Human-readable file name shown as metadata on the section node
   * (e.g. "INDstocks V4"). Pure metadata — the file is NOT a separate
   * brain node in the user-described hierarchy.
   */
  fileName?: string;
  /** Raw figma_section.section_id — pairs with fileKey for uniqueness. */
  sectionID?: string;
  /**
   * Project slug for back-compat file-canvas navigation. Multiple
   * section leaves can share the same projectSlug (sections within one
   * file all map to the same project row).
   */
  projectSlug?: string;
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
  /**
   * Plan 005 U5 — target_kind on the underlying drd_comments row. The
   * inspector renders a chip when this is non-empty AND not "drd_block"
   * (the default scope), so PMs can tell a state-anchored comment apart
   * from a generic DRD comment in the merged thread.
   */
  targetKind?: string;
  /** Raw target_id — currently surfaced for "prd_state" chips so a future
   * deep-link can route to the PRD tab's specific state. */
  targetID?: string;
}

export interface DRDDocument {
  /** BlockNote JSON content (stored as opaque string to mirror server). */
  content: string;
  /** Optimistic-concurrency token; PUT requires sending this. */
  revision: number;
  updatedAt: string;
  updatedBy: string;
}

/**
 * PRDFull — typed PRD document returned by the MCP `prd.get` tool.
 *
 * Snake-case keys mirror the Go-side JSON tags in
 * services/ds-service/internal/projects/prd.go (PRD, PRDTab, PRDState, and
 * each typed-stem row). The MCP envelope is `{data: PRDFull | {sub_flow_id,
 * prd: null, note}}`; the empty branch is represented here as `null` in the
 * store (`prdByLeaf[leafID] === null`), so consumers only ever see PRDFull
 * or null.
 */
export interface PRDFull {
  /** prd.id (UUID). */
  id: string;
  /** tenant_id; server-supplied, opaque to the client. */
  tenant_id: string;
  /** sub_flow.id this PRD hangs off. */
  sub_flow_id: string;
  title: string;
  summary_md: string;
  design_notes_md: string;
  created_at: string;
  updated_at: string;
  tabs?: PRDTabFull[];
}

export interface PRDTabFull {
  id: string;
  tenant_id: string;
  prd_id: string;
  name: string;
  position: number;
  overview_md: string;
  created_at: string;
  states?: PRDStateFull[];
}

export interface PRDStateFull {
  id: string;
  tenant_id: string;
  prd_tab_id: string;
  label: string;
  position: number;
  /** Designer-supplied display name for the bound frame (optional). */
  frame_name?: string | null;
  condition_md: string;
  design_handling_md: string;
  fe_handling_md: string;
  /** Soft-delete sentinel; soft-deleted states never surface in the doc view. */
  deleted_at?: string | null;
  created_at: string;
  updated_at: string;

  acceptance_criteria?: AcceptanceCriterion[];
  edge_cases?: EdgeCase[];
  copy_strings?: CopyString[];
  events?: PRDStateEvent[];
  a11y_notes?: A11yNote[];
  frame_tags?: FrameTag[];
}

export interface AcceptanceCriterion {
  id: string;
  tenant_id: string;
  prd_state_id: string;
  position: number;
  criterion: string;
  created_at: string;
}

export interface EdgeCase {
  id: string;
  tenant_id: string;
  prd_state_id: string;
  position: number;
  edge_case: string;
  created_at: string;
}

export interface CopyString {
  id: string;
  tenant_id: string;
  prd_state_id: string;
  key: string;
  value: string;
  locale: string;
  created_at: string;
}

/**
 * Mixpanel-style analytics event. Named `PRDStateEvent` (not `Event`) so it
 * doesn't shadow the global DOM `Event` symbol when imported alongside
 * React handler types.
 */
export interface PRDStateEvent {
  id: string;
  tenant_id: string;
  prd_state_id: string;
  position: number;
  name: string;
  /** JSON-shaped string (best-effort parse); stored verbatim per prd.go. */
  properties_schema: string;
  fires_on: string;
  created_at: string;
}

export interface A11yNote {
  id: string;
  tenant_id: string;
  prd_state_id: string;
  position: number;
  note: string;
  created_at: string;
}

export interface FrameTag {
  id: string;
  tenant_id: string;
  prd_state_id: string;
  figma_node_id: string;
  variant?: string | null;
  position: number;
  created_at: string;
}

// ─── Coverage Wall (plan 005 U7) ────────────────────────────────────────────
//
// Mirror of the Go shape in
// services/ds-service/internal/projects/prd_outline.go (WallRow/WallCounts/
// WallResult). The Wall corkboard view in the Atlas center pane consumes
// this verbatim. Keep these in lockstep with the Go-side json tags.

export type BindingStatus = "bound" | "untagged" | "orphaned";

export interface WallRow {
  figma_node_id: string;
  frame_name: string;
  binding_status: BindingStatus;
  prd_state_id?: string;
  prd_state_label?: string;
  criteria_count: number;
  events_count: number;
  copy_count: number;
  edge_cases_count: number;
  a11y_count: number;
  total_word_count: number;
  last_touched_by?: string;
  last_touched_at?: string;
  has_render: boolean;
}

export interface WallCounts {
  total: number;
  bound: number;
  untagged: number;
  orphaned: number;
  coverage_percent: number;
}

export interface WallResult {
  frames: WallRow[];
  counts: WallCounts;
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
  | { type: "comment.created"; flowID: string; commentID: string }
  /**
   * Plan 005 U6 — autosync detected that the bound Figma section now has
   * shipped frames; sub_flow.canvas_lifecycle flips to `design-shipped`.
   * The store refetches the sub_flow for any open leaf whose
   * subFlow.id matches; AtlasShellInner swaps PrototypeCanvas → LeafCanvas
   * without a page reload.
   *
   * Payload shape mirrors plan 002 U3b's SSE definitions:
   *   {sub_flow_id, sub_flow_slug?, tenant_id?}
   */
  | { type: "figma.design_shipped"; subFlowID: string; subFlowSlug?: string }
  /**
   * Plan 005 U6 — a PM has attached or replaced a prototype URL via the
   * MCP `drd.attach_prototype` tool. The bound sub_flow's
   * canvas_lifecycle flips to `proto-only` (or `proto-wip` if a designer
   * is already working on the Figma section). The store refetches so
   * the iframe URL updates live.
   */
  | { type: "drd.prototype_attached"; subFlowID: string; subFlowSlug?: string };

// ─── Platform alias ──────────────────────────────────────────────────────────

export type Platform = "mobile" | "web";
