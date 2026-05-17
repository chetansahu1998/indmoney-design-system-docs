"use client";

/**
 * DocumentView — full typed-stems PRD render.
 *
 * Fetches /api/prd/{sp}/{sf}/full on mount and renders the
 * nested PRDFull shape: PRD title + summary → tabs → overview → states
 * (StateCard each).
 *
 * The `refetchKey` prop is bumped by PRDShell whenever an inbox SSE
 * event for this sub_flow lands. Re-runs the load effect so the doc
 * stays in step with prd.author writes from Claude.
 */

import { useEffect, useState } from "react";

import { useAuth } from "@/lib/auth-client";

import { StateCard } from "./StateCard";
import { isPRDFull, type PRDGetResult, type PRDFull } from "./types";

interface Props {
  subProduct: string;
  subFlow: string;
  refetchKey: number;
}

type LoadState =
  | { kind: "loading" }
  | { kind: "error"; message: string }
  | { kind: "empty"; note: string }
  | { kind: "ok"; data: PRDFull };

export function DocumentView({ subProduct, subFlow, refetchKey }: Props) {
  const token = useAuth((s) => s.token);
  const [state, setState] = useState<LoadState>({ kind: "loading" });

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    setState({ kind: "loading" });
    (async () => {
      try {
        const res = await fetch(
          `/api/prd/${encodeURIComponent(subProduct)}/${encodeURIComponent(
            subFlow,
          )}/prd/full`,
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
            /* fallthrough */
          }
          setState({ kind: "error", message: detail });
          return;
        }
        const body = (await res.json()) as PRDGetResult;
        if (isPRDFull(body)) {
          setState({ kind: "ok", data: body });
        } else {
          setState({ kind: "empty", note: body.note });
        }
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

  if (state.kind === "loading") {
    return <div className="doc doc--empty">Loading PRD…</div>;
  }
  if (state.kind === "error") {
    return (
      <div className="doc doc--empty">
        <strong>Could not load PRD.</strong>
        <span>{state.message}</span>
        <style jsx>{`
          .doc {
            padding: 32px;
            text-align: center;
            color: var(--text-3);
            font-size: 13px;
          }
        `}</style>
      </div>
    );
  }
  if (state.kind === "empty") {
    return (
      <div className="doc doc--empty">
        <strong>No PRD yet</strong>
        <span>{state.note}</span>
        <style jsx>{`
          .doc {
            padding: 48px 16px;
            text-align: center;
            color: var(--text-3);
            font-size: 13px;
            display: flex;
            flex-direction: column;
            gap: 8px;
            align-items: center;
          }
          .doc strong {
            color: var(--text-1);
            font-weight: 600;
            font-size: 14px;
          }
        `}</style>
      </div>
    );
  }

  const { data } = state;
  return (
    <article className="doc">
      <header className="doc__head">
        <h2>{data.title || "Untitled PRD"}</h2>
        {data.summary_md && <pre className="md">{data.summary_md}</pre>}
      </header>

      {(data.tabs?.length ?? 0) === 0 && (
        <div className="doc__empty">
          No tabs yet. Use <code>/ind-prd add-tab</code> in Claude to seed the
          first tab.
        </div>
      )}

      {data.tabs?.map((tab) => (
        <section key={tab.id} className="doc__tab">
          <h3>{tab.name}</h3>
          {tab.overview_md && <pre className="md">{tab.overview_md}</pre>}
          {(tab.states?.length ?? 0) === 0 && (
            <div className="doc__thin">
              No states yet for this tab. Auto-skeleton creates one row per
              named frame once the designer ships the section, or use{" "}
              <code>/ind-prd add-state</code> to author manually.
            </div>
          )}
          <div className="doc__states">
            {tab.states?.map((s) => (
              <StateCard key={s.id} state={s} />
            ))}
          </div>
        </section>
      ))}

      {data.design_notes_md && (
        <section className="doc__tab">
          <h3>Design notes</h3>
          <pre className="md">{data.design_notes_md}</pre>
        </section>
      )}

      <style jsx>{`
        .doc {
          display: flex;
          flex-direction: column;
          gap: 24px;
        }
        .doc__head h2 {
          margin: 0;
          font-size: 18px;
          font-weight: 600;
          color: var(--text-1);
        }
        .doc__tab {
          display: flex;
          flex-direction: column;
          gap: 12px;
        }
        .doc__tab h3 {
          margin: 0;
          font-size: 14px;
          font-weight: 600;
          color: var(--text-1);
          letter-spacing: 0.02em;
        }
        .doc__states {
          display: flex;
          flex-direction: column;
          gap: 12px;
        }
        .doc__thin,
        .doc__empty {
          padding: 12px 16px;
          font-size: 12px;
          color: var(--text-3);
          border: 1px dashed var(--border, rgba(255, 255, 255, 0.08));
          border-radius: 6px;
          line-height: 1.55;
        }
        pre.md {
          margin: 0;
          font-family: inherit;
          font-size: 13px;
          color: var(--text-2);
          white-space: pre-wrap;
          line-height: 1.6;
        }
        code {
          font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
          font-size: 11px;
          background: var(--surface-1, rgba(255, 255, 255, 0.04));
          padding: 1px 5px;
          border-radius: 4px;
        }
      `}</style>
    </article>
  );
}
