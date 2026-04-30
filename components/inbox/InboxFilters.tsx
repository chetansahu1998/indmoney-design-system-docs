"use client";

/**
 * Filter chip bar for /inbox. Severity + category chips ship in U5; the
 * full filter UI (rule_id, persona, mode, project, date range) lands as
 * narrow inputs alongside the chips. Keeping them lightweight on
 * mobile-first design — chips wrap to multiple rows; dropdowns sit
 * inline.
 */

import type { InboxFilters as Filters } from "@/lib/inbox/client";

const SEVERITIES: Array<{
  id: "critical" | "high" | "medium" | "low" | "info";
  label: string;
  tint: string;
}> = [
  { id: "critical", label: "Critical", tint: "#dc2626" },
  { id: "high", label: "High", tint: "#ea580c" },
  { id: "medium", label: "Medium", tint: "#ca8a04" },
  { id: "low", label: "Low", tint: "#2563eb" },
  { id: "info", label: "Info", tint: "#64748b" },
];

const CATEGORIES: Array<{ id: string; label: string }> = [
  { id: "theme_parity", label: "Theme parity" },
  { id: "cross_persona", label: "Cross persona" },
  { id: "a11y_contrast", label: "A11y contrast" },
  { id: "a11y_touch_target", label: "Touch target" },
  { id: "spacing_drift", label: "Spacing" },
  { id: "radius_drift", label: "Radius" },
  { id: "text_style_drift", label: "Text style" },
  { id: "token_drift", label: "Token drift" },
  { id: "component_governance", label: "Components" },
];

interface Props {
  filters: Filters;
  onChange: (next: Filters) => void;
  totalMatching: number;
  loading: boolean;
}

export default function InboxFilters({
  filters,
  onChange,
  totalMatching,
  loading,
}: Props) {
  const activeSeverities = new Set(filters.severity ?? []);

  const toggleSeverity = (id: string) => {
    const next = new Set(activeSeverities);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    onChange({
      ...filters,
      severity: next.size > 0 ? Array.from(next) : undefined,
      offset: 0,
    });
  };

  const setCategory = (id?: string) => {
    onChange({ ...filters, category: id, offset: 0 });
  };

  const setRuleID = (value: string) => {
    onChange({ ...filters, rule_id: value || undefined, offset: 0 });
  };

  const reset = () => {
    onChange({});
  };

  const filtersActive =
    !!filters.rule_id ||
    !!filters.category ||
    !!filters.persona_id ||
    !!filters.mode ||
    !!filters.project_id ||
    !!filters.date_from ||
    !!filters.date_to ||
    (filters.severity?.length ?? 0) > 0;

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 12,
        padding: "16px 0",
        borderBottom: "1px solid var(--border)",
      }}
    >
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
        {SEVERITIES.map((s) => {
          const active = activeSeverities.has(s.id);
          return (
            <button
              key={s.id}
              type="button"
              onClick={() => toggleSeverity(s.id)}
              style={{
                padding: "4px 10px",
                fontSize: 11,
                fontFamily: "var(--font-mono)",
                borderRadius: 999,
                border: `1px solid ${active ? s.tint : "var(--border)"}`,
                background: active ? `${s.tint}1a` : "transparent",
                color: active ? s.tint : "var(--text-2)",
                cursor: "pointer",
                transition: "background 150ms ease, border-color 150ms ease",
              }}
              aria-pressed={active}
            >
              {s.label}
            </button>
          );
        })}
      </div>

      <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
        <button
          type="button"
          onClick={() => setCategory(undefined)}
          style={chipStyle(!filters.category)}
          aria-pressed={!filters.category}
        >
          All categories
        </button>
        {CATEGORIES.map((c) => (
          <button
            key={c.id}
            type="button"
            onClick={() => setCategory(c.id)}
            style={chipStyle(filters.category === c.id)}
            aria-pressed={filters.category === c.id}
          >
            {c.label}
          </button>
        ))}
      </div>

      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 12,
          flexWrap: "wrap",
        }}
      >
        <label
          style={{
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
            display: "flex",
            alignItems: "center",
            gap: 6,
          }}
        >
          rule_id:
          <input
            type="text"
            value={filters.rule_id ?? ""}
            onChange={(e) => setRuleID(e.target.value)}
            placeholder="theme_parity.fill"
            style={{
              fontSize: 11,
              fontFamily: "var(--font-mono)",
              padding: "4px 8px",
              border: "1px solid var(--border)",
              borderRadius: 4,
              background: "var(--bg-surface)",
              color: "var(--text-1)",
              width: 220,
            }}
          />
        </label>

        {filtersActive && (
          <button
            type="button"
            onClick={reset}
            style={{
              fontSize: 11,
              fontFamily: "var(--font-mono)",
              color: "var(--accent)",
              background: "transparent",
              border: "none",
              cursor: "pointer",
              textDecoration: "underline",
            }}
          >
            reset filters
          </button>
        )}

        <span
          style={{
            marginLeft: "auto",
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          {loading ? "loading…" : `${totalMatching} match${totalMatching === 1 ? "" : "es"}`}
        </span>
      </div>
    </div>
  );
}

function chipStyle(active: boolean): React.CSSProperties {
  return {
    padding: "4px 10px",
    fontSize: 11,
    fontFamily: "var(--font-mono)",
    borderRadius: 999,
    border: `1px solid ${active ? "var(--accent)" : "var(--border)"}`,
    background: active ? "var(--accent)" : "transparent",
    color: active ? "var(--bg-base, #fff)" : "var(--text-2)",
    cursor: "pointer",
    transition: "background 150ms ease, border-color 150ms ease",
  };
}
