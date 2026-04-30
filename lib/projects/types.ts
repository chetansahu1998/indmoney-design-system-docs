/**
 * TypeScript mirror of `services/ds-service/internal/projects/types.go`.
 *
 * Field names match the Go struct field names converted to JSON-friendly
 * snake_case where the Go side serializes with json tags, otherwise the
 * literal Go field name (kept as-is). The ds-service handlers currently
 * marshal via the default reflect-based json encoder, so unexported tags →
 * exact field names with default casing rules. Fields that the Go side
 * exposes via explicit json tags (the wire-shape helpers) keep their tag.
 *
 * Why this file exists separately from the wire-shape helpers in the same
 * Go package: the docs-site never sends `ExportRequest` (that's plugin →
 * server). U6 only consumes the read shapes — Project / ProjectVersion /
 * Flow / Persona / Screen / ScreenMode / AuditJob / Violation — but we ship
 * every type so later phases (U8 JSON tab, U9 DRD tab, U10 Violations) can
 * reuse the same module without churn.
 */

// ─── DB row mirrors ──────────────────────────────────────────────────────────

export interface Project {
  /** UUID, server-assigned. */
  ID: string;
  /** URL slug; unique within tenant. */
  Slug: string;
  Name: string;
  /** "mobile" | "web". */
  Platform: string;
  /** Free-text product (Plutus, Tax, Indian Stocks, …). */
  Product: string;
  /** Free-text path within the product (Onboarding, F&O, …). */
  Path: string;
  OwnerUserID: string;
  TenantID: string;
  /** ISO timestamp; null when not soft-deleted. */
  DeletedAt?: string | null;
  CreatedAt: string;
  UpdatedAt: string;
}

export type ProjectVersionStatus = "pending" | "view_ready" | "failed";

export interface ProjectVersion {
  ID: string;
  ProjectID: string;
  TenantID: string;
  /** Monotonic per-project, 1-indexed. */
  VersionIndex: number;
  Status: ProjectVersionStatus;
  PipelineStartedAt?: string | null;
  PipelineHeartbeatAt?: string | null;
  Error: string;
  CreatedByUserID: string;
  CreatedAt: string;
}

export interface Flow {
  ID: string;
  ProjectID: string;
  TenantID: string;
  /** Figma file_id, e.g. "abc123". */
  FileID: string;
  /** Figma section node ID; null if the flow was synthetic. */
  SectionID?: string | null;
  Name: string;
  PersonaID?: string | null;
  DeletedAt?: string | null;
  CreatedAt: string;
  UpdatedAt: string;
}

export type PersonaStatus = "approved" | "pending";

export interface Persona {
  ID: string;
  TenantID: string;
  Name: string;
  Status: PersonaStatus;
  CreatedByUserID: string;
  ApprovedByUserID?: string | null;
  ApprovedAt?: string | null;
  DeletedAt?: string | null;
  CreatedAt: string;
}

export interface Screen {
  ID: string;
  VersionID: string;
  FlowID: string;
  TenantID: string;
  X: number;
  Y: number;
  Width: number;
  Height: number;
  /** Stable across re-exports of the same Figma frame within a flow. */
  ScreenLogicalID: string;
  /** Relative path under data/screens/; null if PNG not yet rendered. */
  PNGStorageKey?: string | null;
  CreatedAt: string;
}

export interface ScreenMode {
  ID: string;
  ScreenID: string;
  TenantID: string;
  /** "light" | "dark" | "default" | free-text. */
  ModeLabel: string;
  FigmaFrameID: string;
  /** JSON-encoded explicit Variable Modes payload. */
  ExplicitVariableModesJSON: string;
}

export type AuditJobStatus = "queued" | "running" | "done" | "failed";

/**
 * What kicked off an audit job. Phase 1 only ever emitted "export"; Phase 2's
 * fan-out endpoint and DS-lead admin actions add the other two.
 */
export type AuditJobTrigger =
  | "export"
  | "rule_change"
  | "tokens_published";

export interface AuditJob {
  ID: string;
  VersionID: string;
  TenantID: string;
  Status: AuditJobStatus;
  TraceID: string;
  IdempotencyKey: string;
  LeasedBy?: string | null;
  LeaseExpiresAt?: number | null;
  CreatedAt: string;
  StartedAt?: string | null;
  CompletedAt?: string | null;
  Error: string;
  /** Phase 2 (migration 0002): default 50 (routine), 100 = recently-edited
   *  flow, 10 = fan-out re-audit. */
  Priority?: number;
  /** Phase 2 (migration 0002). */
  TriggeredBy?: AuditJobTrigger;
  /** Phase 2: JSON metadata (e.g. {fanout_id, trigger, reason, rule_id}). */
  Metadata?: string | null;
}

export type ViolationSeverity =
  | "critical"
  | "high"
  | "medium"
  | "low"
  | "info";

export type ViolationStatus =
  | "active"
  | "acknowledged"
  | "dismissed"
  | "fixed";

/**
 * Phase 2 (migration 0002) added Category to violations. Used by U11 filter
 * chips on the Violations tab. Backfilled via migration UPDATE statements;
 * new rows set explicitly by Phase 2 RuleRunners.
 */
export type ViolationCategory =
  | "theme_parity"
  | "cross_persona"
  | "a11y_contrast"
  | "a11y_touch_target"
  | "flow_graph"
  | "component_governance"
  | "token_drift"
  | "text_style_drift"
  | "spacing_drift"
  | "radius_drift"
  | "component_match";

export interface Violation {
  ID: string;
  VersionID: string;
  ScreenID: string;
  TenantID: string;
  RuleID: string;
  Severity: ViolationSeverity;
  /** Phase 2: filter chip key. Default 'token_drift' on legacy rows. */
  Category: ViolationCategory;
  Property: string;
  Observed: string;
  Suggestion: string;
  PersonaID?: string | null;
  ModeLabel?: string | null;
  Status: ViolationStatus;
  /** Phase 2: drives the Phase 4 Fix-in-Figma CTA. */
  AutoFixable?: boolean;
  CreatedAt: string;
}

// ─── Wire-shape helpers (response envelopes) ─────────────────────────────────

/**
 * Response of GET /v1/projects.
 *
 * Server returns `{ projects: [...], count: N }`. Phase 1 caps `count` at 100.
 */
export interface ListProjectsResponse {
  projects: Project[];
  count: number;
}

/**
 * Response of GET /v1/projects/{slug}.
 *
 * Phase 1 ships only `project`. U7+ extends with `versions`, `flows`, etc.
 * The client tolerates extra fields — we use this struct purely for narrowing.
 */
export interface FetchProjectResponse {
  project: Project;
  // The plan's later units (U7/U8) extend the GET response with these arrays;
  // we declare them as optional today so client.ts can read them without a
  // version bump on the server side.
  versions?: ProjectVersion[];
  flows?: Flow[];
  screens?: Screen[];
  screen_modes?: ScreenMode[];
  active_persona?: Persona | null;
  available_personas?: Persona[];
}

/**
 * Response of POST /v1/projects/{slug}/events/ticket.
 */
export interface EventsTicketResponse {
  ticket: string;
  trace_id: string;
  expires_in: number;
}

/**
 * One SSE event payload — the union of all event types the broker publishes.
 * Phase 1 emits two: `view_ready` (fast preview ready) and `audit_complete`.
 * U6 only inspects `type` for toast routing; later units narrow further.
 */
export type ProjectEventType =
  | "view_ready"
  | "audit_complete"
  | "audit_failed"
  | "export_failed";

export interface ProjectEvent {
  type: ProjectEventType;
  /** Server-attached. Helps the UI ignore stale events after navigation. */
  trace_id?: string;
  /** Free-form payload — type-specific shape; UI inspects opportunistically. */
  data?: Record<string, unknown>;
}

/**
 * Filters for listViolations(). Phase 1 supports persona + mode_label only;
 * Phase 2 U11 adds the `category` filter for the new chip row.
 */
export interface ViolationsFilters {
  persona_id?: string;
  mode_label?: string;
  /** Phase 2 U11: comma-joined list of ViolationCategory values, or omit
   *  for "all categories". */
  category?: ViolationCategory[];
}
