"use client";

/**
 * Decisions tab — Phase 5 U4. Lists every decision for the active flow,
 * with a "+ New decision" button that opens an inline DecisionForm.
 * Toggle "show superseded" surfaces predecessors that were retired by a
 * newer decision; default view hides them so the active record stays
 * focused.
 *
 * View-state machine mirrors the Phase 3 pattern:
 *   loading | ok(empty | rows) | error
 *
 * Phase 5's SSE wiring lands when the lifecycle channel is extended in
 * U10 (project.decision_made event). For now the tab refetches on
 * onSubmitted so the new decision lands immediately in the local list.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import EmptyState from "@/components/empty-state/EmptyState";
import DecisionCard from "@/components/decisions/DecisionCard";
import DecisionForm from "@/components/decisions/DecisionForm";
import SupersessionChain from "@/components/decisions/SupersessionChain";
import {
  listDecisionsForFlow,
  type Decision,
} from "@/lib/decisions/client";

interface Props {
  slug: string;
  flowID: string | null;
  readOnly?: boolean;
  /** Phase 6 U7 — when the user clicks a row inside a card's "Linked
   *  violations" subsection, this callback runs. ProjectShell wires it
   *  to a tab-switch + violation-row scroll-into-view. Optional so
   *  callers that don't host the Violations tab still render. */
  onViewViolation?: (violationID: string, screenID: string) => void;
}

type ViewState =
  | { kind: "idle" } // no flow_id yet
  | { kind: "loading" }
  | { kind: "ok"; decisions: Decision[] }
  | { kind: "error"; status: number; error: string };

export default function DecisionsTab({ slug, flowID, readOnly, onViewViolation }: Props) {
  const [state, setState] = useState<ViewState>({ kind: "idle" });
  const [includeSuperseded, setIncludeSuperseded] = useState(false);
  const [composing, setComposing] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);

  // Phase 5.1 P3 — deep-link from /atlas/admin or anywhere else.
  // ?decision=<id> in the URL scrolls + highlights the matching card
  // on first ok-state render. The highlight is a brief outline pulse
  // that fades after 1.5s; the flag clears so navigating away + back
  // doesn't re-trigger it.
  const searchParams = useSearchParams();
  const decisionTarget = searchParams?.get("decision") ?? null;
  const highlightedRef = useRef<HTMLDivElement | null>(null);
  const [highlightedID, setHighlightedID] = useState<string | null>(null);

  useEffect(() => {
    if (!flowID) {
      setState({ kind: "idle" });
      return;
    }
    let cancelled = false;
    setState({ kind: "loading" });
    void listDecisionsForFlow(slug, flowID, { includeSuperseded }).then((r) => {
      if (cancelled) return;
      if (!r.ok) {
        setState({ kind: "error", status: r.status, error: r.error });
        return;
      }
      setState({ kind: "ok", decisions: r.data.decisions ?? [] });
    });
    return () => {
      cancelled = true;
    };
  }, [slug, flowID, includeSuperseded, reloadKey]);

  const onSubmitted = useCallback(() => {
    setComposing(false);
    setReloadKey((k) => k + 1);
  }, []);

  // Once decisions arrive AND the URL targets one, scroll into view +
  // mark for highlight. Toggling includeSuperseded if the target is a
  // superseded card so it actually renders.
  useEffect(() => {
    if (!decisionTarget) return;
    if (state.kind !== "ok") return;
    const target = state.decisions.find((d) => d.id === decisionTarget);
    if (!target) {
      // Target is superseded → flip the toggle so the next fetch
      // includes it. The next render will scroll.
      if (!includeSuperseded) setIncludeSuperseded(true);
      return;
    }
    setHighlightedID(target.id);
    // Scroll on next paint so the DOM ref has resolved.
    requestAnimationFrame(() => {
      highlightedRef.current?.scrollIntoView({
        behavior: "smooth",
        block: "center",
      });
    });
    const t = setTimeout(() => setHighlightedID(null), 1500);
    return () => clearTimeout(t);
  }, [decisionTarget, state, includeSuperseded]);

  // Group cards by chain root: cards reachable via supersedes_id form a
  // group; standalone decisions render alone. Within a chain, render
  // newest first (the current head); predecessors below.
  const grouped = useMemo(() => {
    if (state.kind !== "ok") return [] as Decision[][];
    const byID = new Map<string, Decision>();
    for (const d of state.decisions) byID.set(d.id, d);

    const seen = new Set<string>();
    const groups: Decision[][] = [];
    for (const d of state.decisions) {
      if (seen.has(d.id)) continue;
      // Walk forward to the chain head.
      let head: Decision = d;
      while (head.superseded_by_id) {
        const next = byID.get(head.superseded_by_id);
        if (!next) break;
        head = next;
      }
      // Then walk backward from the head, collecting the chain.
      const chain: Decision[] = [];
      let cur: Decision | undefined = head;
      while (cur) {
        if (seen.has(cur.id)) break;
        seen.add(cur.id);
        chain.push(cur);
        cur = cur.supersedes_id ? byID.get(cur.supersedes_id) : undefined;
      }
      groups.push(chain);
    }
    // Sort groups by their head's made_at DESC.
    groups.sort((a, b) => {
      const ta = a[0]?.made_at ?? "";
      const tb = b[0]?.made_at ?? "";
      return tb.localeCompare(ta);
    });
    return groups;
  }, [state]);

  return (
    <div
      data-testid="decisions-tab"
      style={{ display: "flex", flexDirection: "column", gap: 12 }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 12,
          flexWrap: "wrap",
        }}
      >
        <h2 style={{ fontSize: 16, margin: 0, color: "var(--text-1)" }}>
          Decisions
        </h2>
        <span
          style={{
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          {state.kind === "ok"
            ? `${state.decisions.length} record${state.decisions.length === 1 ? "" : "s"}`
            : ""}
        </span>
        <label
          style={{
            marginLeft: "auto",
            display: "flex",
            alignItems: "center",
            gap: 6,
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
            cursor: "pointer",
          }}
        >
          <input
            type="checkbox"
            checked={includeSuperseded}
            onChange={(e) => setIncludeSuperseded(e.target.checked)}
          />
          Show superseded
        </label>
        {!readOnly && flowID && (
          <button
            type="button"
            onClick={() => setComposing((c) => !c)}
            style={{
              padding: "6px 12px",
              fontSize: 12,
              fontFamily: "var(--font-mono)",
              background: composing ? "transparent" : "var(--accent)",
              color: composing ? "var(--text-1)" : "var(--bg-base, #fff)",
              border: `1px solid ${composing ? "var(--border)" : "var(--accent)"}`,
              borderRadius: 6,
              cursor: "pointer",
            }}
          >
            {composing ? "Cancel" : "+ New decision"}
          </button>
        )}
      </div>

      {composing && flowID && (
        <DecisionForm
          slug={slug}
          flowID={flowID}
          onSubmitted={onSubmitted}
          onCancel={() => setComposing(false)}
        />
      )}

      {state.kind === "idle" && (
        <EmptyState
          variant="welcome"
          title="No flow selected"
          description="Pick a flow on the left to see its decisions."
        />
      )}

      {state.kind === "loading" && <EmptyState variant="loading" />}

      {state.kind === "error" && (
        <EmptyState
          variant="error"
          title="Couldn't load decisions"
          description={`${state.error} (status ${state.status})`}
        />
      )}

      {state.kind === "ok" && state.decisions.length === 0 && (
        <EmptyState
          variant="welcome"
          title="No decisions yet"
          description="Type / in the DRD or click + New decision to capture the first one."
        />
      )}

      {state.kind === "ok" && grouped.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          {grouped.map((chain) => (
            <SupersessionChain key={chain[0].id} decorate={chain.length > 1}>
              {chain.map((d, i) => (
                <div
                  key={d.id}
                  ref={d.id === highlightedID ? highlightedRef : undefined}
                  data-decision-id={d.id}
                  style={{
                    outline:
                      d.id === highlightedID
                        ? "2px solid var(--accent)"
                        : "none",
                    outlineOffset: 2,
                    borderRadius: 8,
                    transition: "outline-color 200ms ease, outline-width 200ms ease",
                  }}
                >
                  <DecisionCard
                    decision={d}
                    compact={i > 0}
                    onViewViolation={onViewViolation}
                  />
                </div>
              ))}
            </SupersessionChain>
          ))}
        </div>
      )}
    </div>
  );
}
