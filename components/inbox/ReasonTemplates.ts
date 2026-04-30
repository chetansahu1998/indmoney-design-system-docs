"use client";

/**
 * Pre-canned reason templates for bulk Acknowledge / Dismiss.
 *
 * Phase 4 U5 ships an opinionated starter set; Phase 5 will dogfood-tune
 * the list. The "custom" template signals the reason field should accept
 * free-text input — keeping it as an option in the dropdown lets the
 * user start with a template and edit it.
 */

export interface ReasonTemplate {
  id: string;
  label: string;
  reason: string;
  appliesTo: ("acknowledge" | "dismiss")[];
}

export const REASON_TEMPLATES: ReasonTemplate[] = [
  {
    id: "deferred-v2",
    label: "Deferred to v2",
    reason:
      "Deferred to a v2 effort — tracked in the design backlog, not actionable in this release.",
    appliesTo: ["acknowledge"],
  },
  {
    id: "follow-up-fix",
    label: "Follow-up fix queued",
    reason:
      "Follow-up fix is already queued in the next sprint. Acknowledging to clear the inbox in the meantime.",
    appliesTo: ["acknowledge"],
  },
  {
    id: "design-tradeoff",
    label: "Intentional design tradeoff",
    reason:
      "Intentional design tradeoff approved by the DS lead. Documented in the DRD.",
    appliesTo: ["acknowledge", "dismiss"],
  },
  {
    id: "not-applicable",
    label: "Not applicable to this persona",
    reason:
      "Rule does not apply to this persona context (e.g., logged-out users don't trigger network errors).",
    appliesTo: ["dismiss"],
  },
  {
    id: "false-positive",
    label: "False positive",
    reason:
      "Audit rule misclassified this node — actual implementation matches the design system.",
    appliesTo: ["dismiss"],
  },
  {
    id: "external-component",
    label: "External / third-party component",
    reason:
      "Frame uses a third-party / vendor component outside our design-system contract.",
    appliesTo: ["dismiss"],
  },
  {
    id: "custom",
    label: "Custom reason…",
    reason: "",
    appliesTo: ["acknowledge", "dismiss"],
  },
];

export function templatesForAction(
  action: "acknowledge" | "dismiss",
): ReasonTemplate[] {
  return REASON_TEMPLATES.filter((t) => t.appliesTo.includes(action));
}
