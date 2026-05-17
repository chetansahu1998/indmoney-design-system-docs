/**
 * leafcanvas-v2/types.ts — local TypeScript shapes for the canonical_tree
 * blob the renderer walks.
 *
 * The audit pipeline emits a Figma-shaped tree (subset of the Figma REST
 * Node schema) into `screen_canonical_trees.canonical_tree` per project
 * screen. This file declares only the fields the v2 renderer actually
 * reads — everything else stays `unknown` so future schema additions
 * don't force a recompile here.
 *
 * Strict TS: no `// @ts-nocheck`.
 */

export type LayoutMode = "HORIZONTAL" | "VERTICAL" | "NONE" | null;

export type LayoutAlign = "MIN" | "CENTER" | "MAX" | "STRETCH" | "INHERIT";
export type LayoutGrow = 0 | 1;

export type PrimaryAxisAlign = "MIN" | "CENTER" | "MAX" | "SPACE_BETWEEN";
export type CounterAxisAlign = "MIN" | "CENTER" | "MAX" | "BASELINE";

export type NodeType =
  | "FRAME"
  | "GROUP"
  | "BOOLEAN_OPERATION"
  | "INSTANCE"
  | "COMPONENT"
  | "COMPONENT_SET"
  | "RECTANGLE"
  | "ELLIPSE"
  | "VECTOR"
  | "STAR"
  | "POLYGON"
  | "LINE"
  | "TEXT"
  | "SECTION"
  | "DOCUMENT"
  | "CANVAS"
  | string;

export interface BoundingBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface RGBA {
  r: number;
  g: number;
  b: number;
  a?: number;
}

export interface SolidPaint {
  type: "SOLID";
  color?: RGBA;
  opacity?: number;
  visible?: boolean;
}

export interface ImagePaint {
  type: "IMAGE";
  imageRef?: string;
  scaleMode?: "FILL" | "FIT" | "TILE" | "STRETCH";
  visible?: boolean;
  opacity?: number;
}

/**
 * Gradient stop — Figma serializes a normalized position [0..1] and an
 * RGBA colour. Identical shape across the four GRADIENT_* paint kinds.
 */
export interface ColorStop {
  position: number;
  color: RGBA;
}

/**
 * Gradient paint. Figma supplies four kinds:
 *
 *   GRADIENT_LINEAR    — two-handle linear gradient
 *   GRADIENT_RADIAL    — center + radius
 *   GRADIENT_ANGULAR   — sweep around a center (a.k.a. conic)
 *   GRADIENT_DIAMOND   — angular with diamond shape
 *
 * `gradientHandlePositions` is a 3-tuple of {x, y} in NORMALIZED node
 * coordinates (0..1) that defines the gradient's geometry. CSS can
 * approximate all four via linear-gradient / radial-gradient /
 * conic-gradient + stops.
 */
export interface GradientPaint {
  type:
    | "GRADIENT_LINEAR"
    | "GRADIENT_RADIAL"
    | "GRADIENT_ANGULAR"
    | "GRADIENT_DIAMOND";
  gradientHandlePositions?: Array<{ x: number; y: number }>;
  gradientStops?: ColorStop[];
  visible?: boolean;
  opacity?: number;
}

export type Paint =
  | SolidPaint
  | ImagePaint
  | GradientPaint
  | { type: string; visible?: boolean };

/**
 * Effect — drop shadow / inner shadow / layer blur / background blur.
 * Shape mirrors Figma's REST API verbatim so the renderer can convert
 * straight to CSS.
 */
export interface Effect {
  type: "DROP_SHADOW" | "INNER_SHADOW" | "LAYER_BLUR" | "BACKGROUND_BLUR" | string;
  visible?: boolean;
  /** Shadow color (RGBA). Absent for blur effects. */
  color?: RGBA;
  /** Shadow offset in px. Absent for blur effects. */
  offset?: { x: number; y: number };
  /** Blur radius (shadow softness, OR filter blur amount). */
  radius?: number;
  /** Shadow spread in px (Figma's "Spread" slider). Optional. */
  spread?: number;
  blendMode?: string;
  /** Drop shadow only — whether the shadow paints behind the shape's
   *  fill (true) or only outside the shape's outline (false). */
  showShadowBehindNode?: boolean;
}

export interface TextStyle {
  fontFamily?: string;
  fontPostScriptName?: string;
  fontWeight?: number;
  fontSize?: number;
  letterSpacing?: number;
  lineHeightPx?: number;
  lineHeightUnit?: string;
  textAlignHorizontal?: "LEFT" | "CENTER" | "RIGHT" | "JUSTIFIED";
  textAlignVertical?: "TOP" | "CENTER" | "BOTTOM";
  italic?: boolean;
  /**
   * Figma's text-case enum. Wired into the renderer 2026-05-12 (fidelity
   * audit P1); pre-fix the field was unread and any UPPER/LOWER/TITLE
   * casing authored in Figma silently flattened to mixed-case at the
   * source-character casing. Maps to CSS `text-transform` in renderText.
   */
  textCase?: "ORIGINAL" | "UPPER" | "LOWER" | "TITLE" | "SMALL_CAPS" | "SMALL_CAPS_FORCED";
  /**
   * Figma's text-decoration enum. Wired into the renderer 2026-05-12
   * (fidelity audit P1); pre-fix Figma underlines and strikethroughs
   * never reached the DOM. Maps to CSS `text-decoration` in renderText.
   */
  textDecoration?: "NONE" | "UNDERLINE" | "STRIKETHROUGH";
}

export interface CanonicalNode {
  id?: string;
  name?: string;
  type?: NodeType;
  visible?: boolean;
  /**
   * Some Figma exports (notably plugin-side dumps) carry a `removed: true`
   * flag instead of `visible: false` for soft-deleted layers. The visible
   * filter treats it the same.
   */
  removed?: boolean;
  opacity?: number;
  /** Figma absolute bounds (relative to the page, in px). */
  absoluteBoundingBox?: BoundingBox | null;
  /**
   * SVG path strings for VECTOR / ELLIPSE / LINE / BOOLEAN_OPERATION
   * shape nodes. Populated when the canonical_tree pipeline calls
   * /v1/files/<key>/nodes with `&geometry=paths`. Each entry is
   * `{ path: "M 0 0 L 100 100 ...", windingRule: "EVENODD" | "NONZERO" }`.
   * The renderer emits `<svg viewBox="x y w h"><path d="..."/></svg>`
   * when this is present; without it, shape nodes degrade to bbox-
   * sized coloured divs (icons render as solid rectangles).
   */
  fillGeometry?: Array<{ path: string; windingRule?: "EVENODD" | "NONZERO" }>;
  strokeGeometry?: Array<{ path: string; windingRule?: "EVENODD" | "NONZERO" }>;
  /**
   * Pre-rendered SVG markup populated by the server pipeline for
   * named vector groups (`illustration/...`, `icon/.../.../...`). When
   * present, the renderer (`nodeToHTML.renderClusterPlaceholder`)
   * inlines the markup directly via `dangerouslySetInnerHTML` instead
   * of routing to the cluster-URL → `<img>` path. The asset-stream
   * subscriber (`useIconClusterURLs.collectClusterIDsWithBBox`) also
   * skips these nodes — no URL mint, no SSE wait, no render-retry
   * loop. See plan U7 (client) + U8 (server inlining pass).
   *
   * Origin: Figma `/v1/images/<key>?format=svg` bytes spliced into the
   * canonical_tree post-Stage 9 by ds-service's `svg_inliner.go`. The
   * markup is server-sanitized; the client trusts the bytes. The
   * U8 commit lands the producer side.
   */
  svg_markup?: string;
  /** Figma blend mode applied to this node's painting. CSS mix-blend-mode equivalent. */
  blendMode?: string;
  /**
   * Figma effects array — drop shadow, inner shadow, layer blur,
   * background blur. CSS counterparts:
   *   DROP_SHADOW      → box-shadow (or filter:drop-shadow for shapes)
   *   INNER_SHADOW     → box-shadow inset
   *   LAYER_BLUR       → filter: blur()
   *   BACKGROUND_BLUR  → backdrop-filter: blur()
   * Multiple effects stack via CSS comma-list / chained filters.
   */
  effects?: Effect[];
  /** Auto-layout direction. null/absent = absolute children. */
  layoutMode?: LayoutMode;
  itemSpacing?: number;
  paddingLeft?: number;
  paddingRight?: number;
  paddingTop?: number;
  paddingBottom?: number;
  primaryAxisAlignItems?: PrimaryAxisAlign;
  counterAxisAlignItems?: CounterAxisAlign;
  layoutAlign?: LayoutAlign;
  layoutGrow?: LayoutGrow;
  /**
   * Figma's auto-layout wrap mode. When `WRAP`, the autolayout
   * container wraps items to a new row (HORIZONTAL) or column
   * (VERTICAL) when the primary axis runs out of space. Maps to CSS
   * `flex-wrap: wrap` on the parent.
   *
   * Wired into the renderer 2026-05-13 (round-4 audit P19): pre-fix
   * the field was unread, so a pill rail authored to wrap to two rows
   * (e.g. Nifty 50 / Nifty 100 / Nifty 250 / Nifty 500 / Top 1000 by
   * Mkt Cap on INDstocks V5 Filters BS) overflowed off the right edge
   * of its container instead. Production case: `Frame 2147228897`
   * (243×64, layoutWrap=WRAP) in 249:55005.
   */
  layoutWrap?: "WRAP" | "NO_WRAP";
  /**
   * Figma's "absolute positioning inside autolayout" escape hatch
   * (introduced 2023). When set to `ABSOLUTE`, the node is taken OUT
   * of the autolayout flex flow and positioned at `absoluteBoundingBox`
   * relative to the autolayout parent's bbox — same as a non-autolayout
   * child. Used for overlays like ribbons, accent strips, badges, and
   * "floating" elements layered over flex content.
   *
   * Wired into the renderer 2026-05-13 (round-4 audit P20): without
   * this branch, every Figma `ABSOLUTE` child got swept into the flex
   * flow and rendered at the wrong position (bottom of column / side
   * of row). Production case: Tax Centre Transaction cards have a 4×18
   * green accent rectangle (`Rectangle 427321283`) anchored to the
   * card's top-left corner — visible in every transaction-card design
   * across the file but absent from our render.
   */
  layoutPositioning?: "AUTO" | "ABSOLUTE";
  /** Default true on FRAME, undefined elsewhere. */
  clipsContent?: boolean;
  fills?: Paint[];
  strokes?: Paint[];
  strokeWeight?: number;
  cornerRadius?: number;
  rectangleCornerRadii?: [number, number, number, number];
  characters?: string;
  style?: TextStyle;
  /**
   * Per-character style override indices into `styleOverrideTable`.
   * Index 0 means "no override — use top-level style". Any non-zero
   * index points to a `styleOverrideTable[<index>]` partial TextStyle
   * that overrides the top-level style for that character.
   *
   * 2026-05-12 fidelity audit P3: the renderer pre-fix ignored both
   * this field and `styleOverrideTable`, so authoring patterns like
   * "label Medium 14px / digits Regular 14px no-letter-spacing" lost
   * the run-level overrides entirely. Symptom: ticker text overflowed
   * its 75px HUG container because top-level letterSpacing:1 applied
   * to every char even when every char overrode to letterSpacing:0.
   */
  characterStyleOverrides?: number[];
  /**
   * Lookup table for per-character style overrides keyed by the index
   * values in `characterStyleOverrides`. Each entry is a partial
   * TextStyle that merges OVER the top-level `style` for any character
   * whose override index points here.
   */
  styleOverrideTable?: Record<string, TextStyle>;
  /**
   * Legacy autoresize mode for non-autolayout TEXT nodes. See the comment on
   * `layoutSizingHorizontal` for the modern equivalent. When present, this
   * wins over `layoutSizingHorizontal` because Figma exports it only for
   * standalone (non-autolayout) text where the auto-layout fields don't
   * apply.
   *
   *   WIDTH_AND_HEIGHT — bbox grows on both axes to fit the text.
   *   HEIGHT           — width fixed, height grows.
   *   NONE             — both fixed; clip overflow.
   *   TRUNCATE         — width fixed, single line, ellipsis.
   */
  textAutoResize?: "WIDTH_AND_HEIGHT" | "HEIGHT" | "NONE" | "TRUNCATE";
  /**
   * Auto-layout sizing for any node (TEXT, FRAME, INSTANCE, COMPONENT) whose
   * parent runs auto-layout. This is the modern field — Figma's REST API
   * returns it on every auto-layout child, while `textAutoResize` only
   * appears on standalone TEXT nodes that aren't in auto-layout.
   *
   *   HUG   — size to content (width hugs the glyph run for TEXT, hugs the
   *           inner-children sum for containers). Renderer emits
   *           width=auto + white-space=nowrap for TEXT, width=auto for
   *           containers.
   *   FILL  — fill the parent's cross-axis. Renderer uses bbox-derived
   *           width and wraps text inside it.
   *   FIXED — use the bbox dimension exactly. Renderer uses bbox width
   *           with overflow clipped.
   *
   * Production case: INDmoney splash "AES-256 SSL Secured" had
   * layoutSizingHorizontal=HUG but the renderer treated TEXT as fixed-
   * width-wrap by default, forcing the natural-width label onto two lines
   * (2026-05-09). Extending the renderer to honor HUG width=auto fixes it.
   */
  layoutSizingHorizontal?: "HUG" | "FILL" | "FIXED";
  layoutSizingVertical?: "HUG" | "FILL" | "FIXED";
  children?: CanonicalNode[];
}

/** Image-fill ref → URL map (populated by U7's asset-export client). */
export type ImageRefMap = Record<string, string>;

/**
 * A node that survived the visibility filter. Carries an optional
 * `__stateGroup` tag for co-positioned siblings (state-picker UI in U14).
 * Children are `AnnotatedNode[]` so the recursive type stays accurate.
 */
export interface AnnotatedNode extends Omit<CanonicalNode, "children"> {
  /** Set when this node shares (x, y, w, h) with sibling(s). UI in U14. */
  __stateGroup?: string;
  children?: AnnotatedNode[];
}
