"use client";

/**
 * lib/notifications/client.ts — browser wrappers for Phase 5 U7
 * notifications inbox API.
 */

import { getToken } from "../auth-client";

export type NotificationKind =
  | "mention"
  | "decision_made"
  | "decision_superseded"
  | "comment_resolved"
  | "drd_edited_on_owned_flow";

export interface NotificationRecord {
  id: string;
  tenant_id: string;
  recipient_user_id: string;
  kind: NotificationKind;
  target_kind?: string;
  target_id?: string;
  flow_id?: string;
  actor_user_id?: string;
  payload_json?: string;
  delivered_via?: string[];
  read_at?: string;
  created_at: string;
}

export interface NotificationListResponse {
  notifications: NotificationRecord[];
  count: number;
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

export async function listNotifications(
  opts: { unreadOnly?: boolean; limit?: number } = {},
): Promise<ApiResult<NotificationListResponse>> {
  const params = new URLSearchParams();
  if (opts.unreadOnly) params.set("unread", "1");
  if (opts.limit) params.set("limit", String(opts.limit));
  const qs = params.toString() ? `?${params.toString()}` : "";
  try {
    const res = await fetch(`${dsBaseURL()}/v1/notifications${qs}`, {
      headers: authedHeaders(),
    });
    if (!res.ok) {
      return { ok: false, status: res.status, error: await safeJSONErr(res) };
    }
    const data = (await res.json()) as NotificationListResponse;
    return { ok: true, data };
  } catch (err) {
    return { ok: false, status: 0, error: err instanceof Error ? err.message : String(err) };
  }
}

export async function markNotificationsRead(
  ids: string[],
): Promise<ApiResult<{ updated: number }>> {
  if (ids.length === 0) return { ok: true, data: { updated: 0 } };
  try {
    const res = await fetch(`${dsBaseURL()}/v1/notifications/mark-read`, {
      method: "POST",
      headers: authedHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({ ids }),
    });
    if (!res.ok) {
      return { ok: false, status: res.status, error: await safeJSONErr(res) };
    }
    const data = (await res.json()) as { updated: number };
    return { ok: true, data };
  } catch (err) {
    return { ok: false, status: 0, error: err instanceof Error ? err.message : String(err) };
  }
}
