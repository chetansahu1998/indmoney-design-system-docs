"use client";

/**
 * lib/drd/client.ts — DRD-side helpers. Phase 5 ships the activity-rail
 * fetch + comment CRUD + flow-events SSE. The Hocuspocus / Yjs collab
 * client lands when U1 ships its sidecar.
 */

import { getToken } from "../auth-client";

export interface FlowActivityEntry {
  id: string;
  ts: string;
  event_type: string;
  user_id: string;
  endpoint: string;
  status_code: number;
  details?: string;
}

export interface FlowActivityResponse {
  activity: FlowActivityEntry[];
  count: number;
}

export type ApiOk<T> = { ok: true; data: T };
export type ApiErr = { ok: false; status: number; error: string };
export type ApiResult<T> = ApiOk<T> | ApiErr;

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL ?? "http://localhost:8080";
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

export async function fetchFlowActivity(
  slug: string,
  flowID: string,
  limit = 100,
): Promise<ApiResult<FlowActivityResponse>> {
  try {
    const res = await fetch(
      `${dsBaseURL()}/v1/projects/${encodeURIComponent(slug)}/flows/${encodeURIComponent(flowID)}/activity?limit=${limit}`,
      { headers: authedHeaders() },
    );
    if (!res.ok) {
      return { ok: false, status: res.status, error: await safeJSONErr(res) };
    }
    const data = (await res.json()) as FlowActivityResponse;
    return { ok: true, data };
  } catch (err) {
    return { ok: false, status: 0, error: err instanceof Error ? err.message : String(err) };
  }
}
