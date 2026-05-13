"use client";

/**
 * RunsStrip — compact horizontal strip of recent poll cycles.
 *
 * Replaces the Phase 2A draft's full-height side panel. Surface area is
 * minimal because the FilesTable already shows per-file `last_modified`;
 * the strip exists for ops to see "is the poller alive and what did the
 * last cycle do".
 *
 * Auto-refreshes every 10 s, drops to 2 s while a run is in progress.
 */

import { useCallback, useEffect, useState } from "react";

import { adminFetchJSON } from "@/app/atlas/admin/_lib/adminFetch";
import { useAuth } from "@/lib/auth-client";

import { GhostBtn, formatAgo } from "../_lib/Table";
import type { InventoryRun, ListRunsResponse, SyncTriggerResponse } from "../types";

export function RunsStrip() {
  const token = useAuth((s) => s.token);
  const [runs, setRuns] = useState<InventoryRun[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string>("");
  const [toast, setToast] = useState<string>("");

  const load = useCallback(async () => {
    if (!token) return;
    try {
      const res = await adminFetchJSON<ListRunsResponse>("/v1/admin/figma-inventory/runs?limit=8");
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

  useEffect(() => {
    if (!token) return;
    const inProgress = runs.some((r) => !r.finished_at);
    const ms = inProgress ? 2000 : 10_000;
    const t = setInterval(() => void load(), ms);
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
        setToast("Sync triggered.");
        await load();
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setToast(msg.toLowerCase().includes("no_poller")
        ? "Poller not configured on this server."
        : `Failed: ${msg}`);
    }
    setTimeout(() => setToast(""), 4000);
  }

  return (
    <section style={{ marginTop: 32 }}>
      <div style={{ display: "flex", alignItems: "baseline", gap: 12, marginBottom: 12 }}>
        <h2 style={{ fontSize: 14, fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.08em", color: "var(--text-2, #aaa)", margin: 0 }}>
          Recent runs
        </h2>
        <span style={{ fontSize: 11, color: "var(--text-3, #777)" }}>
          Poller crawls every 5 minutes. Manual trigger →
        </span>
        <button
          type="button"
          onClick={syncNow}
          style={{
            marginLeft: "auto",
            padding: "6px 14px",
            background: "var(--accent-soft, rgba(80, 180, 255, 0.12))",
            border: "1px solid var(--accent, #7b9fff)",
            color: "var(--accent, #7b9fff)",
            borderRadius: 6,
            cursor: "pointer",
            fontSize: 13,
          }}
        >
          ⟳ Sync now
        </button>
      </div>

      {toast && (
        <div
          style={{
            marginBottom: 8,
            padding: "6px 12px",
            background: "var(--accent-soft, rgba(80, 180, 255, 0.08))",
            border: "1px solid var(--accent, rgba(80, 180, 255, 0.3))",
            color: "var(--accent, #7b9fff)",
            borderRadius: 6,
            fontSize: 12,
          }}
        >
          {toast}
        </div>
      )}

      {loading && runs.length === 0 && (
        <p style={{ fontSize: 12, color: "var(--text-3, #888)", margin: 0 }}>Loading runs…</p>
      )}
      {err && (
        <p style={{ fontSize: 12, color: "var(--danger, #ef4444)", margin: 0 }}>Failed: {err}</p>
      )}
      {!loading && runs.length === 0 && !err && (
        <p style={{ fontSize: 12, color: "var(--text-3, #888)", margin: 0 }}>
          No runs yet. Click <strong>Sync now</strong> or wait 5 minutes.
        </p>
      )}

      {runs.length > 0 && (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(180px, 1fr))",
            gap: 8,
          }}
        >
          {runs.map((r) => (
            <RunCard key={r.id} run={r} />
          ))}
        </div>
      )}
    </section>
  );
}

function RunCard({ run }: { run: InventoryRun }) {
  const inProgress = !run.finished_at;
  return (
    <div
      style={{
        padding: "10px 12px",
        background: "var(--bg-surface, rgba(255,255,255,0.03))",
        border: `1px solid ${inProgress ? "var(--accent, #7b9fff)" : "var(--border, rgba(255,255,255,0.08))"}`,
        borderRadius: 8,
        display: "flex",
        flexDirection: "column",
        gap: 6,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <span style={{ fontSize: 12, fontWeight: 500, color: "var(--text-1, #f7f7f7)" }} title={run.started_at}>
          {formatAgo(run.started_at)}
        </span>
        {inProgress ? (
          <span style={{ color: "var(--accent, #7b9fff)", fontSize: 12, animation: "pulse 1s infinite ease-in-out" }}>
            ●
          </span>
        ) : (
          <span style={{ fontSize: 11, color: "var(--text-3, #888)", fontFamily: "ui-monospace, monospace" }}>
            {(run.duration_ms ?? 0) > 0 ? `${((run.duration_ms ?? 0) / 1000).toFixed(1)}s` : ""}
          </span>
        )}
      </div>
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: 8,
          fontSize: 11,
          fontFamily: "ui-monospace, monospace",
          color: "var(--text-2, #aaa)",
          fontVariantNumeric: "tabular-nums",
        }}
      >
        <span title="files seen">F {run.files_seen}</span>
        <span title="files refetched (depth=2 pages/sections)">F* {run.files_refetched}</span>
        <span title="pages upserted">pg {run.pages_upserted}</span>
        <span title="sec upserted">sec {run.sections_upserted}</span>
        {run.error_count > 0 && (
          <span style={{ color: "var(--danger, #ef4444)", fontWeight: 600 }} title={(run.error_sample || []).slice(0, 3).join("\n")}>
            !{run.error_count}
          </span>
        )}
      </div>
      <style jsx>{`
        @keyframes pulse {
          0%, 100% { opacity: 1; }
          50% { opacity: 0.3; }
        }
      `}</style>
    </div>
  );
}
