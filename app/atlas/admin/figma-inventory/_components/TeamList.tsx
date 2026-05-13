"use client";

/**
 * TeamList — Phase 2A U2.
 *
 * Lists the tenant's seeded teams. Lets the admin add a new team (by
 * Figma team_id + display name) or soft-disable an existing seed. The
 * server triggers an immediate sync after an add via Phase 1's existing
 * POST handler; we re-fetch the list once the next run row lands.
 *
 * Selection is purely UI state — clicking a row updates the parent
 * page's selectedTeamID so the tree fetches that team.
 */

import { useCallback, useEffect, useMemo, useState } from "react";

import { adminFetchJSON } from "@/app/atlas/admin/_lib/adminFetch";
import { useAuth } from "@/lib/auth-client";

import type {
  AddTeamRequest,
  FigmaTeamSeed,
  ListTeamsResponse,
} from "../types";

interface Props {
  selectedTeamID: string;
  onSelectTeam: (teamID: string) => void;
}

export function TeamList({ selectedTeamID, onSelectTeam }: Props) {
  const token = useAuth((s) => s.token);
  const [teams, setTeams] = useState<FigmaTeamSeed[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string>("");
  const [showAdd, setShowAdd] = useState(false);
  const [addBusy, setAddBusy] = useState(false);
  const [addForm, setAddForm] = useState<AddTeamRequest>({ team_id: "", team_name: "" });
  const [addErr, setAddErr] = useState<string>("");

  const load = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    setErr("");
    try {
      const res = await adminFetchJSON<ListTeamsResponse>("/v1/admin/figma-inventory/teams");
      setTeams(res.teams || []);
      // Auto-select the first enabled team if none is selected yet.
      if (!selectedTeamID && res.teams) {
        const firstEnabled = res.teams.find((t) => t.enabled);
        if (firstEnabled) onSelectTeam(firstEnabled.team_id);
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [token, selectedTeamID, onSelectTeam]);

  useEffect(() => {
    void load();
  }, [load]);

  // Refresh teams + last-crawl status periodically so badges stay fresh
  // when a poll cycle finishes in the background.
  useEffect(() => {
    if (!token) return;
    const t = setInterval(() => {
      void load();
    }, 15_000);
    return () => clearInterval(t);
  }, [token, load]);

  async function submitAdd(e: React.FormEvent) {
    e.preventDefault();
    if (!addForm.team_id.trim() || !addForm.team_name.trim()) {
      setAddErr("team_id and team_name are required");
      return;
    }
    setAddBusy(true);
    setAddErr("");
    try {
      await adminFetchJSON("/v1/admin/figma-inventory/teams", {
        method: "POST",
        body: addForm,
      });
      setShowAdd(false);
      setAddForm({ team_id: "", team_name: "" });
      // Server triggered a sync; pull the new row so the user sees it
      // immediately + the "Syncing…" badge while last_crawl_at is empty.
      await load();
      onSelectTeam(addForm.team_id);
    } catch (e) {
      setAddErr(e instanceof Error ? e.message : String(e));
    } finally {
      setAddBusy(false);
    }
  }

  async function disable(teamID: string) {
    if (!window.confirm("Disable this team? Crawled data is preserved.")) return;
    try {
      await adminFetchJSON(`/v1/admin/figma-inventory/teams/${encodeURIComponent(teamID)}`, {
        method: "DELETE",
      });
      await load();
      if (selectedTeamID === teamID) onSelectTeam("");
    } catch (e) {
      window.alert(`Failed to disable: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  const sorted = useMemo(
    () => [...teams].sort((a, b) => Number(b.enabled) - Number(a.enabled) || a.team_name.localeCompare(b.team_name)),
    [teams],
  );

  return (
    <div className="tl-root">
      <div className="tl-head">
        <h2>Teams</h2>
        <button type="button" className="tl-add-btn" onClick={() => setShowAdd((v) => !v)}>
          {showAdd ? "Cancel" : "+ Add team"}
        </button>
      </div>

      {showAdd && (
        <form onSubmit={submitAdd} className="tl-add-form">
          <label>
            <span>Team ID</span>
            <input
              type="text"
              value={addForm.team_id}
              onChange={(e) => setAddForm({ ...addForm, team_id: e.target.value.trim() })}
              placeholder="898419887480849435"
              autoFocus
            />
          </label>
          <label>
            <span>Team name</span>
            <input
              type="text"
              value={addForm.team_name}
              onChange={(e) => setAddForm({ ...addForm, team_name: e.target.value })}
              placeholder="INDmoney"
            />
          </label>
          <p className="tl-hint">
            Paste from a Figma URL — the number after <code>/team/</code> in{" "}
            <code>figma.com/files/team/&lt;id&gt;/…</code>
          </p>
          {addErr && <p className="tl-err">{addErr}</p>}
          <button type="submit" disabled={addBusy} className="tl-submit-btn">
            {addBusy ? "Adding…" : "Add team + sync"}
          </button>
        </form>
      )}

      {loading && teams.length === 0 && <p className="tl-loading">Loading teams…</p>}
      {err && <p className="tl-err">Failed to load: {err}</p>}
      {!loading && teams.length === 0 && !err && (
        <p className="tl-empty">
          No teams seeded yet. Paste a Figma team id above to start mirroring.
        </p>
      )}

      <ul className="tl-list">
        {sorted.map((t) => {
          const isActive = t.team_id === selectedTeamID;
          const statusBadge = teamStatusBadge(t);
          return (
            <li
              key={t.team_id}
              className={`tl-item${isActive ? " active" : ""}${t.enabled ? "" : " disabled"}`}
            >
              <button
                type="button"
                className="tl-row-btn"
                onClick={() => t.enabled && onSelectTeam(t.team_id)}
                title={t.enabled ? "Select team" : "Disabled — crawled data is preserved but no new fetches"}
              >
                <span className="tl-name">{t.team_name}</span>
                <span className="tl-id" title={t.team_id}>
                  {t.team_id}
                </span>
                {statusBadge}
              </button>
              {t.enabled && (
                <button
                  type="button"
                  className="tl-disable-btn"
                  onClick={() => disable(t.team_id)}
                  aria-label={`Disable ${t.team_name}`}
                  title="Disable team (soft — data preserved)"
                >
                  ×
                </button>
              )}
            </li>
          );
        })}
      </ul>

      <style jsx>{`
        .tl-root {
          display: flex;
          flex-direction: column;
          gap: 12px;
        }
        .tl-head {
          display: flex;
          align-items: center;
          justify-content: space-between;
        }
        .tl-head h2 {
          margin: 0;
          font-size: 14px;
          font-weight: 600;
          text-transform: uppercase;
          letter-spacing: 0.08em;
          color: var(--text-2, #888);
        }
        .tl-add-btn,
        .tl-disable-btn,
        .tl-submit-btn,
        .tl-row-btn {
          background: transparent;
          border: 1px solid var(--border, rgba(255, 255, 255, 0.12));
          color: var(--text-1, #eee);
          font: inherit;
          cursor: pointer;
          border-radius: 6px;
          padding: 4px 10px;
        }
        .tl-add-btn:hover,
        .tl-submit-btn:hover {
          background: var(--surface-2, rgba(255, 255, 255, 0.04));
        }
        .tl-submit-btn:disabled {
          opacity: 0.5;
          cursor: wait;
        }
        .tl-add-form {
          display: flex;
          flex-direction: column;
          gap: 10px;
          padding: 12px;
          background: var(--surface-2, rgba(255, 255, 255, 0.04));
          border-radius: 8px;
        }
        .tl-add-form label {
          display: flex;
          flex-direction: column;
          gap: 4px;
          font-size: 12px;
        }
        .tl-add-form label span {
          color: var(--text-2, #aaa);
          text-transform: uppercase;
          letter-spacing: 0.06em;
        }
        .tl-add-form input {
          background: var(--bg, #0b0b0c);
          border: 1px solid var(--border, rgba(255, 255, 255, 0.12));
          color: var(--text-1, #eee);
          border-radius: 6px;
          padding: 6px 8px;
          font: inherit;
        }
        .tl-hint {
          font-size: 11px;
          color: var(--text-3, #777);
          margin: 0;
        }
        .tl-hint code {
          background: rgba(255, 255, 255, 0.06);
          padding: 1px 4px;
          border-radius: 3px;
          font-size: 10px;
        }
        .tl-list {
          display: flex;
          flex-direction: column;
          gap: 4px;
          margin: 0;
          padding: 0;
          list-style: none;
        }
        .tl-item {
          display: flex;
          align-items: center;
          gap: 4px;
        }
        .tl-item.disabled {
          opacity: 0.5;
        }
        .tl-row-btn {
          flex: 1;
          display: flex;
          flex-direction: column;
          align-items: flex-start;
          gap: 2px;
          padding: 8px 12px;
          text-align: left;
        }
        .tl-row-btn:hover {
          background: var(--surface-2, rgba(255, 255, 255, 0.04));
        }
        .tl-item.active .tl-row-btn {
          background: var(--accent-dim, rgba(99, 102, 241, 0.15));
          border-color: var(--accent, #6366f1);
        }
        .tl-name {
          font-weight: 500;
          font-size: 13px;
        }
        .tl-id {
          font-size: 10px;
          font-family: ui-monospace, monospace;
          color: var(--text-3, #777);
        }
        .tl-disable-btn {
          width: 28px;
          padding: 0;
          font-size: 16px;
          line-height: 1;
        }
        .tl-disable-btn:hover {
          background: var(--danger-dim, rgba(239, 68, 68, 0.15));
          border-color: var(--danger, #ef4444);
          color: var(--danger, #ef4444);
        }
        .tl-loading,
        .tl-empty,
        .tl-err {
          font-size: 12px;
          color: var(--text-3, #777);
          margin: 0;
        }
        .tl-err {
          color: var(--danger, #ef4444);
        }
        .tl-badge {
          display: inline-block;
          margin-top: 2px;
          font-size: 10px;
          padding: 1px 6px;
          border-radius: 999px;
          text-transform: uppercase;
          letter-spacing: 0.06em;
          font-weight: 600;
        }
        .tl-badge-ok {
          background: rgba(34, 197, 94, 0.15);
          color: #4ade80;
        }
        .tl-badge-pending {
          background: rgba(99, 102, 241, 0.15);
          color: #a5b4fc;
        }
        .tl-badge-yellow {
          background: rgba(234, 179, 8, 0.15);
          color: #fde047;
        }
        .tl-badge-red {
          background: rgba(239, 68, 68, 0.15);
          color: #fca5a5;
        }
        .tl-badge-off {
          background: rgba(255, 255, 255, 0.04);
          color: var(--text-3, #777);
        }
      `}</style>
    </div>
  );
}

function teamStatusBadge(t: FigmaTeamSeed) {
  if (!t.enabled) return <span className="tl-badge tl-badge-off">disabled</span>;
  const status = (t.last_crawl_status || "").toLowerCase();
  if (status === "ok") return <span className="tl-badge tl-badge-ok" title={t.last_crawl_at}>ok</span>;
  if (status === "forbidden") return <span className="tl-badge tl-badge-red" title="403 from Figma — PAT lacks access to this team">forbidden</span>;
  if (status === "error") return <span className="tl-badge tl-badge-yellow" title={t.last_crawl_error || "see logs"}>error</span>;
  return <span className="tl-badge tl-badge-pending">syncing…</span>;
}
