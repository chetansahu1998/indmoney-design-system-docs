/**
 * lib/links.ts — single source of truth for external URLs referenced from
 * marketing/onboarding/footer surfaces.
 *
 * S18 (audit) — moved the hardcoded GitHub-issues URL out of
 * app/onboarding/page.tsx so it lives in one place and is easy to swap
 * (e.g. when the public repo URL changes, or when the team moves to
 * Linear / a different issue tracker).
 *
 * Keep this file intentionally tiny: only links that appear in *user-facing*
 * copy belong here. API base URLs, MCP endpoints, etc. live in their own
 * config modules.
 */

export const EXTERNAL_LINKS = {
  /** Public GitHub repo issue tracker — surfaced from /onboarding footer. */
  githubIssues: "https://github.com/indmoney/design-system-docs/issues",
} as const;

export type ExternalLinkKey = keyof typeof EXTERNAL_LINKS;
