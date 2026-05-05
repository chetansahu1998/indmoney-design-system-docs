/**
 * leafcanvas-v2/tokens.ts — pure converters from canonical_tree node
 * fields into copyable code snippets across CSS / iOS Swift / Android XML
 * / React Native.
 *
 * Used by `AtomicChildInspector.tsx` (the Zeplin-style sidebar) to render
 * the "Code" panel under each layer. Every snippet generator is pure:
 * given the same node + override + brand, it returns the same string.
 *
 * Token resolution: hex → semantic path via `buildHexToTokenMap`. When
 * the hex isn't in the registry we fall through to the raw literal with
 * a `// no token match` comment so designers immediately see the gap.
 *
 * Strict TS — no // @ts-nocheck.
 */

import { lookupTokenByHex } from "../../../../lib/tokens/hex-to-token";

import type { AnnotatedNode, CanonicalNode, Paint, RGBA, SolidPaint, TextStyle } from "./types";

/** Subset of the U2 ScreenTextOverride row the inspector cares about. */
export interface ScreenTextOverride {
  figma_node_id: string;
  value: string;
  status?: string; // active | orphaned
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

/**
 * Convert a normalized semantic path (e.g. `colour.special.spl-brown`) to
 * the leaf identifier the docs site uses in CSS variables and asset names.
 *
 * Drops the bucket prefix (`colour.`) but keeps the rest so multi-bucket
 * tokens like `colour.brand.primary` retain their disambiguator. We cap
 * the leaf at the last two segments because deeper nesting is internal
 * to the token pipeline.
 */
function semanticLeaf(path: string): string {
  const segs = path.split(".");
  if (segs.length <= 1) return path;
  // drop the leading bucket ("colour", "spacing", ...)
  return segs.slice(1).join("-");
}

/** Format the slug used by `--ds-color-<slug>` CSS variables. */
function cssVarFromPath(path: string): string {
  const leaf = semanticLeaf(path);
  // colour.special.spl-brown → special-spl-brown → special-spl-brown
  // colour.spl-brown          → spl-brown
  return leaf.toLowerCase();
}

/**
 * Format the slug used by iOS asset catalogs / colour sets. The convention
 * is `Spl/Brown` style — matches the Figma collection name pattern in our
 * extractor metadata.
 */
function iosAssetFromPath(path: string): string {
  const leaf = semanticLeaf(path);
  // "special-spl-brown" → "Special/Spl/Brown"
  return leaf
    .split("-")
    .map((s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s))
    .join("/");
}

/** Format the slug used by Android `<color name="…">` (snake_case). */
function androidNameFromPath(path: string): string {
  return semanticLeaf(path).toLowerCase().replace(/-/g, "_");
}

/** Pull the first SOLID fill's hex out of a paints array. */
export function firstSolidHex(paints: Paint[] | undefined): string | null {
  if (!Array.isArray(paints)) return null;
  for (const p of paints) {
    if (p.visible === false) continue;
    if (p.type !== "SOLID") continue;
    const sp = p as SolidPaint;
    if (!sp.color) continue;
    return rgbaToHex(sp.color);
  }
  return null;
}

function rgbaToHex(c: RGBA): string {
  const hh = (x: number) =>
    Math.round(Math.max(0, Math.min(1, x)) * 255)
      .toString(16)
      .padStart(2, "0");
  const hex = `#${hh(c.r)}${hh(c.g)}${hh(c.b)}`.toUpperCase();
  return hex;
}

// ─── Color snippets ───────────────────────────────────────────────────────────

/**
 * `color: var(--ds-color-spl-brown);  /* #854236 *\/`
 * or, when no token matches:
 * `color: #854236;  /* no token match *\/`
 */
export function colorSnippetCSS(hex: string, tokenPath?: string | null): string {
  const upper = hex.toUpperCase();
  if (tokenPath) {
    return `color: var(--ds-color-${cssVarFromPath(tokenPath)});  /* ${upper} */`;
  }
  return `color: ${upper};  /* no token match */`;
}

/**
 * `UIColor(named: "Spl/Brown")  // #854236`
 * or `UIColor(red: 0.52, green: 0.26, blue: 0.21, alpha: 1)  // no token match`
 */
export function colorSnippetIOS(hex: string, tokenPath?: string | null): string {
  const upper = hex.toUpperCase();
  if (tokenPath) {
    return `UIColor(named: "${iosAssetFromPath(tokenPath)}")  // ${upper}`;
  }
  return `UIColor(hex: "${upper}")  // no token match`;
}

/**
 * `<color name="spl_brown">#854236</color>`
 * or `<color name="custom">#854236</color>  <!-- no token match -->`
 */
export function colorSnippetAndroid(hex: string, tokenPath?: string | null): string {
  const upper = hex.toUpperCase();
  if (tokenPath) {
    return `<color name="${androidNameFromPath(tokenPath)}">${upper}</color>`;
  }
  return `<color name="custom">${upper}</color>  <!-- no token match -->`;
}

/**
 * `Tokens.Color.spl-brown  // #854236`
 * or `"#854236"  // no token match`
 */
export function colorSnippetReactNative(
  hex: string,
  tokenPath?: string | null,
): string {
  const upper = hex.toUpperCase();
  if (tokenPath) {
    return `Tokens.Color.${semanticLeaf(tokenPath)}  // ${upper}`;
  }
  return `"${upper}"  // no token match`;
}

// ─── Text-style snippets ──────────────────────────────────────────────────────

/** Pick the text content the inspector should display. Override wins. */
export function effectiveText(
  node: CanonicalNode | AnnotatedNode,
  override?: ScreenTextOverride | null,
): string {
  if (override && override.status !== "orphaned") {
    return override.value;
  }
  return node.characters ?? "";
}

function styleFontWeight(s: TextStyle | undefined): number {
  return s?.fontWeight ?? 400;
}

function fontFamily(s: TextStyle | undefined): string {
  return s?.fontFamily ?? "inherit";
}

function fontSize(s: TextStyle | undefined): number | null {
  return typeof s?.fontSize === "number" ? s.fontSize : null;
}

function lineHeightPx(s: TextStyle | undefined): number | null {
  return typeof s?.lineHeightPx === "number" ? s.lineHeightPx : null;
}

function letterSpacing(s: TextStyle | undefined): number | null {
  return typeof s?.letterSpacing === "number" ? s.letterSpacing : null;
}

/**
 * Full CSS block for a TEXT atomic. Uses the override value (`R6 — engineer
 * copies the live text`). Indents two spaces per declaration so the output
 * is paste-ready into a `.element {}` rule.
 */
export function textSnippetCSS(
  node: CanonicalNode | AnnotatedNode,
  brand: string,
  override?: ScreenTextOverride | null,
): string {
  const s = node.style;
  const lines: string[] = [];
  const fam = fontFamily(s);
  if (fam) lines.push(`  font-family: "${fam}";`);
  lines.push(`  font-weight: ${styleFontWeight(s)};`);
  const fs = fontSize(s);
  if (fs !== null) lines.push(`  font-size: ${fs}px;`);
  const lh = lineHeightPx(s);
  if (lh !== null) lines.push(`  line-height: ${lh}px;`);
  const ls = letterSpacing(s);
  if (ls !== null) lines.push(`  letter-spacing: ${ls}px;`);
  if (s?.italic) lines.push(`  font-style: italic;`);

  const hex = firstSolidHex(node.fills);
  if (hex) {
    const tokenPath = lookupTokenByHex(hex, brand);
    lines.push(`  ${colorSnippetCSS(hex, tokenPath)}`);
  }
  const text = effectiveText(node, override);
  const header = `/* "${escapeBlockComment(text)}" */`;
  return `${header}\n.element {\n${lines.join("\n")}\n}`;
}

/**
 * SwiftUI snippet — chains `.font(...)` + `.foregroundColor(...)` onto a
 * Text() literal. Doesn't try to map every Figma weight to a SwiftUI
 * font (that's a U13 pass); we emit `.system(size:weight:)` which is
 * always valid.
 */
export function textSnippetIOS(
  node: CanonicalNode | AnnotatedNode,
  brand: string,
  override?: ScreenTextOverride | null,
): string {
  const text = effectiveText(node, override);
  const s = node.style;
  const fs = fontSize(s) ?? 16;
  const weight = swiftUIWeight(styleFontWeight(s));
  const lines: string[] = [];
  lines.push(`Text("${escapeSwift(text)}")`);
  lines.push(`  .font(.system(size: ${fs}, weight: ${weight}))`);
  const hex = firstSolidHex(node.fills);
  if (hex) {
    const tokenPath = lookupTokenByHex(hex, brand);
    if (tokenPath) {
      lines.push(`  .foregroundColor(Color("${iosAssetFromPath(tokenPath)}"))  // ${hex.toUpperCase()}`);
    } else {
      lines.push(`  .foregroundColor(Color(hex: "${hex.toUpperCase()}"))  // no token match`);
    }
  }
  return lines.join("\n");
}

function swiftUIWeight(w: number): string {
  if (w <= 200) return ".thin";
  if (w <= 300) return ".light";
  if (w <= 400) return ".regular";
  if (w <= 500) return ".medium";
  if (w <= 600) return ".semibold";
  if (w <= 700) return ".bold";
  if (w <= 800) return ".heavy";
  return ".black";
}

/**
 * Compose snippet — pairs a `Text()` call with a `TextStyle(...)` so the
 * paste target is a Composable function body. Mirrors how INDmoney's
 * Compose theme exposes typography via `Tokens.Type.*`.
 */
export function textSnippetAndroid(
  node: CanonicalNode | AnnotatedNode,
  brand: string,
  override?: ScreenTextOverride | null,
): string {
  const text = effectiveText(node, override);
  const s = node.style;
  const fs = fontSize(s) ?? 16;
  const weight = composeWeight(styleFontWeight(s));
  const lh = lineHeightPx(s);

  const styleLines: string[] = [`fontSize = ${fs}.sp`, `fontWeight = ${weight}`];
  if (lh !== null) styleLines.push(`lineHeight = ${lh}.sp`);
  const hex = firstSolidHex(node.fills);
  if (hex) {
    const tokenPath = lookupTokenByHex(hex, brand);
    if (tokenPath) {
      styleLines.push(
        `color = colorResource(R.color.${androidNameFromPath(tokenPath)})  // ${hex.toUpperCase()}`,
      );
    } else {
      styleLines.push(`color = Color(0xFF${hex.replace("#", "").toUpperCase()})  // no token match`);
    }
  }

  return [
    `Text(`,
    `  text = "${escapeKotlin(text)}",`,
    `  style = TextStyle(`,
    ...styleLines.map((l) => `    ${l},`),
    `  ),`,
    `)`,
  ].join("\n");
}

function composeWeight(w: number): string {
  if (w <= 100) return "FontWeight.Thin";
  if (w <= 200) return "FontWeight.ExtraLight";
  if (w <= 300) return "FontWeight.Light";
  if (w <= 400) return "FontWeight.Normal";
  if (w <= 500) return "FontWeight.Medium";
  if (w <= 600) return "FontWeight.SemiBold";
  if (w <= 700) return "FontWeight.Bold";
  if (w <= 800) return "FontWeight.ExtraBold";
  return "FontWeight.Black";
}

// ─── Spacing / layout snippet ─────────────────────────────────────────────────

export interface PaddingPx {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

/**
 * `padding: 16px 24px;  gap: 8px;` — collapses CSS's 4-value padding
 * shorthand to the shortest equivalent so the snippet matches what an
 * engineer would hand-write.
 */
export function spacingSnippet(padding: PaddingPx, gap: number | null): string {
  const { top, right, bottom, left } = padding;
  let pad: string;
  if (top === right && right === bottom && bottom === left) {
    pad = `${top}px`;
  } else if (top === bottom && left === right) {
    pad = `${top}px ${right}px`;
  } else if (left === right) {
    pad = `${top}px ${right}px ${bottom}px`;
  } else {
    pad = `${top}px ${right}px ${bottom}px ${left}px`;
  }
  const parts = [`padding: ${pad};`];
  if (gap !== null && gap > 0) parts.push(`gap: ${gap}px;`);
  return parts.join("  ");
}

// ─── String-escape helpers ────────────────────────────────────────────────────

function escapeSwift(s: string): string {
  return s.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, "\\n");
}

function escapeKotlin(s: string): string {
  return s.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, "\\n");
}

function escapeBlockComment(s: string): string {
  // Prevent `*/` from breaking out of the leading `/* … */` text-preview header.
  return s.replace(/\*\//g, "*\\/");
}
