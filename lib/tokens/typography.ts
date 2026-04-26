/**
 * Typography loader — reads text-styles.tokens.json (extracted from Glyph's
 * 18 published TEXT styles, dereferenced for full font metadata).
 */
import textStylesData from "./indmoney/text-styles.tokens.json";

export interface DereferencedTypography {
  slug: string;
  name: string; // "10 - Small 1"
  fontFamily: string;
  fontWeight: number;
  fontSize: number;
  lineHeight: number;
  letterSpacing: number;
  description?: string;
}

interface RawValue {
  fontFamily?: string[] | string;
  fontWeight?: number;
  fontSize?: { value: number; unit: string } | number;
  lineHeight?: number;
  letterSpacing?: { value: number; unit: string } | number;
  textTransform?: string;
}
interface RawNode {
  $type?: string;
  $value?: RawValue;
  $description?: string;
}

function unboxNumber(v: { value: number; unit?: string } | number | undefined): number {
  if (v == null) return 0;
  return typeof v === "number" ? v : v.value;
}

function unboxFontFamily(v: string[] | string | undefined): string {
  if (!v) return "system-ui, sans-serif";
  if (Array.isArray(v)) return v.join(", ");
  return v;
}

export function loadTypography(): DereferencedTypography[] {
  const data = (textStylesData as { text?: Record<string, unknown> }).text ?? {};
  const out: DereferencedTypography[] = [];
  for (const [slug, raw] of Object.entries(data)) {
    if (!raw || typeof raw !== "object") continue;
    const node = raw as RawNode;
    if (node.$type !== "typography") continue;
    const v = node.$value ?? {};
    out.push({
      slug,
      name: slug.replace(/-/g, " ").replace(/\s+/g, " ").trim(),
      fontFamily: unboxFontFamily(v.fontFamily),
      fontWeight: v.fontWeight ?? 400,
      fontSize: unboxNumber(v.fontSize),
      lineHeight: unboxNumber(v.lineHeight),
      letterSpacing: unboxNumber(v.letterSpacing),
      description: node.$description,
    });
  }
  // Sort by font size descending (largest type first — natural for type ramp display)
  out.sort((a, b) => b.fontSize - a.fontSize);
  return out;
}

export function typographyByCategory(): Map<string, DereferencedTypography[]> {
  const grouped = new Map<string, DereferencedTypography[]>();
  for (const t of loadTypography()) {
    // Categorize from style name: "Heading", "Body", "Caption", "Subtitle", "Overline", "Small"
    let cat = "other";
    const lower = t.name.toLowerCase();
    if (lower.includes("h1") || lower.includes("h2") || lower.includes("h3") || lower.includes("heading")) {
      cat = "heading";
    } else if (lower.includes("subtitle")) {
      cat = "subtitle";
    } else if (lower.includes("body")) {
      cat = "body";
    } else if (lower.includes("caption")) {
      cat = "caption";
    } else if (lower.includes("overline")) {
      cat = "overline";
    } else if (lower.includes("small")) {
      cat = "small";
    }
    if (!grouped.has(cat)) grouped.set(cat, []);
    grouped.get(cat)!.push(t);
  }
  return grouped;
}
