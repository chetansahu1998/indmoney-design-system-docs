"use client";

/**
 * Recent decisions feed — Phase 5 wires the real `decisions` entity. For
 * now this renders the empty stub with a friendly Phase 5 placeholder so
 * the dashboard layout doesn't have a hole.
 */

import type { DashboardDecision } from "@/lib/dashboard/client";

interface Props {
  data: DashboardDecision[];
}

export default function RecentDecisions({ data }: Props) {
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
          Decisions land as a first-class entity in Phase 5 (DRD collab + decisions).
          The feed activates here without code changes once that ships.
        </p>
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
          {data.map((d) => (
            <li
              key={d.id}
              style={{
                fontSize: 12,
                fontFamily: "var(--font-mono)",
                color: "var(--text-1)",
              }}
            >
              <span style={{ color: "var(--text-3)" }}>{d.created_at}</span> · {d.title}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
