"use client";

/**
 * lib/telemetry.ts — fire-and-forget client telemetry.
 *
 * Posts to ds-service `/v1/telemetry/event`. Always best-effort — never
 * throws, never blocks UX. Includes a session ID so events from one
 * page-load group together in `fly logs`.
 *
 * Wired into:
 *   - app/layout.tsx → installs global window.onerror + unhandledrejection
 *   - app/atlas/_lib/AtlasShell.tsx → emits hydration + key-state events
 *   - lib/atlas/data-adapters.ts → emits SSE connect/disconnect (optional)
 */

const SESSION_ID = (() => {
  if (typeof window === "undefined") return "";
  if ((window as any).__TELEMETRY_SESSION) return (window as any).__TELEMETRY_SESSION as string;
  const id =
    (typeof crypto !== "undefined" && (crypto as any).randomUUID
      ? (crypto as any).randomUUID()
      : Date.now().toString(36) + Math.random().toString(36).slice(2));
  (window as any).__TELEMETRY_SESSION = id;
  return id;
})();

const BUILD = process.env.NEXT_PUBLIC_BUILD_SHA || "dev";

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL || "http://localhost:8080";
}

export type TelemetryLevel = "info" | "warn" | "error";

export function track(level: TelemetryLevel, event: string, payload?: Record<string, unknown>): void {
  if (typeof window === "undefined") return;
  // Cap payload size client-side too — server has its own 8KB hard cap
  // but rejecting on the wire is wasteful when we know it'll fail.
  let safePayload = payload;
  if (payload) {
    try {
      const s = JSON.stringify(payload);
      if (s.length > 6000) {
        safePayload = { _truncated: true, _size: s.length, summary: s.slice(0, 5800) + "…" };
      }
    } catch {
      safePayload = { _stringify_failed: true };
    }
  }

  const body = JSON.stringify({
    source: "web",
    level,
    event,
    payload: safePayload,
    session: SESSION_ID,
    build: BUILD,
  });

  // Use sendBeacon for unload-resilient delivery when available; fall
  // back to fetch w/ keepalive otherwise.
  try {
    const url = `${dsBaseURL()}/v1/telemetry/event`;
    if (navigator.sendBeacon) {
      const blob = new Blob([body], { type: "application/json" });
      navigator.sendBeacon(url, blob);
    } else {
      void fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
        keepalive: true,
      }).catch(() => {});
    }
  } catch {
    // Truly best-effort — never let telemetry break the app.
  }

  // Mirror to console so `Cmd+Opt+J` on the user's other Mac shows the
  // same events, in case telemetry can't reach the server (offline).
  if (level === "error") {
    console.error(`[tel] ${event}`, payload);
  } else if (level === "warn") {
    console.warn(`[tel] ${event}`, payload);
  } else {
    console.log(`[tel] ${event}`, payload);
  }
}

let installed = false;

/** Install global error capture. Idempotent — call from app/layout.tsx. */
export function installGlobalTelemetry(): void {
  if (typeof window === "undefined" || installed) return;
  installed = true;

  // Uncaught synchronous errors
  window.addEventListener("error", (e) => {
    track("error", "window.error", {
      message: e.message,
      filename: e.filename,
      lineno: e.lineno,
      colno: e.colno,
      stack: e.error?.stack ? String(e.error.stack).slice(0, 1500) : undefined,
    });
  });

  // Unhandled promise rejections
  window.addEventListener("unhandledrejection", (e) => {
    const reason: any = e.reason;
    track("error", "window.unhandledrejection", {
      message: reason?.message ?? String(reason).slice(0, 200),
      stack: reason?.stack ? String(reason.stack).slice(0, 1500) : undefined,
    });
  });

  // First-byte breadcrumb so we know the page actually loaded for the user.
  track("info", "page.load", {
    url: window.location.pathname + window.location.search,
    ua: navigator.userAgent.slice(0, 120),
    width: window.innerWidth,
    height: window.innerHeight,
  });
}

export const TELEMETRY_SESSION_ID = SESSION_ID;
