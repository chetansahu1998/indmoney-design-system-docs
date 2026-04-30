"use client";

/**
 * lib/projects/view-machine.ts — Phase 3 U7 — project-view state machine.
 *
 * Replaces the ad-hoc loading state ProjectShell carried in Phase 1+2
 * with a reducer-driven discriminated union. Every project-view render
 * branch maps to exactly one state; transitions are explicit and
 * unit-testable.
 *
 * The 9 states from the Phase 3 plan flatten into 6 top-level kinds
 * because the plan's audit sub-states (running / complete / failed) all
 * render the same shell — only the Violations tab cares about which one
 * is active. So we collapse them into an `audit` discriminator inside
 * the `view_ready` kind.
 *
 *   plan                                view-machine
 *   ─────────                           ──────────────────────────────
 *   loading                          → { kind: "loading" }
 *   view_ready                       → { kind: "view_ready", audit: complete }
 *   audit_running                    → { kind: "view_ready", audit: running }
 *   audit_complete                   → { kind: "view_ready", audit: complete }
 *   audit_failed                     → { kind: "view_ready", audit: failed }
 *   pending                          → { kind: "pending" }
 *   permission_denied                → { kind: "permission_denied" }
 *   version_not_found                → { kind: "version_not_found" }
 *   error                            → { kind: "error" }
 *
 * Why this scope: the audit's running/complete/failed sub-state already
 * has dedicated UI in U6 (ViolationsTab progress bar). The state
 * machine doesn't need separate top-level kinds for them; one
 * `view_ready` kind with a sub-state field keeps the render branching
 * shallow.
 *
 * Versions / screens / personas / screen_modes data stay as separate
 * useState in ProjectShell — they're orthogonal to "should we show the
 * shell or the loading state?" and lifting them into the reducer would
 * bloat the action space without UX payoff.
 */

import type { ProjectVersion } from "./types";

// ─── State ──────────────────────────────────────────────────────────────────

/** Audit sub-state inside `view_ready`. */
export type AuditStatus =
  | { kind: "complete" }
  | { kind: "running"; completed: number; total: number }
  | { kind: "failed"; error: string };

/** Top-level project-view state. Every render branch in ProjectShell
 *  maps to exactly one of these kinds. */
export type ProjectViewMachineState =
  | { kind: "loading" }
  | { kind: "view_ready"; audit: AuditStatus; readOnly: boolean }
  | { kind: "pending"; landingSince: number }
  | { kind: "permission_denied"; reason: "preview" | "acl" }
  | { kind: "version_not_found"; requestedVersionID: string }
  | { kind: "error"; message: string; statusCode: number };

// ─── Actions ────────────────────────────────────────────────────────────────

export type ProjectViewMachineAction =
  /** GET /v1/projects/:slug succeeded; record any version status hint. */
  | {
      type: "fetch_succeeded";
      versions: ProjectVersion[];
      activeVersionStatus?: "pending" | "view_ready" | "failed";
      readOnly: boolean;
    }
  /** GET /v1/projects/:slug failed. Triggers `error`, `version_not_found`,
   *  or `permission_denied` depending on the status code. */
  | {
      type: "fetch_failed";
      statusCode: number;
      message: string;
      requestedVersionID?: string;
    }
  /** SSE: project.view_ready landed. */
  | { type: "view_ready" }
  /** SSE: project.audit_progress tick (per Phase 3 U6 — per-rule). */
  | { type: "audit_progress"; completed: number; total: number }
  /** SSE: project.audit_complete. */
  | { type: "audit_complete" }
  /** SSE: project.audit_failed. */
  | { type: "audit_failed"; error: string }
  /** SSE: project.export_failed mid-pending. */
  | { type: "export_failed"; error: string }
  /** Query param ?read_only_preview=1 → simulate Phase 7 ACL denial.
   *  Phase 7 will replace this with a server-resolved ACL flag. */
  | { type: "permission_denied_detected"; reason: "preview" | "acl" }
  /** User clicks "Try again" on the error variant or hits the loading
   *  retry from RetryableError. */
  | { type: "retry" };

// ─── Initial state ──────────────────────────────────────────────────────────

/**
 * Builds the initial state. Phase 1+2 ProjectShell receives the project
 * payload server-rendered, so the most common entry path is
 * `view_ready`/`audit_complete`. The machine accepts the initial
 * versions list to skip the loading kind on hydration.
 */
export function initialState(opts: {
  /** Versions array from the SSR payload (may be empty when single-version). */
  initialVersions: ProjectVersion[];
  /** Active version's status — drives audit sub-state on hydration. */
  activeVersionStatus: "pending" | "view_ready" | "failed";
  /** ?read_only_preview=1 → start in permission_denied. */
  permissionDeniedFromQuery: boolean;
}): ProjectViewMachineState {
  if (opts.permissionDeniedFromQuery) {
    return { kind: "permission_denied", reason: "preview" };
  }
  if (opts.activeVersionStatus === "pending") {
    return { kind: "pending", landingSince: Date.now() };
  }
  if (opts.activeVersionStatus === "failed") {
    return {
      kind: "view_ready",
      audit: { kind: "failed", error: "Export failed; see audit logs." },
      readOnly: false,
    };
  }
  // view_ready (the most common cold-start path).
  return {
    kind: "view_ready",
    audit: { kind: "complete" },
    readOnly: false,
  };
}

// ─── Reducer ────────────────────────────────────────────────────────────────

/**
 * State transitions. Pure function — no side effects, no async. Side
 * effects (SSE subscription, fetch retries, URL navigation) live in
 * useEffects in ProjectShell that dispatch actions; the reducer just
 * decides what state to land in.
 */
export function reducer(
  state: ProjectViewMachineState,
  action: ProjectViewMachineAction,
): ProjectViewMachineState {
  switch (action.type) {
    case "fetch_succeeded": {
      // permission_denied always wins (set by query param at mount); a
      // successful fetch shouldn't downgrade to view_ready under the
      // preview flag.
      if (state.kind === "permission_denied") return state;
      if (action.activeVersionStatus === "pending") {
        return { kind: "pending", landingSince: Date.now() };
      }
      if (action.activeVersionStatus === "failed") {
        return {
          kind: "view_ready",
          audit: { kind: "failed", error: "Export failed." },
          readOnly: action.readOnly,
        };
      }
      return {
        kind: "view_ready",
        audit: { kind: "complete" },
        readOnly: action.readOnly,
      };
    }
    case "fetch_failed": {
      if (action.statusCode === 404 && action.requestedVersionID) {
        return {
          kind: "version_not_found",
          requestedVersionID: action.requestedVersionID,
        };
      }
      if (action.statusCode === 403) {
        return { kind: "permission_denied", reason: "acl" };
      }
      return {
        kind: "error",
        message: action.message,
        statusCode: action.statusCode,
      };
    }
    case "view_ready": {
      // Pending → view_ready. SSE event during pending state means the
      // backend pipeline finished the fast preview; the audit may still
      // be running, but the atlas can render now.
      if (state.kind !== "pending") return state;
      return {
        kind: "view_ready",
        audit: { kind: "complete" },
        readOnly: false,
      };
    }
    case "audit_progress": {
      if (state.kind !== "view_ready") return state;
      // Don't regress from complete back to running. SSE events are
      // ordered per channel, but if a stale tick arrives after
      // audit_complete we ignore it.
      if (state.audit.kind === "complete") return state;
      if (action.total <= 0) return state;
      return {
        ...state,
        audit: {
          kind: "running",
          completed: action.completed,
          total: action.total,
        },
      };
    }
    case "audit_complete": {
      if (state.kind !== "view_ready") return state;
      return { ...state, audit: { kind: "complete" } };
    }
    case "audit_failed": {
      if (state.kind !== "view_ready") return state;
      return {
        ...state,
        audit: { kind: "failed", error: action.error },
      };
    }
    case "export_failed": {
      // Mid-pending failure: bail to error so the retry path works.
      if (state.kind === "pending") {
        return { kind: "error", message: action.error, statusCode: 500 };
      }
      // Otherwise treat as audit_failed (best approximation).
      if (state.kind === "view_ready") {
        return {
          ...state,
          audit: { kind: "failed", error: action.error },
        };
      }
      return state;
    }
    case "permission_denied_detected": {
      return { kind: "permission_denied", reason: action.reason };
    }
    case "retry": {
      return { kind: "loading" };
    }
    default:
      return state;
  }
}

// ─── Helpers ────────────────────────────────────────────────────────────────

/** True when the project shell (atlas + 4 tabs) should render. */
export function shouldRenderShell(state: ProjectViewMachineState): boolean {
  return state.kind === "view_ready" || state.kind === "permission_denied";
}

/** True when the DRD editor's edit affordances should be disabled. */
export function isReadOnly(state: ProjectViewMachineState): boolean {
  if (state.kind === "permission_denied") return true;
  if (state.kind === "view_ready") return state.readOnly;
  return false;
}

/** Audit progress tuple for the Violations tab; null when no audit is
 *  in flight. */
export function auditProgressFromState(
  state: ProjectViewMachineState,
): { completed: number; total: number } | null {
  if (state.kind !== "view_ready") return null;
  if (state.audit.kind !== "running") return null;
  return { completed: state.audit.completed, total: state.audit.total };
}
