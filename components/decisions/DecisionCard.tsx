"use client";

/**
 * DecisionCard — single-decision card used in the Decisions tab and
 * (Phase 5 U5) inline inside the DRD body. Status pill + author
 * + relative time + click-to-expand body.
 */

import { useEffect, useState } from "react";
import { listLinkedViolations, type Decision } from "@/lib/decisions/client";
import type { Violation } from "@/lib/projects/types";

interface Props {
  decision: Decision;
  /** When true, show the body expanded by default (used inline in DRD). */
  defaultExpanded?: boolean;
  /** Compact rendering for activity rails / inbox previews. */
  compact?: boolean;
  /** Phase 6 U7 — when provided, the "Linked violations" subsection
   *  surfaces a "View" CTA per row that hands the violation id back to
   *  the parent (typically ProjectShell, which switches the tab to
   *  Violations + scrolls to the row). When omitted the subsection
   *  still renders rule_id + severity but without a click target. */
  onViewViolation?: (violationID: string, screenID: string) => void;
}

type LinkedState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "ok"; violations: Violation[] }
  | { kind: "error"; status: number; error: string };

const SEVERITY_TINT_LV: Record<string, string> = {
  critical: "var(--danger)",
  high: "var(--warning)",
  medium: "var(--info)",
  low: "var(--text-3)",
  info: "var(--text-3)",
};

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

export default function DecisionCard({
  decision,
  defaultExpanded = false,
  compact = false,
  onViewViolation,
}: Props) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  const tint = STATUS_TINTS[decision.status] ?? STATUS_TINTS.accepted;
  const isSuperseded = decision.status === "superseded";

  // Phase 6 U7 — Linked violations subsection. We only fetch on the
  // expanded variant (compact === false) to avoid N round-trips when
  // the card renders inside an activity rail. The fetch is also gated
  // on whether the card has any violation links at all (decision.links
  // is already populated by the server-side side-load); zero-link
  // decisions skip the fetch entirely and render the empty state.
  const hasViolationLinks = !!decision.links?.some((l) => l.link_type === "violation");
  const [linked, setLinked] = useState<LinkedState>({ kind: "idle" });
  const [linkedReloadKey, setLinkedReloadKey] = useState(0);

  useEffect(() => {
    if (compact) return;
    if (!hasViolationLinks) {
      setLinked({ kind: "ok", violations: [] });
      return;
    }
    const controller = new AbortController();
    setLinked({ kind: "loading" });
    void listLinkedViolations(decision.id, controller.signal).then((r) => {
      if (controller.signal.aborted) return;
      if (!r.ok) {
        // Aborted fetches surface as status=0 + error="aborted" — skip
        // setState so we don't flap from loading → error during a fast
        // tab change.
        if (r.status === 0 && r.error === "aborted") return;
        setLinked({ kind: "error", status: r.status, error: r.error });
        return;
      }
      setLinked({ kind: "ok", violations: r.data.violations });
    });
    return () => controller.abort();
  }, [decision.id, compact, hasViolationLinks, linkedReloadKey]);

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
          {decision.links
            // Phase 6 U7 — violation links surface in the dedicated
            // "Linked violations" subsection below; suppress them here
            // so the chip row stays compact (screens / components /
            // external still render).
            .filter((l) => l.link_type !== "violation")
            .map((l) => (
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

      {/* Phase 6 U7 — Linked violations subsection. Renders only on the
          expanded variant (compact === false) to avoid clutter inside
          activity rails. The list itself is keyed off the AE-4 cross-link
          requirement: every linked violation is one click away from its
          decision, and (when onViewViolation is provided by the parent)
          clicking it switches tabs + highlights the violation row. */}
      {!compact && (
        <LinkedViolationsSection
          state={linked}
          onRetry={() => setLinkedReloadKey((k) => k + 1)}
          onViewViolation={onViewViolation}
        />
      )}
    </article>
  );
}

interface LinkedViolationsSectionProps {
  state: LinkedState;
  onRetry: () => void;
  onViewViolation?: (violationID: string, screenID: string) => void;
}

function LinkedViolationsSection({
  state,
  onRetry,
  onViewViolation,
}: LinkedViolationsSectionProps) {
  const [open, setOpen] = useState(true);

  const headerLabel = (() => {
    if (state.kind === "ok") {
      return state.violations.length === 0
        ? "Linked violations"
        : `Linked violations (${state.violations.length})`;
    }
    return "Linked violations";
  })();

  return (
    <div
      data-testid="decision-linked-violations"
      style={{
        marginTop: 4,
        borderTop: "1px dashed var(--border)",
        paddingTop: 8,
        display: "flex",
        flexDirection: "column",
        gap: 6,
      }}
    >
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        style={{
          background: "transparent",
          border: "none",
          color: "var(--text-2)",
          fontSize: 11,
          fontFamily: "var(--font-mono)",
          textAlign: "left",
          padding: 0,
          cursor: "pointer",
          letterSpacing: 0.4,
          textTransform: "uppercase",
        }}
      >
        {open ? "▾" : "▸"} {headerLabel}
      </button>

      {open && state.kind === "loading" && (
        <div
          style={{
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          Loading…
        </div>
      )}

      {open && state.kind === "error" && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          <span>Couldn't load linked violations.</span>
          <button
            type="button"
            onClick={onRetry}
            style={{
              background: "transparent",
              border: "1px solid var(--border)",
              borderRadius: 4,
              padding: "2px 6px",
              fontSize: 10,
              fontFamily: "var(--font-mono)",
              color: "var(--text-1)",
              cursor: "pointer",
            }}
          >
            Retry
          </button>
        </div>
      )}

      {open && state.kind === "ok" && state.violations.length === 0 && (
        <div
          style={{
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          No linked violations.
        </div>
      )}

      {open && state.kind === "ok" && state.violations.length > 0 && (
        <ul
          style={{
            listStyle: "none",
            padding: 0,
            margin: 0,
            display: "flex",
            flexDirection: "column",
            gap: 4,
          }}
        >
          {state.violations.map((v) => {
            const tintColor = SEVERITY_TINT_LV[v.Severity] ?? "var(--text-3)";
            return (
              <li
                key={v.ID}
                data-testid="decision-linked-violation-row"
                data-violation-id={v.ID}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 8,
                  fontSize: 11,
                  fontFamily: "var(--font-mono)",
                  color: "var(--text-2)",
                }}
              >
                <span
                  aria-hidden
                  style={{
                    width: 6,
                    height: 6,
                    borderRadius: 999,
                    background: tintColor,
                    flexShrink: 0,
                  }}
                />
                <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {v.RuleID} · {v.Property}
                </span>
                {onViewViolation && (
                  <button
                    type="button"
                    onClick={() => onViewViolation(v.ID, v.ScreenID)}
                    style={{
                      background: "transparent",
                      border: "1px solid var(--border)",
                      borderRadius: 4,
                      padding: "2px 6px",
                      fontSize: 10,
                      fontFamily: "var(--font-mono)",
                      color: "var(--accent)",
                      cursor: "pointer",
                    }}
                  >
                    View
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
