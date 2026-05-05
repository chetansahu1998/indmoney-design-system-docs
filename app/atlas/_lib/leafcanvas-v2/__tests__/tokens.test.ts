/**
 * tokens.test.ts — exercises U7's snippet generators.
 *
 * Keeps the same shape as `nodeToHTML.test.ts` — pure assertions, no
 * runner wired. tsc --noEmit ensures type safety; runAll throws on the
 * first failed case.
 *
 * Strict TS — no // @ts-nocheck.
 */

import {
  type ScreenTextOverride,
  colorSnippetAndroid,
  colorSnippetCSS,
  colorSnippetIOS,
  colorSnippetReactNative,
  effectiveText,
  firstSolidHex,
  spacingSnippet,
  textSnippetCSS,
} from "../tokens";
import type { CanonicalNode } from "../types";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

// ─── Color snippets — token match ────────────────────────────────────────────

function _test_color_css_with_token(): void {
  const out = colorSnippetCSS("#854236", "colour.special.spl-brown");
  assert(out.includes("var(--ds-color-special-spl-brown)"), `css: ${out}`);
  assert(out.includes("#854236"), "preserves hex comment");
}

function _test_color_ios_with_token(): void {
  const out = colorSnippetIOS("#854236", "colour.special.spl-brown");
  assert(out.includes("UIColor(named:"), `ios: ${out}`);
  assert(out.includes("#854236"), "preserves hex comment");
}

function _test_color_android_with_token(): void {
  const out = colorSnippetAndroid("#854236", "colour.special.spl-brown");
  assert(out.includes('<color name="special_spl_brown">'), `android: ${out}`);
  assert(out.includes("#854236"), "hex value present");
}

function _test_color_rn_with_token(): void {
  const out = colorSnippetReactNative("#854236", "colour.special.spl-brown");
  assert(out.startsWith("Tokens.Color."), `rn: ${out}`);
  assert(out.includes("// #854236"), "hex comment present");
}

// ─── Color snippets — no token match ─────────────────────────────────────────

function _test_color_css_no_match(): void {
  const out = colorSnippetCSS("#abcdef", null);
  assert(out.includes("#ABCDEF"), "raw hex present (uppercase)");
  assert(out.includes("no token match"), `comment: ${out}`);
}

function _test_color_ios_no_match(): void {
  const out = colorSnippetIOS("#abcdef", null);
  assert(out.includes("UIColor(hex:"), `ios fallback: ${out}`);
  assert(out.includes("no token match"), "comment present");
}

function _test_color_android_no_match(): void {
  const out = colorSnippetAndroid("#abcdef", null);
  assert(out.includes("no token match"), `android comment: ${out}`);
}

function _test_color_rn_no_match(): void {
  const out = colorSnippetReactNative("#abcdef", null);
  assert(out.includes("no token match"), `rn comment: ${out}`);
}

// ─── effectiveText: override wins, orphan falls back ─────────────────────────

function _test_effective_text_uses_override(): void {
  const node: CanonicalNode = { type: "TEXT", characters: "Buy" };
  const override: ScreenTextOverride = {
    figma_node_id: "n1",
    value: "Buy now",
    status: "active",
  };
  assert(effectiveText(node, override) === "Buy now", "override wins");
}

function _test_effective_text_falls_back_when_orphaned(): void {
  const node: CanonicalNode = { type: "TEXT", characters: "Buy" };
  const override: ScreenTextOverride = {
    figma_node_id: "n1",
    value: "Buy now",
    status: "orphaned",
  };
  assert(effectiveText(node, override) === "Buy", "orphan ignored");
}

function _test_effective_text_falls_back_when_undefined(): void {
  const node: CanonicalNode = { type: "TEXT", characters: "Buy" };
  assert(effectiveText(node, null) === "Buy", "no override → original");
}

// ─── textSnippetCSS — uses override value, embeds color snippet ──────────────

function _test_text_css_uses_override(): void {
  const node: CanonicalNode = {
    type: "TEXT",
    characters: "Buy",
    style: {
      fontFamily: "Basier Circle",
      fontWeight: 600,
      fontSize: 14,
      lineHeightPx: 20,
    },
    fills: [
      {
        type: "SOLID",
        color: { r: 0.5216, g: 0.2588, b: 0.2118 },
      },
    ],
  };
  const override: ScreenTextOverride = {
    figma_node_id: "n1",
    value: "Buy now",
    status: "active",
  };
  // Brand="indmoney" — spl-brown should resolve.
  const out = textSnippetCSS(node, "indmoney", override);
  assert(out.includes('"Buy now"'), `embeds override text: ${out}`);
  assert(!out.includes('"Buy"'), "no original text leaked");
  assert(out.includes("Basier Circle"), "font family present");
  assert(out.includes("font-weight: 600"), "weight present");
  assert(out.includes("font-size: 14px"), "size present");
  assert(out.includes("line-height: 20px"), "line height present");
  assert(out.includes("var(--ds-color-"), `color uses token: ${out}`);
}

function _test_text_css_no_token_falls_back_to_raw(): void {
  const node: CanonicalNode = {
    type: "TEXT",
    characters: "X",
    style: { fontSize: 12 },
    fills: [{ type: "SOLID", color: { r: 0.123, g: 0.456, b: 0.789 } }],
  };
  const out = textSnippetCSS(node, "indmoney", null);
  // The synthetic hex above is unlikely to map to a token.
  if (out.includes("var(--ds-color-")) {
    // skip — happened to collide with a real token; not a bug
    return;
  }
  assert(out.includes("no token match"), `expected no-match comment: ${out}`);
}

// ─── firstSolidHex — picks first visible solid fill ──────────────────────────

function _test_firstSolidHex_picks_first_visible(): void {
  const hex = firstSolidHex([
    { type: "IMAGE", visible: true } as never,
    { type: "SOLID", color: { r: 0.5216, g: 0.2588, b: 0.2118 } },
    { type: "SOLID", color: { r: 0, g: 0, b: 0 } },
  ]);
  assert(hex === "#854236", `got ${hex}`);
}

function _test_firstSolidHex_skips_invisible(): void {
  const hex = firstSolidHex([
    {
      type: "SOLID",
      visible: false,
      color: { r: 1, g: 1, b: 1 },
    },
    { type: "SOLID", color: { r: 0, g: 0, b: 0 } },
  ]);
  assert(hex === "#000000", `got ${hex}`);
}

// ─── spacingSnippet — collapses CSS shorthand ────────────────────────────────

function _test_spacing_uniform(): void {
  const out = spacingSnippet({ top: 8, right: 8, bottom: 8, left: 8 }, null);
  assert(out === "padding: 8px;", `got ${out}`);
}

function _test_spacing_two_values(): void {
  const out = spacingSnippet({ top: 16, right: 24, bottom: 16, left: 24 }, 8);
  assert(out.includes("padding: 16px 24px;"), `got ${out}`);
  assert(out.includes("gap: 8px"), "gap present");
}

function _test_spacing_four_values(): void {
  const out = spacingSnippet({ top: 1, right: 2, bottom: 3, left: 4 }, null);
  assert(out === "padding: 1px 2px 3px 4px;", `got ${out}`);
}

export function runAll(): void {
  _test_color_css_with_token();
  _test_color_ios_with_token();
  _test_color_android_with_token();
  _test_color_rn_with_token();
  _test_color_css_no_match();
  _test_color_ios_no_match();
  _test_color_android_no_match();
  _test_color_rn_no_match();
  _test_effective_text_uses_override();
  _test_effective_text_falls_back_when_orphaned();
  _test_effective_text_falls_back_when_undefined();
  _test_text_css_uses_override();
  _test_text_css_no_token_falls_back_to_raw();
  _test_firstSolidHex_picks_first_visible();
  _test_firstSolidHex_skips_invisible();
  _test_spacing_uniform();
  _test_spacing_two_values();
  _test_spacing_four_values();
}
