"use client";

/**
 * PRDShell — client-side root of the /projects/{subProduct}/{subFlow}/prd
 * viewer (U9).
 *
 * Layout (mirrors app/atlas/_lib/Shell.tsx chrome + atlas canvas+panel
 * split):
 *
 *   ┌────────────────────────────────────┬─────────────────────┐
 *   │  Header (title + Wall/Doc tabs)    │                     │
 *   ├────────────────────────────────────┤   DRD pane          │
 *   │  CanvasShell (lifecycle-picked)    │   (read-only v1)    │
 *   ├────────────────────────────────────┤                     │
 *   │  Wall   |   DocumentView           │                     │
 *   └────────────────────────────────────┴─────────────────────┘
 *
 * Lifecycle:
 *   1. Mount → fetch /api/projects/{sp}/{sf}/prd (section.inspect bundle).
 *   2. Subscribe to inbox:<tenant> SSE; on figma.design_shipped or
 *      drd.prototype_attached for this sub_flow, re-fetch.
 *   3. Wall (default) reads `data.wall` directly. Document tab triggers
 *      a one-time fetch of /api/projects/{sp}/{sf}/prd/full.
 *
 * Auth + base URL match the conventions in lib/projects/client.ts and
 * app/atlas/_lib/Shell.tsx. We never bypass app/projects/layout.tsx's
 * auth gate — by the time this component renders, the token exists.
 */

import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";

import { useAuth } from "@/lib/auth-client";

import { CanvasShell } from "./CanvasShell";
import { DocumentView } from "./DocumentView";
import { DRDPane } from "./DRDPane";
import { Wall } from "./Wall";
import type { SectionInspect } from "./types";

interface Props {
  subProduct: string;
  subFlow: string;
}

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL || "http://localhost:8080";
}

type LoadState =
  | { kind: "loading" }
  | { kind: "error"; message: string; status?: number }
  | { kind: "ok"; data: SectionInspect };

type Tab = "wall" | "doc";

export function PRDShell({ subProduct, subFlow }: Props) {
  const token = useAuth((s) => s.token);
  const fullSlug = `${subProduct}/${subFlow}`;
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const [tab, setTab] = useState<Tab>("wall");
  // refetchKey forces the wall + section.inspect to reload on SSE events.
  // Bumps the React key on the load effect; cheaper than a manual fetch
  // function the SSE handler closes over.
  const [refetchKey, setRefetchKey] = useState(0);
  const bumpRefetch = useCallback(() => setRefetchKey((k) => k + 1), []);

  // ─── 1. section.inspect fetch ─────────────────────────────────────────
  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    setState({ kind: "loading" });
    (async () => {
      try {
        const res = await fetch(
          `/api/projects/${encodeURIComponent(subProduct)}/${encodeURIComponent(
            subFlow,
          )}/prd`,
          {
            headers: {
              Authorization: `Bearer ${token}`,
              Accept: "application/json",
            },
            cache: "no-store",
          },
        );
        if (cancelled) return;
        if (!res.ok) {
          let detail = `HTTP ${res.status}`;
          try {
            const body = (await res.json()) as { detail?: string; error?: string };
            detail = body.detail ?? body.error ?? detail;
          } catch {
            /* keep status string */
          }
          setState({ kind: "error", message: detail, status: res.status });
          return;
        }
        const data = (await res.json()) as SectionInspect;
        setState({ kind: "ok", data });
      } catch (err) {
        if (cancelled) return;
        setState({
          kind: "error",
          message: err instanceof Error ? err.message : String(err),
        });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [token, subProduct, subFlow, refetchKey]);

  // ─── 2. SSE subscription to inbox:<tenant> ────────────────────────────
  // Reuses the same ticket-then-EventSource handshake the AtlasShell uses
  // for the personas badge. We listen for two event types relevant to the
  // PRD viewer + filter by sub_flow_slug client-side.
  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    let es: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay = 1000;
    const MAX_BACKOFF = 30_000;

    async function subscribe() {
      if (cancelled) return;
      try {
        const tres = await fetch(`${dsBaseURL()}/v1/inbox/events/ticket`, {
          method: "POST",
          headers: {
            Accept: "application/json",
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
          },
          body: "{}",
        });
        if (!tres.ok) {
          if (tres.status >= 500 && !cancelled) scheduleReconnect();
          return;
        }
        const t = (await tres.json()) as { ticket: string };
        if (cancelled) return;
        es = new EventSource(
          `${dsBaseURL()}/v1/inbox/events?ticket=${encodeURIComponent(t.ticket)}`,
        );
        es.addEventListener("open", () => {
          reconnectDelay = 1000;
        });
        // Both events carry { sub_flow_slug } — match on it so other
        // sub_flows' events don't trigger our refetch.
        const onSubFlowEvent = (raw: Event) => {
          const ev = raw as MessageEvent<string>;
          try {
            const payload = JSON.parse(ev.data) as { sub_flow_slug?: string };
            if (payload.sub_flow_slug === fullSlug) {
              bumpRefetch();
            }
          } catch {
            /* malformed payload; ignore */
          }
        };
        es.addEventListener("figma.design_shipped", onSubFlowEvent);
        es.addEventListener("drd.prototype_attached", onSubFlowEvent);
        // Future-friendly: PRD-state events flow through the same channel
        // once U2b + author tools start publishing. Treat them as refetch
        // triggers too so the wall stays fresh.
        es.addEventListener("prd.state_added", onSubFlowEvent);
        es.addEventListener("frame_tag.created", onSubFlowEvent);
        es.addEventListener("error", () => {
          if (cancelled) return;
          es?.close();
          es = null;
          scheduleReconnect();
        });
      } catch {
        if (!cancelled) scheduleReconnect();
      }
    }

    function scheduleReconnect() {
      if (cancelled || reconnectTimer) return;
      reconnectTimer = setTimeout(() => {
        reconnectTimer = null;
        reconnectDelay = Math.min(reconnectDelay * 2, MAX_BACKOFF);
        void subscribe();
      }, reconnectDelay);
    }

    void subscribe();
    return () => {
      cancelled = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      es?.close();
    };
  }, [token, fullSlug, bumpRefetch]);

  // ─── 3. Render ─────────────────────────────────────────────────────────
  return (
    <div className="prd-shell">
      <header className="prd-shell__header">
        <div>
          <div className="prd-shell__breadcrumb">
            <Link href="/atlas">Atlas</Link>
            <span> / </span>
            <span>{subProduct}</span>
            <span> / </span>
            <span>{subFlow}</span>
          </div>
          <h1>
            {state.kind === "ok"
              ? state.data.sub_flow.name
              : `${subProduct} / ${subFlow}`}
          </h1>
          {state.kind === "ok" && (
            <p className="prd-shell__meta">
              {lifecycleLabel(state.data.sub_flow.canvas_lifecycle)} ·{" "}
              {state.data.wall.counts.bound}/{state.data.wall.counts.total}{" "}
              frames bound · {state.data.wall.counts.coverage_percent}% covered
            </p>
          )}
        </div>
        <nav className="prd-shell__tabs" aria-label="View">
          <button
            type="button"
            className={tab === "wall" ? "active" : ""}
            onClick={() => setTab("wall")}
          >
            Wall
          </button>
          <button
            type="button"
            className={tab === "doc" ? "active" : ""}
            onClick={() => setTab("doc")}
          >
            Document
          </button>
        </nav>
      </header>

      <div className="prd-shell__body">
        <main className="prd-shell__main">
          {state.kind === "loading" && (
            <div className="prd-shell__empty">Loading…</div>
          )}
          {state.kind === "error" && (
            <div className="prd-shell__empty prd-shell__empty--error">
              <strong>Failed to load PRD</strong>
              <span>
                {state.status ? `${state.status} · ` : ""}
                {state.message}
              </span>
            </div>
          )}
          {state.kind === "ok" && (
            <>
              <CanvasShell
                lifecycle={state.data.sub_flow.canvas_lifecycle}
                prototypeUrl={state.data.sub_flow.prototype_url ?? null}
                prototypeTitle={state.data.sub_flow.prototype_title ?? null}
                frames={state.data.wall.frames.filter(
                  (r) => r.binding_status !== "orphaned",
                )}
                slug={fullSlug}
                fileKey={state.data.sub_flow.figma_file_key}
              />
              <div className="prd-shell__view">
                {tab === "wall" ? (
                  <Wall
                    data={state.data.wall}
                    slug={fullSlug}
                    fileKey={state.data.sub_flow.figma_file_key}
                  />
                ) : (
                  <DocumentView
                    subProduct={subProduct}
                    subFlow={subFlow}
                    refetchKey={refetchKey}
                  />
                )}
              </div>
            </>
          )}
        </main>

        <aside className="prd-shell__drd">
          <DRDPane subProductSlug={subProduct} subFlowSlug={subFlow} />
        </aside>
      </div>

      <style jsx>{`
        .prd-shell {
          min-height: 100vh;
          background: var(--bg);
          color: var(--text-1);
          font-family: var(--font-sans, "Inter Variable", sans-serif);
          display: flex;
          flex-direction: column;
        }
        .prd-shell__header {
          display: flex;
          justify-content: space-between;
          align-items: flex-end;
          gap: 24px;
          padding: 24px 32px 16px;
          border-bottom: 1px solid var(--border, rgba(255, 255, 255, 0.08));
        }
        .prd-shell__header h1 {
          margin: 4px 0 0;
          font-size: 22px;
          font-weight: 600;
        }
        .prd-shell__breadcrumb {
          font-size: 12px;
          color: var(--text-3);
          letter-spacing: 0.02em;
        }
        .prd-shell__breadcrumb :global(a) {
          color: var(--text-3);
          text-decoration: none;
        }
        .prd-shell__breadcrumb :global(a:hover) {
          color: var(--text-1);
        }
        .prd-shell__meta {
          margin: 6px 0 0;
          font-size: 12px;
          color: var(--text-3);
          font-variant-numeric: tabular-nums;
        }
        .prd-shell__tabs {
          display: flex;
          gap: 4px;
          padding: 4px;
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          border-radius: 999px;
        }
        .prd-shell__tabs button {
          appearance: none;
          background: transparent;
          color: var(--text-3);
          border: 0;
          padding: 6px 14px;
          border-radius: 999px;
          font-size: 12px;
          letter-spacing: 0.02em;
          cursor: pointer;
        }
        .prd-shell__tabs button.active {
          background: var(--accent);
          color: var(--bg-canvas);
        }
        .prd-shell__tabs button:hover:not(.active) {
          color: var(--text-1);
        }
        .prd-shell__body {
          display: grid;
          grid-template-columns: 1fr 420px;
          gap: 0;
          flex: 1;
          min-height: 0;
        }
        .prd-shell__main {
          display: flex;
          flex-direction: column;
          min-width: 0;
          padding: 16px 24px 48px;
          gap: 16px;
          overflow: auto;
        }
        .prd-shell__drd {
          border-left: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          padding: 16px 20px;
          overflow: auto;
        }
        .prd-shell__view {
          min-height: 0;
        }
        .prd-shell__empty {
          padding: 48px 16px;
          text-align: center;
          color: var(--text-3);
          font-size: 14px;
          display: flex;
          flex-direction: column;
          gap: 8px;
          align-items: center;
        }
        .prd-shell__empty--error {
          color: var(--text-2);
        }
        .prd-shell__empty strong {
          color: var(--text-1);
          font-weight: 600;
        }
        @media (max-width: 960px) {
          .prd-shell__body {
            grid-template-columns: 1fr;
          }
          .prd-shell__drd {
            border-left: 0;
            border-top: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          }
        }
      `}</style>
    </div>
  );
}

function lifecycleLabel(l: string): string {
  switch (l) {
    case "design-shipped":
      return "Design shipped";
    case "proto-only":
      return "Prototype only";
    case "proto-wip":
      return "Prototype · designer in progress";
    case "empty":
      return "No canvas yet";
    default:
      return l;
  }
}

