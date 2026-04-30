"use client";

/**
 * "Where this breaks" — Phase 4 U8 — per-component reverse view.
 *
 * Renders the cross-org aggregate (severity tally + flow count + rule
 * sub-buckets) plus the caller's tenant-scoped per-flow detail rows so
 * a component owner can prioritize a fix that fans out across N flows.
 *
 * Auth-gated by the surrounding route (token in zustand-persist). When
 * the user isn't authenticated this component renders nothing — the
 * /components/[slug] page already provides the rest of the spec page.
 */

import { useEffect, useState } from "react";
import Link from "next/link";
import { getToken } from "@/lib/auth-client";

interface Aggregate {
  total_violations: number;
  by_severity: Record<string, number>;
  by_set_sprawl: number;
  by_set_detached: number;
  by_set_override: number;
  flow_count: number;
}

interface FlowRow {
  project_id: string;
  project_slug: string;
  project_name: string;
  product: string;
  flow_id: string;
  flow_name: string;
  violation_count: number;
  highest_severity: string;
}

interface ApiResponse {
  name: string;
  aggregate: Aggregate;
  flows: FlowRow[];
}

type ViewState =
  | { kind: "idle" } // not authenticated
  | { kind: "loading" }
  | { kind: "ok"; data: ApiResponse }
  | { kind: "empty" }
  | { kind: "error"; error: string; status: number };

interface Props {
  name: string;
}

const SEVERITY_TINTS: Record<string, string> = {
  critical: "#dc2626",
  high: "#ea580c",
  medium: "#ca8a04",
  low: "#2563eb",
  info: "#64748b",
};

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL ?? "http://localhost:8080";
}

export default function WhereThisBreaks({ name }: Props) {
  const [state, setState] = useState<ViewState>({ kind: "idle" });

  useEffect(() => {
    const token = getToken();
    if (!token) {
      setState({ kind: "idle" });
      return;
    }
    let cancelled = false;
    setState({ kind: "loading" });
    fetch(
      `${dsBaseURL()}/v1/components/violations?name=${encodeURIComponent(name)}`,
      {
        headers: {
          Accept: "application/json",
          Authorization: `Bearer ${token}`,
        },
      },
    )
      .then(async (res) => {
        if (cancelled) return;
        if (!res.ok) {
          let msg = `HTTP ${res.status}`;
          try {
            const body = (await res.json()) as { error?: string; detail?: string };
            msg = body.detail ?? body.error ?? msg;
          } catch {}
          setState({ kind: "error", error: msg, status: res.status });
          return;
        }
        const data = (await res.json()) as ApiResponse;
        if (data.aggregate.total_violations === 0) {
          setState({ kind: "empty" });
          return;
        }
        setState({ kind: "ok", data });
      })
      .catch((err) => {
        if (cancelled) return;
        setState({
          kind: "error",
          error: err instanceof Error ? err.message : String(err),
          status: 0,
        });
      });
    return () => {
      cancelled = true;
    };
  }, [name]);

  if (state.kind === "idle") return null;

  return (
    <section
      id="where-this-breaks"
      data-testid="where-this-breaks"
      style={{
        padding: "24px 0",
        borderTop: "1px solid var(--border)",
        marginTop: 32,
      }}
    >
      <header style={{ marginBottom: 12 }}>
        <h2
          style={{
            fontSize: 16,
            margin: 0,
            color: "var(--text-1)",
          }}
        >
          Where this breaks
        </h2>
        <p
          style={{
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
            marginTop: 4,
          }}
        >
          Active violations across every flow that uses this component. Aggregate is
          org-wide; the per-flow list shows your tenant.
        </p>
      </header>

      {state.kind === "loading" && (
        <div
          style={{
            fontSize: 12,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          loading…
        </div>
      )}

      {state.kind === "error" && (
        <div
          style={{
            fontSize: 12,
            fontFamily: "var(--font-mono)",
            color: "var(--danger)",
          }}
        >
          {state.error} (status {state.status})
        </div>
      )}

      {state.kind === "empty" && (
        <div
          style={{
            fontSize: 12,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          No active violations involve this component. Nice.
        </div>
      )}

      {state.kind === "ok" && (
        <>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))",
              gap: 8,
              marginBottom: 16,
            }}
          >
            <Stat label="Total violations" value={state.data.aggregate.total_violations} />
            <Stat label="Affected flows" value={state.data.aggregate.flow_count} />
            <Stat label="Detached" value={state.data.aggregate.by_set_detached} />
            <Stat label="Override sprawl" value={state.data.aggregate.by_set_override} />
            <Stat label="Set sprawl" value={state.data.aggregate.by_set_sprawl} />
          </div>

          <div
            style={{
              display: "flex",
              flexWrap: "wrap",
              gap: 6,
              marginBottom: 12,
            }}
          >
            {Object.entries(state.data.aggregate.by_severity).map(([sev, n]) => (
              <span
                key={sev}
                style={{
                  fontSize: 10,
                  fontFamily: "var(--font-mono)",
                  textTransform: "uppercase",
                  letterSpacing: 0.6,
                  padding: "2px 8px",
                  borderRadius: 999,
                  border: `1px solid ${SEVERITY_TINTS[sev] ?? "#64748b"}`,
                  color: SEVERITY_TINTS[sev] ?? "#64748b",
                }}
              >
                {sev} {n}
              </span>
            ))}
          </div>

          {state.data.flows.length === 0 ? (
            <div
              style={{
                fontSize: 12,
                fontFamily: "var(--font-mono)",
                color: "var(--text-3)",
              }}
            >
              Aggregate counts above include flows from other tenants you can't see.
            </div>
          ) : (
            <ul
              style={{
                listStyle: "none",
                margin: 0,
                padding: 0,
                display: "flex",
                flexDirection: "column",
                gap: 6,
              }}
            >
              {state.data.flows.map((f) => (
                <li
                  key={`${f.project_id}-${f.flow_id}`}
                  style={{
                    display: "grid",
                    gridTemplateColumns: "auto 1fr auto auto",
                    alignItems: "center",
                    gap: 12,
                    padding: "8px 12px",
                    border: "1px solid var(--border)",
                    borderLeft: `3px solid ${SEVERITY_TINTS[f.highest_severity] ?? "#64748b"}`,
                    borderRadius: 6,
                    background: "var(--bg-surface)",
                  }}
                >
                  <span
                    style={{
                      fontSize: 10,
                      fontFamily: "var(--font-mono)",
                      textTransform: "uppercase",
                      color: SEVERITY_TINTS[f.highest_severity] ?? "#64748b",
                    }}
                  >
                    {f.highest_severity}
                  </span>
                  <Link
                    href={`/projects/${encodeURIComponent(f.project_slug)}`}
                    style={{
                      fontSize: 12,
                      color: "var(--text-1)",
                      textDecoration: "none",
                    }}
                  >
                    <strong>{f.product}</strong>{" "}
                    <span style={{ color: "var(--text-3)" }}>· {f.project_name}</span>{" "}
                    <span style={{ color: "var(--text-2)" }}>· {f.flow_name}</span>
                  </Link>
                  <span
                    style={{
                      fontSize: 11,
                      fontFamily: "var(--font-mono)",
                      color: "var(--text-3)",
                    }}
                  >
                    {f.violation_count} violation{f.violation_count === 1 ? "" : "s"}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div
      style={{
        padding: "10px 12px",
        border: "1px solid var(--border)",
        borderRadius: 8,
        background: "var(--bg-surface)",
      }}
    >
      <div
        style={{
          fontSize: 10,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
          textTransform: "uppercase",
          letterSpacing: 0.6,
          marginBottom: 4,
        }}
      >
        {label}
      </div>
      <div
        style={{
          fontSize: 22,
          fontWeight: 600,
          color: "var(--text-1)",
        }}
      >
        {value}
      </div>
    </div>
  );
}
