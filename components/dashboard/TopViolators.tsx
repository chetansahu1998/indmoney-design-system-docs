"use client";

/**
 * Top-violating rules table. Plain DOM (no Recharts) — sortable in v2;
 * Phase 4 ships the static list ordered by active_count DESC server-side.
 */

import type { TopViolator } from "@/lib/dashboard/client";

const SEVERITY_TINTS: Record<string, string> = {
  critical: "#dc2626",
  high: "#ea580c",
  medium: "#ca8a04",
  low: "#2563eb",
  info: "#64748b",
};

interface Props {
  data: TopViolator[];
}

export default function TopViolators({ data }: Props) {
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
        Top violators
      </h3>
      {data.length === 0 ? (
        <div
          style={{
            fontSize: 12,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          No active violations across the org.
        </div>
      ) : (
        <table
          style={{
            width: "100%",
            borderCollapse: "collapse",
            fontSize: 12,
            fontFamily: "var(--font-mono)",
          }}
        >
          <thead>
            <tr
              style={{
                color: "var(--text-3)",
                textTransform: "uppercase",
                letterSpacing: 0.6,
                fontSize: 10,
                textAlign: "left",
              }}
            >
              <th style={th}>rule_id</th>
              <th style={th}>category</th>
              <th style={{ ...th, textAlign: "right" }}>active</th>
              <th style={th}>severity</th>
            </tr>
          </thead>
          <tbody>
            {data.map((tv) => (
              <tr key={tv.rule_id} style={{ borderTop: "1px solid var(--border)" }}>
                <td style={{ ...td, color: "var(--text-1)" }}>{tv.rule_id}</td>
                <td style={td}>{tv.category}</td>
                <td style={{ ...td, textAlign: "right", color: "var(--text-1)" }}>
                  {tv.active_count}
                </td>
                <td style={td}>
                  <span
                    style={{
                      color: SEVERITY_TINTS[tv.highest_severity] ?? "#64748b",
                      borderColor: SEVERITY_TINTS[tv.highest_severity] ?? "#64748b",
                      border: `1px solid currentColor`,
                      borderRadius: 999,
                      padding: "2px 8px",
                      fontSize: 10,
                      textTransform: "uppercase",
                      letterSpacing: 0.6,
                    }}
                  >
                    {tv.highest_severity}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

const th: React.CSSProperties = { padding: "6px 8px" };
const td: React.CSSProperties = { padding: "6px 8px" };
