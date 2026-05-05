/**
 * hex-to-token.ts — reverse-lookup map from a raw `#RRGGBB` value to its
 * canonical semantic token path (e.g. `colour.special.spl-brown`).
 *
 * Wired by the canvas-v2 atomic-child inspector (U7) to surface the token
 * name alongside the literal hex pulled out of `canonical_tree`. Designers
 * see "spl-brown" rather than `#854236`; engineers paste the right
 * `var(--ds-color-spl-brown)` instead of inlining a hex.
 *
 * Lookup precedence: semantic tokens win over base palette. The base
 * palette ("brand.dark-blue.500") is internal to the tokens pipeline and
 * doesn't surface in product code; semantic ("colour.special.spl-brown")
 * is the engineer-facing name.
 *
 * Build cost: ~O(N) over the flattened token list (N ≈ 200 for indmoney).
 * We cache per brand because re-walking on every render of the inspector
 * panel is wasteful and the trees are static at runtime.
 *
 * Strict TS — no // @ts-nocheck.
 */

import { type Brand, isBrand } from "../brand";

import { flattenColorTokens, loadBrandTokens } from "./loader";

const cache = new Map<string, Map<string, string>>();

/**
 * Normalise a hex string for map lookup:
 *   - strips a leading `#`
 *   - drops a fully-opaque alpha (`#RRGGBBFF` → `RRGGBB`)
 *   - lowercases
 *
 * Anything that isn't 6 or 8 hex chars after stripping returns the
 * original lowercased string — avoids accidentally widening "FF" into a
 * valid 2-char hex match.
 */
export function normalizeHex(hex: string): string {
  let h = hex.trim().toLowerCase();
  if (h.startsWith("#")) h = h.slice(1);
  if (h.length === 8 && h.endsWith("ff")) h = h.slice(0, 6);
  return h;
}

/**
 * Build (or return cached) hex → semantic-path map for the given brand.
 *
 * Walk order: base first, then semantic — so semantic entries overwrite
 * base entries that happen to share a hex (which is the common case,
 * e.g. `base.colour.brown.700` and `colour.special.spl-brown` both
 * resolve to `#854236`).
 */
export function buildHexToTokenMap(brand: string): Map<string, string> {
  const cached = cache.get(brand);
  if (cached) return cached;

  // Narrow to the Brand union so loadBrandTokens stays strict; unknown
  // brands fall back to indmoney by way of loader.ts's own default.
  const safeBrand: Brand = isBrand(brand) ? brand : "indmoney";
  const data = loadBrandTokens(safeBrand);
  const map = new Map<string, string>();

  // Base first — gives us a hit even when the hex isn't aliased to a
  // semantic token. The semantic walk below overwrites where it can.
  for (const t of flattenColorTokens(data.base)) {
    map.set(normalizeHex(t.hex), t.path);
  }
  for (const t of flattenColorTokens(data.semanticLight)) {
    map.set(normalizeHex(t.hex), t.path);
  }

  cache.set(brand, map);
  return map;
}

/**
 * Test/inspector helper. Returns the semantic-path string or `null` when
 * no token registered. Accepts hex with or without leading `#` and with
 * an optional `FF` alpha — see `normalizeHex`.
 */
export function lookupTokenByHex(hex: string, brand: string): string | null {
  const map = buildHexToTokenMap(brand);
  return map.get(normalizeHex(hex)) ?? null;
}

/** Reset the cache (test-only — production builds never call this). */
export function _resetHexToTokenCache(): void {
  cache.clear();
}
