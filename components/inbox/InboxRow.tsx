"use client";

/**
 * Single inbox row. Selection checkbox + per-row lifecycle controls.
 *
 * Per-row controls (acknowledge / dismiss) defer the reason-template
 * interaction to a parent-owned modal/dropdown that wraps the bulk-action
 * UX — keeps the row component dumb and avoids one inline form per row.
 */

import Link from "next/link";
import type { InboxRow as Row } from "@/lib/inbox/client";

interface Props {
  row: Row;
  selected: boolean;
  onToggle: (id: string) => void;
  fadeOut: boolean;
  onAcknowledge: (row: Row) => void;
  onDismiss: (row: Row) => void;
}

const SEVERITY_TINTS: Record<Row["severity"], string> = {
  critical: "#dc2626",
  high: "#ea580c",
  medium: "#ca8a04",
  low: "#2563eb",
  info: "#64748b",
};

export default function InboxRow({
  row,
  selected,
  onToggle,
  fadeOut,
  onAcknowledge,
  onDismiss,
}: Props) {
  const tint = SEVERITY_TINTS[row.severity] ?? "#64748b";
  return (
    <li
      data-testid="inbox-row"
      data-violation-id={row.violation_id}
      style={{
        listStyle: "none",
        display: "grid",
        gridTemplateColumns: "auto auto 1fr auto",
        alignItems: "start",
        gap: 12,
        padding: "12px 14px",
        border: "1px solid var(--border)",
        borderLeft: `3px solid ${tint}`,
        borderRadius: 8,
        background: "var(--bg-surface)",
        opacity: fadeOut ? 0 : 1,
        transform: fadeOut ? "translateX(-12px)" : "none",
        transition: "opacity 220ms ease, transform 220ms ease",
        pointerEvents: fadeOut ? "none" : "auto",
      }}
    >
      <input
        type="checkbox"
        checked={selected}
        onChange={() => onToggle(row.violation_id)}
        aria-label={`Select violation ${row.violation_id}`}
        // S21 — align native checkbox with brand.
        style={{ marginTop: 4, accentColor: "var(--accent)" }}
      />

      <span
        style={{
          fontSize: 10,
          fontFamily: "var(--font-mono)",
          textTransform: "uppercase",
          letterSpacing: 0.6,
          color: tint,
          padding: "2px 6px",
          border: `1px solid ${tint}`,
          borderRadius: 4,
          alignSelf: "start",
        }}
      >
        {row.severity}
      </span>

      <div style={{ minWidth: 0 }}>
        <div
          style={{
            fontSize: 13,
            color: "var(--text-1)",
            fontWeight: 500,
            marginBottom: 2,
            overflow: "hidden",
            textOverflow: "ellipsis",
          }}
        >
          {row.rule_id}{" "}
          <span style={{ color: "var(--text-3)", fontWeight: 400 }}>
            · {row.property}
          </span>
        </div>
        <div
          style={{
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
            display: "flex",
            gap: 8,
            flexWrap: "wrap",
          }}
        >
          <Link
            href={`/projects/${encodeURIComponent(row.project_slug)}`}
            style={{ color: "var(--accent)", textDecoration: "none" }}
          >
            {row.product} · {row.project_name}
          </Link>
          <span>{row.flow_name}</span>
          {row.mode_label && <span>· {row.mode_label}</span>}
          {row.auto_fixable && (
            <span style={{ color: "#16a34a" }}>· auto-fixable</span>
          )}
        </div>
        {row.suggestion && (
          <div
            style={{
              marginTop: 4,
              fontSize: 11,
              fontFamily: "var(--font-mono)",
              color: "var(--text-2)",
              lineHeight: 1.45,
            }}
          >
            {row.suggestion}
          </div>
        )}
      </div>

      <div style={{ display: "flex", gap: 6 }}>
        <button
          type="button"
          onClick={() => onAcknowledge(row)}
          style={lifecycleBtnStyle("ack")}
          title="Acknowledge with reason"
        >
          Acknowledge
        </button>
        <button
          type="button"
          onClick={() => onDismiss(row)}
          style={lifecycleBtnStyle("dismiss")}
          title="Dismiss with rationale"
        >
          Dismiss
        </button>
      </div>
    </li>
  );
}

function lifecycleBtnStyle(kind: "ack" | "dismiss"): React.CSSProperties {
  const base: React.CSSProperties = {
    padding: "4px 10px",
    fontSize: 11,
    fontFamily: "var(--font-mono)",
    borderRadius: 4,
    border: "1px solid var(--border)",
    background: "transparent",
    cursor: "pointer",
    color: "var(--text-2)",
    transition: "background 150ms ease",
  };
  if (kind === "ack") {
    base.color = "var(--text-1)";
  }
  return base;
}
