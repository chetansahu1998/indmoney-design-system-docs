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
  const [pendingPersonaCount, setPendingPersonaCount] = useState(0);

  useEffect(() => {
    setHydrated(true);
  }, []);
  useEffect(() => {
    if (!hydrated || token) return;
    const next = encodeURIComponent(pathname || "/atlas/admin");
    router.replace(`/?next=${next}`);
  }, [hydrated, token, pathname, router]);

  // Phase 7.6 — initial pending-persona count + SSE subscription. The
  // initial GET keeps the badge accurate on hard refresh; the SSE events
  // bump the count for new pending personas in real time. Visiting
  // /atlas/admin/personas resets the badge.
  useEffect(() => {
    if (!hydrated || !token) return;
    let cancelled = false;
    let es: EventSource | null = null;

    async function loadInitialCount() {
      try {
        const body = await adminFetchJSON<{ personas: { id: string }[] }>(
          "/v1/atlas/admin/personas/pending",
        );
        if (!cancelled) setPendingPersonaCount(body.personas?.length ?? 0);
      } catch {
        // Non-admins will get 403 — fine, badge stays at 0.
      }
    }
    void loadInitialCount();

    async function subscribe() {
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
        if (!tres.ok) return;
        const t = (await tres.json()) as { ticket: string };
        if (cancelled) return;
        es = new EventSource(
          `${dsBaseURL()}/v1/inbox/events?ticket=${encodeURIComponent(t.ticket)}`,
        );
        es.addEventListener("persona.pending", () => {
          setPendingPersonaCount((c) => c + 1);
        });
      } catch {
        // ignore — bell just doesn't update live
      }
    }
    void subscribe();

    return () => {
      cancelled = true;
      es?.close();
    };
  }, [hydrated, token]);

  // Reset the badge when the user lands on the personas page — they're
  // about to read the queue, so unseen count drops to 0.
  useEffect(() => {
    if (pathname === "/atlas/admin/personas") {
      setPendingPersonaCount(0);
    }
  }, [pathname]);

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
                  className="badge"
                  aria-label={`${pendingPersonaCount} pending`}
                >
                  {/* Cap display at "9+" so the pill stays compact under
                      bursts. The aria-label still carries the full count
                      for screen-reader users. */}
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
          background: var(--accent, #7b9fff);
          color: var(--bg);
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
          background: #ffb347;
          color: #2a1a00;
          font-size: 9px;
          font-weight: 700;
          font-variant-numeric: tabular-nums;
          animation: bellPulse 2s ease-in-out infinite;
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
