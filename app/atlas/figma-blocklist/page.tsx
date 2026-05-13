"use client";

/**
 * /atlas/figma-blocklist — designer + ops surface for the
 * figma_render_blocklist (2026-05-12).
 *
 * Lists every (file_id, node_id) currently suppressed by the
 * persistent-failure skip list. Each row shows:
 *   - The Figma deeplink so a designer can jump into the file, touch
 *     the frame, and let the next sync's canonical_tree hash change
 *     auto-clear the row.
 *   - First/last failure timestamps + consecutive count for triage.
 *   - The last error so designers know whether to retry or fix the
 *     underlying frame (some errors signal real Figma render bugs,
 *     others signal designer-fixable issues like layered SVG masks).
 *   - A "Clear" button calling DELETE /v1/admin/figma-render-blocklist/{file}/{node}
 *     for operators who confirmed the upstream issue is resolved and
 *     don't want to wait for the 24h cooldown.
 *
 * Auth model: same as the rest of /atlas/admin — JWT in zustand-persist;
 * non-admins get 403 from the server which we surface as an EmptyState.
 */

import { useCallback, useEffect, useState } from "react";
import { Shell } from "@/app/atlas/_lib/Shell";
import { useAuth } from "@/lib/auth-client";

interface BlocklistEntry {
  file_id: string;
  node_id: string;
  first_failure_at: string;
  last_failure_at: string;
  consecutive_failures: number;
  last_error: string;
  cooldown_until: string;
  active: boolean;
  clear_hash?: string;
}

interface ListResponse {
  entries: BlocklistEntry[];
  total: number;
}

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL || "http://localhost:8080";
}

function figmaDeeplink(fileID: string, nodeID: string): string {
  // Figma node IDs come in "X:Y" form; the URL needs them as "X-Y".
  const urlNodeID = nodeID.replace(/:/g, "-");
  return `https://www.figma.com/design/${fileID}?node-id=${urlNodeID}`;
}

function formatAgo(iso: string): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const seconds = Math.floor((Date.now() - t) / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

function formatCooldown(iso: string, active: boolean): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  if (!active) return "expired";
  const seconds = Math.floor((t - Date.now()) / 1000);
  if (seconds <= 0) return "expired";
  if (seconds < 60) return `${seconds}s left`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m left`;
  return `${Math.floor(seconds / 3600)}h left`;
}

export default function FigmaBlocklistPage() {
  const token = useAuth((s) => s.token);
  const [entries, setEntries] = useState<BlocklistEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<"all" | "active">("active");

  const fetchEntries = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(`${dsBaseURL()}/v1/admin/figma-render-blocklist`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!res.ok) {
        const body = await res.text();
        setError(`HTTP ${res.status}: ${body.slice(0, 200)}`);
        setEntries([]);
        return;
      }
      const json: ListResponse = await res.json();
      setEntries(json.entries ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setEntries([]);
    } finally {
      setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    void fetchEntries();
  }, [fetchEntries]);

  const clearEntry = useCallback(
    async (fileID: string, nodeID: string) => {
      if (!token) return;
      // Optimistic local removal — refetch on settle keeps the list
      // honest if the DELETE failed.
      setEntries((prev) =>
        prev.filter((e) => !(e.file_id === fileID && e.node_id === nodeID)),
      );
      try {
        const res = await fetch(
          `${dsBaseURL()}/v1/admin/figma-render-blocklist/${encodeURIComponent(fileID)}/${encodeURIComponent(nodeID)}`,
          {
            method: "DELETE",
            headers: { Authorization: `Bearer ${token}` },
          },
        );
        if (!res.ok) {
          setError(`Clear failed: HTTP ${res.status}`);
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        void fetchEntries();
      }
    },
    [token, fetchEntries],
  );

  const visibleEntries =
    filter === "active" ? entries.filter((e) => e.active) : entries;

  return (
    <Shell
      title="Figma render blocklist"
      description="Frames Figma can't render. Touch the frame in Figma to auto-clear on next sync, or clear manually below."
    >
      <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 16 }}>
        <button
          type="button"
          className="admin-pill"
          aria-pressed={filter === "active"}
          onClick={() => setFilter("active")}
          style={pillStyle(filter === "active")}
        >
          Active ({entries.filter((e) => e.active).length})
        </button>
        <button
          type="button"
          className="admin-pill"
          aria-pressed={filter === "all"}
          onClick={() => setFilter("all")}
          style={pillStyle(filter === "all")}
        >
          All ({entries.length})
        </button>
        <button
          type="button"
          onClick={() => void fetchEntries()}
          disabled={loading}
          style={{
            marginLeft: "auto",
            padding: "6px 12px",
            background: "transparent",
            border: "1px solid rgba(255,255,255,0.2)",
            color: "rgba(255,255,255,0.7)",
            borderRadius: 6,
            cursor: loading ? "wait" : "pointer",
          }}
        >
          {loading ? "Refreshing…" : "Refresh"}
        </button>
      </div>

      {error && (
        <div
          role="alert"
          style={{
            padding: 12,
            marginBottom: 16,
            background: "rgba(255, 80, 80, 0.1)",
            border: "1px solid rgba(255, 80, 80, 0.4)",
            borderRadius: 8,
            color: "rgba(255, 80, 80, 0.9)",
            fontSize: 13,
          }}
        >
          {error}
        </div>
      )}

      {loading && entries.length === 0 ? (
        <div style={emptyState}>Loading…</div>
      ) : visibleEntries.length === 0 ? (
        <div style={emptyState}>
          {filter === "active"
            ? "No frames are currently suppressed. Nothing for designers to fix."
            : "No frames have ever been blocklisted on this tenant."}
        </div>
      ) : (
        <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
          {visibleEntries.map((entry) => (
            <li
              key={`${entry.file_id}:${entry.node_id}`}
              style={{
                padding: 16,
                marginBottom: 12,
                background: "rgba(255,255,255,0.03)",
                border: `1px solid ${entry.active ? "rgba(255, 200, 80, 0.4)" : "rgba(255,255,255,0.08)"}`,
                borderRadius: 8,
              }}
            >
              <div style={{ display: "flex", alignItems: "flex-start", gap: 12, marginBottom: 8 }}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontFamily: "ui-monospace, monospace", fontSize: 13, color: "rgba(255,255,255,0.95)" }}>
                    {entry.file_id}
                    <span style={{ color: "rgba(255,255,255,0.4)" }}> · </span>
                    {entry.node_id}
                  </div>
                  <div style={{ fontSize: 12, color: "rgba(255,255,255,0.5)", marginTop: 2 }}>
                    First failure {formatAgo(entry.first_failure_at)} ·{" "}
                    Last failure {formatAgo(entry.last_failure_at)} ·{" "}
                    {entry.consecutive_failures} consecutive ·{" "}
                    Cooldown: {formatCooldown(entry.cooldown_until, entry.active)}
                  </div>
                </div>
                <div style={{ display: "flex", gap: 8, flexShrink: 0 }}>
                  <a
                    href={figmaDeeplink(entry.file_id, entry.node_id)}
                    target="_blank"
                    rel="noopener noreferrer"
                    style={{
                      padding: "6px 12px",
                      background: "rgba(80, 180, 255, 0.1)",
                      border: "1px solid rgba(80, 180, 255, 0.4)",
                      color: "rgba(150, 200, 255, 1)",
                      borderRadius: 6,
                      textDecoration: "none",
                      fontSize: 12,
                    }}
                  >
                    Open in Figma →
                  </a>
                  <button
                    type="button"
                    onClick={() => void clearEntry(entry.file_id, entry.node_id)}
                    style={{
                      padding: "6px 12px",
                      background: "rgba(255, 200, 80, 0.1)",
                      border: "1px solid rgba(255, 200, 80, 0.4)",
                      color: "rgba(255, 200, 80, 1)",
                      borderRadius: 6,
                      cursor: "pointer",
                      fontSize: 12,
                    }}
                    title="Force-clear this entry; the next render call will re-attempt the frame."
                  >
                    Clear
                  </button>
                </div>
              </div>
              <div
                style={{
                  fontFamily: "ui-monospace, monospace",
                  fontSize: 12,
                  color: "rgba(255, 150, 150, 0.85)",
                  background: "rgba(255, 80, 80, 0.06)",
                  padding: 8,
                  borderRadius: 4,
                  wordBreak: "break-word",
                }}
              >
                {entry.last_error || "(no error message recorded)"}
              </div>
              <div style={{ marginTop: 8, fontSize: 11, color: "rgba(255,255,255,0.35)" }}>
                💡 Re-touching the frame in Figma (toggle visibility, re-save) will change its canonical_tree
                hash. The next sync will auto-clear this entry and re-attempt the render.
              </div>
            </li>
          ))}
        </ul>
      )}
    </Shell>
  );
}

function pillStyle(active: boolean): React.CSSProperties {
  return {
    padding: "6px 14px",
    background: active ? "rgba(80, 180, 255, 0.15)" : "transparent",
    border: `1px solid ${active ? "rgba(80, 180, 255, 0.5)" : "rgba(255,255,255,0.15)"}`,
    color: active ? "rgba(150, 200, 255, 1)" : "rgba(255,255,255,0.6)",
    borderRadius: 6,
    cursor: "pointer",
    fontSize: 13,
  };
}

const emptyState: React.CSSProperties = {
  padding: 40,
  textAlign: "center",
  color: "rgba(255,255,255,0.4)",
  background: "rgba(255,255,255,0.02)",
  border: "1px dashed rgba(255,255,255,0.1)",
  borderRadius: 8,
};
