"use client";

/**
 * Recent decisions feed — Phase 5 wires the real `decisions` entity.
 * Phase 5.2 P1 adds: rows are deep-links into /projects/<slug>?decision=<id>
 * (the Decisions tab scrolls + highlights the matching card on mount),
 * and superseded rows offer an admin-only "Reactivate" affordance that
 * flips the row back to accepted via /v1/atlas/admin/decisions/:id/reactivate.
 *
 * Reactivate is super-admin gated server-side. The button is rendered
 * unconditionally; non-admin clicks fail with 403 and surface as an
 * inline error. (We don't pre-gate on a role claim because the dashboard
 * is already super-admin-only — anyone reading this panel can see the
 * button, by definition.)
 */

import Link from "next/link";
import { useState } from "react";
import {
  reactivateDecision,
  type DashboardDecision,
} from "@/lib/dashboard/client";

interface Props {
  data: DashboardDecision[];
  /** Mark the current row's id when a reactivate succeeds so the panel
   *  can swap the row's status pill without a re-fetch. The dashboard
   *  shell triggers a refetch on next mount; this is for immediate
   *  feedback within the same session. */
  onReactivated?: (decisionID: string) => void;
}

const STATUS_TINT: Record<NonNullable<DashboardDecision["status"]>, string> = {
  proposed: "#2563eb",
  accepted: "#16a34a",
  superseded: "var(--text-3)",
};

export default function RecentDecisions({ data, onReactivated }: Props) {
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reactivated, setReactivated] = useState<Set<string>>(new Set());

  const reactivate = async (id: string) => {
    setPending(id);
    setError(null);
    const r = await reactivateDecision(id);
    setPending(null);
    if (!r.ok) {
      setError(`${r.error} (status ${r.status})`);
      return;
    }
    setReactivated((prev) => {
      const next = new Set(prev);
      next.add(id);
      return next;
    });
    onReactivated?.(id);
  };

  return (
    <section
      style={{
        padding: 16,
        border: "1px solid var(--border)",
        borderRadius: 10,
        background: "var(--bg-surface)",
      }}
    >
      <h3
        style={{
          fontSize: 12,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
          textTransform: "uppercase",
          letterSpacing: 0.6,
          margin: 0,
          marginBottom: 12,
        }}
      >
        Recent decisions
      </h3>
      {data.length === 0 ? (
        <p
          style={{
            fontSize: 12,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
            margin: 0,
          }}
        >
          No decisions yet. Type <code>/decision</code> in any DRD to capture
          the first one.
        </p>
      ) : (
        <ul
          style={{
            listStyle: "none",
            margin: 0,
            padding: 0,
            display: "flex",
            flexDirection: "column",
            gap: 8,
          }}
        >
          {data.map((d) => {
            const status = reactivated.has(d.id) ? "accepted" : d.status;
            const tint = status ? STATUS_TINT[status] ?? "var(--text-3)" : "var(--text-3)";
            const href = d.slug
              ? `/projects/${encodeURIComponent(d.slug)}?decision=${encodeURIComponent(d.id)}`
              : null;
            return (
              <li
                key={d.id}
                data-decision-id={d.id}
                data-status={status}
                style={{
                  fontSize: 12,
                  fontFamily: "var(--font-mono)",
                  color: "var(--text-1)",
                  borderLeft: `3px solid ${tint}`,
                  paddingLeft: 8,
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  flexWrap: "wrap",
                }}
              >
                <span style={{ color: "var(--text-3)" }}>{d.created_at}</span>
                {status && (
                  <span
                    style={{
                      fontSize: 10,
                      textTransform: "uppercase",
                      letterSpacing: 0.6,
                      color: tint,
                      border: `1px solid ${tint}`,
                      borderRadius: 999,
                      padding: "1px 6px",
                    }}
                  >
                    {status}
                  </span>
                )}
                {href ? (
                  <Link
                    href={href}
                    style={{
                      color: "var(--accent)",
                      textDecoration: "none",
                      flex: 1,
                      minWidth: 0,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {d.title}
                  </Link>
                ) : (
                  <span style={{ flex: 1, minWidth: 0 }}>{d.title}</span>
                )}
                {status === "superseded" && !reactivated.has(d.id) && (
                  <button
                    type="button"
                    onClick={() => void reactivate(d.id)}
                    disabled={pending === d.id}
                    style={{
                      fontSize: 10,
                      fontFamily: "var(--font-mono)",
                      padding: "2px 8px",
                      borderRadius: 4,
                      border: "1px solid var(--border)",
                      background: "transparent",
                      color: "var(--text-1)",
                      cursor: pending === d.id ? "wait" : "pointer",
                    }}
                    title="Flip this superseded decision back to accepted (admin only)"
                  >
                    {pending === d.id ? "…" : "Reactivate"}
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {error && (
        <div
          style={{
            marginTop: 8,
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--danger)",
          }}
        >
          {error}
        </div>
      )}
    </section>
  );
}
