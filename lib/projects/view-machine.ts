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
  | { kind: "complete"; completedTotal?: number }
  | {
      kind: "running";
      completed: number;
      total: number;
      /** Monotonic counter incremented on every accepted progress tick.
       *  Used to drop out-of-order ticks (U5: stale events fired after a
       *  later tick has already landed). */
      lastTickAt: number;
    }
  | { kind: "failed"; error: string };

/** Top-level project-view state. Every render branch in ProjectShell
 *  maps to exactly one of these kinds. */
export type ProjectViewMachineState =
  | { kind: "loading" }
  | { kind: "view_ready"; audit: AuditStatus; readOnly: boolean }
  | { kind: "pending"; landingSince: number }
  | { kind: "permission_denied"; reason: "preview" | "acl" }
  | { kind: "version_not_found"; requestedVersionID: string }
  | { kind: "error"; message: string; statusCode: number }
  /** Plan 2026-05-03-001 / T4. Pipeline render failed (Figma timeout, basisu
   *  crash, etc.). The atlas can't render — screens have NULL png_storage_key
   *  and the PNG handler 404s them. ProjectShell hides the shell entirely
   *  and shows a retry CTA that POSTs `/v1/projects/:slug/versions/:vid/retry`.
   *  Pre-T4 a failed version landed in `view_ready/audit:failed` which kept
   *  the shell mounted on top of broken data. */
  | { kind: "export_failed"; versionID: string; error: string };

// ─── Actions ────────────────────────────────────────────────────────────────

export type ProjectViewMachineAction =
  /** GET /v1/projects/:slug succeeded; record any version status hint. */
  | {
      type: "fetch_succeeded";
      versions: ProjectVersion[];
      activeVersionStatus?: "pending" | "view_ready" | "failed";
      activeVersionError?: string;
      activeVersionID?: string;
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
  | {
      type: "audit_progress";
      completed: number;
      total: number;
      /** Wall-clock timestamp when the event was received, in ms. The
       *  reducer drops ticks whose timestamp is older than the most-
       *  recently-accepted tick (U5: out-of-order SSE events). */
      receivedAt?: number;
    }
  /** SSE: project.audit_complete. */
  | { type: "audit_complete"; finalCount?: number }
  /** SSE: project.audit_failed. */
  | { type: "audit_failed"; error: string }
  /** SSE: project.export_failed mid-pending. */
  | { type: "export_failed"; error: string; versionID?: string }
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
  /** Active version's `error` column (for the export_failed branch). */
  activeVersionError?: string;
  /** Active version's ID — needed by the retry CTA target. */
  activeVersionID?: string;
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
    // T4 — explicit export_failed state. Pre-T4 we routed failed versions
    // to view_ready/audit:failed which still rendered the shell on top of
    // broken data; the atlas would 404 every screen PNG forever.
    return {
      kind: "export_failed",
      versionID: opts.activeVersionID ?? "",
      error: opts.activeVersionError || "Export failed.",
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
        // T4 — export_failed kind so ProjectShell hides the shell and
        // surfaces the retry CTA instead of rendering on top of broken
        // (NULL png_storage_key) data.
        return {
          kind: "export_failed",
          versionID: action.activeVersionID ?? "",
          error: action.activeVersionError || "Export failed.",
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
      // SSE view_ready: the backend pipeline has finished the fast
      // preview — the atlas + tabs can render now, but the audit is
      // still running. U5: this is the moment the spinner should appear
      // in the toolbar.
      //
      // From `pending`: fresh-export deeplink path — promote into
      //   view_ready/running so the shell mounts with the audit-running
      //   indicator visible.
      // From `view_ready/complete` (the SSR cold-start path): a redundant
      //   view_ready event after page hydration. Keep `complete`; we don't
      //   want to regress a finished audit back to running.
      // From `view_ready/running` or `view_ready/failed`: ignore (already
      //   in a more-specific sub-state).
      if (state.kind === "pending") {
        return {
          kind: "view_ready",
          audit: { kind: "running", completed: 0, total: 0, lastTickAt: 0 },
          readOnly: false,
        };
      }
      return state;
    }
    case "audit_progress": {
      if (state.kind !== "view_ready") return state;
      // Don't regress from complete back to running. SSE events are
      // ordered per channel, but if a stale tick arrives after
      // audit_complete we ignore it.
      if (state.audit.kind === "complete") return state;
      if (state.audit.kind === "failed") return state;
      if (action.total <= 0) return state;
      // U5 — out-of-order tick guard. The broker doesn't guarantee
      // strict per-trace ordering across reconnects, so a late tick can
      // arrive after a newer one (e.g. completed=3 after completed=7).
      // We use the receivedAt timestamp to drop stale ticks; if the
      // caller didn't supply one, fall back to monotonic completed
      // (current tick must be ≥ previous completed to advance).
      const ts = action.receivedAt ?? Date.now();
      if (state.audit.kind === "running") {
        if (ts < state.audit.lastTickAt) return state;
        if (
          ts === state.audit.lastTickAt &&
          action.completed < state.audit.completed
        ) {
          return state;
        }
      }
      return {
        ...state,
        audit: {
          kind: "running",
          completed: action.completed,
          total: action.total,
          lastTickAt: ts,
        },
      };
    }
    case "audit_complete": {
      if (state.kind !== "view_ready") return state;
      return {
        ...state,
        audit: { kind: "complete", completedTotal: action.finalCount },
      };
    }
    case "audit_failed": {
      if (state.kind !== "view_ready") return state;
      return {
        ...state,
        audit: { kind: "failed", error: action.error },
      };
    }
    case "export_failed": {
      // T4 — explicit export_failed kind. Pre-T4 we mapped this to error
      // (mid-pending) or audit:failed (post-mount). Both kept the shell
      // mounted; the new kind hides it and shows a retry CTA. Carries the
      // version_id so the CTA can target the retry endpoint.
      return {
        kind: "export_failed",
        versionID: action.versionID ?? "",
        error: action.error,
      };
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
