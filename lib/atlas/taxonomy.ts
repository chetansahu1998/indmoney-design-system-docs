/**
 * lib/atlas/taxonomy.ts — anatomy of the brain graph + sub-sheet → product
 * → lobe mapping.
 *
 * Six anatomical lobes, each anchored to a fixed region of the brain
 * silhouette. Lobes are *geometric*: re-ordering or renaming changes
 * where nodes land on screen, so keep these stable. Adding a new lobe
 * means picking a free anatomical region and reserving an area in
 * `app/atlas/_lib/atlas.tsx` (the `inBrain` / lobe-position map).
 *
 * The data model is:
 *   Sub-sheet (Google Sheet tab) → Product (DB project row) → Lobe (here)
 *
 * Sub-sheets known today are mapped explicitly. New sub-sheets that
 * appear in future syncs land in their explicit lobe via `subSheetToLobe`,
 * else fall through to "platform". This keeps the taxonomy authored —
 * unknown products are visible (Platform lobe) instead of silently
 * dropped.
 */

import type { Domain, LobeID } from "./types";

/** The six anatomical lobes shown on the brain silhouette. */
export const ATLAS_DOMAINS: Domain[] = [
  { id: "markets",            label: "Markets",            sub: "Trade & invest",       lobe: "frontalL" },
  { id: "money_matters",      label: "Money Matters",      sub: "Pay, save, plan",      lobe: "frontalR" },
  { id: "platform",           label: "Platform",           sub: "Identity & system",    lobe: "parietalL" },
  { id: "lending",            label: "Lending",            sub: "Borrow & repay",       lobe: "parietalR" },
  { id: "recurring_payments", label: "Recurring Payments", sub: "Bills & cards",        lobe: "temporal"  },
  { id: "web_platform",       label: "Web Platform",       sub: "Web touch-points",     lobe: "occipital" },
];

/** O(1) lookup. */
export const ATLAS_DOMAINS_BY_ID: Readonly<Record<string, Domain>> = Object.freeze(
  Object.fromEntries(ATLAS_DOMAINS.map((d) => [d.id, d])),
);

export const ATLAS_DOMAINS_BY_LOBE: Readonly<Record<LobeID, Domain | undefined>> = Object.freeze(
  ATLAS_DOMAINS.reduce(
    (acc, d) => { acc[d.lobe] = d; return acc; },
    {} as Record<LobeID, Domain | undefined>,
  ),
);

/**
 * Sub-sheet (Google Sheet tab name) → lobe ID.
 *
 * Source of truth for the brain layout. Hand-authored, matches the
 * spreadsheet `Design <> InD` tab structure. Unknown tab names fall
 * through to the default lobe (Platform) so they're visible until
 * curated. Names are matched case-insensitively after stripping
 * punctuation/whitespace, so the sheet's typos ("Onbording KYC",
 * "Platfrom & global serach") still resolve.
 */
const SUB_SHEET_LOBES: ReadonlyArray<{ pattern: RegExp; lobe: string; product?: string }> = [
  // ── Markets
  { pattern: /^indstocks?$/i,                                 lobe: "markets",            product: "INDstocks" },
  { pattern: /^mutual\s*funds?$/i,                            lobe: "markets",            product: "Mutual Funds" },
  { pattern: /^us\s*stocks?$/i,                               lobe: "markets",            product: "US Stocks" },
  { pattern: /^fixed\s*deposits?$/i,                          lobe: "markets",            product: "Fixed Deposit" },
  { pattern: /^f&?o$/i,                                       lobe: "markets",            product: "F&O" },

  // ── Recurring Payments
  { pattern: /^bbps\s*&?\s*tpap$/i,                           lobe: "recurring_payments", product: "BBPS & TPAP" },
  { pattern: /^credit\s*cards?$/i,                            lobe: "recurring_payments", product: "Credit Card" },

  // ── Lending
  { pattern: /^insta\s*cash$/i,                               lobe: "lending",            product: "Insta Cash" },
  { pattern: /^insta\s*plus$/i,                               lobe: "lending",            product: "Insta Plus" },
  { pattern: /^nbfc$/i,                                       lobe: "lending",            product: "NBFC" },

  // ── Money Matters
  { pattern: /^plutus$/i,                                     lobe: "money_matters",      product: "Plutus" },
  { pattern: /^neo\s*banking$/i,                              lobe: "money_matters",      product: "Neobanking" },
  { pattern: /^insurance$/i,                                  lobe: "money_matters",      product: "Insurance" },
  { pattern: /^goals?$/i,                                     lobe: "money_matters",      product: "Goals" },

  // ── Platform
  { pattern: /^onb?o?r?ding\s*kyc$/i,                         lobe: "platform",           product: "Onboarding KYC" },
  { pattern: /^pl[a-z]+m\s*&?\s*global\s*s[ea]?[ea]?rch$/i,   lobe: "platform",           product: "Platform & Global Search" },
  { pattern: /^referral$/i,                                   lobe: "platform",           product: "Referral" },
  { pattern: /^chatbot$/i,                                    lobe: "platform",           product: "Chatbot" },
  { pattern: /^social\s*project$/i,                           lobe: "platform",           product: "Social Project" },
  { pattern: /^ind\s*learn$/i,                                lobe: "platform",           product: "INDlearn" },

  // ── Web Platform
  { pattern: /^ind\s*web$/i,                                  lobe: "web_platform",       product: "INDWeb" },
];

/** Tabs to skip entirely (master rolls-ups, design assets, animations). */
const SKIP_TABS: ReadonlyArray<RegExp> = [
  /^product\s*design$/i, // master roll-up — would double-count
  /^lotties$/i,          // animation assets, not product UX
  // Illustrations is KEPT but stays out of the product brain — fetched separately.
  /^illustrations$/i,
];

export interface SubSheetMapping {
  /** True when the sync should ignore this tab entirely. */
  skip: boolean;
  /** Resolved lobe ID. Defaults to "platform" for unknown tabs. */
  lobe: string;
  /** Canonical product name; falls back to the raw tab name. */
  product: string;
  /** Reason the mapping resolved this way (for logging). */
  source: "explicit" | "default" | "skip";
}

/**
 * Resolve a Google Sheet tab name to its lobe + product. Unknown tabs
 * default to Platform so they appear on the brain immediately and can
 * be re-classified later.
 */
export function subSheetToLobe(tabName: string): SubSheetMapping {
  const cleaned = (tabName ?? "").trim();
  for (const skip of SKIP_TABS) {
    if (skip.test(cleaned)) {
      return { skip: true, lobe: "platform", product: cleaned, source: "skip" };
    }
  }
  for (const rule of SUB_SHEET_LOBES) {
    if (rule.pattern.test(cleaned)) {
      return { skip: false, lobe: rule.lobe, product: rule.product ?? cleaned, source: "explicit" };
    }
  }
  return { skip: false, lobe: "platform", product: cleaned, source: "default" };
}

/**
 * `productToDomain` covers two paths:
 *   1. Sheet sync — pass the sub-sheet name ("INDstocks") → matches via
 *      subSheetToLobe.
 *   2. Plugin export — pass the project's product field ("Indian Stocks",
 *      "Mutual Funds", "Plutus") → fall through to a freeform pattern map
 *      that handles whitespace variants and common product names the
 *      sheet rules don't catch.
 */
const LEGACY_PRODUCT_PATTERNS: ReadonlyArray<{ test: RegExp; lobe: string }> = [
  // Markets
  { test: /\bindian\s*stocks?\b/i, lobe: "markets" },
  { test: /\bus\s*stocks?\b/i, lobe: "markets" },
  { test: /\bmutual\s*funds?\b/i, lobe: "markets" },
  { test: /\b(mf|sip|etf)\b/i, lobe: "markets" },
  { test: /\bf&?o\b/i, lobe: "markets" },
  { test: /\bfixed\s*deposits?\b/i, lobe: "markets" },
  { test: /\bipo\b/i, lobe: "markets" },
  { test: /\bbonds?\b/i, lobe: "markets" },
  { test: /\bsmallcase\b/i, lobe: "markets" },
  // Money Matters
  { test: /\bplutus\b/i, lobe: "money_matters" },
  { test: /\bneo\s*banking?\b/i, lobe: "money_matters" },
  { test: /\binsurance\b/i, lobe: "money_matters" },
  { test: /\bgoals?\b/i, lobe: "money_matters" },
  { test: /\b(tax|gains?)\b/i, lobe: "money_matters" },
  // Lending
  { test: /\binsta\s*cash\b/i, lobe: "lending" },
  { test: /\binsta\s*plus\b/i, lobe: "lending" },
  { test: /\bnbfc\b/i, lobe: "lending" },
  // Recurring Payments
  { test: /\bbbps\b/i, lobe: "recurring_payments" },
  { test: /\btpap\b/i, lobe: "recurring_payments" },
  { test: /\bcredit\s*cards?\b/i, lobe: "recurring_payments" },
  // Web Platform
  { test: /\bind\s*web\b/i, lobe: "web_platform" },
  // Platform — onboarding/auth/identity/settings/system
  { test: /\bonboarding\b/i, lobe: "platform" },
  { test: /\bkyc\b/i, lobe: "platform" },
  { test: /\bauth/i, lobe: "platform" },
  { test: /\b(referral|chatbot|search|watchlists?|settings|notifications?)\b/i, lobe: "platform" },
  { test: /\bind\s*learn\b/i, lobe: "platform" },
];

export function productToDomain(product: string | null | undefined): string {
  if (!product) return "platform";
  // Sheet-tab path first — exact-match patterns win.
  const m = subSheetToLobe(product);
  if (m.source !== "default") return m.lobe;
  // Legacy plugin product strings (free-text, with spaces).
  for (const rule of LEGACY_PRODUCT_PATTERNS) {
    if (rule.test.test(product)) return rule.lobe;
  }
  return "platform";
}

/**
 * Whether a brain node should render larger / glow more strongly.
 * Tuned so a healthy production brain marks ~25% of nodes as primary.
 */
export function isPrimaryFlow(screenCount: number): boolean {
  return screenCount >= 80;
}
