"use client";

/**
 * Shell — unified chrome for every /atlas/* tab (2026-05-13).
 *
 * Replaces the previous AdminShell + DashboardShell split. There is ONE
 * frontend; tabs render based on the viewer's access. Today every tab
 * is super_admin-gated; the `requiredRole` field is here so future
 * designer-facing tabs can show up for non-admin tokens without code
 * changes to the gate.
 *
 * Mechanics carried forward from AdminShell:
 *   - Token gate (redirect to /login when no token)
 *   - Inbox SSE subscription for the personas badge
 *   - localStorage-backed mark-all-seen for the badge
 *   - Token-aware initial fetch for the initial badge count
 *
 * New:
 *   - "Brain" tab links back to the /atlas brain-graph canvas root.
 *   - All other tabs live directly under /atlas/* (no /atlas/admin/* prefix).
 *   - Tab visibility filtered through `requiredRole` against current claims.
 *     Today every non-Brain tab is `admin`; non-admin users see only Brain.
 */

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, useState, type ReactNode } from "react";

import { useAuth } from "@/lib/auth-client";

import { adminFetchJSON } from "./adminFetch";

interface Props {
  title: string;
  description?: string;
  children: ReactNode;
}

type TabRole = "any" | "admin";

interface TabLink {
  href: string;
  label: string;
  key: string;
  requiredRole: TabRole;
}

const TABS: TabLink[] = [
  { href: "/atlas", label: "Brain", key: "brain", requiredRole: "any" },
  { href: "/atlas/dashboard", label: "Dashboard", key: "dashboard", requiredRole: "admin" },
  { href: "/atlas/rules", label: "Rules", key: "rules", requiredRole: "admin" },
  { href: "/atlas/personas", label: "Personas", key: "personas", requiredRole: "admin" },
  { href: "/atlas/taxonomy", label: "Taxonomy", key: "taxonomy", requiredRole: "admin" },
  // 2026-05-12 — designer + ops surface for the figma_render_blocklist.
  { href: "/atlas/figma-blocklist", label: "Figma blocklist", key: "figma-blocklist", requiredRole: "admin" },
  // 2026-05-13 — organism-pattern-detection dashboard (Part C, U11+U14).
  { href: "/atlas/organisms", label: "Organisms", key: "organisms", requiredRole: "admin" },
  // 2026-05-13 — FIGMA DB Phase 2A. Sortable inventory table.
  { href: "/atlas/figma-inventory", label: "Figma inventory", key: "figma-inventory", requiredRole: "admin" },
];

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL || "http://localhost:8080";
}

export function Shell({ title, description, children }: Props) {
  const token = useAuth((s) => s.token);
  const router = useRouter();
  const pathname = usePathname();
  const [hydrated, setHydrated] = useState(false);
  // Probed once at mount via a request to an admin-only endpoint. 200 →
  // current viewer has admin access; 403 → does not. The Brain tab is
  // always visible; everything else hides on a 403.
  const [isAdmin, setIsAdmin] = useState<boolean | null>(null);
  // Internal raw count — incremented on every persona.pending event.
  // DISPLAY count subtracts the localStorage "seen marker" so dismissals
  // survive reload.
  const [rawCount, setRawCount] = useState(0);
  const [seenMarker, setSeenMarker] = useState(0);

  useEffect(() => {
    setHydrated(true);
  }, []);

  useEffect(() => {
    if (!hydrated || token) return;
    const next = encodeURIComponent(pathname || "/atlas");
    router.replace(`/login?next=${next}`);
  }, [hydrated, token, pathname, router]);

  // Probe admin access once we have a token. Uses /v1/atlas/admin/summary
  // (the same endpoint AdminShell pinged historically) — 200 = admin,
  // 403 = non-admin, network failure = assume non-admin to fail safe.
  useEffect(() => {
    if (!hydrated || !token) return;
    let cancelled = false;
    (async () => {
      try {
        await adminFetchJSON("/v1/atlas/admin/summary");
        if (!cancelled) setIsAdmin(true);
      } catch {
        if (!cancelled) setIsAdmin(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [hydrated, token]);

  // Hydrate the personas badge dismissal marker FIRST (synchronous), so
  // the badge doesn't briefly show pre-existing pending count before the
  // localStorage value lands.
  useEffect(() => {
    if (!hydrated) return;
    try {
      const v = window.localStorage.getItem("admin-personas-seen-marker");
      if (v) setSeenMarker(parseInt(v, 10) || 0);
    } catch {
      /* localStorage may be blocked; degrade gracefully */
    }
  }, [hydrated]);

  // Initial pending-persona count + SSE subscription. Identical behaviour
  // to the previous AdminShell — 1s → 2s → 4s … backoff on socket loss,
  // capped at 30s. Non-admins get 403 on the initial fetch which we
  // silently ignore (the badge stays at 0).
  useEffect(() => {
    if (!hydrated || !token) return;
    let cancelled = false;
    let es: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay = 1000;
    const MAX_BACKOFF = 30_000;

    async function loadInitialCount() {
      try {
        const body = await adminFetchJSON<{ personas?: { length: number }[] }>(
          "/v1/atlas/admin/personas?status=pending",
        );
        if (!cancelled) setRawCount(body.personas?.length ?? 0);
      } catch {
        // Non-admins will get 403 — fine, badge stays at 0.
      }
    }
    void loadInitialCount();

    async function subscribe() {
      if (cancelled) return;
      try {
        const tres = await fetch(`${dsBaseURL()}/v1/inbox/events/ticket`, {
          method: "POST",
          headers: {
            Accept: "application/json",
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
          },
          body: "{}",
        });
        if (!tres.ok) {
          if (tres.status >= 500 && !cancelled) scheduleReconnect();
          return;
        }
        const t = (await tres.json()) as { ticket: string };
        if (cancelled) return;
        es = new EventSource(
          `${dsBaseURL()}/v1/inbox/events?ticket=${encodeURIComponent(t.ticket)}`,
        );
        es.addEventListener("open", () => {
          reconnectDelay = 1000;
        });
        es.addEventListener("persona.pending", () => {
          setRawCount((c) => c + 1);
        });
        es.addEventListener("error", () => {
          if (cancelled) return;
          es?.close();
          es = null;
          scheduleReconnect();
        });
      } catch {
        if (!cancelled) scheduleReconnect();
      }
    }

    function scheduleReconnect() {
      if (cancelled || reconnectTimer) return;
      reconnectTimer = setTimeout(() => {
        reconnectTimer = null;
        reconnectDelay = Math.min(reconnectDelay * 2, MAX_BACKOFF);
        void subscribe();
      }, reconnectDelay);
    }

    void subscribe();

    return () => {
      cancelled = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      es?.close();
    };
  }, [hydrated, token]);

  // Mark all personas seen when landing on the Personas tab (or via
  // explicit click). Persisted to localStorage so dismissals survive reload.
  function markAllSeen() {
    setSeenMarker(rawCount);
    try {
      window.localStorage.setItem("admin-personas-seen-marker", String(rawCount));
    } catch {
      /* fine */
    }
  }

  useEffect(() => {
    if (pathname !== "/atlas/personas") return;
    if (rawCount === seenMarker) return;
    markAllSeen();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pathname, rawCount, seenMarker]);

  const pendingPersonaCount = Math.max(0, rawCount - seenMarker);

  if (!hydrated || !token) {
    return <div aria-hidden style={{ minHeight: "100vh", background: "var(--bg)" }} />;
  }

  // Visible tabs: Brain always, admin tabs only if isAdmin === true.
  // While isAdmin === null (probe in flight), show Brain only — avoids a
  // flash of admin tabs that vanish a moment later.
  const visibleTabs = TABS.filter((t) => t.requiredRole === "any" || isAdmin === true);

  return (
    <main className="atlas-shell">
      <nav className="atlas-nav" aria-label="Workspace sections">
        {visibleTabs.map((l) => {
          const isActive = pathname === l.href || (l.href !== "/atlas" && pathname?.startsWith(l.href));
          const showBadge = l.key === "personas" && pendingPersonaCount > 0 && !isActive;
          return (
            <Link
              key={l.href}
              href={l.href}
              className={isActive ? "active" : ""}
            >
              {l.label}
              {showBadge && (
                <span
                  role="button"
                  tabIndex={0}
                  className="badge"
                  aria-label={`${pendingPersonaCount} pending — click to dismiss`}
                  title="Mark all as seen"
                  onClick={(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                    markAllSeen();
                  }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      e.stopPropagation();
                      markAllSeen();
                    }
                  }}
                >
                  {pendingPersonaCount > 9 ? "9+" : pendingPersonaCount}
                </span>
              )}
            </Link>
          );
        })}
      </nav>
      <header className="atlas-header">
        <h1>{title}</h1>
        {description && <p>{description}</p>}
      </header>
      {children}
      <style jsx>{`
        .atlas-shell {
          min-height: 100vh;
          background: var(--bg);
          color: var(--text-1);
          font-family: var(--font-sans, "Inter Variable", sans-serif);
          padding: 24px 32px 64px;
          display: flex;
          flex-direction: column;
          gap: 24px;
        }
        .atlas-nav {
          display: flex;
          gap: 4px;
          padding: 4px;
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          border-radius: 999px;
          width: fit-content;
        }
        .atlas-nav :global(a) {
          padding: 6px 14px;
          color: var(--text-3);
          font-size: 12px;
          letter-spacing: 0.02em;
          border-radius: 999px;
          text-decoration: none;
        }
        .atlas-nav :global(a.active) {
          background: var(--accent);
          color: var(--bg-canvas);
        }
        .atlas-nav :global(a:hover:not(.active)) {
          color: var(--text-1);
        }
        .atlas-nav :global(.badge) {
          display: inline-flex;
          align-items: center;
          justify-content: center;
          min-width: 16px;
          height: 16px;
          margin-left: 6px;
          padding: 0 5px;
          border-radius: 999px;
          background: var(--warning);
          color: var(--warning-fg);
          font-size: 9px;
          font-weight: 700;
          font-variant-numeric: tabular-nums;
          cursor: pointer;
          user-select: none;
          animation: bellPulse 2s ease-in-out infinite;
        }
        .atlas-nav :global(.badge:hover) {
          background: var(--warning-soft);
          animation-play-state: paused;
        }
        .atlas-nav :global(.badge:focus-visible) {
          outline: 2px solid var(--accent);
          outline-offset: 2px;
        }
        @keyframes bellPulse {
          0%, 100% {
            transform: scale(1);
            box-shadow: 0 0 0 0 rgba(255, 179, 71, 0.5);
          }
          50% {
            transform: scale(1.08);
            box-shadow: 0 0 0 4px rgba(255, 179, 71, 0);
          }
        }
        .atlas-header h1 {
          margin: 0 0 6px;
          font-size: 24px;
          font-weight: 600;
        }
        .atlas-header p {
          margin: 0;
          font-size: 13px;
          color: var(--text-3);
          max-width: 60ch;
        }
      `}</style>
    </main>
  );
}

/**
 * Convenience re-export so call sites that haven't migrated yet keep
 * compiling. New code should import `Shell` directly.
 *
 * @deprecated Use `Shell` instead.
 */
export const AdminShell = Shell;
