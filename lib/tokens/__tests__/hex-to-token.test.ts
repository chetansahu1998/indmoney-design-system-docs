/**
 * hex-to-token.test.ts — covers the U7 reverse-lookup map.
 *
 * No test runner is wired in this repo (mirrors the approach in
 * `app/atlas/_lib/leafcanvas-v2/__tests__/nodeToHTML.test.ts`); each
 * `_test_*` function throws on failure, and `runAll` is the umbrella
 * driver. tsc --noEmit exercises the type-level surface.
 *
 * Strict TS — no // @ts-nocheck.
 */

import {
  _resetHexToTokenCache,
  buildHexToTokenMap,
  lookupTokenByHex,
  normalizeHex,
} from "../hex-to-token";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

function _test_normalize_hex_strips_hash_and_alpha(): void {
  assert(normalizeHex("#854236") === "854236", "drops leading #");
  assert(normalizeHex("854236") === "854236", "no-op without #");
  assert(normalizeHex("#854236FF") === "854236", "drops opaque alpha");
  assert(normalizeHex("#854236ff") === "854236", "case-insensitive alpha");
  assert(normalizeHex("#854236") === normalizeHex("854236FF"), "matches across forms");
}

function _test_buildHexToTokenMap_returns_real_indmoney_tokens(): void {
  _resetHexToTokenCache();
  const map = buildHexToTokenMap("indmoney");
  assert(map.size > 0, "indmoney map non-empty");
  // The hex `#854236` is registered under colour.special.* — multiple
  // semantic tokens share that exact RGB triple in our extracted set
  // (Figma rounded the source colours), so we only assert the bucket
  // resolves to a colour.special.spl-* path. The reverse map is "best
  // effort" for shared hexes — last-write-wins via the walk order.
  const spl = map.get("854236");
  assert(
    typeof spl === "string" && spl.startsWith("colour.special.spl-"),
    `expected colour.special.spl-* path, got: ${spl}`,
  );
}

function _test_lookupTokenByHex_handles_all_input_forms(): void {
  _resetHexToTokenCache();
  const a = lookupTokenByHex("#854236", "indmoney");
  const b = lookupTokenByHex("854236", "indmoney");
  const c = lookupTokenByHex("#854236FF", "indmoney");
  assert(a !== null, "with leading #");
  assert(a === b, "with vs without #");
  assert(a === c, "with vs without alpha");
}

function _test_lookupTokenByHex_returns_null_for_unknown_hex(): void {
  _resetHexToTokenCache();
  // A hex very unlikely to appear in the tokenset.
  const v = lookupTokenByHex("#abcdef", "indmoney");
  assert(v === null, "unknown hex returns null");
}

function _test_semantic_wins_over_base_when_both_resolve_same_hex(): void {
  _resetHexToTokenCache();
  const map = buildHexToTokenMap("indmoney");
  // The map walks base first, semantic second, so any path returned for a
  // hex that lives in *both* trees must come from semantic.
  // We check by asserting that no base-only segment ("base." or
  // "brand.") leaks into the value when a semantic mapping exists for a
  // common hex like spl-brown.
  const spl = map.get("854236");
  assert(typeof spl === "string", "spl-brown present");
  // semantic paths in this repo start with "colour."; base entries are
  // flattened directly off the JSON root which begins with "base.".
  if (typeof spl === "string") {
    assert(!spl.startsWith("base."), `semantic wins, got "${spl}"`);
  }
}

function _test_cache_returns_same_reference_on_repeat_calls(): void {
  _resetHexToTokenCache();
  const a = buildHexToTokenMap("indmoney");
  const b = buildHexToTokenMap("indmoney");
  assert(a === b, "second call returns cached map reference");
}

export function runAll(): void {
  _test_normalize_hex_strips_hash_and_alpha();
  _test_buildHexToTokenMap_returns_real_indmoney_tokens();
  _test_lookupTokenByHex_handles_all_input_forms();
  _test_lookupTokenByHex_returns_null_for_unknown_hex();
  _test_semantic_wins_over_base_when_both_resolve_same_hex();
  _test_cache_returns_same_reference_on_repeat_calls();
}
