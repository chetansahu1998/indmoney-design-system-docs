"use client";

/**
 * Phase 7.5 / U2 — rule catalog editor.
 *
 * Calls GET /v1/atlas/admin/rules to list every audit rule, then PATCH
 * /v1/atlas/admin/rules/{rule_id} to toggle `enabled` or change
 * `default_severity`. Super-admin gated server-side; the AdminShell
 * surfaces the 403 as a generic error.
 */

import { useEffect, useState } from "react";

import { AdminShell } from "../_lib/AdminShell";
import { adminFetchJSON } from "../_lib/adminFetch";

interface Rule {
  rule_id: string;
  name: string;
  description: string;
  category: string;
  default_severity: "critical" | "high" | "medium" | "low" | "info";
  enabled: boolean;
}

const SEVERITY_OPTIONS: Rule["default_severity"][] = [
  "critical",
  "high",
  "medium",
  "low",
  "info",
];

const SEVERITY_COLOR: Record<Rule["default_severity"], string> = {
  critical: "#FF6B6B",
  high: "#FFB347",
  medium: "#FFD93D",
  low: "#9F8FFF",
  info: "#7B9FFF",
};

export default function AdminRulesPage() {
  const [rules, setRules] = useState<Rule[]>([]);
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState<string | null>(null);
  const [savingID, setSavingID] = useState<string | null>(null);

  async function load() {
    setStatus("loading");
    try {
      const body = await adminFetchJSON<{ rules: Rule[] }>("/v1/atlas/admin/rules");
      setRules(body.rules ?? []);
      setStatus("ready");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus("error");
    }
  }

  useEffect(() => {
    void load();
  }, []);

  async function patch(ruleID: string, patch: Partial<Pick<Rule, "enabled" | "default_severity">>) {
    // Optimistic update — flip locally, revert on error.
    const prev = rules;
    setRules((rs) => rs.map((r) => (r.rule_id === ruleID ? { ...r, ...patch } : r)));
    setSavingID(ruleID);
    try {
      await adminFetchJSON(`/v1/atlas/admin/rules/${encodeURIComponent(ruleID)}`, {
        method: "PATCH",
        body: patch,
      });
    } catch (err) {
      setRules(prev);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSavingID(null);
    }
  }

  // Group by category for visual scanning.
  const grouped: Record<string, Rule[]> = {};
  for (const r of rules) {
    (grouped[r.category] ??= []).push(r);
  }

  return (
    <AdminShell
      title="Rule catalog"
      description="Toggle audit rules and adjust their default severity. Disabling a rule stops new violations being created — existing violations stay in place. Severity changes apply to the next audit run."
    >
      {status === "loading" && <div className="msg">Loading rules…</div>}
      {status === "error" && (
        <div className="msg err">
          Couldn&apos;t load rules: {error}.{" "}
          <button onClick={() => void load()}>Retry</button>
        </div>
      )}
      {status === "ready" && rules.length === 0 && (
        <div className="msg">No rules in the catalog. Run the seed migration?</div>
      )}
      {status === "ready" &&
        Object.keys(grouped)
          .sort()
          .map((cat) => (
            <section key={cat} className="cat">
              <h2>{cat}</h2>
              <table>
                <thead>
                  <tr>
                    <th>Rule</th>
                    <th>Severity</th>
                    <th>Enabled</th>
                  </tr>
                </thead>
                <tbody>
                  {grouped[cat].map((r) => (
                    <tr key={r.rule_id} className={savingID === r.rule_id ? "saving" : ""}>
                      <td>
                        <div className="rule-name">{r.name}</div>
                        <div className="rule-desc">{r.description}</div>
                        <code className="rule-id">{r.rule_id}</code>
                      </td>
                      <td>
                        <select
                          value={r.default_severity}
                          onChange={(e) =>
                            void patch(r.rule_id, {
                              default_severity: e.target.value as Rule["default_severity"],
                            })
                          }
                          style={{
                            color: SEVERITY_COLOR[r.default_severity],
                            borderColor: SEVERITY_COLOR[r.default_severity] + "55",
                          }}
                          aria-label={`Severity for ${r.name}`}
                        >
                          {SEVERITY_OPTIONS.map((s) => (
                            <option key={s} value={s}>
                              {s}
                            </option>
                          ))}
                        </select>
                      </td>
                      <td>
                        <label className="toggle">
                          <input
                            type="checkbox"
                            checked={r.enabled}
                            onChange={(e) => void patch(r.rule_id, { enabled: e.target.checked })}
                          />
                          <span>{r.enabled ? "On" : "Off"}</span>
                        </label>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </section>
          ))}
      <style jsx>{`
        .msg {
          padding: 16px;
          color: var(--text-3);
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
        .cat h2 {
          margin: 24px 0 12px;
          font-size: 13px;
          font-weight: 600;
          letter-spacing: 0.04em;
          text-transform: uppercase;
          color: var(--text-3);
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
          vertical-align: top;
        }
        tbody tr.saving {
          opacity: 0.6;
        }
        .rule-name {
          font-weight: 600;
          margin-bottom: 2px;
        }
        .rule-desc {
          color: var(--text-3);
          margin-bottom: 4px;
          max-width: 60ch;
        }
        .rule-id {
          font-family: var(--font-mono, ui-monospace, monospace);
          font-size: 10px;
          color: var(--text-3);
        }
        select {
          padding: 4px 8px;
          background: transparent;
          border: 1px solid;
          border-radius: 6px;
          font-family: inherit;
          font-size: 12px;
          font-weight: 600;
          text-transform: capitalize;
          cursor: pointer;
        }
        .toggle {
          display: inline-flex;
          align-items: center;
          gap: 8px;
          cursor: pointer;
          color: var(--text-2);
        }
        .toggle input {
          accent-color: var(--accent, #7b9fff);
        }
      `}</style>
    </AdminShell>
  );
}
