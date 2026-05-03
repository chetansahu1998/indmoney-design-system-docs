/**
 * lib/atlas/rules-registry.ts — display metadata for ds-service `rule_id`s.
 *
 * Violations come back from the server with a stable `rule_id` (e.g.
 * "color-token", "spacing-token", "a11y-contrast"). The leaf inspector wants
 * a human label, a category bucket, and (eventually) a doc URL. This file
 * is the lookup; missing rule_ids fall back to a humanized version of the id.
 *
 * Keep entries short. If you find yourself wanting per-rule logic, push it
 * into `data-adapters.ts` instead — this file should stay flat key→display.
 */

import type { ViolationCategory } from "../projects/types";

export interface RuleMeta {
  id: string;
  /** Display name shown in the violations list. */
  label: string;
  /** Filter chip category. Falls back to "token_drift". */
  category: ViolationCategory;
  /** Optional one-line explainer rendered on hover / in the row body. */
  blurb?: string;
  /** Optional docs URL surfaced in the inspector "Learn more" link. */
  docs?: string;
}

const RULES: ReadonlyArray<RuleMeta> = [
  // Tokens
  {
    id: "color-token",
    label: "Color must use a design token",
    category: "token_drift",
    blurb: "Layer fill or stroke uses a raw value instead of a design token.",
  },
  {
    id: "spacing-token",
    label: "Spacing must match a token",
    category: "spacing_drift",
    blurb: "Gap or padding doesn't snap to a 4-pt scale token.",
  },
  {
    id: "radius-token",
    label: "Radius must match a token",
    category: "radius_drift",
    blurb: "Corner radius doesn't match an approved token value.",
  },
  {
    id: "text-style",
    label: "Text style must use a token",
    category: "text_style_drift",
    blurb: "Font family / size / weight isn't bound to a text-style token.",
  },

  // A11y
  {
    id: "a11y-contrast",
    label: "Contrast below WCAG AA",
    category: "a11y_contrast",
    blurb: "Text-to-background contrast ratio is below 4.5 : 1 (AA).",
  },
  {
    id: "a11y-touch-target",
    label: "Touch target too small",
    category: "a11y_touch_target",
    blurb: "Interactive area is below the 44 × 44 minimum target size.",
  },

  // Component governance
  {
    id: "component-detached",
    label: "Detached from a library component",
    category: "component_governance",
    blurb: "Layer was once an instance but is now a standalone copy.",
  },
  {
    id: "component-override",
    label: "Override breaks component contract",
    category: "component_governance",
    blurb: "Local override changes a property the component owns.",
  },
  {
    id: "component-match",
    label: "Should be a library component",
    category: "component_match",
    blurb: "Pattern matches a published component but isn't using it.",
  },

  // Cross-mode / persona
  {
    id: "theme-parity",
    label: "Light/Dark theme parity broken",
    category: "theme_parity",
    blurb: "Layer differs between Light and Dark in a non-token-driven way.",
  },
  {
    id: "cross-persona",
    label: "Persona variant differs unexpectedly",
    category: "cross_persona",
    blurb: "Same screen renders inconsistently across personas.",
  },

  // Flow
  {
    id: "flow-graph",
    label: "Flow has dangling screen",
    category: "flow_graph",
    blurb: "A screen has no incoming navigation in the canonical flow graph.",
  },
];

const RULES_BY_ID: Readonly<Record<string, RuleMeta>> = Object.freeze(
  Object.fromEntries(RULES.map((r) => [r.id, r])),
);

/**
 * Look up a rule's display metadata. Always returns a value — unknown rules
 * get a humanized fallback so the UI never shows raw kebab-case to users.
 */
export function ruleMeta(ruleID: string): RuleMeta {
  const found = RULES_BY_ID[ruleID];
  if (found) return found;
  return {
    id: ruleID,
    label: humanize(ruleID),
    category: "token_drift",
  };
}

function humanize(id: string): string {
  return id
    .split(/[-_]/)
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

export const ALL_RULES: ReadonlyArray<RuleMeta> = RULES;
