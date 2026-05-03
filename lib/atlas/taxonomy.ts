/**
 * lib/atlas/taxonomy.ts — the four anatomical domains of the brain graph and
 * the project.product → domain mapping.
 *
 * The reference UI ships with four hand-tuned lobes (Markets, Investing,
 * Money, Platform). Lobe IDs are *geometric anchors*: the brain renderer
 * (atlas.tsx) maps `lobe: "frontalL"` to a precise region of the silhouette.
 * Re-ordering or renaming a lobe shifts where its nodes land on screen, so
 * keep these stable.
 *
 * `productToDomain()` is the only piece of policy here: each Figma project
 * carries a free-text `product` field (Plutus, Tax, Indian Stocks, …) which
 * is mapped to one of the four domains. Unknown products fall through to
 * "platform" — a deliberate choice so a new product type ships visibly
 * (under Platform/Identity & settings) instead of silently disappearing.
 */

import type { Domain, LobeID } from "./types";

/** The four anatomical lobes shown on the brain silhouette. */
export const ATLAS_DOMAINS: Domain[] = [
  { id: "markets", label: "Markets", sub: "Trade & invest", lobe: "frontalL" },
  { id: "investing", label: "Investing", sub: "Track & analyze", lobe: "parietalR" },
  { id: "money", label: "Money", sub: "Move & spend", lobe: "frontalR" },
  { id: "platform", label: "Platform", sub: "Identity & settings", lobe: "parietalL" },
];

/** O(1) lookup. */
export const ATLAS_DOMAINS_BY_ID: Readonly<Record<string, Domain>> = Object.freeze(
  Object.fromEntries(ATLAS_DOMAINS.map((d) => [d.id, d])),
);

/** O(1) lookup by lobe (used when projecting graph_index nodes). */
export const ATLAS_DOMAINS_BY_LOBE: Readonly<Record<LobeID, Domain | undefined>> = Object.freeze(
  ATLAS_DOMAINS.reduce(
    (acc, d) => {
      acc[d.lobe] = d;
      return acc;
    },
    {} as Record<LobeID, Domain | undefined>,
  ),
);

/**
 * Project.product → domain ID map. Case-insensitive substring matching with
 * an explicit allow-list so "Indian Stocks" / "Indian Stocks Research" /
 * "INDIAN-STOCKS" all resolve identically.
 *
 * Order matters — first match wins. More-specific products (e.g. "Tax & Reports")
 * should appear before broader ones if there's any chance of overlap.
 */
const PRODUCT_RULES: ReadonlyArray<{ test: RegExp; domain: string }> = [
  // Markets — direct trading / fund products
  { test: /\bindian\s*stocks?\b/i, domain: "markets" },
  { test: /\bus\s*stocks?\b/i, domain: "markets" },
  { test: /\bmutual\s*fund/i, domain: "markets" },
  { test: /\b(mf|sip)\b/i, domain: "markets" },
  { test: /\bf&?o\b/i, domain: "markets" },
  { test: /\bsmallcase\b/i, domain: "markets" },
  { test: /\bipo\b/i, domain: "markets" },
  { test: /\betf\b/i, domain: "markets" },
  { test: /\bbonds?\b/i, domain: "markets" },
  { test: /\bderivatives?\b/i, domain: "markets" },
  { test: /\boption\s*chain\b/i, domain: "markets" },

  // Investing — analysis, research, reporting
  { test: /\bportfolio\b/i, domain: "investing" },
  { test: /\bresearch\b/i, domain: "investing" },
  { test: /\bwatchlists?\b/i, domain: "investing" },
  { test: /\borders?\b/i, domain: "investing" },
  { test: /\btax\b/i, domain: "investing" },
  { test: /\bgains?\b/i, domain: "investing" },
  { test: /\bnetworth\b/i, domain: "investing" },
  { test: /\binsights?\b/i, domain: "investing" },
  { test: /\bscreeners?\b/i, domain: "investing" },

  // Money — wallet, payments, cards
  { test: /\bwallet\b/i, domain: "money" },
  { test: /\bplutus\b/i, domain: "money" },
  { test: /\bcards?\b/i, domain: "money" },
  { test: /\bdeposit\b/i, domain: "money" },
  { test: /\bwithdraw/i, domain: "money" },
  { test: /\b(upi|neft|imps|rtgs)\b/i, domain: "money" },
  { test: /\bpayments?\b/i, domain: "money" },
  { test: /\bbanks?\b/i, domain: "money" },

  // Platform — identity, auth, settings, system
  { test: /\bkyc\b/i, domain: "platform" },
  { test: /\bonboarding\b/i, domain: "platform" },
  { test: /\bauth/i, domain: "platform" },
  { test: /\blogin\b/i, domain: "platform" },
  { test: /\bsign\s*(in|up)\b/i, domain: "platform" },
  { test: /\bsettings?\b/i, domain: "platform" },
  { test: /\bnotifications?\b/i, domain: "platform" },
  { test: /\bprofile\b/i, domain: "platform" },
  { test: /\bsupport\b/i, domain: "platform" },
];

/**
 * Resolve a free-text product label to a domain ID.
 * Falls back to "platform" so unknown products surface in a known lobe
 * rather than silently filtering out of the graph.
 */
export function productToDomain(product: string | null | undefined): string {
  if (!product) return "platform";
  for (const rule of PRODUCT_RULES) {
    if (rule.test.test(product)) return rule.domain;
  }
  return "platform";
}

/**
 * Does this project count as "primary"? Drives the larger / brighter rendering
 * on the brain. Threshold tuned so ~25% of nodes in a populated graph are
 * primary (tweak if downstream data shifts).
 */
export function isPrimaryFlow(screenCount: number): boolean {
  return screenCount >= 80;
}
