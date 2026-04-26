/**
 * Required-token contract.
 *
 * Every brand MUST resolve these semantic paths. The DTCG adapter writes a
 * `_contract_check` block on every sync; `scripts/check-token-contract.ts`
 * runs in CI and fails if any required path is missing.
 *
 * This is the load-bearing seam between Figma extraction and section components —
 * adding a new section (or removing an obsolete token) is a contract change.
 */

export const REQUIRED_SEMANTIC_PATHS = [
  // Text & icon — primary content
  "colour.text-n-icon.primary",
  "colour.text-n-icon.secondary",
  "colour.text-n-icon.tertiary",
  "colour.text-n-icon.muted",
  "colour.text-n-icon.action",
  "colour.text-n-icon.success",
  "colour.text-n-icon.warning",
  "colour.text-n-icon.error",
  "colour.text-n-icon.on-surface-bold",
  "colour.text-n-icon.on-surface-subtle",

  // Surface
  "colour.surface.primary",
  "colour.surface.secondary",
  "colour.surface.tertiary",
  "colour.surface.muted",
  "colour.surface.action-bold",
  "colour.surface.action-subtle",
  "colour.surface.success-subtle",
  "colour.surface.warning-subtle",
  "colour.surface.error-subtle",

  // Border
  "colour.border.primary",
  "colour.border.subtle",
  "colour.border.action",
  "colour.border.success",
  "colour.border.warning",
  "colour.border.error",
] as const;

export const REQUIRED_BASE_PATHS = [
  // Neutral palette presence (full ramp not required, but anchors must exist)
  "colour.neutral.white",
  "colour.neutral.black",
  "colour.grey.50",
  "colour.grey.500",
  "colour.grey.900",
] as const;

export type RequiredSemanticPath = (typeof REQUIRED_SEMANTIC_PATHS)[number];
export type RequiredBasePath = (typeof REQUIRED_BASE_PATHS)[number];
