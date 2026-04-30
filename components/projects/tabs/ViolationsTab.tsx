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

import { useCallback, useEffect, useRef, useState } from "react";
import gsap from "gsap";
import { listViolations } from "@/lib/projects/client";
import type {
  Violation,
  ViolationCategory,
  ViolationSeverity,
  ViolationsFilters,
} from "@/lib/projects/types";
import { useGSAPContext } from "@/lib/animations/hooks/useGSAPContext";
import { useReducedMotion } from "@/lib/animations/context";
import { STAGGER_MAX_MS, STAGGER_PER_FRAME_MS } from "@/lib/animations/easings";
import EmptyTab from "./EmptyTab";
import EmptyState from "@/components/empty-state/EmptyState";
import { CategoryFilterChips } from "./violations/CategoryFilterChips";
import LifecycleButtons from "./violations/LifecycleButtons";
import FixInFigmaButton from "./violations/FixInFigmaButton";

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
  /** Phase 3 U6: per-rule audit-progress tick from SSE. Non-null while
   *  the audit is in flight; cleared on audit_complete / audit_failed. */
  auditProgress?: { completed: number; total: number } | null;
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
  auditProgress,
}: ViolationsTabProps) {
  const [state, setState] = useState<ViolationsState>({ status: "loading" });
  // Phase 2 U11 — category filter chips. Selected = empty set means "all"
  // (default). Toggling adds/removes from the set; "Clear" empties it.
  const [selectedCategories, setSelectedCategories] = useState<
    Set<ViolationCategory>
  >(() => new Set());
  // Track which violation IDs we've already rendered so SSE-driven re-fetches
  // can identify "new" rows for the arrival-flash animation.
  const seenIDs = useRef<Set<string>>(new Set());
  const newIDs = useRef<Set<string>>(new Set());
  const rootRef = useRef<HTMLDivElement>(null);
  const ctx = useGSAPContext(rootRef);
  const reduced = useReducedMotion();

  const toggleCategory = useCallback((cat: ViolationCategory) => {
    setSelectedCategories((prev) => {
      const next = new Set(prev);
      if (next.has(cat)) next.delete(cat);
      else next.add(cat);
      return next;
    });
  }, []);
  const clearCategories = useCallback(() => {
    setSelectedCategories(new Set());
  }, []);

  // Phase 3 U6: re-fetch violations when an audit transitions from
  // "running" to "complete". The fetch effect below depends on
  // `reloadTrigger` so a bump here re-runs it. We DON'T put auditProgress
  // itself in the fetch deps because that would re-fetch on every 100ms
  // throttled tick.
  const [reloadTrigger, setReloadTrigger] = useState(0);
  // Phase 4 U6 — set of violation IDs whose lifecycle the user just
  // resolved (Acknowledge / Dismiss). The row fades out and is filtered
  // from the rendered list; on next reload it disappears server-side too.
  const [resolvedSet, setResolvedSet] = useState<Set<string>>(new Set());
  const wasAuditRunning = useRef(false);
  useEffect(() => {
    const running = auditProgress != null && auditProgress.total > 0;
    if (wasAuditRunning.current && !running) {
      setReloadTrigger((t) => t + 1);
    }
    wasAuditRunning.current = running;
  }, [auditProgress]);

  useEffect(() => {
    let cancelled = false;
    setState({ status: "loading" });
    setResolvedSet(new Set());
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
      // Track new IDs for arrival-flash. On the first load, the entire set
      // is "seen" without flashing (the existing stagger handles that
      // mount); on subsequent loads driven by SSE re-fetch, only previously-
      // unseen IDs flash.
      const fresh = new Set<string>();
      const isFirstLoad = seenIDs.current.size === 0;
      if (!isFirstLoad) {
        for (const v of r.data.violations) {
          if (!seenIDs.current.has(v.ID)) fresh.add(v.ID);
        }
      }
      newIDs.current = fresh;
      seenIDs.current = new Set(r.data.violations.map((v) => v.ID));
      setState({ status: "ok", violations: r.data.violations });
    });
    return () => {
      cancelled = true;
    };
    // Filter shape may change identity each render — stringify for a stable dep.
    // reloadTrigger forces a re-fetch on audit_complete (Phase 3 U6).
  }, [slug, versionID, filters?.persona_id, filters?.mode_label, reloadTrigger]);

  // Stagger row reveal once rows are in the DOM. ~50ms per row, clamped to a
  // 600ms total window so a thousand-violation flow doesn't take 50 seconds.
  // On SSE-driven re-fetch, ALSO flash newly-arrived rows (newIDs.current)
  // briefly to draw attention without re-staggering the whole list.
  useEffect(() => {
    if (!ctx || state.status !== "ok" || reduced) return;
    ctx.add(() => {
      const rows = rootRef.current?.querySelectorAll<HTMLLIElement>(
        "[data-violation-row]",
      );
      if (!rows || rows.length === 0) return;
      const perItemMs = Math.min(
        STAGGER_PER_FRAME_MS,
        STAGGER_MAX_MS / rows.length,
      );
      gsap.from(rows, {
        opacity: 0,
        y: 6,
        duration: 0.32,
        ease: "expo.out",
        stagger: perItemMs / 1000,
      });
      // Phase 2 U11: new-arrival flash. Only fires on subsequent re-fetches;
      // the first mount has empty newIDs and skips this branch.
      if (newIDs.current.size > 0) {
        const newRows: HTMLLIElement[] = [];
        rows.forEach((row) => {
          const id = row.getAttribute("data-violation-id");
          if (id && newIDs.current.has(id)) newRows.push(row);
        });
        if (newRows.length > 0) {
          gsap.fromTo(
            newRows,
            { backgroundColor: "color-mix(in oklab, var(--bg-surface) 70%, var(--accent) 30%)" },
            {
              backgroundColor: "rgba(0,0,0,0)",
              duration: 0.6,
              ease: "cubic.out",
            },
          );
        }
      }
    });
  }, [ctx, state, reduced]);

  if (state.status === "loading") {
    // Phase 3 U6: when an audit is actively running AND we have no
    // violations to show yet, surface the audit-running variant instead
    // of a plain "Loading…". The progress bar updates as SSE ticks
    // arrive; on audit_complete the parent re-fetches and we transition
    // to either the zero-violations celebration or the populated list.
    if (auditProgress && auditProgress.total > 0) {
      return (
        <EmptyState
          variant="audit-running"
          progress={auditProgress}
          description="Findings appear here as workers complete each rule."
        />
      );
    }
    return <EmptyState variant="loading" title="Loading violations…" />;
  }

  if (state.status === "empty") {
    // Audit running but the worker hasn't produced any violations yet
    // (or the version legitimately has none and we just got the
    // complete tick). Distinguish via auditProgress: if a tick is in
    // flight, show the running variant; otherwise the zero-violations
    // celebration.
    if (auditProgress && auditProgress.total > 0 && auditProgress.completed < auditProgress.total) {
      return (
        <EmptyState
          variant="audit-running"
          progress={auditProgress}
          description="Findings appear here as workers complete each rule."
        />
      );
    }
    if (state.reason.startsWith("No violations found")) {
      return <EmptyState variant="zero-violations" />;
    }
    return <EmptyTab title="No violations" description={state.reason} />;
  }

  if (state.status === "error") {
    return (
      <EmptyState
        variant="error"
        title="Couldn't load violations"
        description={`${state.error} (status ${state.statusCode || "n/a"})`}
      />
    );
  }

  // Phase 2 U11: derive category-axis bookkeeping BEFORE applying the
  // category filter so chip counts reflect the dataset, not the filtered
  // view. Then apply the filter and group by severity for rendering.
  const categoryCounts = new Map<ViolationCategory, number>();
  const availableCategories = new Set<ViolationCategory>();
  for (const v of state.violations) {
    if (!v.Category) continue;
    availableCategories.add(v.Category);
    categoryCounts.set(v.Category, (categoryCounts.get(v.Category) ?? 0) + 1);
  }

  const filteredViolations =
    selectedCategories.size === 0
      ? state.violations
      : state.violations.filter((v) => selectedCategories.has(v.Category));

  // Group by severity, preserving the canonical order (critical → info).
  const grouped: Record<ViolationSeverity, Violation[]> = {
    critical: [],
    high: [],
    medium: [],
    low: [],
    info: [],
  };
  for (const v of filteredViolations) {
    grouped[v.Severity]?.push(v);
  }

  return (
    <div
      ref={rootRef}
      style={{ display: "flex", flexDirection: "column", gap: 0 }}
    >
      <CategoryFilterChips
        available={availableCategories}
        counts={categoryCounts}
        selected={selectedCategories}
        onToggle={toggleCategory}
        onClear={clearCategories}
      />
      <div style={{ display: "flex", flexDirection: "column", gap: 12, padding: "12px 0" }}>
      {filteredViolations.length === 0 ? (
        <EmptyTab
          title="No violations match the selected filters"
          description="Clear the chips or pick different categories to see more results."
        />
      ) : null}
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
              onMouseEnter={(e) => {
                const chip = e.currentTarget.querySelector<HTMLSpanElement>(
                  "[data-severity-chip]",
                );
                if (chip) chip.style.transform = "scale(1.6)";
              }}
              onMouseLeave={(e) => {
                const chip = e.currentTarget.querySelector<HTMLSpanElement>(
                  "[data-severity-chip]",
                );
                if (chip) chip.style.transform = "scale(1)";
              }}
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
                data-severity-chip
                style={{
                  width: 8,
                  height: 8,
                  borderRadius: 999,
                  background: SEVERITY_TINT[sev],
                  flexShrink: 0,
                  transition: "transform 220ms cubic-bezier(0.34, 1.56, 0.64, 1)",
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
              {rows.map((v) => {
                const fading = resolvedSet.has(v.ID);
                return (
                <li
                  key={v.ID}
                  data-violation-row
                  data-violation-id={v.ID}
                  data-category={v.Category}
                  style={{
                    display: "grid",
                    gridTemplateColumns: "1fr auto",
                    gap: 12,
                    padding: "10px 14px",
                    opacity: fading ? 0 : 1,
                    transform: fading ? "translateX(-12px)" : "none",
                    transition: "opacity 220ms ease, transform 220ms ease",
                    pointerEvents: fading ? "none" : "auto",
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
                  <div style={{ display: "flex", gap: 6, alignSelf: "center", flexWrap: "wrap", justifyContent: "flex-end" }}>
                    <LifecycleButtons
                      slug={slug}
                      violationID={v.ID}
                      onResolved={() =>
                        setResolvedSet((prev) => {
                          const next = new Set(prev);
                          next.add(v.ID);
                          return next;
                        })
                      }
                    />
                    {v.AutoFixable && (
                      <FixInFigmaButton violationID={v.ID} />
                    )}
                    <button
                      type="button"
                      onClick={() => onViewInJSON?.(v.ScreenID)}
                      style={viewJsonBtnStyle}
                    >
                      View in JSON
                    </button>
                  </div>
                </li>
                );
              })}
            </ul>
          </section>
        );
      })}
      </div>
    </div>
  );
}

const viewJsonBtnStyle: React.CSSProperties = {
  padding: "6px 10px",
  fontSize: 11,
  fontFamily: "var(--font-mono)",
  background: "transparent",
  border: "1px solid var(--border)",
  borderRadius: 6,
  color: "var(--text-1)",
  cursor: "pointer",
};
