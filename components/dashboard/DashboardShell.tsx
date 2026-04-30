"use client";

/**
 * /atlas/admin shell — Phase 4 U10. Fetches the summary, renders a 2x2
 * panel grid of charts + tables, plus a recent-decisions feed below.
 *
 * Recharts-bearing panels are lazy-loaded into a chunks/dashboard
 * split so the route shell stays under the per-route bundle budget.
 */

import dynamic from "next/dynamic";
import { useEffect, useMemo, useState } from "react";
import EmptyState from "@/components/empty-state/EmptyState";
import {
  fetchDashboardSummary,
  type DashboardSummary,
} from "@/lib/dashboard/client";
import RecentDecisions from "./RecentDecisions";
import TopViolators from "./TopViolators";

const ViolationsByProduct = dynamic(() => import("./ViolationsByProduct"), {
  ssr: false,
});
const SeverityTrend = dynamic(() => import("./SeverityTrend"), {
  ssr: false,
});

type ViewState =
  | { kind: "loading" }
  | { kind: "ok"; data: DashboardSummary }
  | { kind: "error"; status: number; error: string };

const WEEKS_OPTIONS: Array<4 | 8 | 12 | 24> = [4, 8, 12, 24];

export default function DashboardShell() {
  const [state, setState] = useState<ViewState>({ kind: "loading" });
  const [weeks, setWeeks] = useState<4 | 8 | 12 | 24>(8);

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    void fetchDashboardSummary(weeks).then((r) => {
      if (cancelled) return;
      if (!r.ok) {
        setState({ kind: "error", status: r.status, error: r.error });
        return;
      }
      setState({ kind: "ok", data: r.data });
    });
    return () => {
      cancelled = true;
    };
  }, [weeks]);

  const totalActive = state.kind === "ok" ? state.data.total_active : 0;
  const sevTotal = useMemo(() => {
    if (state.kind !== "ok") return [] as Array<{ sev: string; n: number }>;
    return Object.entries(state.data.by_severity).map(([sev, n]) => ({ sev, n }));
  }, [state]);

  return (
    <main
      style={{
        padding: "32px 24px 96px",
        maxWidth: 1200,
        margin: "0 auto",
        minHeight: "100vh",
      }}
      data-testid="dashboard-shell"
    >
      <header
        style={{
          marginBottom: 16,
          display: "flex",
          justifyContent: "space-between",
          alignItems: "flex-end",
          gap: 16,
          flexWrap: "wrap",
        }}
      >
        <div>
          <h1 style={{ fontSize: 24, marginBottom: 4 }}>Atlas admin</h1>
          <p
            style={{
              fontSize: 12,
              fontFamily: "var(--font-mono)",
              color: "var(--text-3)",
              margin: 0,
            }}
          >
            Org-wide design-system signal. {totalActive} active violations across
            every tenant + product.
          </p>
        </div>
        <div style={{ display: "flex", gap: 6 }}>
          {WEEKS_OPTIONS.map((w) => (
            <button
              key={w}
              type="button"
              onClick={() => setWeeks(w)}
              style={{
                padding: "4px 10px",
                fontSize: 11,
                fontFamily: "var(--font-mono)",
                borderRadius: 999,
                border: `1px solid ${weeks === w ? "var(--accent)" : "var(--border)"}`,
                background: weeks === w ? "var(--accent)" : "transparent",
                color: weeks === w ? "var(--bg-base, #fff)" : "var(--text-2)",
                cursor: "pointer",
              }}
              aria-pressed={weeks === w}
            >
              {w}w
            </button>
          ))}
        </div>
      </header>

      {state.kind === "loading" && <EmptyState variant="loading" />}
      {state.kind === "error" && (
        <EmptyState
          variant="error"
          title="Couldn't load dashboard"
          description={`${state.error} (status ${state.status})`}
        />
      )}

      {state.kind === "ok" && (
        <>
          <div
            style={{
              display: "flex",
              flexWrap: "wrap",
              gap: 6,
              marginBottom: 16,
            }}
          >
            {sevTotal.map(({ sev, n }) => (
              <span
                key={sev}
                style={{
                  fontSize: 11,
                  fontFamily: "var(--font-mono)",
                  textTransform: "uppercase",
                  letterSpacing: 0.6,
                  padding: "3px 10px",
                  borderRadius: 999,
                  border: `1px solid ${severityTint(sev)}`,
                  color: severityTint(sev),
                }}
              >
                {sev} {n}
              </span>
            ))}
          </div>

          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(420px, 1fr))",
              gap: 16,
              marginBottom: 16,
            }}
          >
            <ViolationsByProduct data={state.data.by_product} />
            <SeverityTrend data={state.data.trend} />
            <TopViolators data={state.data.top_violators} />
            <RecentDecisions data={state.data.recent_decisions} />
          </div>
        </>
      )}
    </main>
  );
}

function severityTint(sev: string): string {
  switch (sev) {
    case "critical":
      return "#dc2626";
    case "high":
      return "#ea580c";
    case "medium":
      return "#ca8a04";
    case "low":
      return "#2563eb";
    default:
      return "#64748b";
  }
}
