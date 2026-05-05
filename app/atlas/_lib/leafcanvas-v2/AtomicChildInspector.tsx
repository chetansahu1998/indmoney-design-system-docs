"use client";

/**
 * AtomicChildInspector — Zeplin-style sidebar panel rendered when a user
 * single-clicks any TEXT / cluster / RECTANGLE / ELLIPSE / VECTOR atomic
 * inside the canvas-v2 renderer.
 *
 * Surfaces:
 *   - Layer info: name, type, dimensions, x/y, parent autolayout dir
 *   - Type styles for TEXT atomics (font, weight, size, line-height,
 *     letter-spacing, color hex + token name)
 *   - Fills, strokes, radius, opacity, shadows
 *   - Tokens panel with copyable CSS / iOS / Android / RN snippets
 *   - Export buttons (PNG / SVG / 2x / 3x) — onClick wires through
 *     `mintAssetExportURL` from `lib/projects/client.ts`. The U5 server
 *     side is a placeholder today; the URL is still openable in a tab.
 *
 * Tabs follow `app/atlas/_lib/leafcanvas.tsx:LeafInspector` shape
 * (sticky head, scrollable body, tab pill row). We re-use the same
 * `lc-ins-*` classnames so the visual rhythm matches the rest of the
 * leaf inspector.
 *
 * Strict TS — no // @ts-nocheck.
 */

import { useCallback, useMemo, useState } from "react";

import { currentBrand } from "../../../../lib/brand";
import {
  type ApiResult,
  type MintAssetResponse,
  deleteTextOverride,
  mintAssetExportURL,
} from "../../../../lib/projects/client";
import { lookupTokenByHex } from "../../../../lib/tokens/hex-to-token";

import type { AnnotatedNode, BoundingBox, CanonicalNode, Paint } from "./types";
import {
  type ScreenTextOverride,
  colorSnippetAndroid,
  colorSnippetCSS,
  colorSnippetIOS,
  colorSnippetReactNative,
  effectiveText,
  firstSolidHex,
  spacingSnippet,
  textSnippetAndroid,
  textSnippetCSS,
  textSnippetIOS,
} from "./tokens";

export interface AtomicChildInspectorProps {
  /** screens.id — passed through to the asset-export request. */
  screenID: string;
  /** Figma node id (matches the `id` field on the canonical_tree node). */
  figmaNodeID: string;
  /** Whole canonical_tree for the screen — we walk to find figmaNodeID. */
  canonicalTree: unknown;
  /** Active text override pinned to this node (R6 — engineer copies live text). */
  override?: ScreenTextOverride | null;
  /** ds-service project slug — needed by the asset-export endpoint. */
  slug: string;
  /** Brand override for token lookups. Defaults to NEXT_PUBLIC_BRAND. */
  brand?: string;
  /** Closes the inspector — wired to Esc / explicit ✕ button. */
  onClose?: () => void;
  /**
   * U8 — fired after a successful Reset-to-original (DELETE override).
   * The host component clears the override from its store slice so the
   * canvas re-paints with the canonical_tree's original `characters`.
   */
  onOverrideReset?: () => void;
}

type InspectorTab = "layer" | "type" | "tokens" | "export";

const TABS: ReadonlyArray<{ id: InspectorTab; label: string }> = [
  { id: "layer", label: "Layer" },
  { id: "type", label: "Type" },
  { id: "tokens", label: "Tokens" },
  { id: "export", label: "Export" },
];

export function AtomicChildInspector(props: AtomicChildInspectorProps) {
  const { screenID, figmaNodeID, canonicalTree, override, slug, onClose, onOverrideReset } = props;
  const brand = props.brand ?? currentBrand();

  const found = useMemo(
    () => findByFigmaID(canonicalTree, figmaNodeID),
    [canonicalTree, figmaNodeID],
  );

  const [tab, setTab] = useState<InspectorTab>("layer");

  if (!found) {
    return (
      <div className="lc-ins lcv2-ins" data-empty="true">
        <div className="lc-ins-head">
          <div>
            <div className="lc-ins-eyebrow">Inspector</div>
            <div className="lc-ins-name">No layer selected</div>
            <div className="lc-ins-meta">
              Click a TEXT, icon, or shape on the canvas.
            </div>
          </div>
          {onClose && (
            <button className="lc-ins-close" onClick={onClose} aria-label="Close inspector">
              ✕
            </button>
          )}
        </div>
      </div>
    );
  }

  const { node, parent } = found;
  const isText = node.type === "TEXT";

  return (
    <div className="lc-ins lcv2-ins" data-figma-id={figmaNodeID}>
      <div className="lc-ins-head">
        <div>
          <div className="lc-ins-eyebrow">{node.type ?? "Layer"}</div>
          <div className="lc-ins-name">{node.name ?? figmaNodeID}</div>
          <div className="lc-ins-meta">
            {dimensionsLabel(node.absoluteBoundingBox)}
            {parent?.layoutMode && parent.layoutMode !== "NONE" ? (
              <> · parent: {parent.layoutMode.toLowerCase()}</>
            ) : null}
          </div>
        </div>
        {onClose && (
          <button className="lc-ins-close" onClick={onClose} aria-label="Close inspector">
            ✕
          </button>
        )}
      </div>

      <div className="lc-ins-tabs">
        {TABS.map((t) => (
          <button
            key={t.id}
            className={`lc-ins-tab ${tab === t.id ? "is-active" : ""}`}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div className="lc-ins-body">
        {tab === "layer" && <LayerTab node={node} parent={parent} brand={brand} />}
        {tab === "type" && (
          <TypeTab
            node={node}
            brand={brand}
            override={override ?? null}
            isText={isText}
            slug={slug}
            screenID={screenID}
            figmaNodeID={figmaNodeID}
            onOverrideReset={onOverrideReset}
          />
        )}
        {tab === "tokens" && (
          <TokensTab node={node} brand={brand} override={override ?? null} isText={isText} />
        )}
        {tab === "export" && (
          <ExportTab slug={slug} screenID={screenID} figmaNodeID={figmaNodeID} />
        )}
      </div>
    </div>
  );
}

// ─── LAYER tab ────────────────────────────────────────────────────────────────

function LayerTab({
  node,
  parent,
  brand,
}: {
  node: CanonicalNode;
  parent: CanonicalNode | null;
  brand: string;
}) {
  const fillHexes = collectSolidHexes(node.fills);
  const strokeHexes = collectSolidHexes(node.strokes);
  const bb = node.absoluteBoundingBox;

  return (
    <div className="lcv2-ins-section">
      <Row label="Type" value={node.type ?? "—"} />
      <Row label="Name" value={node.name ?? "—"} />
      {bb && (
        <>
          <Row label="X" value={`${Math.round(bb.x)}`} />
          <Row label="Y" value={`${Math.round(bb.y)}`} />
          <Row label="W" value={`${Math.round(bb.width)}`} />
          <Row label="H" value={`${Math.round(bb.height)}`} />
        </>
      )}
      {typeof node.opacity === "number" && node.opacity < 1 && (
        <Row label="Opacity" value={`${Math.round(node.opacity * 100)}%`} />
      )}
      {typeof node.cornerRadius === "number" && (
        <Row label="Radius" value={`${node.cornerRadius}px`} />
      )}
      {parent?.layoutMode && parent.layoutMode !== "NONE" && (
        <Row label="Parent layout" value={parent.layoutMode.toLowerCase()} />
      )}

      {fillHexes.length > 0 && (
        <div className="lcv2-ins-block">
          <div className="lcv2-ins-block-h">Fills</div>
          {fillHexes.map((hex) => (
            <ColorChip key={`fill-${hex}`} hex={hex} brand={brand} />
          ))}
        </div>
      )}
      {strokeHexes.length > 0 && (
        <div className="lcv2-ins-block">
          <div className="lcv2-ins-block-h">Strokes</div>
          {strokeHexes.map((hex) => (
            <ColorChip key={`stroke-${hex}`} hex={hex} brand={brand} />
          ))}
          {typeof node.strokeWeight === "number" && (
            <Row label="Weight" value={`${node.strokeWeight}px`} />
          )}
        </div>
      )}
    </div>
  );
}

// ─── TYPE tab (TEXT-only metadata) ────────────────────────────────────────────

function TypeTab({
  node,
  brand,
  override,
  isText,
  slug,
  screenID,
  figmaNodeID,
  onOverrideReset,
}: {
  node: CanonicalNode;
  brand: string;
  override: ScreenTextOverride | null;
  isText: boolean;
  slug: string;
  screenID: string;
  figmaNodeID: string;
  onOverrideReset?: () => void;
}) {
  if (!isText) {
    return (
      <div className="lcv2-ins-section">
        <div className="lc-empty">
          <div className="lc-empty-h">Not a text layer</div>
          <div className="lc-empty-sub">
            Type styles surface only for TEXT atomics.
          </div>
        </div>
      </div>
    );
  }
  const s = node.style ?? {};
  const text = effectiveText(node, override);
  const overrideActive = !!override && override.status !== "orphaned";

  const hex = firstSolidHex(node.fills);
  const tokenPath = hex ? lookupTokenByHex(hex, brand) : null;

  return (
    <div className="lcv2-ins-section">
      <div className="lcv2-ins-block">
        <div className="lcv2-ins-block-h">Text</div>
        <div className="lcv2-ins-text-preview">
          {text || <span className="lcv2-ins-muted">(empty)</span>}
        </div>
        {overrideActive && (
          <ResetOverrideRow
            slug={slug}
            screenID={screenID}
            figmaNodeID={figmaNodeID}
            onDeleted={onOverrideReset}
          />
        )}
      </div>
      <Row label="Font" value={s.fontFamily ?? "—"} />
      <Row label="Weight" value={`${s.fontWeight ?? "—"}`} />
      <Row label="Size" value={s.fontSize !== undefined ? `${s.fontSize}px` : "—"} />
      <Row
        label="Line height"
        value={s.lineHeightPx !== undefined ? `${s.lineHeightPx}px` : "—"}
      />
      <Row
        label="Letter spacing"
        value={s.letterSpacing !== undefined ? `${s.letterSpacing}px` : "—"}
      />
      {hex && (
        <div className="lcv2-ins-block">
          <div className="lcv2-ins-block-h">Color</div>
          <ColorChip hex={hex} brand={brand} />
          {tokenPath && (
            <div className="lcv2-ins-token">{tokenPath.replace(/^colour\./, "")}</div>
          )}
        </div>
      )}
    </div>
  );
}

// ─── TOKENS tab (CSS / iOS / Android / RN snippets) ───────────────────────────

function TokensTab({
  node,
  brand,
  override,
  isText,
}: {
  node: CanonicalNode | AnnotatedNode;
  brand: string;
  override: ScreenTextOverride | null;
  isText: boolean;
}) {
  const hex = firstSolidHex(node.fills);
  const tokenPath = hex ? lookupTokenByHex(hex, brand) : null;
  const padding = paddingFromNode(node);
  const gap = typeof node.itemSpacing === "number" ? node.itemSpacing : null;
  const showSpacing =
    node.layoutMode === "HORIZONTAL" || node.layoutMode === "VERTICAL";

  return (
    <div className="lcv2-ins-section">
      {hex && (
        <div className="lcv2-ins-block">
          <div className="lcv2-ins-block-h">Color</div>
          <CodeBlock code={colorSnippetCSS(hex, tokenPath)} label="CSS" />
          <CodeBlock code={colorSnippetIOS(hex, tokenPath)} label="Swift" />
          <CodeBlock code={colorSnippetAndroid(hex, tokenPath)} label="Android" />
          <CodeBlock code={colorSnippetReactNative(hex, tokenPath)} label="React Native" />
        </div>
      )}
      {isText && (
        <div className="lcv2-ins-block">
          <div className="lcv2-ins-block-h">Text style</div>
          <CodeBlock code={textSnippetCSS(node, brand, override)} label="CSS" />
          <CodeBlock code={textSnippetIOS(node, brand, override)} label="SwiftUI" />
          <CodeBlock code={textSnippetAndroid(node, brand, override)} label="Compose" />
        </div>
      )}
      {showSpacing && (
        <div className="lcv2-ins-block">
          <div className="lcv2-ins-block-h">Spacing</div>
          <CodeBlock code={spacingSnippet(padding, gap)} label="CSS" />
        </div>
      )}
    </div>
  );
}

// ─── EXPORT tab ───────────────────────────────────────────────────────────────

function ExportTab({
  slug,
  screenID,
  figmaNodeID,
}: {
  slug: string;
  screenID: string;
  figmaNodeID: string;
}) {
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const onExport = useCallback(
    async (format: "png" | "svg", scale: 1 | 2 | 3) => {
      const key = `${format}-${scale}`;
      setBusy(key);
      setError(null);
      const res: ApiResult<MintAssetResponse> = await mintAssetExportURL({
        slug,
        screenID,
        figmaNodeID,
        format,
        scale,
      });
      setBusy(null);
      if (!res.ok) {
        setError(res.error || "Export failed");
        return;
      }
      // Open in a new tab — preserves the inspector state. U5 will return
      // a signed URL with a short TTL; reopening pulls a fresh one.
      if (typeof window !== "undefined") {
        window.open(res.data.download_url, "_blank", "noopener");
      }
    },
    [slug, screenID, figmaNodeID],
  );

  return (
    <div className="lcv2-ins-section">
      <div className="lcv2-ins-block">
        <div className="lcv2-ins-block-h">Export</div>
        <div className="lcv2-ins-export-grid">
          <ExportButton label="PNG 1x" busy={busy === "png-1"} onClick={() => onExport("png", 1)} />
          <ExportButton label="PNG 2x" busy={busy === "png-2"} onClick={() => onExport("png", 2)} />
          <ExportButton label="PNG 3x" busy={busy === "png-3"} onClick={() => onExport("png", 3)} />
          <ExportButton label="SVG" busy={busy === "svg-1"} onClick={() => onExport("svg", 1)} />
        </div>
        {error && <div className="lcv2-ins-error">{error}</div>}
      </div>
    </div>
  );
}

// ─── Reset-to-original (U8) ──────────────────────────────────────────────────

/**
 * Surface in the Type tab when an active override is pinned to the
 * selected TEXT atomic. Click → DELETE the override row → fire
 * `onDeleted` so the host clears the local cache. The canvas re-paints
 * with the canonical_tree's original `characters` automatically (no
 * extra reload — just clearing the override map flips the renderer).
 */
function ResetOverrideRow({
  slug,
  screenID,
  figmaNodeID,
  onDeleted,
}: {
  slug: string;
  screenID: string;
  figmaNodeID: string;
  onDeleted?: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onClick = useCallback(async () => {
    setBusy(true);
    setError(null);
    const r = await deleteTextOverride(slug, screenID, figmaNodeID);
    setBusy(false);
    if (!r.ok) {
      setError(r.error || "Reset failed");
      return;
    }
    onDeleted?.();
  }, [figmaNodeID, onDeleted, screenID, slug]);

  return (
    <div className="lcv2-ins-pill-row">
      <span className="lcv2-ins-pill" data-status="active">
        override active
      </span>
      <button
        className="lcv2-ins-reset-btn"
        onClick={onClick}
        disabled={busy}
        type="button"
        aria-label="Reset to original"
      >
        {busy ? "Resetting…" : "Reset to original"}
      </button>
      {error && <span className="lcv2-ins-error">{error}</span>}
    </div>
  );
}

function ExportButton({
  label,
  busy,
  onClick,
}: {
  label: string;
  busy: boolean;
  onClick: () => void;
}) {
  return (
    <button
      className="lcv2-ins-export-btn"
      onClick={onClick}
      disabled={busy}
      data-busy={busy ? "true" : undefined}
    >
      {busy ? "…" : label}
    </button>
  );
}

// ─── Atoms ────────────────────────────────────────────────────────────────────

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="lcv2-ins-row">
      <span className="lcv2-ins-row-l">{label}</span>
      <span className="lcv2-ins-row-v">{value}</span>
    </div>
  );
}

function ColorChip({ hex, brand }: { hex: string; brand: string }) {
  const path = lookupTokenByHex(hex, brand);
  const upper = hex.toUpperCase();
  return (
    <div className="lcv2-ins-color">
      <span className="lcv2-ins-color-sw" style={{ background: upper }} aria-hidden="true" />
      <span className="lcv2-ins-color-hex">{upper}</span>
      <span className="lcv2-ins-color-name">{path ?? "no token match"}</span>
    </div>
  );
}

function CodeBlock({ code, label }: { code: string; label: string }) {
  const onCopy = useCallback(() => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      void navigator.clipboard.writeText(code);
    }
  }, [code]);
  return (
    <div className="lcv2-ins-code">
      <div className="lcv2-ins-code-h">
        <span>{label}</span>
        <button className="lcv2-ins-code-copy" onClick={onCopy} aria-label={`Copy ${label} snippet`}>
          Copy
        </button>
      </div>
      <pre className="lcv2-ins-code-pre">
        <code>{code}</code>
      </pre>
    </div>
  );
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function dimensionsLabel(bb: BoundingBox | null | undefined): string {
  if (!bb) return "—";
  return `${Math.round(bb.width)} × ${Math.round(bb.height)}`;
}

function collectSolidHexes(paints: Paint[] | undefined): string[] {
  if (!Array.isArray(paints)) return [];
  const out: string[] = [];
  for (const p of paints) {
    if (p.visible === false) continue;
    if (p.type !== "SOLID") continue;
    const sp = p as Extract<Paint, { type: "SOLID" }>;
    if (!("color" in sp) || !sp.color) continue;
    const hh = (x: number) =>
      Math.round(Math.max(0, Math.min(1, x)) * 255)
        .toString(16)
        .padStart(2, "0");
    out.push(`#${hh(sp.color.r)}${hh(sp.color.g)}${hh(sp.color.b)}`);
  }
  return Array.from(new Set(out));
}

function paddingFromNode(node: CanonicalNode | AnnotatedNode): {
  top: number;
  right: number;
  bottom: number;
  left: number;
} {
  return {
    top: typeof node.paddingTop === "number" ? node.paddingTop : 0,
    right: typeof node.paddingRight === "number" ? node.paddingRight : 0,
    bottom: typeof node.paddingBottom === "number" ? node.paddingBottom : 0,
    left: typeof node.paddingLeft === "number" ? node.paddingLeft : 0,
  };
}

interface FoundNode {
  node: CanonicalNode;
  parent: CanonicalNode | null;
}

/**
 * Walk the canonical_tree blob looking for a node whose `id` matches the
 * given Figma node id. Returns the node and its immediate parent (so the
 * inspector can surface "parent: HORIZONTAL").
 *
 * The tree is opaque (`unknown`) at the inspector boundary because U6's
 * `canonicalTreeByScreenID` slot stores it that way. We narrow as we walk.
 */
export function findByFigmaID(tree: unknown, id: string): FoundNode | null {
  if (!tree || typeof tree !== "object") return null;
  const root = tree as CanonicalNode;
  if (root.id === id) return { node: root, parent: null };
  const stack: Array<{ node: CanonicalNode; parent: CanonicalNode | null }> = [
    { node: root, parent: null },
  ];
  while (stack.length > 0) {
    const top = stack.pop();
    if (!top) break;
    const children = top.node.children;
    if (!Array.isArray(children)) continue;
    for (const child of children) {
      if (!child || typeof child !== "object") continue;
      const c = child as CanonicalNode;
      if (c.id === id) return { node: c, parent: top.node };
      if (Array.isArray(c.children) && c.children.length > 0) {
        stack.push({ node: c, parent: top.node });
      }
    }
  }
  return null;
}
