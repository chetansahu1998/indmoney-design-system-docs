/**
 * TypeScript mirrors of the Go shapes returned by ds-service for the
 * /projects/{sub_product}/{sub_flow}/prd viewer.
 *
 * Keep these in sync (field-by-field) with:
 *   services/ds-service/internal/projects/prd.go       (PRDFull + nested)
 *   services/ds-service/internal/projects/prd_outline.go (WallRow / WallResult)
 *   services/ds-service/internal/projects/subflow_prototype.go (Lifecycle constants)
 *   services/ds-service/internal/mcp/meta.go           (sectionInspectResult)
 *
 * No runtime validation — the proxy route forwards ds-service JSON verbatim,
 * so the shape is whatever the server returned. We trust the wire contract
 * the same way other docs-site clients do (lib/projects/client.ts pattern).
 */

// ─── Lifecycle ──────────────────────────────────────────────────────────────

export type Lifecycle = "empty" | "proto-only" | "proto-wip" | "design-shipped";

// ─── section.inspect shape ──────────────────────────────────────────────────

export interface SectionInspectSubFlow {
  id: string;
  name: string;
  slug: string;
  full_slug: string;
  canvas_lifecycle: Lifecycle;
  prototype_url?: string | null;
  // prototype_title is not currently surfaced by section.inspect's
  // drdReadSubFlow shape (services/ds-service/internal/mcp/meta.go).
  // Kept optional here so a future server-side addition flows through.
  prototype_title?: string | null;
  has_figma_section: boolean;
}

export interface DRDSummary {
  exists: boolean;
  bytes?: number;
}

export interface PRDSummary {
  exists: boolean;
  tab_count: number;
  state_count: number;
}

export interface SectionFrameRow {
  node_id: string;
  name: string;
  parent_node_id?: string;
  depth: number;
  abs_x: number;
  abs_y: number;
  width: number;
  height: number;
  has_render: boolean;
}

export interface SectionInspect {
  sub_flow: SectionInspectSubFlow;
  drd_summary: DRDSummary;
  prd_summary: PRDSummary;
  frames: SectionFrameRow[];
  frames_note?: string;
  wall: WallResult;
}

// ─── Wall ───────────────────────────────────────────────────────────────────

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

// ─── PRDFull (Document view) ────────────────────────────────────────────────

export interface PRD {
  id: string;
  tenant_id: string;
  sub_flow_id: string;
  title: string;
  summary_md: string;
  design_notes_md: string;
  created_at: string;
  updated_at: string;
}

export interface PRDStateBase {
  id: string;
  tenant_id: string;
  prd_tab_id: string;
  label: string;
  position: number;
  frame_name?: string;
  condition_md: string;
  design_handling_md: string;
  fe_handling_md: string;
  deleted_at?: string;
  created_at: string;
  updated_at: string;
}

export interface AcceptanceCriterion {
  id: string;
  prd_state_id: string;
  position: number;
  criterion: string;
  created_at: string;
}

export interface EdgeCase {
  id: string;
  prd_state_id: string;
  position: number;
  edge_case: string;
  created_at: string;
}

export interface CopyString {
  id: string;
  prd_state_id: string;
  key: string;
  value: string;
  locale: string;
  created_at: string;
}

export interface MixpanelEvent {
  id: string;
  prd_state_id: string;
  position: number;
  name: string;
  properties_schema: string;
  fires_on: string;
  created_at: string;
}

export interface A11yNote {
  id: string;
  prd_state_id: string;
  position: number;
  note: string;
  created_at: string;
}

export interface FrameTag {
  id: string;
  prd_state_id: string;
  figma_node_id: string;
  variant?: string;
  position: number;
  created_at: string;
}

export interface PRDStateFull extends PRDStateBase {
  acceptance_criteria?: AcceptanceCriterion[];
  edge_cases?: EdgeCase[];
  copy_strings?: CopyString[];
  events?: MixpanelEvent[];
  a11y_notes?: A11yNote[];
  frame_tags?: FrameTag[];
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

export interface PRDFull extends PRD {
  tabs?: PRDTabFull[];
}

// ─── MCP envelope ───────────────────────────────────────────────────────────

export interface MCPResult<T> {
  data: T;
  next_actions?: unknown;
  schema_hint?: unknown;
}

// prd.get can return either PRDFull or { sub_flow_id, prd: null, note }
// when no PRD row exists yet (auto-skeleton hasn't run).
export interface PRDGetEmpty {
  sub_flow_id: string;
  prd: null;
  note: string;
}

export type PRDGetResult = PRDFull | PRDGetEmpty;

export function isPRDFull(r: PRDGetResult): r is PRDFull {
  return (r as PRDFull).id !== undefined;
}
