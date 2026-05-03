"use client";

/**
 * lib/decisions/client.ts — browser wrappers for Phase 5 Decisions.
 *
 * Mirrors lib/inbox/client.ts conventions: ApiResult envelope, JWT from
 * zustand-persist, no thrown errors. Server returns snake_case keys; we
 * surface them verbatim so the wire shape and the type are identical.
 */

import { getToken } from "../auth-client";
import type { Violation } from "../projects/types";

export type DecisionStatus = "proposed" | "accepted" | "superseded";

export type DecisionLinkType = "violation" | "screen" | "component" | "external";

export interface DecisionLink {
  decision_id: string;
  link_type: DecisionLinkType;
  target_id: string;
  created_at: string;
}

export interface Decision {
  id: string;
  tenant_id: string;
  flow_id: string;
  version_id: string;
  title: string;
  body_json?: string; // BlockNote JSON; base64 of BLOB on the wire
  status: DecisionStatus;
  made_by_user_id: string;
  made_at: string;
  superseded_by_id?: string;
  supersedes_id?: string;
  created_at: string;
  updated_at: string;
  links?: DecisionLink[];
}

export interface DecisionListResponse {
  decisions: Decision[];
  count: number;
}

export interface CreateDecisionInput {
  title: string;
  body_json?: string;
  status?: "proposed" | "accepted";
  supersedes_id?: string;
  links?: { link_type: DecisionLinkType; target_id: string }[];
  version_id?: string;
}

export type ApiOk<T> = { ok: true; data: T };
export type ApiErr = { ok: false; status: number; error: string };
export type ApiResult<T> = ApiOk<T> | ApiErr;

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL || "http://localhost:8080";
}

function authedHeaders(extra?: Record<string, string>): Record<string, string> {
  const token = getToken();
  const headers: Record<string, string> = { Accept: "application/json", ...extra };
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

/**
 * GET /v1/projects/:slug/flows/:flow_id/decisions — flow-scoped list.
 * Default excludes superseded; toggle via {includeSuperseded: true}.
 */
export async function listDecisionsForFlow(
  slug: string,
  flowID: string,
  opts: { includeSuperseded?: boolean } = {},
): Promise<ApiResult<DecisionListResponse>> {
  const qs = opts.includeSuperseded ? "?include_superseded=1" : "";
  try {
    const res = await fetch(
      `${dsBaseURL()}/v1/projects/${encodeURIComponent(slug)}/flows/${encodeURIComponent(flowID)}/decisions${qs}`,
      { headers: authedHeaders() },
    );
    if (!res.ok) {
      return { ok: false, status: res.status, error: await safeJSONErr(res) };
    }
    const data = (await res.json()) as DecisionListResponse;
    return { ok: true, data };
  } catch (err) {
    return { ok: false, status: 0, error: err instanceof Error ? err.message : String(err) };
  }
}

/** POST a new decision into a flow. Returns the persisted record. */
export async function createDecision(
  slug: string,
  flowID: string,
  input: CreateDecisionInput,
): Promise<ApiResult<Decision>> {
  try {
    const res = await fetch(
      `${dsBaseURL()}/v1/projects/${encodeURIComponent(slug)}/flows/${encodeURIComponent(flowID)}/decisions`,
      {
        method: "POST",
        headers: authedHeaders({ "Content-Type": "application/json" }),
        body: JSON.stringify(input),
      },
    );
    if (!res.ok) {
      return { ok: false, status: res.status, error: await safeJSONErr(res) };
    }
    const data = (await res.json()) as Decision;
    return { ok: true, data };
  } catch (err) {
    return { ok: false, status: 0, error: err instanceof Error ? err.message : String(err) };
  }
}

/** GET a single decision by id (tenant-scoped). */
export async function fetchDecision(id: string): Promise<ApiResult<Decision>> {
  try {
    const res = await fetch(`${dsBaseURL()}/v1/decisions/${encodeURIComponent(id)}`, {
      headers: authedHeaders(),
    });
    if (!res.ok) {
      return { ok: false, status: res.status, error: await safeJSONErr(res) };
    }
    const data = (await res.json()) as Decision;
    return { ok: true, data };
  } catch (err) {
    return { ok: false, status: 0, error: err instanceof Error ? err.message : String(err) };
  }
}

/**
 * GET /v1/decisions/:id/violations — Phase 6 U7. Lists every violation
 * linked to a decision via decision_links (link_type='violation'),
 * tenant-scoped. Backs the "Linked violations" subsection on DecisionCard.
 *
 * Returns the same Violation row shape as listViolations() so the
 * caller can reuse rendering helpers if it wants. Empty result is
 * `{violations: [], count: 0}` — the card falls back to the empty state.
 *
 * The optional `signal` lets callers cancel an in-flight request when
 * the card unmounts (or the user changes tab) so React doesn't warn
 * about setState on an unmounted component.
 */
export async function listLinkedViolations(
  decisionID: string,
  signal?: AbortSignal,
): Promise<ApiResult<{ violations: Violation[]; count: number }>> {
  try {
    const res = await fetch(
      `${dsBaseURL()}/v1/decisions/${encodeURIComponent(decisionID)}/violations`,
      { headers: authedHeaders(), signal },
    );
    if (!res.ok) {
      return { ok: false, status: res.status, error: await safeJSONErr(res) };
    }
    const data = (await res.json()) as { violations: Violation[]; count: number };
    return { ok: true, data };
  } catch (err) {
    // Aborted fetches surface as DOMException with name="AbortError" —
    // we surface that as a non-error sentinel so callers know to skip
    // setState. The status=0 + error="aborted" pair is unique to this
    // path and lets the caller distinguish from a real network error.
    if (err instanceof Error && err.name === "AbortError") {
      return { ok: false, status: 0, error: "aborted" };
    }
    return { ok: false, status: 0, error: err instanceof Error ? err.message : String(err) };
  }
}

/**
 * GET /v1/atlas/admin/decisions/recent — super-admin feed for the
 * dashboard's Recent Decisions panel. Cross-tenant; the server gates.
 */
export async function fetchRecentDecisions(
  limit = 20,
): Promise<ApiResult<DecisionListResponse>> {
  try {
    const res = await fetch(
      `${dsBaseURL()}/v1/atlas/admin/decisions/recent?limit=${limit}`,
      { headers: authedHeaders() },
    );
    if (!res.ok) {
      return { ok: false, status: res.status, error: await safeJSONErr(res) };
    }
    const data = (await res.json()) as DecisionListResponse;
    return { ok: true, data };
  } catch (err) {
    return { ok: false, status: 0, error: err instanceof Error ? err.message : String(err) };
  }
}
