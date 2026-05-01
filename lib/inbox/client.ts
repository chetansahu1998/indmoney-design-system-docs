"use client";

/**
 * lib/inbox/client.ts — browser wrappers for the Phase 4 designer inbox.
 *
 * Mirrors lib/projects/client.ts conventions: the JWT lives in
 * zustand-persist, every call returns the discriminated `ApiResult`
 * envelope, no thrown errors. See lib/projects/client.ts for the
 * rationale around the client-only constraint.
 */

import { getToken } from "../auth-client";

export interface InboxRow {
  violation_id: string;
  version_id: string;
  screen_id: string;
  flow_id: string;
  project_id: string;
  project_slug: string;
  project_name: string;
  product: string;
  flow_name: string;
  rule_id: string;
  category: string;
  severity: "critical" | "high" | "medium" | "low" | "info";
  property: string;
  observed?: string;
  suggestion?: string;
  persona_id?: string;
  mode_label?: string;
  auto_fixable: boolean;
  status: "active" | "acknowledged" | "dismissed" | "fixed";
  created_at: string; // RFC3339
}

export interface InboxResponse {
  rows: InboxRow[];
  total: number;
  limit: number;
  offset: number;
}

export interface InboxFilters {
  rule_id?: string;
  category?: string;
  persona_id?: string;
  mode?: string;
  project_id?: string;
  severity?: string[];
  date_from?: string; // RFC3339
  date_to?: string;
  limit?: number;
  offset?: number;
}

export type ApiOk<T> = { ok: true; data: T };
export type ApiErr = { ok: false; status: number; error: string };
export type ApiResult<T> = ApiOk<T> | ApiErr;

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL ?? "http://localhost:8080";
}

function authedHeaders(extra?: Record<string, string>): Record<string, string> {
  const token = getToken();
  const headers: Record<string, string> = {
    Accept: "application/json",
    ...extra,
  };
  if (token) headers["Authorization"] = `Bearer ${token}`;
  return headers;
}

async function safeJSONErr(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: string; detail?: string };
    return body.detail ?? body.error ?? `HTTP ${res.status}`;
  } catch {
    return `HTTP ${res.status}`;
  }
}

function buildInboxQS(f: InboxFilters): string {
  const params = new URLSearchParams();
  if (f.rule_id) params.set("rule_id", f.rule_id);
  if (f.category) params.set("category", f.category);
  if (f.persona_id) params.set("persona_id", f.persona_id);
  if (f.mode) params.set("mode", f.mode);
  if (f.project_id) params.set("project_id", f.project_id);
  if (f.date_from) params.set("date_from", f.date_from);
  if (f.date_to) params.set("date_to", f.date_to);
  if (typeof f.limit === "number") params.set("limit", String(f.limit));
  if (typeof f.offset === "number") params.set("offset", String(f.offset));
  if (f.severity && f.severity.length > 0) {
    for (const s of f.severity) params.append("severity", s);
  }
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

/**
 * GET /v1/inbox — designer personal inbox of Active violations.
 */
export async function fetchInbox(
  filters: InboxFilters = {},
): Promise<ApiResult<InboxResponse>> {
  try {
    const res = await fetch(`${dsBaseURL()}/v1/inbox${buildInboxQS(filters)}`, {
      method: "GET",
      headers: authedHeaders(),
    });
    if (!res.ok) {
      return {
        ok: false,
        status: res.status,
        error: await safeJSONErr(res),
      };
    }
    const data = (await res.json()) as InboxResponse;
    return { ok: true, data };
  } catch (err) {
    return {
      ok: false,
      status: 0,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

export type LifecycleAction = "acknowledge" | "dismiss" | "reactivate";

export interface LifecycleResponse {
  violation_id: string;
  from: string;
  to: string;
  action: string;
}

/**
 * PATCH /v1/projects/:slug/violations/:id — single-row lifecycle transition.
 * Mirrors the Phase 4 U1 endpoint. Used by the inbox + ViolationsTab row
 * controls.
 */
export async function patchViolationLifecycle(
  slug: string,
  violationID: string,
  action: LifecycleAction,
  reason: string,
  linkDecisionID?: string,
): Promise<ApiResult<LifecycleResponse>> {
  try {
    const body: Record<string, string> = { action, reason };
    if (linkDecisionID) body.link_decision_id = linkDecisionID;
    const res = await fetch(
      `${dsBaseURL()}/v1/projects/${encodeURIComponent(slug)}/violations/${encodeURIComponent(violationID)}`,
      {
        method: "PATCH",
        headers: authedHeaders({ "Content-Type": "application/json" }),
        body: JSON.stringify(body),
      },
    );
    if (!res.ok) {
      return {
        ok: false,
        status: res.status,
        error: await safeJSONErr(res),
      };
    }
    const data = (await res.json()) as LifecycleResponse;
    return { ok: true, data };
  } catch (err) {
    return {
      ok: false,
      status: 0,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

export interface BulkLifecycleResponse {
  bulk_id: string;
  updated: string[];
  skipped: string[];
  action: string;
}

// ─── Phase 4.1 — tenant-scoped SSE subscription ──────────────────────────

export interface InboxLifecycleEvent {
  project_slug: string;
  version_id: string;
  violation_id: string;
  tenant_id: string;
  from: string;
  to: string;
  action: string;
  actor_user_id: string;
}

// Phase 5.2 P3 — decision_changed event relayed on the same channel.
export interface DecisionChangedEvent {
  project_slug: string;
  flow_id: string;
  decision_id: string;
  tenant_id: string;
  status: string;
  action: "created" | "superseded" | "admin_reactivated";
  actor_user_id?: string;
}

// Module-level listener sets the subscribers fan out into. The inbox
// SSE pipe is one EventSource per page; per-block listeners piggyback
// on it to avoid N parallel sockets.
type DecisionListener = (event: DecisionChangedEvent) => void;
const decisionListeners = new Set<DecisionListener>();

type ViolationLifecycleListener = (event: InboxLifecycleEvent) => void;
const violationLifecycleListeners = new Set<ViolationLifecycleListener>();

/**
 * subscribeDecisionChanges registers a listener that's invoked on every
 * `project.decision_changed` event the existing tenant-inbox SSE pipe
 * receives. Returns an unsubscribe.
 */
export function subscribeDecisionChanges(cb: DecisionListener): () => void {
  decisionListeners.add(cb);
  return () => {
    decisionListeners.delete(cb);
  };
}

/**
 * subscribeViolationLifecycle — Phase 5.2 P3. Same pattern as
 * subscribeDecisionChanges; lets violationRef custom blocks re-fetch
 * when a violation transitions in any other surface.
 */
export function subscribeViolationLifecycle(cb: ViolationLifecycleListener): () => void {
  violationLifecycleListeners.add(cb);
  return () => {
    violationLifecycleListeners.delete(cb);
  };
}

interface TicketResponse {
  ticket: string;
  trace_id: string;
  expires_in: number;
}

/**
 * Subscribes to /v1/inbox/events for cross-project lifecycle updates.
 * Mirrors lib/projects/client.ts:subscribeProjectEvents — single-use
 * ticket auth + reconnect with exponential backoff.
 */
export function subscribeInboxEvents(
  onEvent: (event: InboxLifecycleEvent) => void,
): () => void {
  let cancelled = false;
  let es: EventSource | null = null;
  let backoffMs = 1000;
  const BACKOFF_MAX_MS = 15_000;

  async function mintAndOpen(): Promise<void> {
    if (cancelled) return;
    const token = getToken();
    if (!token) return;
    let ticketRes: Response;
    try {
      ticketRes = await fetch(`${dsBaseURL()}/v1/inbox/events/ticket`, {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: "{}",
      });
    } catch {
      scheduleReconnect();
      return;
    }
    if (cancelled) return;
    if (!ticketRes.ok) {
      scheduleReconnect();
      return;
    }
    const t = (await ticketRes.json()) as TicketResponse;
    es = new EventSource(
      `${dsBaseURL()}/v1/inbox/events?ticket=${encodeURIComponent(t.ticket)}`,
    );
    es.onopen = () => {
      backoffMs = 1000;
    };
    es.addEventListener("project.violation_lifecycle_changed", (raw) => {
      try {
        const data = JSON.parse((raw as MessageEvent<string>).data) as InboxLifecycleEvent;
        onEvent(data);
        violationLifecycleListeners.forEach((cb) => cb(data));
      } catch {
        // ignore malformed event
      }
    });
    // Phase 5.2 P3 — relay decision_changed too. Custom DRD blocks
    // subscribe via subscribeDecisionChanges below; this listener
    // lets the same SSE stream serve both surfaces.
    es.addEventListener("project.decision_changed", (raw) => {
      try {
        const data = JSON.parse((raw as MessageEvent<string>).data) as DecisionChangedEvent;
        decisionListeners.forEach((cb) => cb(data));
      } catch {
        // ignore malformed event
      }
    });
    es.onerror = () => {
      es?.close();
      es = null;
      if (!cancelled) scheduleReconnect();
    };
  }

  function scheduleReconnect(): void {
    if (cancelled) return;
    const delay = backoffMs;
    backoffMs = Math.min(BACKOFF_MAX_MS, backoffMs * 2);
    setTimeout(() => void mintAndOpen(), delay);
  }

  void mintAndOpen();
  return () => {
    cancelled = true;
    es?.close();
    es = null;
  };
}

/**
 * POST /v1/projects/:slug/violations/bulk-acknowledge — N-at-a-time
 * lifecycle transition. Caps at 100 ids per request server-side; caller
 * is responsible for chunking larger selections.
 */
export async function bulkPatchViolations(
  slug: string,
  violationIDs: string[],
  action: LifecycleAction,
  reason: string,
): Promise<ApiResult<BulkLifecycleResponse>> {
  try {
    const res = await fetch(
      `${dsBaseURL()}/v1/projects/${encodeURIComponent(slug)}/violations/bulk-acknowledge`,
      {
        method: "POST",
        headers: authedHeaders({ "Content-Type": "application/json" }),
        body: JSON.stringify({
          action,
          reason,
          violation_ids: violationIDs,
        }),
      },
    );
    if (!res.ok) {
      return {
        ok: false,
        status: res.status,
        error: await safeJSONErr(res),
      };
    }
    const data = (await res.json()) as BulkLifecycleResponse;
    return { ok: true, data };
  } catch (err) {
    return {
      ok: false,
      status: 0,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}
