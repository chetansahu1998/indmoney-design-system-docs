/**
 * node-classifier.ts — name-aware semantic classifier for canonical_tree
 * nodes. The structural heuristic in `icon-cluster-resolver.ts` works on
 * shape (wrapper type + children), but Figma node names carry richer
 * intent: a node named `Icons/ 2D/ Help` is meant to be exported as a
 * single icon regardless of its sub-tree structure; a node named
 * `Status Bar` is a layout container that should keep its hierarchy
 * even though it has zero text descendants.
 *
 * This module makes that intent first-class:
 *
 *   classifyNode(node) → { kind, role, variantProps }
 *
 * `kind` drives renderer routing — clusters/illustrations get PNG-
 * exported; layouts get walked as containers; standalone shapes get
 * single-shape PNG.
 *
 * `role` and `variantProps` are advisory metadata (analytics, audit,
 * inspector tooltips) — not load-bearing for rendering.
 *
 * Patterns observed in real INDmoney canonical_tree data (NRI VKYC,
 * Plutus, etc.):
 *
 *   Icons/ 2D/ Help                  → kind=icon       role=Icons/2D/Help
 *   Icons/ Filled Icons/ Trustmarker → kind=icon       role=Icons/Filled/Trustmarker
 *   icon/alert/error_24px            → kind=icon       role=icon/alert/error variantProps={size:"24px"}
 *   Help/No/24px                     → kind=icon       role=Help variantProps={state:"No",size:"24px"}
 *   check-verified-02/No/32px        → kind=icon       role=check-verified-02
 *   Illustrations/Equity tracking/   → kind=illustration
 *   Status Bar                       → kind=container  role=status-bar
 *   OTP Input                        → kind=container  role=otp-input
 *   Footer CTA                       → kind=container
 *   1 CTA                            → kind=container
 *   Wifi-path / Vector / Rectangle   → kind=shape
 *   Combined Shape                   → kind=shape
 */

import type { CanonicalNode } from "./types";
import { isIconCluster } from "./icon-cluster-resolver";

export type NodeKind =
  /** Icon — small graphic intended to render as a single PNG. */
  | "icon"
  /** Illustration — larger graphic, also rasterized. */
  | "illustration"
  /** Layout container — walk children, don't rasterize (status bars,
   *  input fields, footers). */
  | "container"
  /** Standalone shape — VECTOR/ELLIPSE/LINE/etc. outside an icon
   *  cluster; rendered as single-shape PNG via the same export path. */
  | "shape"
  /** TEXT node — handled by renderText. */
  | "text"
  /** Anything else — falls through to default container rendering. */
  | "unknown";

export interface ClassifiedNode {
  kind: NodeKind;
  /** Normalized canonical role (lowercase, slash-joined segments). */
  role?: string;
  /**
   * Slash-segments of the original name (whitespace trimmed, original
   * case preserved). Surfaces a structural breakdown the inspector can
   * show without re-parsing the name string. Examples:
   *
   *   "Icons/ 2D/ Help"
   *     → ["Icons", "2D", "Help"]
   *
   *   "Illustrations/Equity tracking/Light/Banners/Got more demat accounts"
   *     → ["Illustrations", "Equity tracking", "Light", "Banners",
   *        "Got more demat accounts"]
   *
   *   "Help/No/24px"
   *     → ["Help", "No", "24px"]
   */
  taxonomy?: string[];
  /**
   * Variant properties parsed from name slashes OR key=value segments:
   *   - state: "Yes" | "No" | "On" | "Off" (toggle)
   *   - size:  "24px", "32px", etc.
   *   - mode:  "Light" | "Dark" (theme inferred from segments)
   *   - any `key=value` pair (Figma component-variant API syntax)
   */
  variantProps?: Record<string, string>;
}

const ICON_NAME_RE = /^\s*icons?\s*\//i;
const ILLUSTRATION_NAME_RE = /^\s*illustrations?\s*\//i;
/** `/Yes/24px` or `/No/24px` style — Figma's slash-segmented variant suffix. */
const SLASH_VARIANT_RE = /\/(yes|no)\/(\d+px)/i;
/** `<thing>/(some-state)/<NN>px` — broader icon variant suffix without Yes/No. */
const SIZED_VARIANT_RE = /\/(\d+px)$/i;
/** Figma component-variant property like `Type=Primary, Size=Large`. */
const VARIANT_PROP_RE = /(\w[\w- ]*)\s*=\s*([^,/]+)/g;

/** Names that look like layout containers — exclude from rasterization
 *  even when the structural heuristic would call them clusters. */
const LAYOUT_NAME_HINTS: ReadonlySet<string> = new Set([
  // System UI surfaces
  "status bar",
  "top strip",
  "footer",
  "header",
  "navigation bar",
  "tab bar",
  "action bar",
  "action bar_1 cta",
  "action bar_2cta",
  // Inputs
  "input field",
  "input field final",
  "text input",
  "text input ",
  "otp input",
  "keyboard",
  // Buttons / CTAs
  "footer cta",
  "footer text",
  "footer icon",
  "footer button",
  "footer_button",
  "1 cta",
  "2 cta",
  "prefix",
  // Generic-shape-named INSTANCEs that are actually styled containers
  // (89× "Rounded Rectangle" in Plutus Term Deposit alone).
  "rounded rectangle",
  "toggle final",
  "filters and tabs",
  "list 311",
  // Backgrounds
  "background",
]);

/** Slash-segment hints that signal a theme/mode rather than an icon. */
const THEME_SEGMENTS: ReadonlySet<string> = new Set(["light", "dark"]);

function normalizeName(s: string): string {
  return s.trim().toLowerCase().replace(/\s+/g, " ");
}

/** Split a name on `/` into trimmed non-empty segments. */
function taxonomySegments(rawName: string): string[] {
  return rawName
    .split("/")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

/** Public — classify any canonical_tree node. */
export function classifyNode(node: CanonicalNode): ClassifiedNode {
  const t = node.type;
  if (t === "TEXT") return { kind: "text" };

  const rawName = typeof node.name === "string" ? node.name : "";
  const name = normalizeName(rawName);
  const taxonomy = taxonomySegments(rawName);

  // Layout-named containers always win over the structural heuristic.
  if (LAYOUT_NAME_HINTS.has(name)) {
    return { kind: "container", role: name.replace(/\s+/g, "-"), taxonomy };
  }

  const variantProps = parseVariantProps(rawName, taxonomy);

  // Explicit icon taxonomy.
  if (ICON_NAME_RE.test(rawName)) {
    return {
      kind: "icon",
      role: rawName
        .replace(/^\s*/, "")
        .replace(/\s*\/\s*/g, "/")
        .toLowerCase(),
      taxonomy,
      variantProps,
    };
  }

  // Illustration taxonomy.
  if (ILLUSTRATION_NAME_RE.test(rawName)) {
    return {
      kind: "illustration",
      role: rawName.replace(/\s*\/\s*/g, "/").toLowerCase(),
      taxonomy,
      variantProps,
    };
  }

  // Slash-variant icon names — `Help/No/24px`, `Shield-Tick/No/20px`,
  // `check-verified-02/No/32px`. These are Figma's pre-variant-API
  // naming convention for icon states.
  if (SLASH_VARIANT_RE.test(rawName) || SIZED_VARIANT_RE.test(rawName)) {
    return {
      kind: "icon",
      role: rawName.split("/")[0]?.trim().toLowerCase() ?? rawName.toLowerCase(),
      taxonomy,
      variantProps,
    };
  }

  // Structural heuristic for icon-cluster wrappers.
  if (isIconCluster(node)) {
    return { kind: "icon", taxonomy };
  }

  // Standalone shape primitives.
  if (
    t === "VECTOR" ||
    t === "ELLIPSE" ||
    t === "LINE" ||
    t === "BOOLEAN_OPERATION" ||
    t === "STAR" ||
    t === "POLYGON"
  ) {
    return { kind: "shape", taxonomy };
  }

  // FRAME/INSTANCE/COMPONENT/GROUP/RECTANGLE without a recognised
  // icon/illustration/layout name — treat as container, walk children.
  return { kind: "container", taxonomy };
}

/**
 * Parse Figma variant-style segments from a name. Two flavours:
 *
 *   1. `Type=Primary, Size=Large` (Figma component-variant API syntax)
 *   2. `/Yes/24px` style (legacy slash-segmented Figma naming)
 *
 * Returns a flat string→string map. Keys are lowercased; values
 * preserve case so `Primary` stays `Primary`.
 */
export function parseVariantProps(
  rawName: string,
  taxonomyArg?: string[],
): Record<string, string> | undefined {
  const out: Record<string, string> = {};
  // Flavour 1: key=value pairs anywhere in the name.
  let m: RegExpExecArray | null;
  const re = new RegExp(VARIANT_PROP_RE.source, "g");
  while ((m = re.exec(rawName)) !== null) {
    out[m[1].trim().toLowerCase()] = m[2].trim();
  }
  // Flavour 2: Yes/No toggle slash-segment.
  const slash = SLASH_VARIANT_RE.exec(rawName);
  if (slash) {
    out["state"] = slash[1];
    out["size"] = slash[2];
  } else {
    // Fallback: trailing /<NN>px size segment without Yes/No.
    const sized = SIZED_VARIANT_RE.exec(rawName);
    if (sized) out["size"] = sized[1];
  }
  // Flavour 3: theme/mode segment (Light / Dark) anywhere in the path.
  // Plutus illustrations carry `Illustrations/<theme>/Light/...`; surface
  // that as `mode=Light` so the inspector can show the rendered theme.
  const tax = taxonomyArg ?? rawName.split("/").map((s) => s.trim());
  for (const seg of tax) {
    if (THEME_SEGMENTS.has(seg.toLowerCase())) {
      out["mode"] = seg;
      break;
    }
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

/**
 * Convenience predicate — true when the node should be exported as a
 * single PNG via the asset-export pipeline. Used by useIconClusterURLs's
 * collector and by nodeToHTML's renderer routing.
 */
export function shouldRasterize(node: CanonicalNode): boolean {
  const c = classifyNode(node);
  return c.kind === "icon" || c.kind === "illustration" || c.kind === "shape";
}
