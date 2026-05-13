"use client";

/**
 * RunsPanel — Phase 2A U4.
 *
 * Lists the last 20 inventory_run rows for the tenant, auto-refreshes
 * every 10s while the page is open, and exposes a "Sync now" button
 * that POSTs to /v1/admin/figma-inventory/sync.
 *
 * The "in-progress" row (finished_at is empty) gets a pulsing indicator
 * and refreshes at 2s cadence — gives the admin live feedback while a
 * cycle is running.
 */

import { useCallback, useEffect, useState } from "react";

import { adminFetchJSON } from "@/app/atlas/admin/_lib/adminFetch";
import { useAuth } from "@/lib/auth-client";

import type { InventoryRun, ListRunsResponse, SyncTriggerResponse } from "../types";

export function RunsPanel() {
  const token = useAuth((s) => s.token);
  const [runs, setRuns] = useState<InventoryRun[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string>("");
  const [toast, setToast] = useState<string>("");

  const load = useCallback(async () => {
    if (!token) return;
    try {
      const res = await adminFetchJSON<ListRunsResponse>("/v1/admin/figma-inventory/runs?limit=20");
      setRuns(res.runs || []);
      setErr("");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    void load();
  }, [load]);

  // 10s baseline poll; bump to 2s when a row is in progress so live
  // counters refresh quickly during a sync.
  useEffect(() => {
    if (!token) return;
    const hasInProgress = runs.some((r) => !r.finished_at);
    const interval = hasInProgress ? 2000 : 10_000;
    const t = setInterval(() => {
      void load();
    }, interval);
    return () => clearInterval(t);
  }, [token, load, runs]);

  async function syncNow() {
    setToast("");
    try {
      const res = await adminFetchJSON<SyncTriggerResponse>(
        "/v1/admin/figma-inventory/sync",
        { method: "POST" },
      );
      if (res.triggered) {
        setToast("Sync triggered — next run will appear here.");
        await load();
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (msg.toLowerCase().includes("no_poller")) {
        setToast("Poller not configured on this server.");
      } else {
        setToast(`Failed: ${msg}`);
      }
    }
    setTimeout(() => setToast(""), 5000);
  }

  return (
    <div className="rp-root">
      <div className="rp-head">
        <h2>Recent runs</h2>
        <button type="button" onClick={syncNow} className="rp-sync-btn">
          ⟳ Sync now
        </button>
      </div>
      {toast && <p className="rp-toast">{toast}</p>}
      {loading && runs.length === 0 && <p className="rp-loading">Loading runs…</p>}
      {err && <p className="rp-err">Failed: {err}</p>}
      {!loading && runs.length === 0 && !err && (
        <p className="rp-empty">No runs yet. Click "Sync now" or wait for the next 5-min tick.</p>
      )}
      <ul className="rp-list">
        {runs.map((r) => (
          <RunRow key={r.id} run={r} />
        ))}
      </ul>
      <style jsx>{`
        .rp-root {
          display: flex;
          flex-direction: column;
          gap: 12px;
        }
        .rp-head {
          display: flex;
          align-items: center;
          justify-content: space-between;
        }
        .rp-head h2 {
          margin: 0;
          font-size: 14px;
          font-weight: 600;
          text-transform: uppercase;
          letter-spacing: 0.08em;
          color: var(--text-2, #888);
        }
        .rp-sync-btn {
          background: transparent;
          border: 1px solid var(--accent, #6366f1);
          color: var(--accent, #6366f1);
          font: inherit;
          font-size: 12px;
          cursor: pointer;
          border-radius: 6px;
          padding: 4px 10px;
        }
        .rp-sync-btn:hover {
          background: var(--accent-dim, rgba(99, 102, 241, 0.15));
        }
        .rp-toast {
          font-size: 12px;
          color: var(--accent, #6366f1);
          margin: 0;
          padding: 6px 10px;
          background: var(--accent-dim, rgba(99, 102, 241, 0.1));
          border-radius: 6px;
        }
        .rp-loading,
        .rp-empty,
        .rp-err {
          font-size: 12px;
          color: var(--text-3, #777);
          margin: 0;
        }
        .rp-err {
          color: var(--danger, #ef4444);
        }
        .rp-list {
          list-style: none;
          padding: 0;
          margin: 0;
          display: flex;
          flex-direction: column;
          gap: 6px;
          max-height: 600px;
          overflow-y: auto;
        }
      `}</style>
    </div>
  );
}

function RunRow({ run }: { run: InventoryRun }) {
  const inProgress = !run.finished_at;
  const errorSample = run.error_sample?.slice(0, 3).join("\n") || "";
  return (
    <li className={`rp-row${inProgress ? " inprogress" : ""}`}>
      <div className="rp-row-head">
        <span className="rp-time" title={run.started_at}>
          {relativeTime(run.started_at)}
        </span>
        {inProgress ? (
          <span className="rp-pulse" aria-label="In progress">●</span>
        ) : (
          <span className="rp-dur">{(run.duration_ms || 0) > 0 ? `${(run.duration_ms! / 1000).toFixed(1)}s` : ""}</span>
        )}
      </div>
      <div className="rp-counters">
        <span title="teams">T {run.teams_crawled}</span>
        <span title="projects">P {run.projects_seen}</span>
        <span title="files seen">F {run.files_seen}</span>
        <span title="files refetched (depth=2)">F* {run.files_refetched}</span>
        <span title="pages upserted">pg {run.pages_upserted}</span>
        <span title="sections upserted">sec {run.sections_upserted}</span>
        {run.error_count > 0 && (
          <span className="rp-err-count" title={errorSample || "see logs"}>
            !{run.error_count}
          </span>
        )}
      </div>
      <style jsx>{`
        .rp-row {
          padding: 8px 10px;
          background: var(--surface-2, rgba(255, 255, 255, 0.03));
          border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          border-radius: 8px;
          display: flex;
          flex-direction: column;
          gap: 4px;
        }
        .rp-row.inprogress {
          border-color: var(--accent, #6366f1);
        }
        .rp-row-head {
          display: flex;
          align-items: center;
          justify-content: space-between;
        }
        .rp-time {
          font-size: 12px;
          font-weight: 500;
        }
        .rp-dur {
          font-size: 11px;
          color: var(--text-3, #888);
          font-family: ui-monospace, monospace;
        }
        .rp-pulse {
          color: var(--accent, #6366f1);
          animation: pulse 1s infinite ease-in-out;
        }
        @keyframes pulse {
          0%, 100% { opacity: 1; }
          50% { opacity: 0.3; }
        }
        .rp-counters {
          display: flex;
          flex-wrap: wrap;
          gap: 8px;
          font-size: 11px;
          font-family: ui-monospace, monospace;
          color: var(--text-2, #aaa);
        }
        .rp-err-count {
          color: var(--danger, #ef4444);
          font-weight: 600;
        }
      `}</style>
    </li>
  );
}

function relativeTime(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso.slice(0, 16);
  const diffMs = Date.now() - d.getTime();
  const diffSec = Math.floor(diffMs / 1000);
  if (diffSec < 60) return `${diffSec}s ago`;
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  return d.toISOString().slice(0, 16).replace("T", " ");
}
