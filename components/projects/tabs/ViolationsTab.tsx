"use client";

/**
 * Violations tab — Phase 1 placeholder rendering of existing audit core
 * output. Full filter + group + bulk-action surface is U10's scope; here we
 * fetch the rows, group by severity, and let the user click "View in JSON"
 * to switch tabs.
 *
 * Why a real fetch and not an empty state: the plan's execution note (U6)
 * says "Build placeholder ViolationsTab (lists existing audit core output)
 * first to anchor right side". Anchoring requires actual rows so the
 * designer perceives weight in the right pane immediately.
 *
 * Failure modes:
 *   - 401 → caller (`ProjectShell`) is responsible for the redirect-to-login
 *     flow. We render an inline error so the redirect happens at one site.
 *   - 404 / endpoint missing → render an empty-state. The U10 endpoint is
 *     not strictly required for U6 to be useful; we degrade gracefully.
 *   - 5xx / network → render an inline error with a retry button.
 */

import { useEffect, useState } from "react";
import { listViolations } from "@/lib/projects/client";
import type {
  Violation,
  ViolationSeverity,
  ViolationsFilters,
} from "@/lib/projects/types";
import EmptyTab from "./EmptyTab";

const SEVERITY_ORDER: readonly ViolationSeverity[] = [
  "critical",
  "high",
  "medium",
  "low",
  "info",
] as const;

const SEVERITY_TINT: Record<ViolationSeverity, string> = {
  critical: "var(--danger)",
  high: "var(--warning)",
  medium: "var(--info)",
  low: "var(--text-3)",
  info: "var(--text-3)",
};

interface ViolationsTabProps {
  slug: string;
  versionID?: string;
  filters?: ViolationsFilters;
  /** Switch to the JSON tab and focus a specific screen (U8 deeplink). */
  onViewInJSON?: (screenID: string) => void;
}

type ViolationsState =
  | { status: "loading" }
  | { status: "ok"; violations: Violation[] }
  | { status: "empty"; reason: string }
  | { status: "error"; error: string; statusCode: number };

export default function ViolationsTab({
  slug,
  versionID,
  filters,
  onViewInJSON,
}: ViolationsTabProps) {
  const [state, setState] = useState<ViolationsState>({ status: "loading" });

  useEffect(() => {
    let cancelled = false;
    setState({ status: "loading" });
    void listViolations(slug, versionID, filters).then((r) => {
      if (cancelled) return;
      if (!r.ok) {
        // 404 may simply mean U10's endpoint isn't deployed yet — show a
        // gentle empty state instead of an angry error banner.
        if (r.status === 404) {
          setState({
            status: "empty",
            reason: "Audit endpoint not yet available (U10).",
          });
          return;
        }
        setState({
          status: "error",
          error: r.error,
          statusCode: r.status,
        });
        return;
      }
      if (r.data.violations.length === 0) {
        setState({
          status: "empty",
          reason: "No violations found for the active version.",
        });
        return;
      }
      setState({ status: "ok", violations: r.data.violations });
    });
    return () => {
      cancelled = true;
    };
    // Filter shape may change identity each render — stringify for a stable dep.
  }, [slug, versionID, filters?.persona_id, filters?.mode_label]);

  if (state.status === "loading") {
    return (
      <div
        style={{
          padding: 24,
          color: "var(--text-3)",
          fontSize: 12,
          fontFamily: "var(--font-mono)",
        }}
      >
        Loading violations…
      </div>
    );
  }

  if (state.status === "empty") {
    return <EmptyTab title="No violations" description={state.reason} />;
  }

  if (state.status === "error") {
    return (
      <EmptyTab
        title="Couldn't load violations"
        description={`${state.error} (status ${state.statusCode || "n/a"})`}
      />
    );
  }

  // Group by severity, preserving the canonical order (critical → info).
  const grouped: Record<ViolationSeverity, Violation[]> = {
    critical: [],
    high: [],
    medium: [],
    low: [],
    info: [],
  };
  for (const v of state.violations) {
    grouped[v.Severity]?.push(v);
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      {SEVERITY_ORDER.map((sev) => {
        const rows = grouped[sev];
        if (!rows || rows.length === 0) return null;
        return (
          <section
            key={sev}
            aria-label={`${sev} violations`}
            style={{
              border: "1px solid var(--border)",
              borderRadius: 10,
              background: "var(--bg-surface)",
              overflow: "hidden",
            }}
          >
            <header
              style={{
                display: "flex",
                alignItems: "center",
                gap: 8,
                padding: "10px 14px",
                borderBottom: "1px solid var(--border)",
              }}
            >
              <span
                aria-hidden
                style={{
                  width: 8,
                  height: 8,
                  borderRadius: 999,
                  background: SEVERITY_TINT[sev],
                  flexShrink: 0,
                }}
              />
              <strong
                style={{
                  fontSize: 12,
                  textTransform: "uppercase",
                  letterSpacing: 0.6,
                  color: "var(--text-1)",
                }}
              >
                {sev}
              </strong>
              <span
                style={{
                  fontSize: 11,
                  color: "var(--text-3)",
                  fontFamily: "var(--font-mono)",
                }}
              >
                {rows.length}
              </span>
            </header>
            <ul
              style={{
                listStyle: "none",
                margin: 0,
                padding: 0,
                display: "flex",
                flexDirection: "column",
              }}
            >
              {rows.map((v) => (
                <li
                  key={v.ID}
                  style={{
                    display: "grid",
                    gridTemplateColumns: "1fr auto",
                    gap: 12,
                    padding: "10px 14px",
                    borderTop: "1px solid var(--border)",
                  }}
                >
                  <div style={{ minWidth: 0 }}>
                    <div
                      style={{
                        fontSize: 13,
                        color: "var(--text-1)",
                        marginBottom: 2,
                      }}
                    >
                      {v.RuleID}
                    </div>
                    <div
                      style={{
                        fontSize: 11,
                        color: "var(--text-3)",
                        fontFamily: "var(--font-mono)",
                        wordBreak: "break-all",
                      }}
                    >
                      {v.Property} · {v.Observed}
                    </div>
                  </div>
                  <button
                    type="button"
                    onClick={() => onViewInJSON?.(v.ScreenID)}
                    style={{
                      alignSelf: "center",
                      padding: "6px 10px",
                      fontSize: 11,
                      fontFamily: "var(--font-mono)",
                      background: "transparent",
                      border: "1px solid var(--border)",
                      borderRadius: 6,
                      color: "var(--text-1)",
                      cursor: "pointer",
                    }}
                  >
                    View in JSON
                  </button>
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}
