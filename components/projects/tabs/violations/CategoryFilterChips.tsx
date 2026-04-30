"use client";

/**
 * CategoryFilterChips — Phase 2 U11.
 *
 * Multi-select chip row above the severity-grouped Violations list. Toggling
 * a chip filters the visible violations to that category (or set of
 * categories). Selected state persists in the URL search-param `vc=cat1,cat2`
 * so a reload preserves the filter and a shared link reproduces it.
 *
 * Design:
 *   - Default = no chips selected = show ALL categories (matches Phase 1
 *     behavior).
 *   - Single click toggles inclusion. Visually indicates selected via tinted
 *     background + bold border. Severity-tinted chip dot mirrors the
 *     ViolationsTab's existing severity dots.
 *   - Counts ride along: each chip shows "(N)" of how many violations match
 *     that category in the unfiltered set, so a designer sees scope before
 *     committing to a filter.
 *   - "Clear all" link appears when ≥1 chip is selected.
 *
 * Reduced motion: chip transitions short-circuit when prefers-reduced-motion
 * is set (handled at the tab level via the existing useReducedMotion hook;
 * this component just sets a CSS transition that gets overridden).
 */

import type { ViolationCategory } from "@/lib/projects/types";

const CATEGORY_LABEL: Record<ViolationCategory, string> = {
  theme_parity: "Theme parity",
  cross_persona: "Cross-persona",
  a11y_contrast: "Contrast",
  a11y_touch_target: "Touch target",
  flow_graph: "Flow graph",
  component_governance: "Components",
  token_drift: "Token drift",
  text_style_drift: "Text style",
  spacing_drift: "Spacing",
  radius_drift: "Radius",
  component_match: "Component match",
};

const CATEGORY_TINT: Record<ViolationCategory, string> = {
  theme_parity: "var(--danger)",
  cross_persona: "var(--warning)",
  a11y_contrast: "var(--info)",
  a11y_touch_target: "var(--info)",
  flow_graph: "var(--text-2)",
  component_governance: "var(--text-3)",
  token_drift: "var(--text-3)",
  text_style_drift: "var(--text-3)",
  spacing_drift: "var(--text-3)",
  radius_drift: "var(--text-3)",
  component_match: "var(--text-3)",
};

/** Canonical chip ordering — most actionable categories first. */
const CHIP_ORDER: readonly ViolationCategory[] = [
  "theme_parity",
  "cross_persona",
  "a11y_contrast",
  "a11y_touch_target",
  "flow_graph",
  "component_governance",
  "token_drift",
  "text_style_drift",
  "spacing_drift",
  "radius_drift",
  "component_match",
] as const;

interface Props {
  /** Categories present in the current dataset → which chips to render. */
  available: Set<ViolationCategory>;
  /** Per-category count for the "(N)" label. */
  counts: Map<ViolationCategory, number>;
  /** Currently selected chips; empty set = "all". */
  selected: Set<ViolationCategory>;
  onToggle: (cat: ViolationCategory) => void;
  onClear: () => void;
}

export function CategoryFilterChips({
  available,
  counts,
  selected,
  onToggle,
  onClear,
}: Props) {
  if (available.size === 0) return null;
  const visible = CHIP_ORDER.filter((c) => available.has(c));
  return (
    <div
      role="group"
      aria-label="Filter violations by category"
      style={containerStyle}
    >
      {visible.map((cat) => {
        const isSelected = selected.has(cat);
        return (
          <button
            key={cat}
            type="button"
            onClick={() => onToggle(cat)}
            aria-pressed={isSelected}
            data-category={cat}
            data-selected={isSelected}
            style={{
              ...chipStyle,
              borderColor: isSelected ? CATEGORY_TINT[cat] : "var(--border)",
              background: isSelected
                ? "color-mix(in oklab, var(--bg-surface) 92%, " + CATEGORY_TINT[cat] + " 8%)"
                : "var(--bg-surface)",
              color: isSelected ? "var(--text-1)" : "var(--text-2)",
            }}
          >
            <span
              aria-hidden
              style={{
                width: 6,
                height: 6,
                borderRadius: 999,
                background: CATEGORY_TINT[cat],
                flexShrink: 0,
              }}
            />
            <span>{CATEGORY_LABEL[cat]}</span>
            <span style={countStyle}>{counts.get(cat) ?? 0}</span>
          </button>
        );
      })}
      {selected.size > 0 ? (
        <button
          type="button"
          onClick={onClear}
          style={clearStyle}
          aria-label="Clear all category filters"
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
