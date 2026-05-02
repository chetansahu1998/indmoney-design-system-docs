"use client";

/**
 * Browser-side wrappers for the ds-service `/v1/projects/*` HTTP surface.
 *
 * Why client-only:
 *   The auth token lives in zustand-persist + localStorage (lib/auth-client.ts)
 *   and only resolves on the client. Server components cannot read it without
 *   a cookie, which Phase 1 doesn't ship. Every consumer of this module must
 *   therefore live inside a "use client" tree — server pages may render a
 *   client component which calls these functions on mount.
 *
 * Error model:
 *   Each function returns a discriminated union `{ ok: true, ... }` /
 *   `{ ok: false, status, error }`. Callers handle 401 → redirect to login,
 *   404 → show empty/not-found state, 5xx → toast + retry option. We never
 *   throw — components don't need a try/catch around every call.
 *
 * Network base URL:
 *   `process.env.NEXT_PUBLIC_DS_SERVICE_URL` matches the convention in
 *   `lib/auth-client.ts:login()`. Defaults to `http://localhost:8080` for
 *   local dev where the Go service binds to 8080.
 */

import { getToken } from "../auth-client";
import type {
  EventsTicketResponse,
  FetchProjectResponse,
  ListProjectsResponse,
  ProjectEvent,
  ProjectEventType,
  Violation,
  ViolationsFilters,
} from "./types";

// ─── Common types ───────────────────────────────────────────────────────────

export type ApiOk<T> = { ok: true; data: T };
export type ApiErr = { ok: false; status: number; error: string };
export type ApiResult<T> = ApiOk<T> | ApiErr;

// ─── Internals ──────────────────────────────────────────────────────────────

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL ?? "http://localhost:8080";
}

/** Returns headers with `Authorization: Bearer <token>` if a token is present. */
function authedHeaders(extra?: Record<string, string>): Record<string, string> {
  const token = getToken();
  const headers: Record<string, string> = {
    Accept: "application/json",
    ...extra,
  };
  if (token) headers["Authorization"] = `Bearer ${token}`;
  return headers;
}

/**
 * Generic JSON GET that wraps fetch in our `ApiResult` envelope. Centralises
 * 401 / network-failure handling so the per-resource wrappers stay tiny.
 */
async function getJSON<T>(path: string): Promise<ApiResult<T>> {
  try {
    const res = await fetch(`${dsBaseURL()}${path}`, {
      method: "GET",
      headers: authedHeaders(),
      // SSE/long-poll never use this helper, so we keep default credentials.
    });
    if (!res.ok) {
      const detail = await safeJSONErr(res);
      return { ok: false, status: res.status, error: detail };
    }
    const data = (await res.json()) as T;
    return { ok: true, data };
  } catch (err) {
    return {
      ok: false,
      status: 0,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

/** POST with JSON body; same envelope as getJSON. */
async function postJSON<T>(
  path: string,
  body: unknown,
): Promise<ApiResult<T>> {
  try {
    const res = await fetch(`${dsBaseURL()}${path}`, {
      method: "POST",
      headers: authedHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify(body ?? {}),
    });
    if (!res.ok) {
      const detail = await safeJSONErr(res);
      return { ok: false, status: res.status, error: detail };
    }
    const data = (await res.json()) as T;
    return { ok: true, data };
  } catch (err) {
    return {
      ok: false,
      status: 0,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

/** Best-effort error-detail extractor; tolerates non-JSON 5xx pages. */
async function safeJSONErr(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: string; detail?: string };
    return body.detail ?? body.error ?? `HTTP ${res.status}`;
  } catch {
    return `HTTP ${res.status}`;
  }
}

/**
 * Builds the URL for the authed PNG render route used by the U7 atlas.
 *
 *   GET /v1/projects/:slug/screens/:id/png
 *
 * The plain URL is consumed by `THREE.TextureLoader` inside r3f. The
 * Authorization header cannot be sent from a `<img>`/Texture loader; the
 * server therefore accepts a `?token=<jwt>` query-param fallback (gated
 * to GET requests only by the auth middleware in cmd/server/main.go).
 * Same risk profile as CloudFront signed URLs / Vercel image-signing
 * tokens — acceptable for an authenticated docs surface.
 *
 * The caller passes the JWT from `useAuth().token` so this function stays
 * pure (no zustand reads at module scope, SSR-safe).
 *
 * Browser caches the response under `Cache-Control: private, max-age=300`
 * (set server-side by U11), so theme-toggle round-trips don't refetch.
 */
export function screenPngUrl(slug: string, screenID: string, token?: string | null): string {
  const base = `${dsBaseURL()}/v1/projects/${encodeURIComponent(
    slug,
  )}/screens/${encodeURIComponent(screenID)}/png`;
  return token ? `${base}?token=${encodeURIComponent(token)}` : base;
}

// ─── Public API ─────────────────────────────────────────────────────────────

/**
 * GET /v1/projects — list projects accessible to the calling user (tenant-
 * scoped server-side; we never trust the client's tenant claim).
 *
 * Server response shape: `{ projects: Project[], count: number }`.
 */
export async function listProjects(): Promise<ApiResult<ListProjectsResponse>> {
  return getJSON<ListProjectsResponse>("/v1/projects");
}

/**
 * GET /v1/projects/:slug — fetch a single project + its versions/flows.
 * `versionID` is forwarded as `?v=<id>` so the server can pick a non-default
 * version for the JSON tab; omit to get the latest.
 */
export async function fetchProject(
  slug: string,
  versionID?: string,
): Promise<ApiResult<FetchProjectResponse>> {
  const qs = versionID ? `?v=${encodeURIComponent(versionID)}` : "";
  return getJSON<FetchProjectResponse>(
    `/v1/projects/${encodeURIComponent(slug)}${qs}`,
  );
}

/**
 * GET /v1/projects/:slug/flows/:flow_id/drd — fetch DRD with revision counter.
 * Returns `{revision: 0, content: {}}` when no DRD has been written yet.
 */
export interface DRDFetchResponse {
  flow_id: string;
  content: unknown;
  revision: number;
  updated_at: string | null;
  updated_by: string | null;
}

export async function fetchDRD(
  slug: string,
  flowID: string,
): Promise<ApiResult<DRDFetchResponse>> {
  return getJSON<DRDFetchResponse>(
    `/v1/projects/${encodeURIComponent(slug)}/flows/${encodeURIComponent(flowID)}/drd`,
  );
}

/**
 * PUT /v1/projects/:slug/flows/:flow_id/drd — optimistic write with revision
 * counter. 409 returns the current revision so the client can show "someone
 * else edited" UX.
 */
export interface DRDPutResponse {
  revision: number;
  updated_at: string;
}

export type DRDPutResult =
  | ApiOk<DRDPutResponse>
  | ApiErr
  | { ok: false; status: 409; conflict: { current_revision: number } };

export async function putDRD(
  slug: string,
  flowID: string,
  content: unknown,
  expectedRevision: number,
): Promise<DRDPutResult> {
  try {
    const res = await fetch(
      `${dsBaseURL()}/v1/projects/${encodeURIComponent(
        slug,
      )}/flows/${encodeURIComponent(flowID)}/drd`,
      {
        method: "PUT",
        headers: authedHeaders({ "Content-Type": "application/json" }),
        body: JSON.stringify({
          content,
          expected_revision: expectedRevision,
        }),
      },
    );
    if (res.status === 409) {
      const body = (await res.json().catch(() => ({}))) as {
        current_revision?: number;
      };
      return {
        ok: false,
        status: 409,
        conflict: { current_revision: body.current_revision ?? 0 },
      };
    }
    if (!res.ok) {
      return {
        ok: false,
        status: res.status,
        error: await safeJSONErr(res),
      };
    }
    return { ok: true, data: (await res.json()) as DRDPutResponse };
  } catch (err) {
    return {
      ok: false,
      status: 0,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

/**
 * GET /v1/projects/:slug/screens/:id/canonical-tree — lazy fetch of the
 * canonical tree blob for the JSON tab. U6 declares the wrapper but does not
 * call it; the JSON tab (U8) is the only consumer.
 *
 * Returns the canonical_tree JSON as an `unknown` so the JSON tab can pass it
 * straight to its viewer without forcing a schema here. Schema lives in U8.
 */
export async function lazyFetchCanonicalTree(
  slug: string,
  screenID: string,
): Promise<ApiResult<{ canonical_tree: unknown; hash: string | null }>> {
  return getJSON<{ canonical_tree: unknown; hash: string | null }>(
    `/v1/projects/${encodeURIComponent(slug)}/screens/${encodeURIComponent(
      screenID,
    )}/canonical-tree`,
  );
}

/**
 * GET /v1/projects/:slug/violations — list violations for the active version.
 *
 * Phase 1 anchors the ViolationsTab; full filter/sort/group surface lands in
 * U10. This wrapper returns the raw rows; the tab groups by severity.
 *
 * NOTE: the `versionID` is sent as `?v=<id>` query — the server resolves
 * latest when omitted, matching `fetchProject`. Filters serialize into the
 * same query string.
 */
export async function listViolations(
  slug: string,
  versionID?: string,
  filters?: ViolationsFilters,
): Promise<ApiResult<{ violations: Violation[]; count: number }>> {
  const params = new URLSearchParams();
  if (versionID) params.set("v", versionID);
  if (filters?.persona_id) params.set("persona_id", filters.persona_id);
  if (filters?.mode_label) params.set("mode_label", filters.mode_label);
  // Phase 2 U11: category filter — multi-select chips serialize as
  // comma-joined values. Empty array = no filter (default = show all).
  if (filters?.category && filters.category.length > 0) {
    params.set("category", filters.category.join(","));
  }
  const qs = params.toString() ? `?${params.toString()}` : "";
  return getJSON<{ violations: Violation[]; count: number }>(
    `/v1/projects/${encodeURIComponent(slug)}/violations${qs}`,
  );
}

// ─── SSE subscription ───────────────────────────────────────────────────────

/**
 * Subscribes to project events via the ds-service SSE endpoint.
 *
 * Per the Phase 1 plan (Network/Auth section) and U6 spec:
 *   1. POST /v1/projects/:slug/events/ticket with the JWT bearer to mint a
 *      single-use, time-bound ticket bound to (user, tenant, trace).
 *   2. Open `EventSource(/events?ticket=<id>)`. The browser forbids custom
 *      headers on EventSource, hence the ticket-in-querystring auth.
 *   3. On EventSource error, mint a fresh ticket and reconnect (we never
 *      replay an old ticket — they're single-use).
 *
 * Returns a cleanup function that closes the stream and prevents reconnect.
 *
 * The `traceID` argument scopes the subscription server-side: the broker
 * filters events by trace_id so two tabs of the same project don't see each
 * other's pipeline updates. Pass the traceID returned by the export response;
 * for "passive" project views (nothing in flight), pass any UUID — the
 * subscription will simply receive heartbeats.
 */
export function subscribeProjectEvents(
  slug: string,
  traceID: string,
  onEvent: (event: ProjectEvent) => void,
): () => void {
  let cancelled = false;
  let es: EventSource | null = null;

  /**
   * Reconnect attempts use exponential backoff with a cap. We never spam-
   * reconnect — if the server is down the user gets a banner via the
   * caller's error handler (out of scope for this wrapper).
   */
  let backoffMs = 1000;
  const BACKOFF_MAX_MS = 15_000;

  async function mintAndOpen(): Promise<void> {
    if (cancelled) return;
    const tres = await postJSON<EventsTicketResponse>(
      `/v1/projects/${encodeURIComponent(slug)}/events/ticket`,
      { trace_id: traceID },
    );
    if (cancelled) return;
    if (!tres.ok) {
      // Schedule a retry; don't surface here — caller's onEvent has no error
      // channel by design (use ApiResult wrappers for error-bearing flows).
      scheduleReconnect();
      return;
    }
    const url = `${dsBaseURL()}/v1/projects/${encodeURIComponent(slug)}/events?ticket=${encodeURIComponent(
      tres.data.ticket,
    )}`;
    es = new EventSource(url);

    // Reset backoff on a successful open so a future hiccup starts fresh.
    es.onopen = () => {
      backoffMs = 1000;
    };

    // The ds-service emits typed events (`event: view_ready\ndata: {...}`).
    // EventSource fires `onmessage` only for unnamed events; named events
    // need explicit listeners. We register one per known type so additions
    // server-side require a matching addition here (loud failure beats
    // silent drop).
    const types: ProjectEventType[] = [
      "view_ready",
      "audit_complete",
      "audit_failed",
      "export_failed",
      "audit_progress",
    ];
    for (const t of types) {
      es.addEventListener(t, (raw) => {
        const ev = raw as MessageEvent<string>;
        let data: Record<string, unknown> | undefined;
        try {
          data = JSON.parse(ev.data) as Record<string, unknown>;
        } catch {
          data = { raw: ev.data };
        }
        onEvent({ type: t, trace_id: traceID, data });
      });
    }

    es.onerror = () => {
      // EventSource will auto-retry by default but it'll re-use the (now-
      // invalid) ticket and 401. Tear it down and re-mint.
      es?.close();
      es = null;
      if (!cancelled) scheduleReconnect();
    };
  }

  function scheduleReconnect(): void {
    if (cancelled) return;
    const delay = backoffMs;
    backoffMs = Math.min(backoffMs * 2, BACKOFF_MAX_MS);
    window.setTimeout(() => {
      if (!cancelled) void mintAndOpen();
    }, delay);
  }

  // Kick off the first connection.
  void mintAndOpen();

  return () => {
    cancelled = true;
    es?.close();
    es = null;
  };
}
