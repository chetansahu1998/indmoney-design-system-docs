/**
 * Token loader — reads the per-brand DTCG JSON and exposes structured access.
 *
 * Used by section components to render INDmoney's actual extracted tokens.
 * Decodes the W3C-DTCG 2024 sRGB object color form back to "#RRGGBB" for display.
 */

import baseDataIndmoney from "./indmoney/base.tokens.json";
import semanticDataIndmoney from "./indmoney/semantic.tokens.json";
import semanticDarkDataIndmoney from "./indmoney/semantic-dark.tokens.json";
import metaIndmoney from "./indmoney/_extraction-meta.json";

import { currentBrand } from "@/lib/brand";

type ColorValue =
  | string
  | {
      colorSpace: "srgb" | string;
      components: [number, number, number];
      alpha?: number;
    };

type ColorTokenLike = {
  $type?: "color";
  $value?: ColorValue;
  $description?: string;
};

type Branch = { [k: string]: Branch | ColorTokenLike };

const BRAND_DATA = {
  indmoney: {
    base: baseDataIndmoney,
    semanticLight: semanticDataIndmoney,
    semanticDark: semanticDarkDataIndmoney,
    meta: metaIndmoney,
  },
  // Tickertape data not yet available — falls back to indmoney.
  tickertape: {
    base: baseDataIndmoney,
    semanticLight: semanticDataIndmoney,
    semanticDark: semanticDarkDataIndmoney,
    meta: metaIndmoney,
  },
} as const;

/** Convert a DTCG-format color value (object or hex string) to "#RRGGBB". */
export function colorToHex(v: ColorValue | undefined): string {
  if (!v) return "#000000";
  if (typeof v === "string") return v.toUpperCase();
  const [r, g, b] = v.components;
  const hh = (x: number) =>
    Math.round(Math.max(0, Math.min(1, x)) * 255)
      .toString(16)
      .padStart(2, "0")
      .toUpperCase();
  let hex = `#${hh(r)}${hh(g)}${hh(b)}`;
  if (v.alpha !== undefined && v.alpha < 0.999) {
    hex += hh(v.alpha);
  }
  return hex;
}

/** Walks a DTCG branch and returns leaf tokens with their dotted path. */
export function flattenColorTokens(
  branch: Branch | unknown,
  prefix = "",
): Array<{ path: string; hex: string; description?: string }> {
  if (!branch || typeof branch !== "object") return [];
  const out: Array<{ path: string; hex: string; description?: string }> = [];
  for (const [k, v] of Object.entries(branch as Record<string, unknown>)) {
    if (k.startsWith("$")) continue;
    const path = prefix ? `${prefix}.${k}` : k;
    if (
      v &&
      typeof v === "object" &&
      ("$value" in v || (v as ColorTokenLike).$value !== undefined)
    ) {
      const tok = v as ColorTokenLike;
      out.push({
        path,
        hex: colorToHex(tok.$value),
        description: tok.$description,
      });
    } else {
      out.push(...flattenColorTokens(v as Branch, path));
    }
  }
  return out;
}

/** Returns the active brand's resolved token tree. */
export function loadBrandTokens(brand = currentBrand()) {
  const data =
    BRAND_DATA[brand as keyof typeof BRAND_DATA] ?? BRAND_DATA.indmoney;
  return data;
}

/**
 * Build a map of dotted-semantic-path → { light, dark } hex pair.
 * Falls back to light hex when the dark file omits the token (e.g. status colors).
 */
export function buildSemanticPairs(brand = currentBrand()): Array<{
  path: string;
  bucket: string;
  leaf: string;
  light: string;
  dark: string;
  description?: string;
}> {
  const { semanticLight, semanticDark } = loadBrandTokens(brand);
  const lightFlat = flattenColorTokens(semanticLight);
  const darkFlat = flattenColorTokens(semanticDark);
  const darkByPath = new Map(darkFlat.map((t) => [t.path, t.hex]));

  return lightFlat
    .map(({ path, hex, description }) => {
      const segments = path.split(".");
      const bucket = segments[1] ?? "other"; // colour.<bucket>.<leaf>
      const leaf = segments.slice(2).join(".") || segments.at(-1) || path;
      const dark = darkByPath.get(path) ?? hex;
      return { path, bucket, leaf, light: hex, dark, description };
    })
    .sort((a, b) => a.bucket.localeCompare(b.bucket) || a.leaf.localeCompare(b.leaf));
}

/** Returns base palette flattened by bucket, sorted by bucket then hex. */
export function buildBasePalette(brand = currentBrand()): Array<{
  bucket: string;
  hex: string;
  path: string;
}> {
  const { base } = loadBrandTokens(brand);
  return flattenColorTokens(base)
    .map(({ path, hex }) => {
      const segments = path.split(".");
      // base.colour.<bucket>.<hex-id>
      const bucket = segments[2] ?? "other";
      return { bucket, hex, path };
    })
    .sort(
      (a, b) =>
        a.bucket.localeCompare(b.bucket) || a.hex.localeCompare(b.hex),
    );
}

/** Sources/observations metadata — surfaced in the docs chrome. */
export function getExtractionMeta(brand = currentBrand()) {
  return loadBrandTokens(brand).meta;
}
