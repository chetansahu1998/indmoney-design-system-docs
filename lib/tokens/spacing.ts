/**
 * Spacing + radius loader — reads spacing.tokens.json (DTCG dimension tokens).
 *
 * Hand-curated today. When Figma exposes these as Variables, the extractor
 * will overwrite this JSON in place; the loader signature stays stable.
 */
import spacingData from "./indmoney/spacing.tokens.json";

interface DimensionValue { value: number; unit: string }
interface DimensionNode {
  $type?: string;
  $value?: DimensionValue;
  $extensions?: { "com.indmoney.usage-count"?: number };
}
type Branch = Record<string, DimensionNode | Record<string, unknown>>;

export interface SpacingToken {
  token: string; // "space.16"
  px: number;
  /** Usage count from layout scan (undefined for hand-curated tokens). */
  usageCount?: number;
}

function flatten(branch: Branch | unknown, prefix = ""): SpacingToken[] {
  if (!branch || typeof branch !== "object") return [];
  const out: SpacingToken[] = [];
  for (const [k, v] of Object.entries(branch as Record<string, unknown>)) {
    if (k.startsWith("$")) continue;
    const path = prefix ? `${prefix}.${k}` : k;
    const node = v as DimensionNode | Branch;
    if (node && typeof node === "object" && "$value" in node && node.$value) {
      const dnode = node as DimensionNode;
      out.push({
        token: path,
        px: (dnode.$value as DimensionValue).value,
        usageCount: dnode.$extensions?.["com.indmoney.usage-count"],
      });
    } else {
      out.push(...flatten(v as Branch, path));
    }
  }
  return out;
}

const ALL = flatten(spacingData);

const byPx = (a: SpacingToken, b: SpacingToken) => a.px - b.px;

export function spacingScale(): SpacingToken[] {
  return ALL.filter((t) => t.token.startsWith("space.")).sort(byPx);
}

export function paddingScale(): SpacingToken[] {
  return ALL.filter((t) => t.token.startsWith("padding.")).sort(byPx);
}

export function radiusScale(): SpacingToken[] {
  return ALL.filter((t) => t.token.startsWith("radius.")).sort(byPx);
}

export function spacingProvenance(): string {
  return (
    (spacingData as { $extensions?: { "com.indmoney.provenance"?: string } })
      .$extensions?.["com.indmoney.provenance"] ?? "unknown"
  );
}
