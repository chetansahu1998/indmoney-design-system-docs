"use client";

/**
 * Phase 7.5 / U4 — persona library approval queue.
 *
 * Lists every status='pending' persona that designers have suggested via
 * the plugin export. DS leads approve (flips status='approved') or reject
 * (soft-deletes via deleted_at).
 *
 * Calls:
 *   GET  /v1/atlas/admin/personas/pending
 *   POST /v1/atlas/admin/personas/{id}/approve
 *   POST /v1/atlas/admin/personas/{id}/reject
 */

import { useEffect, useState } from "react";

import { AdminShell } from "../_lib/AdminShell";
import { adminFetchJSON } from "../_lib/adminFetch";

interface PendingPersona {
  id: string;
  name: string;
  created_by_user_id: string;
  created_by_email?: string;
  created_at: string;
}

export default function AdminPersonasPage() {
  const [rows, setRows] = useState<PendingPersona[]>([]);
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState<string | null>(null);
  const [actingID, setActingID] = useState<string | null>(null);

  async function load() {
    setStatus("loading");
    try {
      const body = await adminFetchJSON<{ personas: PendingPersona[] }>(
        "/v1/atlas/admin/personas/pending",
      );
      setRows(body.personas ?? []);
      setStatus("ready");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus("error");
    }
  }

  useEffect(() => {
    void load();
  }, []);

  async function act(id: string, action: "approve" | "reject") {
    setActingID(id);
    try {
      await adminFetchJSON(`/v1/atlas/admin/personas/${encodeURIComponent(id)}/${action}`, {
        method: "POST",
      });
      // Optimistically drop the row out of the queue.
      setRows((rs) => rs.filter((r) => r.id !== id));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setActingID(null);
    }
  }

  return (
    <AdminShell
      title="Persona approval queue"
      description="Designer-suggested personas awaiting your review. Approval makes the persona available across every project; rejection soft-deletes it (keeps an audit trail). Both actions are tenant-wide — personas are an org-wide library."
    >
      {status === "loading" && <div className="msg">Loading queue…</div>}
      {status === "error" && (
        <div className="msg err">
          Couldn&apos;t load: {error}.{" "}
          <button onClick={() => void load()}>Retry</button>
        </div>
      )}
      {status === "ready" && rows.length === 0 && (
        <div className="msg empty">
          <strong>Queue clear.</strong> No personas are awaiting approval. New
          suggestions appear here automatically when designers export with a
          new persona name.
        </div>
      )}
      {status === "ready" && rows.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Persona</th>
              <th>Suggested by</th>
              <th>Submitted</th>
              <th aria-label="Actions"></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id} className={actingID === r.id ? "acting" : ""}>
                <td>
                  <div className="name">{r.name}</div>
                  <code className="id">{r.id}</code>
                </td>
                <td>{r.created_by_email || r.created_by_user_id}</td>
                <td>{formatRelative(r.created_at)}</td>
                <td>
                  <div className="actions">
                    <button
                      className="approve"
                      onClick={() => void act(r.id, "approve")}
                      disabled={actingID !== null}
                    >
                      Approve
                    </button>
                    <button
                      className="reject"
                      onClick={() => void act(r.id, "reject")}
                      disabled={actingID !== null}
                    >
                      Reject
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <style jsx>{`
        .msg {
          padding: 16px;
          color: var(--text-3);
        }
        .msg.empty {
          padding: 32px;
          text-align: center;
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          border: 1px dashed var(--border);
          border-radius: 12px;
        }
        .msg.empty strong {
          color: var(--text-1);
          display: block;
          margin-bottom: 4px;
        }
        .msg.err {
          color: #ffb347;
        }
        .msg.err button {
          margin-left: 8px;
          padding: 4px 10px;
          border: 1px solid var(--border);
          border-radius: 6px;
          background: transparent;
          color: inherit;
          cursor: pointer;
        }
        table {
          width: 100%;
          border-collapse: collapse;
          font-size: 13px;
        }
        thead th {
          text-align: left;
          padding: 8px 12px;
          color: var(--text-3);
          font-weight: 500;
          font-size: 11px;
          letter-spacing: 0.04em;
          text-transform: uppercase;
          border-bottom: 1px solid var(--border);
        }
        tbody td {
          padding: 12px;
          border-bottom: 1px solid var(--border, rgba(255, 255, 255, 0.04));
          vertical-align: middle;
        }
        tbody tr.acting {
          opacity: 0.5;
        }
        .name {
          font-weight: 600;
          margin-bottom: 2px;
        }
        .id {
          font-family: var(--font-mono, ui-monospace, monospace);
          font-size: 10px;
          color: var(--text-3);
        }
        .actions {
          display: flex;
          gap: 8px;
          justify-content: flex-end;
        }
        .actions button {
          padding: 6px 14px;
          border-radius: 999px;
          font-size: 12px;
          font-weight: 600;
          cursor: pointer;
        }
        .approve {
          background: var(--accent, #7b9fff);
          color: var(--bg);
          border: none;
        }
        .reject {
          background: transparent;
          color: var(--text-2);
          border: 1px solid var(--border);
        }
        button:disabled {
          opacity: 0.5;
          cursor: not-allowed;
        }
      `}</style>
    </AdminShell>
  );
}

function formatRelative(ts: string): string {
  try {
    const d = new Date(ts);
    const diff = Date.now() - d.getTime();
    const sec = Math.round(diff / 1000);
    if (sec < 60) return `${sec}s ago`;
    const min = Math.round(sec / 60);
    if (min < 60) return `${min}m ago`;
    const hr = Math.round(min / 60);
    if (hr < 24) return `${hr}h ago`;
    const day = Math.round(hr / 24);
    return `${day}d ago`;
  } catch {
    return ts;
  }
}
