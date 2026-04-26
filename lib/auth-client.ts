/**
 * Frontend auth helpers — JWT in memory + localStorage backup.
 *
 * Note: per OWASP ASVS V3.5.3 we should NOT store access tokens in localStorage,
 * but for v1 (single-user, local development, no public site exposure) we do
 * for cross-tab persistence. v1.1 will move to httpOnly cookies via ds-service.
 */

import { create } from "zustand";
import { persist } from "zustand/middleware";

interface AuthState {
  token: string | null;
  email: string | null;
  role: string | null;
  setSession: (token: string, email: string, role: string) => void;
  logout: () => void;
}

export const useAuth = create<AuthState>()(
  persist(
    (set) => ({
      token: null,
      email: null,
      role: null,
      setSession: (token, email, role) => set({ token, email, role }),
      logout: () => set({ token: null, email: null, role: null }),
    }),
    { name: "indmoney-ds-auth" },
  ),
);

/** Login via ds-service direct call (bypasses Next.js — same-origin in dev, CORS in prod). */
export async function login(email: string, password: string): Promise<{ ok: true } | { ok: false; error: string }> {
  const dsUrl = process.env.NEXT_PUBLIC_DS_SERVICE_URL ?? "http://localhost:8080";
  try {
    const res = await fetch(`${dsUrl}/v1/auth/login`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email, password }),
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      return { ok: false, error: body.error ?? `HTTP ${res.status}` };
    }
    const body = await res.json();
    useAuth.getState().setSession(body.access_token, body.user.email, body.user.role);
    return { ok: true };
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : String(err) };
  }
}

/** Trigger sync via /api/sync proxy. */
export async function triggerSync(brand: string): Promise<{ ok: true; traceId: string; status: string } | { ok: false; error: string }> {
  const token = useAuth.getState().token;
  if (!token) return { ok: false, error: "not authenticated" };
  try {
    const res = await fetch("/api/sync", {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
      body: JSON.stringify({ brand }),
    });
    const body = await res.json();
    if (!res.ok || !body.ok) {
      return { ok: false, error: body.detail ?? body.error ?? `HTTP ${res.status}` };
    }
    return { ok: true, traceId: body.traceId, status: body.status };
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : String(err) };
  }
}
