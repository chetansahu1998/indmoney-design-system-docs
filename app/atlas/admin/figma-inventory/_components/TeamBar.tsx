"use client";

/**
 * TeamBar — slim horizontal row of team chips.
 *
 * Replaces the Phase 2A draft's full-height TeamList sidebar. Each team
 * is a chip; the active chip is highlighted. "+ Add team" inline pops
 * an add form. "..." menu per chip exposes disable.
 *
 * Backed by /v1/admin/figma-inventory/teams.
 */

import { useCallback, useEffect, useMemo, useState } from "react";

import { adminFetchJSON } from "@/app/atlas/admin/_lib/adminFetch";
import { useAuth } from "@/lib/auth-client";

import { GhostBtn, StatusBadge } from "../_lib/Table";
import type {
  AddTeamRequest,
  FigmaTeamSeed,
  ListTeamsResponse,
} from "../types";

interface Props {
  selectedTeamID: string;
  onSelectTeam: (teamID: string) => void;
}

export function TeamBar({ selectedTeamID, onSelectTeam }: Props) {
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
      if (!selectedTeamID) {
        const first = res.teams?.find((t) => t.enabled);
        if (first) onSelectTeam(first.team_id);
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

  // Refresh every 15 s so last_crawl_status stays fresh in the badge.
  useEffect(() => {
    if (!token) return;
    const t = setInterval(() => void load(), 15_000);
    return () => clearInterval(t);
  }, [token, load]);

  async function submitAdd(e: React.FormEvent) {
    e.preventDefault();
    const id = addForm.team_id.trim();
    const name = addForm.team_name.trim();
    if (!id || !name) {
      setAddErr("Both fields are required");
      return;
    }
    setAddBusy(true);
    setAddErr("");
    try {
      await adminFetchJSON("/v1/admin/figma-inventory/teams", { method: "POST", body: { team_id: id, team_name: name } });
      setShowAdd(false);
      setAddForm({ team_id: "", team_name: "" });
      await load();
      onSelectTeam(id);
    } catch (e) {
      setAddErr(e instanceof Error ? e.message : String(e));
    } finally {
      setAddBusy(false);
    }
  }

  async function disable(teamID: string, teamName: string) {
    if (!window.confirm(`Disable team "${teamName}"? Crawled data is preserved.`)) return;
    try {
      await adminFetchJSON(`/v1/admin/figma-inventory/teams/${encodeURIComponent(teamID)}`, { method: "DELETE" });
      await load();
      if (selectedTeamID === teamID) onSelectTeam("");
    } catch (e) {
      window.alert(`Failed: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  const enabled = useMemo(() => teams.filter((t) => t.enabled), [teams]);

  return (
    <section style={{ marginBottom: 16 }}>
      <div style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: 8 }}>
        {loading && teams.length === 0 && <span style={{ color: "var(--text-3, #888)", fontSize: 13 }}>Loading teams…</span>}
        {!loading && enabled.length === 0 && !showAdd && (
          <span style={{ color: "var(--text-3, #888)", fontSize: 13 }}>
            No teams seeded yet — paste a Figma team id to start.
          </span>
        )}
        {enabled.map((t) => {
          const isActive = t.team_id === selectedTeamID;
          return (
            <span
              key={t.team_id}
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: 6,
                padding: "5px 10px 5px 12px",
                background: isActive ? "var(--accent-soft, rgba(80, 180, 255, 0.15))" : "var(--bg-surface, rgba(255,255,255,0.03))",
                border: `1px solid ${isActive ? "var(--accent, #7b9fff)" : "var(--border, rgba(255,255,255,0.12))"}`,
                borderRadius: 999,
              }}
            >
              <button
                type="button"
                onClick={() => onSelectTeam(t.team_id)}
                title={`team_id ${t.team_id}`}
                style={{
                  background: "transparent",
                  border: "none",
                  color: isActive ? "var(--accent, #7b9fff)" : "var(--text-1, #f7f7f7)",
                  font: "inherit",
                  fontSize: 13,
                  cursor: "pointer",
                  padding: 0,
                }}
              >
                {t.team_name}
              </button>
              <CrawlBadge team={t} />
              <button
                type="button"
                onClick={() => disable(t.team_id, t.team_name)}
                title="Disable team (soft delete)"
                aria-label={`Disable ${t.team_name}`}
                style={{
                  background: "transparent",
                  border: "none",
                  color: "var(--text-3, #888)",
                  cursor: "pointer",
                  padding: "0 2px",
                  fontSize: 14,
                  lineHeight: 1,
                }}
              >
                ×
              </button>
            </span>
          );
        })}
        <GhostBtn onClick={() => setShowAdd((v) => !v)}>
          {showAdd ? "Cancel" : "+ Add team"}
        </GhostBtn>
        {err && (
          <span style={{ color: "var(--danger, #ef4444)", fontSize: 12, marginLeft: 8 }}>
            {err}
          </span>
        )}
      </div>

      {showAdd && (
        <form
          onSubmit={submitAdd}
          style={{
            marginTop: 12,
            display: "flex",
            gap: 8,
            alignItems: "flex-end",
            padding: 12,
            background: "var(--bg-surface, rgba(255,255,255,0.03))",
            border: "1px solid var(--border, rgba(255,255,255,0.08))",
            borderRadius: 8,
          }}
        >
          <Field label="Team ID">
            <input
              value={addForm.team_id}
              onChange={(e) => setAddForm({ ...addForm, team_id: e.target.value.trim() })}
              placeholder="898419887480849435"
              autoFocus
              style={inputStyle}
            />
          </Field>
          <Field label="Team name">
            <input
              value={addForm.team_name}
              onChange={(e) => setAddForm({ ...addForm, team_name: e.target.value })}
              placeholder="INDmoney"
              style={inputStyle}
            />
          </Field>
          <button
            type="submit"
            disabled={addBusy}
            style={{
              padding: "8px 16px",
              background: "var(--accent-soft, rgba(80, 180, 255, 0.15))",
              border: "1px solid var(--accent, #7b9fff)",
              color: "var(--accent, #7b9fff)",
              borderRadius: 6,
              fontSize: 13,
              cursor: addBusy ? "wait" : "pointer",
            }}
          >
            {addBusy ? "Adding…" : "Add team + sync"}
          </button>
          {addErr && <span style={{ color: "var(--danger, #ef4444)", fontSize: 12 }}>{addErr}</span>}
          <span style={{ marginLeft: "auto", color: "var(--text-3, #888)", fontSize: 11 }}>
            Paste the number after <code>/team/</code> in a Figma team URL.
          </span>
        </form>
      )}
    </section>
  );
}

function CrawlBadge({ team }: { team: FigmaTeamSeed }) {
  const status = (team.last_crawl_status || "").toLowerCase();
  if (status === "ok") return <StatusBadge kind="ok" title={team.last_crawl_at}>ok</StatusBadge>;
  if (status === "forbidden") return <StatusBadge kind="danger" title="403 from Figma — PAT lacks access to this team">forbidden</StatusBadge>;
  if (status === "error") return <StatusBadge kind="warn" title={team.last_crawl_error || "see logs"}>error</StatusBadge>;
  return <StatusBadge kind="pending">syncing…</StatusBadge>;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 11, color: "var(--text-2, #aaa)", textTransform: "uppercase", letterSpacing: "0.06em" }}>
      {label}
      {children}
    </label>
  );
}

const inputStyle: React.CSSProperties = {
  background: "var(--bg, #0b0b0c)",
  border: "1px solid var(--border, rgba(255,255,255,0.12))",
  color: "var(--text-1, #f7f7f7)",
  borderRadius: 6,
  padding: "6px 8px",
  font: "inherit",
  fontSize: 13,
  minWidth: 200,
  textTransform: "none",
  letterSpacing: "normal",
};
