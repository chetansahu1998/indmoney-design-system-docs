"use client";

/**
 * /inbox shell. Owns:
 *   - Filter state (URL-bound via Next's searchParams + router.replace)
 *   - View-state machine: loading | ok | empty | filtered_empty | error
 *   - Selection set + bulk-action submission
 *   - Per-row Acknowledge / Dismiss flows (funneled through BulkActionBar
 *     by treating the single-row case as selectedCount=1 transient)
 *   - Optimistic fade-out animation on successful lifecycle transitions
 *
 * SSE: this component does NOT subscribe to project events. The inbox is
 * cross-project; subscribing to N project trace_ids would be expensive
 * for an open-ended dataset. Optimistic local removal covers the common
 * case where the inbox itself initiated the action; concurrent edits
 * from other clients will surface on the next refetch (route remount,
 * filter change, or user-triggered "Refresh"). Phase 7 wires per-tenant
 * SSE so this gap closes without changing call sites.
 */

import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import EmptyState from "@/components/empty-state/EmptyState";
import {
  bulkPatchViolations,
  fetchInbox,
  patchViolationLifecycle,
  type InboxFilters,
  type InboxResponse,
  type InboxRow as Row,
  type LifecycleAction,
} from "@/lib/inbox/client";
import {
  inboxFiltersToSearchParams,
  parseInboxFiltersFromSearchParams,
} from "@/lib/inbox/filters";
import InboxFiltersBar from "./InboxFilters";
import InboxRowComponent from "./InboxRow";
import BulkActionBar from "./BulkActionBar";

type ViewState =
  | { kind: "loading" }
  | { kind: "ok"; data: InboxResponse }
  | { kind: "empty" }
  | { kind: "filtered_empty" }
  | { kind: "error"; status: number; error: string };

export default function InboxShell() {
  const router = useRouter();
  const searchParams = useSearchParams();

  const filters = useMemo<InboxFilters>(() => {
    const params = new URLSearchParams(searchParams?.toString() ?? "");
    return parseInboxFiltersFromSearchParams(params);
  }, [searchParams]);

  const [state, setState] = useState<ViewState>({ kind: "loading" });
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [fadingOut, setFadingOut] = useState<Set<string>>(new Set());
  const [pending, setPending] = useState(false);

  const filtersActive = useMemo(() => {
    const sp = inboxFiltersToSearchParams(filters);
    sp.delete("limit");
    sp.delete("offset");
    return sp.toString().length > 0;
  }, [filters]);

  // Refetch on filter change. Cancellation flag prevents stale responses
  // from clobbering newer ones.
  useEffect(() => {
    let cancelled = false;
    setState({ kind: "loading" });
    void fetchInbox(filters).then((r) => {
      if (cancelled) return;
      if (!r.ok) {
        setState({ kind: "error", status: r.status, error: r.error });
        return;
      }
      if (r.data.total === 0) {
        setState({ kind: filtersActive ? "filtered_empty" : "empty" });
        return;
      }
      setState({ kind: "ok", data: r.data });
    });
    return () => {
      cancelled = true;
    };
  }, [filters, filtersActive]);

  const updateFilters = useCallback(
    (next: InboxFilters) => {
      const sp = inboxFiltersToSearchParams(next).toString();
      router.replace(sp ? `/inbox?${sp}` : "/inbox");
      setSelected(new Set());
    },
    [router],
  );

  const toggleSelect = useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const toggleSelectAll = useCallback(() => {
    if (state.kind !== "ok") return;
    const ids = state.data.rows.map((r) => r.violation_id);
    setSelected((prev) => {
      const allSelected = ids.every((id) => prev.has(id));
      if (allSelected) return new Set();
      return new Set(ids);
    });
  }, [state]);

  const removeRowsLocally = useCallback(
    (ids: Set<string>) => {
      setState((prev) => {
        if (prev.kind !== "ok") return prev;
        const remaining = prev.data.rows.filter(
          (r) => !ids.has(r.violation_id),
        );
        if (remaining.length === 0) {
          return { kind: filtersActive ? "filtered_empty" : "empty" };
        }
        return {
          kind: "ok",
          data: {
            ...prev.data,
            rows: remaining,
            total: Math.max(0, prev.data.total - ids.size),
          },
        };
      });
    },
    [filtersActive],
  );

  // Bulk-action submit. Groups selected rows by project_slug because the
  // bulk-acknowledge endpoint is project-scoped server-side.
  const submitBulk = useCallback(
    async (action: "acknowledge" | "dismiss", reason: string) => {
      if (state.kind !== "ok") return;
      const rowsByID = new Map(state.data.rows.map((r) => [r.violation_id, r]));
      const targets = Array.from(selected)
        .map((id) => rowsByID.get(id))
        .filter((r): r is Row => Boolean(r));
      if (targets.length === 0) return;

      setPending(true);
      const fadeIDs = new Set(targets.map((r) => r.violation_id));
      setFadingOut(fadeIDs);

      try {
        // Group by slug to fit the bulk-acknowledge endpoint signature.
        const bySlug = new Map<string, string[]>();
        for (const r of targets) {
          const list = bySlug.get(r.project_slug) ?? [];
          list.push(r.violation_id);
          bySlug.set(r.project_slug, list);
        }

        const updated = new Set<string>();
        for (const [slug, ids] of bySlug.entries()) {
          // Chunk to the server's 100-row cap.
          for (let i = 0; i < ids.length; i += 100) {
            const chunk = ids.slice(i, i + 100);
            const res = await bulkPatchViolations(
              slug,
              chunk,
              action as LifecycleAction,
              reason,
            );
            if (res.ok) {
              for (const id of res.data.updated) updated.add(id);
            }
          }
        }

        // Fade-out animation duration mirrors InboxRow's CSS transition.
        await new Promise((r) => setTimeout(r, 240));
        removeRowsLocally(updated);
      } finally {
        setSelected(new Set());
        setFadingOut(new Set());
        setPending(false);
      }
    },
    [state, selected, removeRowsLocally],
  );

  // Per-row submit shares the bulk endpoint by selecting just one id.
  // BulkActionBar appears as soon as selectedCount > 0; we set selection
  // to a singleton to surface the reason form.
  const submitSingle = useCallback(
    async (row: Row, action: LifecycleAction, reason: string) => {
      setPending(true);
      const fadeIDs = new Set([row.violation_id]);
      setFadingOut(fadeIDs);
      try {
        const res = await patchViolationLifecycle(
          row.project_slug,
          row.violation_id,
          action,
          reason,
        );
        if (!res.ok) return;
        await new Promise((r) => setTimeout(r, 240));
        removeRowsLocally(fadeIDs);
      } finally {
        setSelected(new Set());
        setFadingOut(new Set());
        setPending(false);
      }
    },
    [removeRowsLocally],
  );

  const onAcknowledgeRow = useCallback((row: Row) => {
    // Surface the reason form by selecting just this row. The bulk action
    // bar handles the rest.
    setSelected(new Set([row.violation_id]));
  }, []);
  const onDismissRow = useCallback((row: Row) => {
    setSelected(new Set([row.violation_id]));
  }, []);

  const totalMatching = state.kind === "ok" ? state.data.total : 0;
  const allSelected =
    state.kind === "ok" &&
    state.data.rows.length > 0 &&
    state.data.rows.every((r) => selected.has(r.violation_id));

  return (
    <main
      style={{
        padding: "32px 24px 96px",
        maxWidth: 1100,
        margin: "0 auto",
        minHeight: "100vh",
      }}
      data-testid="inbox-shell"
    >
      <header style={{ marginBottom: 16 }}>
        <h1 style={{ fontSize: 24, marginBottom: 4 }}>Inbox</h1>
        <p
          style={{
            fontSize: 12,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          Active violations across every flow you can edit. Acknowledge with
          a reason, dismiss with a rationale, or open the project to fix.
        </p>
      </header>

      <InboxFiltersBar
        filters={filters}
        onChange={updateFilters}
        totalMatching={totalMatching}
        loading={state.kind === "loading"}
      />

      {state.kind === "loading" && <EmptyState variant="loading" />}

      {state.kind === "empty" && (
        <EmptyState
          variant="welcome"
          title="Inbox zero"
          description="No active violations against your projects right now. Nice."
        />
      )}

      {state.kind === "filtered_empty" && (
        <EmptyState
          variant="welcome"
          title="No matches"
          description="No violations match the current filters. Try clearing them."
        />
      )}

      {state.kind === "error" && (
        <EmptyState
          variant="error"
          title="Couldn't load inbox"
          description={`${state.error} (status ${state.status})`}
        />
      )}

      {state.kind === "ok" && (
        <>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 12,
              padding: "8px 0",
              borderBottom: "1px solid var(--border)",
              marginBottom: 8,
            }}
          >
            <input
              type="checkbox"
              checked={allSelected}
              onChange={toggleSelectAll}
              aria-label="Select all visible rows"
            />
            <span
              style={{
                fontSize: 11,
                fontFamily: "var(--font-mono)",
                color: "var(--text-3)",
              }}
            >
              Showing {state.data.rows.length} of {state.data.total} ·{" "}
              {selected.size} selected
            </span>
          </div>

          <ul
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 8,
              padding: 0,
              margin: 0,
            }}
          >
            {state.data.rows.map((row) => (
              <InboxRowComponent
                key={row.violation_id}
                row={row}
                selected={selected.has(row.violation_id)}
                onToggle={toggleSelect}
                fadeOut={fadingOut.has(row.violation_id)}
                onAcknowledge={onAcknowledgeRow}
                onDismiss={onDismissRow}
              />
            ))}
          </ul>

          {state.data.rows.length < state.data.total && (
            <button
              type="button"
              onClick={() =>
                updateFilters({
                  ...filters,
                  offset: (filters.offset ?? 0) + state.data.rows.length,
                })
              }
              style={{
                marginTop: 12,
                padding: "8px 14px",
                fontSize: 12,
                fontFamily: "var(--font-mono)",
                background: "transparent",
                color: "var(--accent)",
                border: "1px solid var(--border)",
                borderRadius: 6,
                cursor: "pointer",
              }}
            >
              Load more
            </button>
          )}
        </>
      )}

      <BulkActionBar
        selectedCount={selected.size}
        pending={pending}
        onSubmit={(action, reason) => {
          // Single-row case: dispatch through patchViolationLifecycle
          // (PATCH endpoint, not bulk). Saves an audit_log bulk_id field
          // for the trivial case.
          if (selected.size === 1 && state.kind === "ok") {
            const id = Array.from(selected)[0];
            const row = state.data.rows.find((r) => r.violation_id === id);
            if (row) {
              void submitSingle(row, action, reason);
              return;
            }
          }
          void submitBulk(action, reason);
        }}
        onClear={() => setSelected(new Set())}
      />
    </main>
  );
}
