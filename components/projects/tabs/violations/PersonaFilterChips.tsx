"use client";

/**
 * PersonaFilterChips — Phase 6 U6.
 *
 * Multi-select chip row mirroring CategoryFilterChips' shape, but keyed by
 * persona ID. R14 ("filtered by active persona × theme") needs persona to
 * be a first-class filter dimension; today the only persona filtering
 * happens via the project toolbar's single-select dropdown which doesn't
 * compose with the audit narrative ("show me cross-persona violations
 * across these two personas").
 *
 * Default: empty selection = ALL personas (no filter). Toggling adds /
 * removes from the set. A trailing "Unassigned" pseudo-chip surfaces rows
 * with `PersonaID === null`, since otherwise those would be invisible to
 * any persona filter.
 *
 * Reduced-motion: chip transitions are CSS-only and short — the page-level
 * useReducedMotion gate is honored at the parent (ViolationsTab) for
 * row stagger / arrival flash; nothing here needs gating.
 */

import type { Persona } from "@/lib/projects/types";

/** Sentinel used in `selected` to represent "violations with no persona". */
export const NO_PERSONA = "__none__";

interface Props {
  /** Personas referenced by the current dataset (may include NO_PERSONA). */
  available: Set<string>;
  /** Lookup table: persona ID → display name. */
  personas: Persona[];
  /** Per-persona-id count for the "(N)" label. */
  counts: Map<string, number>;
  /** Currently selected chips; empty set = "all". */
  selected: Set<string>;
  onToggle: (personaID: string) => void;
  onClear: () => void;
}

export function PersonaFilterChips({
  available,
  personas,
  counts,
  selected,
  onToggle,
  onClear,
}: Props) {
  if (available.size === 0) return null;
  // Sort personas by name for stable rendering; NO_PERSONA sinks to the end.
  const personaByID = new Map(personas.map((p) => [p.ID, p]));
  const visible = Array.from(available).sort((a, b) => {
    if (a === NO_PERSONA) return 1;
    if (b === NO_PERSONA) return -1;
    const an = personaByID.get(a)?.Name ?? a;
    const bn = personaByID.get(b)?.Name ?? b;
    return an.localeCompare(bn);
  });
  return (
    <div
      role="group"
      aria-label="Filter violations by persona"
      data-filter="persona"
      style={containerStyle}
    >
      <span style={labelStyle}>Persona</span>
      {visible.map((id) => {
        const isSelected = selected.has(id);
        const label =
          id === NO_PERSONA ? "Unassigned" : personaByID.get(id)?.Name ?? id;
        return (
          <button
            key={id}
            type="button"
            onClick={() => onToggle(id)}
            aria-pressed={isSelected}
            data-persona-id={id}
            data-selected={isSelected}
            style={{
              ...chipStyle,
              borderColor: isSelected ? "var(--accent)" : "var(--border)",
              background: isSelected
                ? "color-mix(in oklab, var(--bg-surface) 92%, var(--accent) 8%)"
                : "var(--bg-surface)",
              color: isSelected ? "var(--text-1)" : "var(--text-2)",
            }}
          >
            <span>{label}</span>
            <span style={countStyle}>{counts.get(id) ?? 0}</span>
          </button>
        );
      })}
      {selected.size > 0 ? (
        <button
          type="button"
          onClick={onClear}
          style={clearStyle}
          aria-label="Clear all persona filters"
        >
          Clear
        </button>
      ) : null}
    </div>
  );
}

const containerStyle: React.CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  alignItems: "center",
  gap: 6,
  padding: "8px 12px",
  borderBottom: "1px solid var(--border)",
};

const labelStyle: React.CSSProperties = {
  fontSize: 10,
  textTransform: "uppercase",
  letterSpacing: 0.6,
  color: "var(--text-3)",
  fontFamily: "var(--font-mono)",
  marginRight: 4,
};

const chipStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: 6,
  padding: "5px 10px",
  border: "1px solid var(--border)",
  borderRadius: 999,
  fontSize: 11,
  fontFamily: "var(--font-mono)",
  cursor: "pointer",
  transition:
    "background 200ms cubic-bezier(0.34, 1.56, 0.64, 1), border-color 160ms ease",
};

const countStyle: React.CSSProperties = {
  fontSize: 10,
  color: "var(--text-3)",
  marginLeft: 2,
  fontVariantNumeric: "tabular-nums",
};

const clearStyle: React.CSSProperties = {
  background: "none",
  border: "none",
  color: "var(--accent)",
  cursor: "pointer",
  fontFamily: "inherit",
  fontSize: 11,
  textDecoration: "underline",
  marginLeft: 4,
};
