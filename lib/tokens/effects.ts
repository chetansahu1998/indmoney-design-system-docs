/**
 * Effects/shadow loader — reads effects.tokens.json (W3C-DTCG shadow tokens
 * extracted from Figma EFFECT styles). Empty until the extractor is run.
 */
import effectsData from "./indmoney/effects.tokens.json";

export interface ShadowLayer {
  color: string;
  offsetX: string;
  offsetY: string;
  blur: string;
  spread: string;
  inset: boolean;
}

export interface ShadowToken {
  path: string; // "shadow.card-elevation-1"
  name: string; // "card-elevation-1"
  description?: string;
  layers: ShadowLayer[];
}

interface DTCGNode {
  $type?: string;
  $value?: ShadowLayer | ShadowLayer[];
  $description?: string;
}

const data = effectsData as {
  $extensions?: { "com.indmoney.provenance"?: string };
  shadow?: Record<string, DTCGNode>;
};

export function shadowTokens(): ShadowToken[] {
  const out: ShadowToken[] = [];
  for (const [name, node] of Object.entries(data.shadow ?? {})) {
    if (node.$type !== "shadow" || !node.$value) continue;
    const layers = Array.isArray(node.$value) ? node.$value : [node.$value];
    out.push({
      path: `shadow.${name}`,
      name,
      description: node.$description,
      layers,
    });
  }
  return out;
}

export function shadowsProvenance(): string {
  return data.$extensions?.["com.indmoney.provenance"] ?? "unknown";
}

/** CSS box-shadow string from a multi-layer shadow token. */
export function toCSS(token: ShadowToken): string {
  return token.layers
    .map((l) => `${l.inset ? "inset " : ""}${l.offsetX} ${l.offsetY} ${l.blur} ${l.spread} ${l.color}`)
    .join(", ");
}
