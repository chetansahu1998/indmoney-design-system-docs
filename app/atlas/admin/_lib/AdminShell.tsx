"use client";

/**
 * Phase 7.5 — shared chrome for /atlas/admin/{rules,personas,taxonomy}.
 *
 * Provides: auth gate (mirrors /atlas/admin's), simple top nav, page
 * heading. Each page renders its own body inside.
 *
 * Phase 7.6: subscribes to inbox:<tenant_id> SSE channel and tracks
 * `persona.pending` events; renders a small badge on the "Personas" nav
 * link that pulses + counts unseen pending personas. Badge resets when
 * the user navigates to /atlas/admin/personas (the queue surface).
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

const NAV_LINKS: { href: string; label: string; key: string }[] = [
  { href: "/atlas/admin", label: "Dashboard", key: "dashboard" },
  { href: "/atlas/admin/rules", label: "Rules", key: "rules" },
  { href: "/atlas/admin/personas", label: "Personas", key: "personas" },
  { href: "/atlas/admin/taxonomy", label: "Taxonomy", key: "taxonomy" },
];

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL ?? "http://localhost:8080";
}

export function AdminShell({ title, description, children }: Props) {
  const token = useAuth((s) => s.token);
  const router = useRouter();
  const pathname = usePathname();
  const [hydrated, setHydrated] = useState(false);
  // Internal raw count — incremented on every persona.pending event.
  // The DISPLAYED count subtracts the user's "seen marker" stored in
  // localStorage so dismissals survive reload.
  const [rawCount, setRawCount] = useState(0);
  const [seenMarker, setSeenMarker] = useState(0);

  useEffect(() => {
    setHydrated(true);
  }, []);
  useEffect(() => {
    if (!hydrated || token) return;
    const next = encodeURIComponent(pathname || "/atlas/admin");
    router.replace(`/login?next=${next}`);
  }, [hydrated, token, pathname, router]);

  // Phase 7.6 — initial pending-persona count + SSE subscription. The
  // initial GET keeps the badge accurate on hard refresh; the SSE events
  // bump the count for new pending personas in real time. Visiting
  // /atlas/admin/personas resets the badge.
  useEffect(() => {
    if (!hydrated || !token) return;
    let cancelled = false;
    let es: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay = 1000; // exponential backoff: 1s → 2s → 4s → 8s → 16s → 30s cap
    const MAX_BACKOFF = 30_000;

    // A22 — hydrate the dismissal marker FIRST (synchronous), so the
    // initial-count fetch (async) lands against the correct baseline.
    // Without this ordering the badge briefly shows the unfiltered count
    // before localStorage hydrates the marker.
    try {
      const raw = window.localStorage.getItem("admin-personas-seen-marker");
      if (raw !== null) setSeenMarker(parseInt(raw, 10) || 0);
    } catch {
      /* localStorage may be blocked; badge degrades to no-persistence */
    }

    async function loadInitialCount() {
      try {
        const body = await adminFetchJSON<{ personas: { id: string }[] }>(
          "/v1/atlas/admin/personas/pending",
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
          // Schedule a backoff retry — auth errors don't auto-retry, but
          // ds-service hiccups (502, 504, conn refused) should.
          if (tres.status >= 500 && !cancelled) scheduleReconnect();
          return;
        }
        const t = (await tres.json()) as { ticket: string };
        if (cancelled) return;
        es = new EventSource(
          `${dsBaseURL()}/v1/inbox/events?ticket=${encodeURIComponent(t.ticket)}`,
        );
        es.addEventListener("open", () => {
          // Reset backoff after a clean connect — next outage starts at 1s again.
          reconnectDelay = 1000;
        });
        es.addEventListener("persona.pending", () => {
          setRawCount((c) => c + 1);
        });
        // A23 — reconnect with exponential backoff on socket error.
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

  // Mark-all-seen persists across reloads via localStorage. Triggered
  // either by landing on /atlas/admin/personas (the user is now reading
  // the queue) or via an explicit "mark seen" button click.
  function markAllSeen() {
    setSeenMarker(rawCount);
    try {
      window.localStorage.setItem("admin-personas-seen-marker", String(rawCount));
    } catch {
      /* localStorage may be blocked; badge degrades to no-persistence */
    }
  }
  useEffect(() => {
    if (pathname !== "/atlas/admin/personas") return;
    // A21 — guard against SSE-driven render storms. Only write to
    // localStorage when seenMarker is actually behind. Without this,
    // every persona.pending event triggers a marker bump which writes
    // a fresh localStorage entry — observable as latency on busy
    // admin sessions.
    if (rawCount === seenMarker) return;
    markAllSeen();
    // markAllSeen reads rawCount each call; deps intentionally narrow.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pathname, rawCount, seenMarker]);

  const pendingPersonaCount = Math.max(0, rawCount - seenMarker);

  if (!hydrated || !token) {
    return <div aria-hidden style={{ minHeight: "100vh", background: "var(--bg)" }} />;
  }

  return (
    <main className="admin-shell">
      <nav className="admin-nav" aria-label="Admin sections">
        {NAV_LINKS.map((l) => {
          const isActive = pathname === l.href;
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
                    e.preventDefault(); // don't navigate; just dismiss
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
                  {/* Cap display at "9+" so the pill stays compact under
                      bursts. The aria-label carries the real count + an
                      affordance hint for screen-reader users. */}
                  {pendingPersonaCount > 9 ? "9+" : pendingPersonaCount}
                </span>
              )}
            </Link>
          );
        })}
      </nav>
      <header className="admin-header">
        <h1>{title}</h1>
        {description && <p>{description}</p>}
      </header>
      {children}
      <style jsx>{`
        .admin-shell {
          min-height: 100vh;
          background: var(--bg);
          color: var(--text-1);
          font-family: var(--font-sans, "Inter Variable", sans-serif);
          padding: 24px 32px 64px;
          display: flex;
          flex-direction: column;
          gap: 24px;
        }
        .admin-nav {
          display: flex;
          gap: 4px;
          padding: 4px;
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          border-radius: 999px;
          width: fit-content;
        }
        .admin-nav :global(a) {
          padding: 6px 14px;
          color: var(--text-3);
          font-size: 12px;
          letter-spacing: 0.02em;
          border-radius: 999px;
          text-decoration: none;
        }
        .admin-nav :global(a.active) {
          background: var(--accent);
          color: var(--bg-canvas);
        }
        .admin-nav :global(a:hover:not(.active)) {
          color: var(--text-1);
        }
        .admin-nav :global(.badge) {
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
        .admin-nav :global(.badge:hover) {
          background: var(--warning-soft);
          animation-play-state: paused;
        }
        .admin-nav :global(.badge:focus-visible) {
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
        .admin-header h1 {
          margin: 0 0 6px;
          font-size: 24px;
          font-weight: 600;
        }
        .admin-header p {
          margin: 0;
          font-size: 13px;
          color: var(--text-3);
          max-width: 60ch;
        }
      `}</style>
    </main>
  );
}
