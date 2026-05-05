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
  /** Figma blend mode applied to this node's painting. CSS mix-blend-mode equivalent. */
  blendMode?: string;
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
  /** Default true on FRAME, undefined elsewhere. */
  clipsContent?: boolean;
  fills?: Paint[];
  strokes?: Paint[];
  strokeWeight?: number;
  cornerRadius?: number;
  rectangleCornerRadii?: [number, number, number, number];
  characters?: string;
  style?: TextStyle;
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
