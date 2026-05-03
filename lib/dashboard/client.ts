"use client";

/**
 * Dashboard summary client — Phase 4 U10. Mirrors lib/inbox/client.ts shape.
 */

import { getToken } from "../auth-client";

export interface ProductCount {
  product: string;
  active: number;
}

export interface TrendBucket {
  week_start: string;
  active: number;
  fixed: number;
}

export interface TopViolator {
  rule_id: string;
  category: string;
  active_count: number;
  highest_severity: string;
}

export interface DashboardDecision {
  id: string;
  title: string;
  created_at: string;
  /** Phase 5.2 P1 — backend now returns these so the panel can deep-
   *  link to /projects/<slug>?decision=<id> + offer the admin reactivate
   *  control on superseded rows. */
  status?: "proposed" | "accepted" | "superseded";
  flow_id?: string;
  slug?: string;
}

/**
 * POST /v1/atlas/admin/decisions/:id/reactivate — super-admin only.
 * Flips a superseded decision back to accepted; idempotent on non-
 * superseded rows.
 */
export async function reactivateDecision(
  decisionID: string,
): Promise<{ ok: true; updated: number } | { ok: false; status: number; error: string }> {
  try {
    const token = getToken();
    const headers: Record<string, string> = { Accept: "application/json" };
    if (token) headers["Authorization"] = `Bearer ${token}`;
    const res = await fetch(
      `${dsBaseURL()}/v1/atlas/admin/decisions/${encodeURIComponent(decisionID)}/reactivate`,
      { method: "POST", headers },
    );
    if (!res.ok) {
      let msg = `HTTP ${res.status}`;
      try {
        const body = (await res.json()) as { error?: string; detail?: string };
        msg = body.detail ?? body.error ?? msg;
      } catch {}
      return { ok: false, status: res.status, error: msg };
    }
    const data = (await res.json()) as { updated: number };
    return { ok: true, updated: data.updated };
  } catch (err) {
    return {
      ok: false,
      status: 0,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

export interface DashboardSummary {
  weeks_window: number;
  by_product: ProductCount[];
  by_severity: Record<string, number>;
  trend: TrendBucket[];
  top_violators: TopViolator[];
  recent_decisions: DashboardDecision[];
  total_active: number;
  generated_at: string;
}

export type ApiOk<T> = { ok: true; data: T };
export type ApiErr = { ok: false; status: number; error: string };
export type ApiResult<T> = ApiOk<T> | ApiErr;

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL || "http://localhost:8080";
}

export async function fetchDashboardSummary(
  weeks: 4 | 8 | 12 | 24 = 8,
): Promise<ApiResult<DashboardSummary>> {
  try {
    const token = getToken();
    const headers: Record<string, string> = { Accept: "application/json" };
    if (token) headers["Authorization"] = `Bearer ${token}`;
    const res = await fetch(
      `${dsBaseURL()}/v1/atlas/admin/summary?weeks=${weeks}`,
      { headers },
    );
    if (!res.ok) {
      let msg = `HTTP ${res.status}`;
      try {
        const body = (await res.json()) as { error?: string; detail?: string };
        msg = body.detail ?? body.error ?? msg;
      } catch {}
      return { ok: false, status: res.status, error: msg };
    }
    const data = (await res.json()) as DashboardSummary;
    return { ok: true, data };
  } catch (err) {
    return {
      ok: false,
      status: 0,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}
