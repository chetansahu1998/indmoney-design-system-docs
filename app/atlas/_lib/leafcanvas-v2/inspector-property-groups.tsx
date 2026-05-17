"use client";

/**
 * inspector-property-groups.tsx — Figma-Dev-Mode-style property
 * summary sections (U10).
 *
 * Ships three compact always-visible blocks at the top of the
 * AtomicChildInspector drawer:
 *
 *   - LayoutGroup       W × H, layoutMode, padding, gap, alignment
 *   - TypographyGroup   font family / size / weight / color (TEXT only;
 *                       renders "—" placeholders for non-TEXT per the
 *                       safe_auto fix from doc-review: section always
 *                       renders, communicates "this property category
 *                       exists but doesn't apply here" — matches
 *                       Figma Dev Mode)
 *   - FillsGroup        solid colors as hex chips, image-fill indicator,
 *                       strokes as weight + color
 *
 * The existing Layer / Type / Tokens / Export tabs in
 * AtomicChildInspector continue to render BELOW these sections.
 * This is the doc-review P1 / KTD-7 resolution: extend, don't
 * replace, so the existing DRD authoring affordances stay reachable.
 *
 * Strict-TS; reads a CanonicalNode through the loose-typed
 * inspector property contract. Pure presentation — no Zustand
 * subscriptions, no DOM mutation; the parent component re-renders
 * with a new node on selection change and React handles the rest.
 */

import type { CanonicalNode } from "./types";

interface PropertyGroupProps {
  node: CanonicalNode;
}

export function LayoutGroup({ node }: PropertyGroupProps): React.ReactElement {
  const bbox = node.absoluteBoundingBox;
  const w = bbox ? Math.round(bbox.width) : null;
  const h = bbox ? Math.round(bbox.height) : null;
  const lm = node.layoutMode;
  const hasLayout = lm === "HORIZONTAL" || lm === "VERTICAL";

  return (
    <div className="lcv2-pg-block">
      <div className="lcv2-pg-block-h">Layout</div>
      <PropertyRow label="Size" value={w !== null && h !== null ? `${w} × ${h}` : "—"} mono />
      <PropertyRow
        label="Direction"
        value={hasLayout ? lm.toLowerCase() : lm === "NONE" ? "absolute" : "—"}
      />
      {hasLayout && (
        <>
          <PropertyRow
            label="Padding"
            value={formatPadding(
              node.paddingTop,
              node.paddingRight,
              node.paddingBottom,
              node.paddingLeft,
            )}
            mono
          />
          <PropertyRow
            label="Gap"
            value={
              typeof node.itemSpacing === "number"
                ? `${Math.round(node.itemSpacing)}px`
                : "—"
            }
            mono
          />
          <PropertyRow
            label="Justify"
            value={node.primaryAxisAlignItems?.toLowerCase().replace(/_/g, " ") ?? "—"}
          />
          <PropertyRow
            label="Align"
            value={node.counterAxisAlignItems?.toLowerCase().replace(/_/g, " ") ?? "—"}
          />
        </>
      )}
    </div>
  );
}

export function TypographyGroup({ node }: PropertyGroupProps): React.ReactElement {
  const isText = node.type === "TEXT";
  const style = isText ? (node.style as TextStyle | undefined) : undefined;

  // Per doc-review safe_auto: the section is ALWAYS rendered with
  // "—" placeholders for non-TEXT nodes (matches Figma Dev Mode
  // behavior, where Typography never disappears).
  return (
    <div className="lcv2-pg-block">
      <div className="lcv2-pg-block-h">Typography</div>
      <PropertyRow label="Family" value={style?.fontFamily ?? "—"} />
      <PropertyRow
        label="Size"
        value={style?.fontSize != null ? `${style.fontSize}px` : "—"}
        mono
      />
      <PropertyRow
        label="Weight"
        value={style?.fontWeight != null ? String(style.fontWeight) : "—"}
        mono
      />
      <PropertyRow
        label="Line height"
        value={style?.lineHeightPx != null ? `${Math.round(style.lineHeightPx)}px` : "—"}
        mono
      />
      <PropertyRow
        label="Letter spacing"
        value={
          style?.letterSpacing != null
            ? `${style.letterSpacing.toFixed(2)}px`
            : "—"
        }
        mono
      />
    </div>
  );
}

export function FillsGroup({ node }: PropertyGroupProps): React.ReactElement {
  const fills = Array.isArray(node.fills) ? node.fills : [];
  const strokes = Array.isArray(node.strokes) ? node.strokes : [];

  return (
    <div className="lcv2-pg-block">
      <div className="lcv2-pg-block-h">Fills &amp; strokes</div>
      {fills.length === 0 && strokes.length === 0 && (
        <div className="lcv2-pg-empty">No fills or strokes.</div>
      )}
      {fills.map((fill, i) => (
        <FillRow key={`fill-${i}`} fill={fill} />
      ))}
      {strokes.map((stroke, i) => (
        <StrokeRow
          key={`stroke-${i}`}
          stroke={stroke}
          weight={typeof node.strokeWeight === "number" ? node.strokeWeight : undefined}
        />
      ))}
    </div>
  );
}

// ─── Helpers ──────────────────────────────────────────────────────

interface FigmaFill {
  type?: string;
  color?: { r: number; g: number; b: number; a?: number };
  opacity?: number;
  imageRef?: string;
  visible?: boolean;
}

interface TextStyle {
  fontFamily?: string;
  fontSize?: number;
  fontWeight?: number;
  lineHeightPx?: number;
  letterSpacing?: number;
}

function FillRow({ fill }: { fill: FigmaFill }): React.ReactElement {
  if (fill.visible === false) {
    return (
      <PropertyRow label="Fill" value="(hidden)" />
    );
  }
  if (fill.type === "SOLID" && fill.color) {
    const hex = rgbaToHex(fill.color, fill.opacity);
    return (
      <div className="lcv2-pg-color">
        <div
          className="lcv2-pg-color-sw"
          style={{
            backgroundColor: `rgba(${Math.round(fill.color.r * 255)}, ${Math.round(
              fill.color.g * 255,
            )}, ${Math.round(fill.color.b * 255)}, ${
              (fill.color.a ?? 1) * (fill.opacity ?? 1)
            })`,
          }}
        />
        <span className="lcv2-pg-color-hex">{hex}</span>
      </div>
    );
  }
  if (fill.type === "IMAGE") {
    return <PropertyRow label="Fill" value="Image" />;
  }
  if (fill.type && fill.type.includes("GRADIENT")) {
    return (
      <PropertyRow
        label="Fill"
        value={fill.type
          .toLowerCase()
          .replace("gradient_", "")
          .replace(/_/g, " ")}
      />
    );
  }
  return <PropertyRow label="Fill" value={fill.type ?? "—"} />;
}

function StrokeRow({
  stroke,
  weight,
}: {
  stroke: FigmaFill;
  weight: number | undefined;
}): React.ReactElement {
  if (stroke.type === "SOLID" && stroke.color) {
    const hex = rgbaToHex(stroke.color, stroke.opacity);
    return (
      <div className="lcv2-pg-color">
        <div
          className="lcv2-pg-color-sw"
          style={{
            backgroundColor: `rgba(${Math.round(stroke.color.r * 255)}, ${Math.round(
              stroke.color.g * 255,
            )}, ${Math.round(stroke.color.b * 255)}, ${
              (stroke.color.a ?? 1) * (stroke.opacity ?? 1)
            })`,
          }}
        />
        <span className="lcv2-pg-color-hex">
          {hex}
          {typeof weight === "number" ? ` · ${weight}px` : ""}
        </span>
      </div>
    );
  }
  return <PropertyRow label="Stroke" value={stroke.type ?? "—"} />;
}

function PropertyRow({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}): React.ReactElement {
  return (
    <div className="lcv2-pg-row">
      <span className="lcv2-pg-row-l">{label}</span>
      <span className={mono ? "lcv2-pg-row-v lcv2-pg-mono" : "lcv2-pg-row-v"}>
        {value}
      </span>
    </div>
  );
}

function formatPadding(
  top: number | undefined,
  right: number | undefined,
  bottom: number | undefined,
  left: number | undefined,
): string {
  const vals = [top, right, bottom, left];
  if (vals.every((v) => v == null)) return "—";
  // Figma convention: top, right, bottom, left.
  const parts = vals.map((v) => (typeof v === "number" ? Math.round(v) : 0));
  // Compact when all four are equal.
  if (parts.every((v) => v === parts[0])) return `${parts[0]}px`;
  return parts.map((p) => `${p}`).join(" / ");
}

/**
 * Convert Figma RGBA (0..1 floats) to a 6-char #RRGGBB hex string.
 * Honors the optional `opacity` multiplier on the fill — when < 1,
 * appends a 2-char hex alpha so the rendered chip matches what
 * Figma shows for partially-transparent fills.
 */
export function rgbaToHex(
  color: { r: number; g: number; b: number; a?: number },
  opacity: number | undefined,
): string {
  const r = clamp255(color.r);
  const g = clamp255(color.g);
  const b = clamp255(color.b);
  const alpha = (color.a ?? 1) * (opacity ?? 1);
  const hex = `#${twoHex(r)}${twoHex(g)}${twoHex(b)}`;
  if (alpha < 0.995) {
    return `${hex}${twoHex(Math.round(alpha * 255))}`;
  }
  return hex;
}

function clamp255(v: number): number {
  return Math.max(0, Math.min(255, Math.round(v * 255)));
}

function twoHex(n: number): string {
  return n.toString(16).padStart(2, "0").toUpperCase();
}
