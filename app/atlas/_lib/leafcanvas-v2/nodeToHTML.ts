/**
 * nodeToHTML.ts — pure converter from a canonical_tree node into a React
 * element tree. Mirrors the spike's dual-path algorithm:
 *
 *   Path A (autolayout) — `layoutMode` ∈ {HORIZONTAL, VERTICAL}:
 *     emit `display:flex` + flex-direction + gap + padding* +
 *     justify-content + align-items. Children are laid out by flex; we
 *     do *not* apply absolute positioning.
 *
 *   Path B (absolute) — `layoutMode` is null/NONE/missing:
 *     emit `position:absolute` with left/top relative to the parent's
 *     absoluteBoundingBox.
 *
 * Special-case node kinds:
 *   - GROUP / BOOLEAN_OPERATION → flatten (recurse with parent passed
 *     through unchanged). They don't appear in the DOM.
 *   - Icon cluster wrappers (`isIconCluster`) → render a single
 *     `data-cluster-pending="true"` div at the cluster's bbox. U7 will
 *     swap in real <img> rendering.
 *   - TEXT → <span> styled from `style` + first SOLID fill color.
 *
 * Strict TS: no `// @ts-nocheck`.
 */

import { type CSSProperties, type ReactElement, createElement } from "react";

import { isIconCluster, resolveClusterURL } from "./icon-cluster-resolver";
import type {
  AnnotatedNode,
  BoundingBox,
  CanonicalNode,
  ImagePaint,
  ImageRefMap,
  LayoutMode,
  Paint,
  RGBA,
  SolidPaint,
} from "./types";

export interface NodeToHTMLContext {
  /** image-fill ref → URL. Provided by U7 once it lands; meanwhile {} works. */
  imageRefs: ImageRefMap;
  /** Stable key prefix so nested sibling arrays keep unique React keys. */
  keyPrefix?: string;
}

/**
 * Convert one node into a React element. Returns `null` for nodes that
 * shouldn't render at all (e.g. invisible — though the visible-filter
 * step strips those before this is called).
 */
export function nodeToHTML(
  node: CanonicalNode | AnnotatedNode,
  parentBBox: BoundingBox | null,
  parentLayoutMode: LayoutMode,
  ctx: NodeToHTMLContext,
  keyHint: string = "n",
): ReactElement | null {
  const annotated = node as AnnotatedNode;

  // GROUP / BOOLEAN_OPERATION — flatten: their children render as if they
  // were children of `node`'s parent. Returning the children directly
  // would lose React key uniqueness, so we wrap in a Fragment-shaped
  // container element, but in practice we recurse and let the caller
  // splat. To keep the function signature single-element, we render a
  // `<div>` with `display:contents` so it doesn't affect layout.
  if (isFlattenedWrapper(annotated)) {
    return renderFlattenedChildren(annotated, parentBBox, parentLayoutMode, ctx, keyHint);
  }

  // Icon cluster — single placeholder for now. U7 swaps in real <img>.
  if (isIconCluster(annotated)) {
    return renderClusterPlaceholder(annotated, parentBBox, parentLayoutMode, keyHint);
  }

  // TEXT
  if (annotated.type === "TEXT") {
    return renderText(annotated, parentBBox, parentLayoutMode, keyHint);
  }

  // Default: container (FRAME / RECTANGLE / VECTOR / ELLIPSE / etc.)
  return renderContainer(annotated, parentBBox, parentLayoutMode, ctx, keyHint);
}

// ─── Wrapper flattening ──────────────────────────────────────────────────────

function isFlattenedWrapper(node: AnnotatedNode): boolean {
  // BOOLEAN_OPERATION is a graphical wrapper that the icon-cluster path
  // already catches when it has shape children — but if it's a top-level
  // boolean op without shapes we still flatten its children rather than
  // emitting an empty div.
  if (node.type === "GROUP") return true;
  if (node.type === "BOOLEAN_OPERATION" && !isIconCluster(node)) return true;
  return false;
}

function renderFlattenedChildren(
  node: AnnotatedNode,
  parentBBox: BoundingBox | null,
  parentLayoutMode: LayoutMode,
  ctx: NodeToHTMLContext,
  keyHint: string,
): ReactElement | null {
  const children = Array.isArray(node.children) ? node.children : [];
  if (children.length === 0) return null;

  // `display: contents` keeps the wrapper out of layout so children render
  // as direct geometric children of the real parent. Better than emitting
  // a Fragment because each child still gets a stable key path and we
  // preserve a `data-flattened-from` hint for debugging.
  const elements = children
    .map((c, i) => nodeToHTML(c, parentBBox, parentLayoutMode, ctx, `${keyHint}.f${i}`))
    .filter((e): e is ReactElement => e !== null);

  return createElement(
    "div",
    {
      key: keyHint,
      "data-flattened-from": node.type,
      "data-figma-id": node.id,
      style: { display: "contents" },
    },
    ...elements,
  );
}

// ─── Cluster placeholder ─────────────────────────────────────────────────────

function renderClusterPlaceholder(
  node: AnnotatedNode,
  parentBBox: BoundingBox | null,
  parentLayoutMode: LayoutMode,
  keyHint: string,
): ReactElement {
  const url = resolveClusterURL(node);
  const positioning = positionStyle(node, parentBBox, parentLayoutMode);
  const sizing = sizeStyle(node);

  if (url) {
    // U7 path — once asset-export client lands, render the real asset.
    return createElement("img", {
      key: keyHint,
      src: url,
      alt: node.name ?? "icon",
      "data-figma-id": node.id,
      "data-cluster": "true",
      draggable: false,
      style: { ...positioning, ...sizing, display: "block", objectFit: "contain" },
    });
  }

  return createElement("div", {
    key: keyHint,
    "data-cluster-pending": "true",
    "data-figma-id": node.id,
    "aria-label": node.name ?? "icon (rendering)",
    style: {
      ...positioning,
      ...sizing,
      border: "1px dashed rgba(94, 234, 212, 0.6)",
      borderRadius: 4,
      background: "rgba(94, 234, 212, 0.05)",
      boxSizing: "border-box",
    },
  });
}

// ─── TEXT ────────────────────────────────────────────────────────────────────

function renderText(
  node: AnnotatedNode,
  parentBBox: BoundingBox | null,
  parentLayoutMode: LayoutMode,
  keyHint: string,
): ReactElement {
  const positioning = positionStyle(node, parentBBox, parentLayoutMode);
  const sizing = sizeStyle(node);
  const style = node.style ?? {};
  // Per U13: TEXT atomics with empty/missing/non-SOLID `fills` default to
  // `#000` rather than inheriting from the parent — the spike found that
  // inheriting often produced white-on-white in dark surfaces.
  const color = firstSolidColor(node.fills) ?? "#000";

  const textStyle: CSSProperties = {
    ...positioning,
    ...sizing,
    color,
    fontFamily: style.fontFamily ?? "inherit",
    fontWeight: style.fontWeight ?? 400,
    fontSize:
      typeof style.fontSize === "number" ? `${style.fontSize}px` : undefined,
    letterSpacing:
      typeof style.letterSpacing === "number"
        ? `${style.letterSpacing}px`
        : undefined,
    lineHeight:
      typeof style.lineHeightPx === "number"
        ? `${style.lineHeightPx}px`
        : undefined,
    textAlign: textAlign(style.textAlignHorizontal),
    fontStyle: style.italic ? "italic" : undefined,
    // pre-wrap honors explicit `\n` in `characters` AND wraps long lines
    // when the bbox can hold multiple. Figma sizes the bbox to fit the
    // text under "Auto height"; for "Fixed size" text the bbox is the
    // user's chosen rect and `overflow: hidden` clips overflow. Without
    // textAutoResize on canonical_tree we can't pick the right mode per
    // node — pre-wrap is the safer default for both. See
    // docs/issues/2026-05-05-text-auto-resize.md.
    whiteSpace: "pre-wrap",
    wordBreak: "break-word",
    overflow: "hidden",
    display: "inline-block",
    boxSizing: "border-box",
  };

  return createElement(
    "span",
    {
      key: keyHint,
      "data-figma-id": node.id,
      "data-figma-type": "TEXT",
      "data-state-group": node.__stateGroup,
      style: textStyle,
    },
    node.characters ?? "",
  );
}

// ─── Container (FRAME / shape) ───────────────────────────────────────────────

function renderContainer(
  node: AnnotatedNode,
  parentBBox: BoundingBox | null,
  parentLayoutMode: LayoutMode,
  ctx: NodeToHTMLContext,
  keyHint: string,
): ReactElement {
  const ownLayoutMode: LayoutMode = node.layoutMode ?? null;
  const isAutolayout = ownLayoutMode === "HORIZONTAL" || ownLayoutMode === "VERTICAL";

  const baseStyle: CSSProperties = {
    ...positionStyle(node, parentBBox, parentLayoutMode),
    ...sizeStyle(node),
    boxSizing: "border-box",
  };

  // FRAME defaults clipsContent=true; explicit false disables it. Other
  // node types only clip when explicitly requested.
  const clip =
    node.clipsContent === true ||
    (node.type === "FRAME" && node.clipsContent !== false);
  if (clip) baseStyle.overflow = "hidden";

  // Background — first SOLID fill or first IMAGE fill.
  const bg = backgroundStyle(node.fills, ctx.imageRefs);
  Object.assign(baseStyle, bg);

  // Border / radius
  if (typeof node.cornerRadius === "number") {
    baseStyle.borderRadius = `${node.cornerRadius}px`;
  } else if (node.rectangleCornerRadii) {
    const [tl, tr, br, bl] = node.rectangleCornerRadii;
    baseStyle.borderRadius = `${tl}px ${tr}px ${br}px ${bl}px`;
  }
  const strokeColor = firstSolidColor(node.strokes);
  if (strokeColor && typeof node.strokeWeight === "number" && node.strokeWeight > 0) {
    baseStyle.border = `${node.strokeWeight}px solid ${strokeColor}`;
  }

  // Auto-layout flex props
  if (isAutolayout) {
    baseStyle.display = "flex";
    baseStyle.flexDirection = ownLayoutMode === "HORIZONTAL" ? "row" : "column";
    if (typeof node.itemSpacing === "number") {
      baseStyle.gap = `${node.itemSpacing}px`;
    }
    if (typeof node.paddingLeft === "number") baseStyle.paddingLeft = `${node.paddingLeft}px`;
    if (typeof node.paddingRight === "number") baseStyle.paddingRight = `${node.paddingRight}px`;
    if (typeof node.paddingTop === "number") baseStyle.paddingTop = `${node.paddingTop}px`;
    if (typeof node.paddingBottom === "number") baseStyle.paddingBottom = `${node.paddingBottom}px`;
    baseStyle.justifyContent = primaryAxisAlign(node.primaryAxisAlignItems);
    baseStyle.alignItems = counterAxisAlign(node.counterAxisAlignItems);
  }

  // Opacity (visible-filter already removed near-zero opacity nodes; what's
  // left is rendered honestly).
  if (typeof node.opacity === "number" && node.opacity < 1) {
    baseStyle.opacity = node.opacity;
  }

  const children = Array.isArray(node.children) ? node.children : [];
  const childBBox = node.absoluteBoundingBox ?? parentBBox;
  const childElements = children
    .map((c, i) => nodeToHTML(c, childBBox, ownLayoutMode, ctx, `${keyHint}.${i}`))
    .filter((e): e is ReactElement => e !== null);

  return createElement(
    "div",
    {
      key: keyHint,
      "data-figma-id": node.id,
      "data-figma-type": node.type,
      "data-state-group": node.__stateGroup,
      style: baseStyle,
    },
    ...childElements,
  );
}

// ─── Style helpers ───────────────────────────────────────────────────────────

function positionStyle(
  node: CanonicalNode,
  parentBBox: BoundingBox | null,
  parentLayoutMode: LayoutMode,
): CSSProperties {
  // In autolayout: don't position — let flex handle it. Only emit
  // flex-grow / align-self where the node opts in.
  if (parentLayoutMode === "HORIZONTAL" || parentLayoutMode === "VERTICAL") {
    const out: CSSProperties = { position: "relative" };
    if (node.layoutGrow === 1) out.flexGrow = 1;
    if (node.layoutAlign === "STRETCH") out.alignSelf = "stretch";
    return out;
  }
  // Absolute path
  const bb = node.absoluteBoundingBox;
  if (!bb || !parentBBox) {
    return { position: "absolute", left: 0, top: 0 };
  }
  return {
    position: "absolute",
    left: `${bb.x - parentBBox.x}px`,
    top: `${bb.y - parentBBox.y}px`,
  };
}

function sizeStyle(node: CanonicalNode): CSSProperties {
  const bb = node.absoluteBoundingBox;
  if (!bb) return {};
  return { width: `${bb.width}px`, height: `${bb.height}px` };
}

function backgroundStyle(
  fills: Paint[] | undefined,
  imageRefs: ImageRefMap,
): CSSProperties {
  if (!Array.isArray(fills) || fills.length === 0) return {};
  for (const f of fills) {
    if (f.visible === false) continue;
    if (isSolidPaint(f)) {
      const c = rgbaToCSS(f.color, f.opacity);
      if (c) return { backgroundColor: c };
    }
    if (isImagePaint(f) && f.imageRef) {
      const url = imageRefs[f.imageRef];
      if (!url) {
        // Placeholder rendering — keeps the slot occupied so layout
        // doesn't reflow once U7's asset-export client populates the
        // imageRef → URL map. We avoid emitting an `<img>` with a
        // broken src so the surface stays clean during PNG snapshots.
        return imagePlaceholderStyle();
      }
      const out: CSSProperties = {
        backgroundImage: `url(${JSON.stringify(url)})`,
        backgroundRepeat: "no-repeat",
        backgroundPosition: "center",
      };
      switch (f.scaleMode) {
        case "FIT":
          out.backgroundSize = "contain";
          break;
        case "FILL":
          out.backgroundSize = "cover";
          break;
        case "STRETCH":
          out.backgroundSize = "100% 100%";
          break;
        case "TILE":
          out.backgroundSize = "auto";
          out.backgroundRepeat = "repeat";
          break;
        default:
          out.backgroundSize = "cover";
      }
      return out;
    }
  }
  return {};
}

/**
 * Soft grey checker for IMAGE fills whose `imageRef` isn't yet in the
 * resolution map (U7 plumbing pending). No broken-image glyph; just a
 * neutral placeholder that visually communicates "image goes here".
 */
function imagePlaceholderStyle(): CSSProperties {
  return {
    backgroundColor: "rgba(0, 0, 0, 0.04)",
    backgroundImage:
      "linear-gradient(45deg, rgba(0,0,0,0.06) 25%, transparent 25%, transparent 75%, rgba(0,0,0,0.06) 75%), " +
      "linear-gradient(45deg, rgba(0,0,0,0.06) 25%, transparent 25%, transparent 75%, rgba(0,0,0,0.06) 75%)",
    backgroundSize: "12px 12px",
    backgroundPosition: "0 0, 6px 6px",
  };
}

function firstSolidColor(paints: Paint[] | undefined): string | null {
  if (!Array.isArray(paints)) return null;
  for (const p of paints) {
    if (p.visible === false) continue;
    if (isSolidPaint(p)) {
      const c = rgbaToCSS(p.color, p.opacity);
      if (c) return c;
    }
  }
  return null;
}

function isSolidPaint(p: Paint): p is SolidPaint {
  return p.type === "SOLID";
}

function isImagePaint(p: Paint): p is ImagePaint {
  return p.type === "IMAGE";
}

function rgbaToCSS(color: RGBA | undefined, opacity: number | undefined): string | null {
  if (!color) return null;
  const r = clamp255(color.r);
  const g = clamp255(color.g);
  const b = clamp255(color.b);
  const a =
    typeof opacity === "number"
      ? opacity
      : typeof color.a === "number"
        ? color.a
        : 1;
  if (a >= 1) {
    return `rgb(${r}, ${g}, ${b})`;
  }
  return `rgba(${r}, ${g}, ${b}, ${a})`;
}

function clamp255(v: number): number {
  // Figma RGB is 0–1; clamp + round.
  const x = Math.max(0, Math.min(1, v));
  return Math.round(x * 255);
}

function textAlign(t: string | undefined): CSSProperties["textAlign"] {
  switch (t) {
    case "LEFT":
      return "left";
    case "CENTER":
      return "center";
    case "RIGHT":
      return "right";
    case "JUSTIFIED":
      return "justify";
    default:
      return undefined;
  }
}

function primaryAxisAlign(a: string | undefined): CSSProperties["justifyContent"] {
  switch (a) {
    case "MIN":
      return "flex-start";
    case "CENTER":
      return "center";
    case "MAX":
      return "flex-end";
    case "SPACE_BETWEEN":
      return "space-between";
    default:
      return "flex-start";
  }
}

function counterAxisAlign(a: string | undefined): CSSProperties["alignItems"] {
  switch (a) {
    case "MIN":
      return "flex-start";
    case "CENTER":
      return "center";
    case "MAX":
      return "flex-end";
    case "BASELINE":
      return "baseline";
    default:
      return "flex-start";
  }
}
