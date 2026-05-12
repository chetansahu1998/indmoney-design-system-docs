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
  TextStyle,
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
  /**
   * Cluster node ids the server explicitly marked as failed in the asset
   * stream (asset_stream.go emits `asset-failed` events with a reason).
   * Threaded so `renderClusterPlaceholder` can distinguish "still
   * loading" (no URL yet, no failed entry) from "server already gave up"
   * (failed entry present). Pre-2026-05-12 both rendered as
   * data-cluster-pending="true" with the same pulsing dashed border,
   * which gave users the impression that 15+ icons on every leaf were
   * "loading forever" when in fact the stream had completed and the
   * server had emitted asset-failed events that the renderer silently
   * ignored. Defaults to empty when the asset stream is unwired (tests,
   * SSR).
   */
  clusterFailedIDs?: ReadonlySet<string>;
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

  // Screen-root guard. The canonical_tree's document root is by
  // definition a layout surface (a phone screen, an overlay/bottomsheet,
  // a card stack), never an icon or illustration. The cluster heuristic
  // can match a small-enough screen FRAME (e.g. a 375×521 overlay with
  // shape-heavy content like a chart + Footer CTA) and rasterize the
  // ENTIRE screen as one PNG — losing all atomic interactivity, text
  // selection, override editing, and the inspector. Pin the root to the
  // container path regardless of classification.
  //
  // Detection: keyHint==="root" matches the call site in
  // LeafFrameRenderer (line 597). Recursive descents pass keyHint like
  // "root.0.1.f3" so they never collide with this guard. Pre-2026-05-08
  // overlay screens in insurance-insurance-whatsapp-creative rendered
  // as single cluster <img>s with childCount=1 and zero hasContent.
  const isScreenRoot = keyHint === "root";

  // GROUP / BOOLEAN_OPERATION — flatten: their children render as if they
  // were children of `node`'s parent. Returning the children directly
  // would lose React key uniqueness, so we wrap in a Fragment-shaped
  // container element, but in practice we recurse and let the caller
  // splat. To keep the function signature single-element, we render a
  // `<div>` with `display:contents` so it doesn't affect layout.
  if (!isScreenRoot && isFlattenedWrapper(annotated)) {
    return renderFlattenedChildren(annotated, parentBBox, parentLayoutMode, ctx, keyHint);
  }

  // TEXT renders before classification — text nodes never rasterize.
  if (annotated.type === "TEXT") {
    return renderText(annotated, parentBBox, parentLayoutMode, keyHint);
  }

  // LINE / zero-axis VECTOR CSS fast-path (2026-05-12 + round-2 audit
  // P9). Three node shapes hit this:
  //   • type: "LINE" — Figma's native line primitive (P2).
  //   • type: "VECTOR" with bbox.height < 1 — a horizontal hairline
  //     authored as a stroked vector path (very common for header /
  //     between-card dividers; both round-2 audits independently
  //     surfaced these as missing — 5 dividers on the Filters BS, the
  //     "Realised Gains" header divider on Tax Transactions).
  //   • type: "VECTOR" with bbox.width < 1 — vertical hairline,
  //     symmetric to the horizontal case.
  // All three render identically: emit a thin <div> with the stroke
  // colour as border. Without this fast-path the VECTORs fall through
  // to the cluster placeholder which either shows a dashed teal box
  // (no URL) or drops them entirely when the cluster never resolves
  // for a 0-height bbox.
  if (!isScreenRoot && isZeroAxisLineLike(annotated)) {
    return renderLine(annotated, parentBBox, parentLayoutMode, keyHint);
  }

  // node-classifier combines name patterns (Icons/.../, Illustrations/,
  // Yes/No/24px slash variants) with the structural icon-cluster
  // heuristic. Single source of truth for "should this rasterize?" so
  // useIconClusterURLs's collector and this renderer can never disagree.
  if (!isScreenRoot) {
    const klass = classifyNode(annotated);
    if (klass.kind === "icon" || klass.kind === "illustration" || klass.kind === "shape") {
      return renderClusterPlaceholder(annotated, parentBBox, parentLayoutMode, ctx, keyHint);
    }
  }

  // Default: container (layouts, named UI components, generic FRAMEs).
  return renderContainer(annotated, parentBBox, parentLayoutMode, ctx, keyHint);
}

// ─── Wrapper flattening ──────────────────────────────────────────────────────

export function isFlattenedWrapper(node: AnnotatedNode): boolean {
  // GROUP and BOOLEAN_OPERATION are both graphical wrappers that the
  // icon-cluster path catches when their subtree is shape-heavy. If the
  // wrapper qualifies as a cluster we want it to rasterize as one PNG
  // (via renderClusterPlaceholder) — flattening here would render the
  // children individually and orphan the parent's pre-rendered cluster
  // PNG (Go's walkClusters captured the wrapper id; TS picking child ids
  // means each child mints a URL the cache doesn't have → 404 → dashed
  // placeholders). Real production case from indlearn-learn-revamp 2026-
  // 05-08: course-tile GROUPs (180×218, 30 vector leaves, 3 text leaves)
  // were unconditionally flattened, leaving the FRAME child to claim
  // cluster — that FRAME id has no cache entry, so 100 thumbnails
  // rendered as gradient-only placeholders instead of full illustrations.
  //
  // Pre-fix: GROUP was unconditionally flattened, BOOLEAN_OPERATION
  // already had the guard. Symmetry restored here.
  if (node.type === "GROUP" && !isIconCluster(node)) return true;
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

  // Autolayout-parent guard (2026-05-12, fidelity audit Bug P5).
  //
  // Pre-fix: `display: contents` was emitted unconditionally and the
  // parent's layoutMode/bbox were forwarded to children. When the
  // parent was HORIZONTAL/VERTICAL autolayout, children with absolute
  // coordinates inside the GROUP got positioned RELATIVE to the
  // autolayout flow (positionStyle short-circuits to
  // `{position:"relative"}` for autolayout, discarding left/top), so
  // every authored "card body as GROUP{rect bg + icon + label + CTA}
  // inside a vertical column" collapsed into stacked flex items.
  // Production case: Help Center / Refer & Earn cards in the
  // INDstocks referral V2 file rendered as plain stacked labels with
  // no card backgrounds — the GROUP's children's absolute coords were
  // silently zeroed by the flex flow.
  //
  // Fix: when the wrapper sits inside autolayout AND has its own
  // absoluteBoundingBox, emit a single positioned-and-sized wrapper
  // (one flex item from the autolayout's perspective) and recurse
  // INTO it with parentLayoutMode=null + parentBBox=group.bbox so the
  // children's absolute coordinates resolve correctly inside the
  // wrapper. The flex parent sees one box; everything inside that box
  // positions exactly as Figma authored.
  if (
    parentLayoutMode !== null &&
    node.absoluteBoundingBox &&
    Number.isFinite(node.absoluteBoundingBox.width) &&
    Number.isFinite(node.absoluteBoundingBox.height)
  ) {
    const wrapperPos = positionStyle(node, parentBBox, parentLayoutMode);
    const wrapperSize = sizeStyle(node, parentLayoutMode);
    const elements = children
      .map((c, i) =>
        nodeToHTML(c, node.absoluteBoundingBox ?? null, null, ctx, `${keyHint}.f${i}`),
      )
      .filter((e): e is ReactElement => e !== null);
    return createElement(
      "div",
      {
        key: keyHint,
        "data-flattened-from": node.type,
        "data-figma-id": node.id,
        "data-flatten-mode": "wrapped-for-autolayout",
        style: { ...wrapperPos, ...wrapperSize },
      },
      ...elements,
    );
  }

  // Non-autolayout parent: `display: contents` keeps the wrapper out
  // of layout so children render as direct geometric children of the
  // real parent. Better than emitting a Fragment because each child
  // still gets a stable key path and we preserve a `data-flattened-
  // from` hint for debugging.
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
  const sizing = sizeStyle(node, parentLayoutMode);

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

  // Failed-vs-pending fork. Pre-2026-05-12 both paths rendered the same
  // pulsing teal dashed border, which made server-side render failures
  // visually indistinguishable from in-flight renders. The asset stream
  // emits `asset-failed` events with reasons like `render_timeout` /
  // `default_tier_not_persisted`; we surface those as a muted,
  // non-pulsing placeholder so the user understands the asset is not
  // coming back without a fresh sync. Hover title carries the cluster id
  // so designers can copy-paste it into the blocklist admin UI.
  const failed = node.id ? ctx.clusterFailedIDs?.has(node.id) ?? false : false;
  return createElement("div", {
    key: keyHint,
    "data-cluster-pending": failed ? undefined : "true",
    "data-cluster-failed": failed ? "true" : undefined,
    "data-figma-id": node.id,
    "aria-label": failed
      ? `${node.name ?? "icon"} (render failed)`
      : `${node.name ?? "icon"} (rendering)`,
    title: failed
      ? `Cluster render failed: ${node.id ?? ""}. Re-sync the file to retry.`
      : undefined,
    style: {
      ...positioning,
      ...sizing,
      // Failed: flat muted slate; Pending: teal pulse (unchanged).
      border: failed
        ? "1px dashed rgba(148, 163, 184, 0.35)"
        : "1px dashed rgba(94, 234, 212, 0.6)",
      borderRadius: 4,
      background: failed
        ? "rgba(148, 163, 184, 0.04)"
        : "rgba(94, 234, 212, 0.05)",
      boxSizing: "border-box",
      opacity: failed ? 0.5 : 1,
    },
  });
}

// ─── LINE (CSS fast-path) ────────────────────────────────────────────────────

/**
 * Renders a Figma LINE as a CSS-bordered div. LINEs have a single
 * SOLID stroke whose width is the visible thickness; bbox carries one
 * zero or near-zero dimension that picks the orientation.
 *
 * Pre-2026-05-12 LINEs routed through `renderClusterPlaceholder` and
 * fell back to the dashed teal placeholder when no Figma export was
 * cached. With this fast-path we never need a network round-trip for
 * what is fundamentally a 1px border.
 *
 * 2026-05-12 round-2 audit P9: extended to cover VECTOR nodes whose
 * bbox collapses to a single axis (height < 1 or width < 1) and carry
 * a single SOLID stroke. Figma exports horizontal hairlines as zero-
 * height VECTORs ("Vector 2779" — 343 × 0.000021 with strokeWeight:1)
 * that visually function identically to LINE. Both round-2 audit
 * agents independently surfaced this as missing dividers.
 */
function isZeroAxisLineLike(node: AnnotatedNode): boolean {
  if (node.type === "LINE") return true;
  if (node.type !== "VECTOR") return false;
  const bb = node.absoluteBoundingBox;
  if (!bb) return false;
  // "Near-zero" — Figma's degenerate stroked-vector exports report
  // ~2e-5 px on the collapsed axis (floating-point noise).
  if (bb.height >= 1 && bb.width >= 1) return false;
  // Must have a stroke (otherwise it's an invisible vector that
  // shouldn't render at all).
  const strokes = (node as unknown as { strokes?: Paint[] }).strokes;
  if (!Array.isArray(strokes) || strokes.length === 0) return false;
  // P17 — snap near-zero rotation to 0 so the transform path doesn't
  // drop sub-pixel boxes. The function only inspects, but downstream
  // renderLine reads node.absoluteBoundingBox which already encodes
  // the axis correctly; rotation only matters if we ever route
  // through the transform path.
  return true;
}

function renderLine(
  node: AnnotatedNode,
  parentBBox: BoundingBox | null,
  parentLayoutMode: LayoutMode,
  keyHint: string,
): ReactElement {
  const positioning = positionStyle(node, parentBBox, parentLayoutMode);
  const sizing = sizeStyle(node, parentLayoutMode);

  // Stroke colour: first SOLID stroke wins. Fall back to a faint grey
  // so the line is still visible if the canonical_tree omits strokes
  // (very rare; defensive).
  const strokes = Array.isArray((node as unknown as { strokes?: Paint[] }).strokes)
    ? ((node as unknown as { strokes?: Paint[] }).strokes as Paint[])
    : [];
  let color = "rgba(0, 0, 0, 0.12)";
  for (const s of strokes) {
    if (s && s.type === "SOLID" && (s as SolidPaint).color) {
      const sp = s as SolidPaint;
      const op = (sp as unknown as { opacity?: number }).opacity;
      const c = rgbaToCSS(sp.color, op);
      if (c) {
        color = c;
        break;
      }
    }
  }
  // strokeWeight is on the node directly per Figma's REST schema.
  const sw = (node as unknown as { strokeWeight?: number }).strokeWeight;
  const weight = typeof sw === "number" && sw > 0 ? sw : 1;

  const bb = node.absoluteBoundingBox;
  const isVertical = bb ? bb.height > bb.width : false;

  // LINE bbox can be degenerate (0 in one axis). Set the long axis
  // from the bbox, force the short axis to the strokeWeight so the
  // div has visible footprint, and use border-* for the actual pixel.
  const linedStyle: Record<string, string | number> = {
    ...positioning,
    ...sizing,
    boxSizing: "border-box",
  };
  if (isVertical) {
    // Vertical line: take width from strokeWeight, paint with border-left.
    linedStyle.width = `${weight}px`;
    linedStyle.borderLeft = `${weight}px solid ${color}`;
  } else {
    linedStyle.height = `${weight}px`;
    linedStyle.borderTop = `${weight}px solid ${color}`;
  }

  return createElement("div", {
    key: keyHint,
    "data-figma-id": node.id,
    "data-figma-type": "LINE",
    "aria-hidden": true,
    style: linedStyle,
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
  const sizing = sizeStyle(node, parentLayoutMode);

  // 2026-05-12 fidelity audit P3 — uniform-override consult.
  // Figma's authoring tool routinely emits a "set every character to
  // the same override" pattern when designers tweak a property after
  // typing (e.g., make every char letterSpacing:0 even though
  // top-level style still says letterSpacing:1, or apply
  // textDecoration:UNDERLINE via the inspector for the whole label).
  // Pre-fix the renderer only read top-level `node.style.*`, so these
  // uniform-override patterns silently dropped — production case:
  // ticker text "Sensex 61,245" overflowed its 75px HUG container
  // because top-level letterSpacing:1 stayed applied. Mixed-run
  // overrides (multiple distinct indices) still fall through to
  // top-level here; segmenting into per-run <span>s is a separate
  // follow-up — flagged via console.warn so the bug stays visible.
  const overrides = node.characterStyleOverrides;
  const overrideTable = node.styleOverrideTable;
  let effectiveStyle: TextStyle = node.style ?? {};
  if (Array.isArray(overrides) && overrides.length > 0 && overrideTable) {
    const first = overrides[0] ?? 0;
    const uniform = overrides.every((idx) => (idx ?? 0) === first);
    if (uniform && first !== 0) {
      const override = overrideTable[String(first)];
      if (override) {
        effectiveStyle = { ...effectiveStyle, ...override };
      }
    } else if (!uniform) {
      // Mixed runs — fall through to top-level for now. Surfaces as
      // a console signal so the gap stays visible until the per-run
      // splitter ships.
      // eslint-disable-next-line no-console
      console.debug(
        "[nodeToHTML] mixed characterStyleOverrides not yet split:",
        node.id,
        node.name,
      );
    }
  }
  const style = effectiveStyle;
  // Per U13: TEXT atomics with empty/missing/non-SOLID `fills` default to
  // `#000` rather than inheriting from the parent — the spike found that
  // inheriting often produced white-on-white in dark surfaces.
  const color = firstSolidColor(node.fills) ?? "#000";

  // Text typography properties that Figma exposes on `style` but our
  // pre-2026-05-12 renderer silently dropped (fidelity audit P1 + P4):
  //   - textCase → CSS text-transform
  //   - textDecoration → CSS text-decoration
  //   - opacity (node-level) → CSS opacity (renderContainer already
  //     does this — renderText was missing the branch).
  // Mapping mirrors Figma's documented enum values; anything unknown
  // (SMALL_CAPS_FORCED, etc.) falls through to `undefined` rather than
  // emitting an invalid CSS value.
  const textTransform: CSSProperties["textTransform"] = (() => {
    switch (style.textCase) {
      case "UPPER":
        return "uppercase";
      case "LOWER":
        return "lowercase";
      case "TITLE":
        return "capitalize";
      default:
        return undefined;
    }
  })();
  const textDecoration: CSSProperties["textDecoration"] = (() => {
    switch (style.textDecoration) {
      case "UNDERLINE":
        return "underline";
      case "STRIKETHROUGH":
        return "line-through";
      default:
        return undefined;
    }
  })();
  const nodeOpacity =
    typeof node.opacity === "number" && node.opacity >= 0 && node.opacity < 1
      ? node.opacity
      : undefined;

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
    textTransform,
    textDecoration,
    opacity: nodeOpacity,
    display: "inline-block",
    boxSizing: "border-box",
  };

  // Honor textAutoResize when present (canonical_tree carries it from
  // Figma's /v1/files/.../nodes response). Pre-2026-05-09 the renderer
  // forced pre-wrap + bbox-width on every TEXT node — fine for HEIGHT-
  // autoresized text but wrong for WIDTH_AND_HEIGHT, which clipped or
  // wrapped labels the designer expected to flow on a single line
  // (production case: INDmoney splash "SSL Secured" wrapping to two
  // lines because the bbox width was forced from the rendered box,
  // not from the natural single-line glyph run).
  // Resolve the effective sizing mode. Figma exposes TWO ways to express
  // text-box sizing — and they appear on different node populations:
  //   1. `textAutoResize` — legacy field. Returned for standalone (non-
  //      autolayout) TEXT nodes. Values: WIDTH_AND_HEIGHT | HEIGHT |
  //      NONE | TRUNCATE.
  //   2. `layoutSizingHorizontal/Vertical` — modern field. Returned for
  //      every auto-layout child (TEXT or container). Values: HUG | FILL
  //      | FIXED.
  //
  // Real production canonical_trees (audited 2026-05-09) show 100% of
  // TEXT nodes inside auto-layout use the modern field with
  // `textAutoResize` ABSENT. The pre-fix renderer only honored
  // `textAutoResize` and so applied a fixed-width-wrap default to every
  // modern TEXT — wrapping single-line labels like "AES-256 SSL Secured"
  // (HUG) onto two lines.
  //
  // Resolution order: textAutoResize wins when present (older standalone
  // TEXT). Otherwise read layoutSizingHorizontal. If neither is set,
  // default to "size to content" (matches Figma's WIDTH_AND_HEIGHT
  // default for text without explicit sizing) so anonymous labels render
  // without surprise wrapping.
  type TextMode = "fit" | "fixed-wrap" | "fixed-clip" | "fixed-truncate";
  const resolved: TextMode = (() => {
    switch (node.textAutoResize) {
      case "WIDTH_AND_HEIGHT": return "fit";
      case "HEIGHT":           return "fixed-wrap";
      case "NONE":             return "fixed-clip";
      case "TRUNCATE":         return "fixed-truncate";
    }
    switch (node.layoutSizingHorizontal) {
      case "HUG":   return "fit";
      case "FILL":  return "fixed-wrap";
      case "FIXED": return "fixed-wrap"; // FIXED in autolayout still wraps to box, doesn't clip
    }
    // Neither set — Figma's default for unsized TEXT is auto-width.
    return "fit";
  })();

  switch (resolved) {
    case "fit":
      // Box hugs the single-line glyph run. Override the bbox width
      // applied by sizeStyle so text measures naturally — Figma already
      // sized the bbox to fit; the renderer doesn't need to recompute.
      // Position (left/top) still comes from positionStyle.
      textStyle.width = "auto";
      textStyle.height = "auto";
      textStyle.whiteSpace = "nowrap";
      // No overflow clip — HUG/WIDTH_AND_HEIGHT text is sized by Figma
      // to fit; clipping would falsify that contract.
      break;
    case "fixed-truncate":
      // Single line, fixed width, ellipsis on overflow.
      textStyle.whiteSpace = "nowrap";
      textStyle.overflow = "hidden";
      textStyle.textOverflow = "ellipsis";
      break;
    case "fixed-clip":
      // Both axes fixed — wrap and clip overflow.
      textStyle.whiteSpace = "pre-wrap";
      textStyle.wordBreak = "break-word";
      textStyle.overflow = "hidden";
      break;
    case "fixed-wrap":
    default:
      // Width fixed (from bbox), height auto. pre-wrap honors explicit
      // `\n` and wraps long lines.
      textStyle.whiteSpace = "pre-wrap";
      textStyle.wordBreak = "break-word";
      textStyle.overflow = "hidden";
      break;
  }

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
  const isScreenRoot = keyHint === "root";

  const baseStyle: CSSProperties = {
    ...positionStyle(node, parentBBox, parentLayoutMode),
    ...sizeStyle(node, parentLayoutMode),
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

  // Border / radius. 2026-05-12 fidelity audit P7: suppress
  // cornerRadius on the screen-root when the root looks like a
  // full-screen phone frame (Figma authors set cornerRadius:24 on
  // those for in-Figma device-preview but the official render
  // ignores it). Bottomsheets and modals authored at smaller heights
  // (e.g. Networth us_v2 at 375×573 with cornerRadius:20) keep their
  // intentional rounding — round-2 audit caught the over-suppression.
  //
  // Heuristic: a "full-screen phone frame" has height >= 700px (the
  // shortest standard phone is 667, but anything < 700 is almost
  // certainly a bottomsheet or modal sized to its own content). The
  // width check (>= 360) keeps mobile/tablet-aware screens in the
  // suppression set while rejecting cards/snippets.
  const suppressRootRadius =
    isScreenRoot &&
    node.absoluteBoundingBox != null &&
    node.absoluteBoundingBox.height >= 700 &&
    node.absoluteBoundingBox.width >= 360;
  if (!suppressRootRadius) {
    if (typeof node.cornerRadius === "number") {
      baseStyle.borderRadius = `${node.cornerRadius}px`;
    } else if (node.rectangleCornerRadii) {
      const [tl, tr, br, bl] = node.rectangleCornerRadii;
      baseStyle.borderRadius = `${tl}px ${tr}px ${br}px ${bl}px`;
    }
  }
  const strokeColor = firstSolidColor(node.strokes);
  if (strokeColor && typeof node.strokeWeight === "number" && node.strokeWeight > 0) {
    baseStyle.border = `${node.strokeWeight}px solid ${strokeColor}`;
  }

  // Auto-layout flex props
  if (isAutolayout) {
    baseStyle.display = "flex";
    baseStyle.flexDirection = ownLayoutMode === "HORIZONTAL" ? "row" : "column";
    // 2026-05-13 round-4 audit P19: honor Figma's layoutWrap. WRAP
    // means flex children spill to the next row/column when the
    // primary axis is full; pre-fix this was silently ignored and
    // chip rails overflowed off the right edge of their container
    // (INDstocks V5 Filters BS pill rows). NO_WRAP is the CSS default
    // so we only emit the property when WRAP is set.
    if (node.layoutWrap === "WRAP") {
      baseStyle.flexWrap = "wrap";
    }
    // 2026-05-12 round-2 audit P16: when primaryAxisAlignItems is
    // SPACE_BETWEEN, Figma still carries `itemSpacing` in the canonical
    // tree as a minimum-gap fallback, but the visual gap is computed by
    // SPACE_BETWEEN distribution alone. Emitting `gap: itemSpacing` on
    // top of `justify-content: space-between` double-counts spacing and
    // pushes trailing children past the right edge (My Ownership / Trading
    // Segment checkboxes drifted off-canvas on 249:54609). Drop the gap in
    // SPACE_BETWEEN — CSS distributes the slack correctly.
    if (
      typeof node.itemSpacing === "number" &&
      node.primaryAxisAlignItems !== "SPACE_BETWEEN"
    ) {
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
  // 2026-05-13 round-4 audit P20 — Figma's "absolute positioning inside
  // autolayout" escape hatch. When `layoutPositioning: "ABSOLUTE"` is
  // set, the node is OUT of the flex flow and positioned at its
  // absolute bbox relative to the autolayout parent (same math as a
  // non-autolayout child). Used by overlay ribbons, accent strips,
  // badges. Pre-fix every ABSOLUTE child got swept into flex flow and
  // mispositioned (Tax Centre transaction cards lost their green
  // top-left accent stripe to the bottom of the card column).
  if (node.layoutPositioning === "ABSOLUTE") {
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

function sizeStyle(
  node: CanonicalNode,
  parentLayoutMode: LayoutMode = null,
): CSSProperties {
  const bb = node.absoluteBoundingBox;
  if (!bb) return {};
  // 2026-05-12 fidelity audit P6: when a child opts into cross-axis
  // STRETCH inside autolayout, a fixed `width`/`height` would win over
  // `align-self: stretch` (CSS sizing is intrinsic), silently dropping
  // the stretch. Same logic for `layoutGrow:1` on the primary axis —
  // a fixed dimension defeats `flex-grow:1`. Drop the relevant axis
  // when the node explicitly delegated sizing to the autolayout.
  const stretch = node.layoutAlign === "STRETCH";
  const grow = node.layoutGrow === 1;
  if (parentLayoutMode === "HORIZONTAL") {
    return {
      width: grow ? "auto" : `${bb.width}px`,
      height: stretch ? "auto" : `${bb.height}px`,
    };
  }
  if (parentLayoutMode === "VERTICAL") {
    return {
      width: stretch ? "auto" : `${bb.width}px`,
      height: grow ? "auto" : `${bb.height}px`,
    };
  }
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
