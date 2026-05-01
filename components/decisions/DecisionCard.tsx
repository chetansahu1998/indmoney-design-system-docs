"use client";

/**
 * DecisionCard — single-decision card used in the Decisions tab and
 * (Phase 5 U5) inline inside the DRD body. Status pill + author
 * + relative time + click-to-expand body.
 */

import { useState } from "react";
import type { Decision } from "@/lib/decisions/client";

interface Props {
  decision: Decision;
  /** When true, show the body expanded by default (used inline in DRD). */
  defaultExpanded?: boolean;
  /** Compact rendering for activity rails / inbox previews. */
  compact?: boolean;
}

const STATUS_TINTS: Record<Decision["status"], { color: string; label: string }> = {
  proposed: { color: "#2563eb", label: "Proposed" },
  accepted: { color: "#16a34a", label: "Accepted" },
  superseded: { color: "var(--text-3)", label: "Superseded" },
};

function relativeTime(iso: string): string {
  const now = Date.now();
  const t = new Date(iso).getTime();
  const diffMs = now - t;
  if (Number.isNaN(diffMs)) return "";
  const sec = Math.round(diffMs / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.round(hr / 24);
  if (day < 7) return `${day}d ago`;
  return new Date(iso).toLocaleDateString();
}

export default function DecisionCard({ decision, defaultExpanded = false, compact = false }: Props) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  const tint = STATUS_TINTS[decision.status] ?? STATUS_TINTS.accepted;
  const isSuperseded = decision.status === "superseded";

  return (
    <article
      data-testid="decision-card"
      data-decision-id={decision.id}
      data-status={decision.status}
      style={{
        border: "1px solid var(--border)",
        borderLeft: `3px solid ${tint.color}`,
        borderRadius: 8,
        background: "var(--bg-surface)",
        padding: compact ? 10 : 14,
        display: "flex",
        flexDirection: "column",
        gap: 8,
        opacity: isSuperseded ? 0.7 : 1,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          flexWrap: "wrap",
        }}
      >
        <h3
          style={{
            fontSize: compact ? 13 : 14,
            color: "var(--text-1)",
            fontWeight: 600,
            margin: 0,
            textDecoration: isSuperseded ? "line-through" : "none",
            flex: "1 1 auto",
            minWidth: 0,
          }}
        >
          {decision.title}
        </h3>
        <span
          style={{
            fontSize: 10,
            fontFamily: "var(--font-mono)",
            textTransform: "uppercase",
            letterSpacing: 0.6,
            color: tint.color,
            border: `1px solid ${tint.color}`,
            borderRadius: 999,
            padding: "2px 8px",
          }}
        >
          {tint.label}
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
        <span>by {decision.made_by_user_id.slice(0, 8)}</span>
        <span>· {relativeTime(decision.made_at)}</span>
        {decision.supersedes_id && (
          <span>· supersedes {decision.supersedes_id.slice(0, 8)}</span>
        )}
        {decision.superseded_by_id && (
          <span>· superseded by {decision.superseded_by_id.slice(0, 8)}</span>
        )}
        {decision.links && decision.links.length > 0 && (
          <span>· {decision.links.length} link{decision.links.length === 1 ? "" : "s"}</span>
        )}
      </div>

      {decision.body_json && !compact && (
        <button
          type="button"
          onClick={() => setExpanded((e) => !e)}
          style={{
            background: "transparent",
            border: "none",
            color: "var(--accent)",
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            cursor: "pointer",
            textAlign: "left",
            padding: 0,
          }}
          aria-expanded={expanded}
        >
          {expanded ? "− Hide details" : "+ Show details"}
        </button>
      )}

      {expanded && decision.body_json && (
        <div
          style={{
            fontSize: 12,
            color: "var(--text-2)",
            lineHeight: 1.5,
            background: "rgba(0,0,0,0.04)",
            padding: 10,
            borderRadius: 6,
            fontFamily: "var(--font-mono)",
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          }}
        >
          {/*
            Phase 5 U4 ships a JSON-string preview. Phase 5 U5's
            DecisionBlock will render the BlockNote body via the live
            editor; for the tab we keep the preview simple to avoid
            mounting a second editor per card. Designers wanting a
            rich preview can click into the DRD where the inline card
            renders properly.
          */}
          {decision.body_json}
        </div>
      )}

      {decision.links && decision.links.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
          {decision.links.map((l) => (
            <span
              key={`${l.link_type}:${l.target_id}`}
              style={{
                fontSize: 10,
                fontFamily: "var(--font-mono)",
                color: "var(--text-3)",
                border: "1px solid var(--border)",
                borderRadius: 4,
                padding: "2px 6px",
              }}
            >
              {l.link_type}:{l.target_id.length > 12 ? l.target_id.slice(0, 12) + "…" : l.target_id}
            </span>
          ))}
        </div>
      )}
    </article>
  );
}
