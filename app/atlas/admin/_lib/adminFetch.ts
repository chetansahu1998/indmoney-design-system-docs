"use client";

/**
 * Phase 7.5 — shared fetch helper for admin pages.
 *
 * Adds the Bearer token + JSON envelope handling. Kept tiny so the admin
 * pages can stay as imperative async/await chains without dragging in the
 * lib/projects/client.ts surface (those wrappers are tied to per-project
 * routes).
 */

import { getToken } from "@/lib/auth-client";

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL || "http://localhost:8080";
}

function authedHeaders(extra?: Record<string, string>): Record<string, string> {
  const token = getToken();
  const headers: Record<string, string> = {
    Accept: "application/json",
    ...extra,
  };
  if (token) headers.Authorization = `Bearer ${token}`;
  return headers;
}

export async function adminFetchJSON<T>(
  path: string,
  init?: { method?: string; body?: unknown },
): Promise<T> {
  const headers = authedHeaders(
    init?.body !== undefined ? { "Content-Type": "application/json" } : undefined,
  );
  const res = await fetch(`${dsBaseURL()}${path}`, {
    method: init?.method ?? "GET",
    headers,
    body: init?.body !== undefined ? JSON.stringify(init.body) : undefined,
  });
  if (!res.ok) {
    let detail = `HTTP ${res.status}`;
    try {
      const body = (await res.json()) as { error?: string; detail?: string };
      detail = body.detail || body.error || detail;
    } catch {
      /* ignore */
    }
    throw new Error(detail);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}
