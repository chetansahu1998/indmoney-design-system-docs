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

import { isIconCluster } from "./icon-cluster-resolver";
import { classifyNode } from "./node-classifier";
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
  /** image-fill ref → URL (raster fills proxied through ds-service). */
  imageRefs: ImageRefMap;
  /**
   * Resolved icon-cluster `<img>` URLs keyed by canonical_tree node id.
   * Populated by LeafFrameRenderer's `useIconClusterURLs` hook from
   * Figma's /v1/images node-render endpoint, then served via the
   * existing `?at=<token>` signed URL flow. A miss falls through to
   * the dashed-border placeholder so the canvas degrades gracefully
   * while exports are in flight. ReadonlyMap so callers can pass the
   * hook result directly (it's frozen) without a defensive copy.
   */
  clusterURLs: ReadonlyMap<string, string>;
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

  // TEXT renders before classification — text nodes never rasterize.
  if (annotated.type === "TEXT") {
    return renderText(annotated, parentBBox, parentLayoutMode, keyHint);
  }

  // node-classifier combines name patterns (Icons/.../, Illustrations/,
  // Yes/No/24px slash variants) with the structural icon-cluster
  // heuristic. Single source of truth for "should this rasterize?" so
  // useIconClusterURLs's collector and this renderer can never disagree.
  const klass = classifyNode(annotated);
  if (klass.kind === "icon" || klass.kind === "illustration" || klass.kind === "shape") {
    return renderClusterPlaceholder(annotated, parentBBox, parentLayoutMode, ctx, keyHint);
  }

  // Default: container (layouts, named UI components, generic FRAMEs).
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
  ctx: NodeToHTMLContext,
  keyHint: string,
): ReactElement {
  const url = node.id ? ctx.clusterURLs.get(node.id) ?? null : null;
  const positioning = positionStyle(node, parentBBox, parentLayoutMode);
  const sizing = sizeStyle(node);

  if (url) {
    // Cluster export resolved — render the rasterized icon. onError
    // retries with exponential backoff before falling back to the
    // dashed placeholder.
    //
    // Why retry: ds-service serves 425 (Too Early) when Figma's
    // synchronous render budget elapses — most common when a leaf
    // mounts 30+ standalone shapes simultaneously and Figma's 5
    // req/sec PAT cap saturates. Browsers don't auto-retry image
    // fetches on 425. The retry path keeps the same signed URL
    // (token has 60s TTL — comfortably covers 3 retries at 1.5s,
    // 3s, 6s spacing). After 3 failures we surrender to the dashed
    // placeholder so the canvas isn't held hostage by one stuck
    // render.
    return createElement("img", {
      key: keyHint,
      src: url,
      alt: node.name ?? "icon",
      "data-figma-id": node.id,
      "data-cluster": "true",
      draggable: false,
      onError: (e: { currentTarget: HTMLImageElement }) => {
        const img = e.currentTarget;
        const tries = parseInt(img.getAttribute("data-retry") ?? "0", 10);
        const MAX_TRIES = 3;
        if (tries < MAX_TRIES) {
          // Exponential backoff: 1.5s, 3s, 6s. Stay under the 60s
          // mint-token TTL so the same URL still verifies.
          const delay = 1500 * 2 ** tries;
          img.setAttribute("data-retry", String(tries + 1));
          setTimeout(() => {
            // Cache-bust on retry so the browser re-issues the
            // request even if it cached the 425 response.
            img.src = url + (url.includes("?") ? "&" : "?") + "_r=" + (tries + 1);
          }, delay);
          return;
        }
        // Final failure — degrade to the dashed placeholder.
        img.removeAttribute("src");
        img.setAttribute("data-cluster-failed", "true");
        img.style.border = "1px dashed rgba(94, 234, 212, 0.6)";
        img.style.borderRadius = "4px";
        img.style.background = "rgba(94, 234, 212, 0.05)";
        // eslint-disable-next-line no-console
        console.warn("[icon-cluster] image load failed after retries", { id: node.id, url });
      },
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

  // Effects — drop / inner shadow + layer / background blur. Real card
  // elevation in INDmoney files (e.g. Mutual Funds V2 Dashboard) uses
  // 3-stack DROP_SHADOW for Material-style depth. Without this branch
  // those cards render flat instead of elevated.
  const effectStyle = effectsToStyle(node.effects);
  if (effectStyle) Object.assign(baseStyle, effectStyle);

  // Blend mode — Figma's NORMAL is the CSS default; PASS_THROUGH is
  // group-only and doesn't have a CSS counterpart, so we skip it.
  // Anything else (MULTIPLY / SCREEN / OVERLAY / etc.) maps directly
  // to mix-blend-mode.
  if (typeof node.blendMode === "string" && node.blendMode !== "NORMAL" && node.blendMode !== "PASS_THROUGH") {
    // CSSProperties.mixBlendMode is a string-literal union; we can't
    // narrow at the type level without enumerating every Figma mode,
    // so cast through `as` after lower-casing.
    baseStyle.mixBlendMode = blendModeToCSS(node.blendMode) as CSSProperties["mixBlendMode"];
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
    // GRADIENT_LINEAR / RADIAL / ANGULAR / DIAMOND — emit a CSS
    // gradient. Figma's gradientHandlePositions are 3 normalized
    // {x,y} points in node-local 0..1 coords; we approximate the
    // CSS angle from the first two handles. Stops carry position
    // (0..1) + RGBA already.
    const grad = gradientPaintToCSS(f);
    if (grad) {
      return { backgroundImage: grad };
    }
  }
  return {};
}

/**
 * Convert a Figma GRADIENT_* paint to a CSS gradient string. Returns
 * null for unknown gradient kinds (caller falls through to next paint).
 *
 * - LINEAR  → `linear-gradient(<angle>, <stops>)`
 * - RADIAL  → `radial-gradient(circle at <handle1>, <stops>)`
 * - ANGULAR → `conic-gradient(from <angle> at center, <stops>)`
 *   (Figma's "angular" is CSS conic; close enough for canvas-v2 reads.)
 * - DIAMOND → no exact CSS counterpart; approximate as a linear gradient
 *   at 45deg (rare in practice; document and revisit if encountered).
 */
function gradientPaintToCSS(p: Paint): string | null {
  if (!isGradientPaint(p)) return null;
  const stops = (p.gradientStops ?? [])
    .filter((s): s is { position: number; color: RGBA } => !!s && !!s.color)
    .map((s) => {
      const c = rgbaToCSS(s.color, undefined) ?? "transparent";
      const pct = clamp(s.position * 100, 0, 100).toFixed(2);
      return `${c} ${pct}%`;
    })
    .join(", ");
  if (!stops) return null;

  const handles = p.gradientHandlePositions ?? [];
  const h0 = handles[0] ?? { x: 0.5, y: 0 };
  const h1 = handles[1] ?? { x: 0.5, y: 1 };

  switch (p.type) {
    case "GRADIENT_LINEAR": {
      // CSS `linear-gradient` angle is measured clockwise from the
      // top (0deg = upward). Figma handles run from start → end in
      // node-local coords; convert that vector to a CSS angle.
      const dx = h1.x - h0.x;
      const dy = h1.y - h0.y;
      const angleRad = Math.atan2(dx, -dy); // CSS angle = atan2(dx, -dy)
      const angleDeg = (angleRad * 180) / Math.PI;
      return `linear-gradient(${angleDeg.toFixed(2)}deg, ${stops})`;
    }
    case "GRADIENT_RADIAL": {
      // Center at h0; outer-stop boundary at h1.
      const cx = (clamp(h0.x, 0, 1) * 100).toFixed(2);
      const cy = (clamp(h0.y, 0, 1) * 100).toFixed(2);
      return `radial-gradient(circle at ${cx}% ${cy}%, ${stops})`;
    }
    case "GRADIENT_ANGULAR": {
      const cx = (clamp(h0.x, 0, 1) * 100).toFixed(2);
      const cy = (clamp(h0.y, 0, 1) * 100).toFixed(2);
      return `conic-gradient(at ${cx}% ${cy}%, ${stops})`;
    }
    case "GRADIENT_DIAMOND": {
      // No clean CSS counterpart; degrade to a linear at 45deg.
      // Tracked in classifier docs; revisit when a real example
      // shows up in canonical_tree data.
      return `linear-gradient(45deg, ${stops})`;
    }
    default:
      return null;
  }
}

function isGradientPaint(p: Paint): p is import("./types").GradientPaint {
  return (
    p.type === "GRADIENT_LINEAR" ||
    p.type === "GRADIENT_RADIAL" ||
    p.type === "GRADIENT_ANGULAR" ||
    p.type === "GRADIENT_DIAMOND"
  );
}

function clamp(n: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, n));
}

/**
 * Convert Figma's `effects` array to CSS box-shadow + filter +
 * backdrop-filter. Multiple drop/inner shadows stack via comma-list.
 * Multiple blurs chain via space-separated filter functions.
 */
function effectsToStyle(
  effects: import("./types").Effect[] | undefined,
): CSSProperties | null {
  if (!Array.isArray(effects) || effects.length === 0) return null;
  const boxShadows: string[] = [];
  const filters: string[] = [];
  const backdropFilters: string[] = [];
  for (const e of effects) {
    if (e.visible === false) continue;
    switch (e.type) {
      case "DROP_SHADOW": {
        const c = rgbaToCSS(e.color, undefined);
        if (!c) break;
        const ox = e.offset?.x ?? 0;
        const oy = e.offset?.y ?? 0;
        const r = e.radius ?? 0;
        const sp = typeof e.spread === "number" ? `${e.spread}px ` : "";
        boxShadows.push(`${ox}px ${oy}px ${r}px ${sp}${c}`);
        break;
      }
      case "INNER_SHADOW": {
        const c = rgbaToCSS(e.color, undefined);
        if (!c) break;
        const ox = e.offset?.x ?? 0;
        const oy = e.offset?.y ?? 0;
        const r = e.radius ?? 0;
        const sp = typeof e.spread === "number" ? `${e.spread}px ` : "";
        boxShadows.push(`inset ${ox}px ${oy}px ${r}px ${sp}${c}`);
        break;
      }
      case "LAYER_BLUR":
        if (typeof e.radius === "number" && e.radius > 0) {
          filters.push(`blur(${e.radius}px)`);
        }
        break;
      case "BACKGROUND_BLUR":
        if (typeof e.radius === "number" && e.radius > 0) {
          backdropFilters.push(`blur(${e.radius}px)`);
        }
        break;
    }
  }
  if (boxShadows.length === 0 && filters.length === 0 && backdropFilters.length === 0) {
    return null;
  }
  const out: CSSProperties = {};
  if (boxShadows.length) out.boxShadow = boxShadows.join(", ");
  if (filters.length) out.filter = filters.join(" ");
  if (backdropFilters.length) {
    out.backdropFilter = backdropFilters.join(" ");
    // Safari/older Chrome prefix.
    (out as Record<string, unknown>)["WebkitBackdropFilter"] = backdropFilters.join(" ");
  }
  return out;
}

/**
 * Map Figma blend mode to CSS mix-blend-mode value. Most names match
 * verbatim once lower-cased and underscores replaced with hyphens.
 */
function blendModeToCSS(figma: string): string {
  return figma.toLowerCase().replace(/_/g, "-");
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
