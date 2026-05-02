"use client";

/**
 * ThemeFilterChips — Phase 6 U6.
 *
 * Multi-select chip row for theme/mode filtering of violations. R14 calls
 * for "filtered by active persona × theme"; the persona axis lands in
 * PersonaFilterChips, this is the theme axis.
 *
 * Rationale for chips (vs. a tri-state toggle): a violation row carries
 * a `mode_label` that is one of `light`, `dark`, `default`, or
 * free-text. Free-text labels are rare (only synthetic flows use them),
 * but a chip row degrades gracefully — any label present in the dataset
 * gets a chip; the user picks any subset.
 *
 * Default: empty selection = ALL themes (no filter). Toggling adds /
 * removes from the set. Rows whose `ModeLabel` is null are surfaced via
 * a "Unspecified" pseudo-chip (NO_MODE sentinel).
 */

/** Sentinel used in `selected` to represent "violations with no mode label". */
export const NO_MODE = "__none__";

/** Canonical mode-label ordering: light → dark → default → others alpha. */
const KNOWN_MODES = ["light", "dark", "default"] as const;

interface Props {
  /** Mode labels referenced by the current dataset (may include NO_MODE). */
  available: Set<string>;
  /** Per-label count for the "(N)" suffix. */
  counts: Map<string, number>;
  /** Currently selected chips; empty set = "all". */
  selected: Set<string>;
  onToggle: (modeLabel: string) => void;
  onClear: () => void;
}

export function ThemeFilterChips({
  available,
  counts,
  selected,
  onToggle,
  onClear,
}: Props) {
  if (available.size === 0) return null;
  const visible = Array.from(available).sort((a, b) => {
    if (a === NO_MODE) return 1;
    if (b === NO_MODE) return -1;
    const ai = (KNOWN_MODES as readonly string[]).indexOf(a);
    const bi = (KNOWN_MODES as readonly string[]).indexOf(b);
    if (ai !== -1 && bi !== -1) return ai - bi;
    if (ai !== -1) return -1;
    if (bi !== -1) return 1;
    return a.localeCompare(b);
  });
  return (
    <div
      role="group"
      aria-label="Filter violations by theme"
      data-filter="theme"
      style={containerStyle}
    >
      <span style={labelStyle}>Theme</span>
      {visible.map((mode) => {
        const isSelected = selected.has(mode);
        const label = mode === NO_MODE ? "Unspecified" : capitalize(mode);
        return (
          <button
            key={mode}
            type="button"
            onClick={() => onToggle(mode)}
            aria-pressed={isSelected}
            data-mode-label={mode}
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
            <span style={countStyle}>{counts.get(mode) ?? 0}</span>
          </button>
        );
      })}
      {selected.size > 0 ? (
        <button
          type="button"
          onClick={onClear}
          style={clearStyle}
          aria-label="Clear all theme filters"
        >
          Clear
        </button>
      ) : null}
    </div>
  );
}

function capitalize(s: string): string {
  if (!s) return s;
  return s.charAt(0).toUpperCase() + s.slice(1);
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
