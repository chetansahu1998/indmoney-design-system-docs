"use client";

/**
 * BulkExportPanel — U9.
 *
 * Floating "Export selection (N)" affordance that appears when the user
 * has multi-selected atomic children via Shift-click or lasso (state
 * lives in `live-store.selection.selectedAtomicChildren`). Click triggers
 * a single POST to `/v1/projects/:slug/assets/bulk-export` and points
 * `window.location.href` at the returned `download_url` so the browser
 * handles the zip download natively.
 *
 * Filename preview: a small read-only list rendered above the button so
 * the designer can sanity-check what they're about to export. The plan
 * leaves editable filenames as a v2 option — server appends `-1`, `-2`
 * for collisions today, so client-side renaming would just lie.
 *
 * Strict TS — no `// @ts-nocheck`. Pure presentational; mounts under the
 * canvas wrapper exactly once per leaf via `<LeafFrameRenderer />`'s
 * top-level container so the panel doesn't duplicate per-frame.
 */

import { useCallback, useMemo, useState } from "react";

import {
  type ApiResult,
  type BulkMintAssetResponse,
  mintBulkAssetExportURL,
} from "../../../../lib/projects/client";
import { useAtlas } from "../../../../lib/atlas/live-store";

export interface BulkExportPanelProps {
  /** ds-service project slug. */
  slug: string;
  /** Open leaf id — passed through to the bulk endpoint as `leaf_id`. */
  leafID: string;
  /**
   * Optional name resolver for filename previews. The renderer wires this
   * to walk the canonical_tree by figmaNodeID and return the node's
   * `name` so the preview list shows "icon/back.svg" instead of the raw
   * Figma id. When omitted (or unresolvable), the preview falls back to
   * the figmaNodeID — still useful for sanity-checking selection size.
   */
  resolveNodeName?: (screenID: string, figmaNodeID: string) => string | null;
  /**
   * Test/SSR seam: caller can swap this to assert against the URL-build
   * call without exercising live `fetch`. Production code lets it default
   * to the real `mintBulkAssetExportURL`.
   */
  mintFn?: typeof mintBulkAssetExportURL;
  /**
   * Test/SSR seam: replaces the `window.location.href = url` side effect.
   * Production wires the default which sets `window.location.href` (set
   * via assignment so the browser's download handler kicks in).
   */
  triggerDownload?: (url: string) => void;
}

type ExportState =
  | { kind: "idle" }
  | { kind: "minting" }
  | { kind: "done"; url: string }
  | { kind: "error"; message: string };

/**
 * Selection summary derived from the live-store map. Stable shape so the
 * panel's render is a pure function of `(slug, leafID, summary)`.
 */
interface SelectionRow {
  /** `screenID|figmaNodeID` — composite key (matches store key). */
  key: string;
  screenID: string;
  figmaNodeID: string;
  /** Filename preview (server may append `-1`, `-2` on collision). */
  preview: string;
}

const DEFAULT_FORMAT: "png" | "svg" = "svg";
const DEFAULT_SCALE: 1 | 2 | 3 = 1;

export function BulkExportPanel(props: BulkExportPanelProps) {
  const { slug, leafID, resolveNodeName } = props;
  const mintFn = props.mintFn ?? mintBulkAssetExportURL;
  const triggerDownload = props.triggerDownload ?? defaultTriggerDownload;

  const selected = useAtlas((s) => s.selection.selectedAtomicChildren);
  const clearBulkSelection = useAtlas((s) => s.clearBulkSelection);

  const [format, setFormat] = useState<"png" | "svg">(DEFAULT_FORMAT);
  const [scale, setScale] = useState<1 | 2 | 3>(DEFAULT_SCALE);
  const [state, setState] = useState<ExportState>({ kind: "idle" });

  // Build the preview rows from the live map. Sort by preview filename so
  // the list is stable across renders even when the underlying map
  // iteration order doesn't change. Memoized off the map identity.
  const rows: SelectionRow[] = useMemo(() => {
    const out: SelectionRow[] = [];
    for (const [key, figmaNodeID] of selected) {
      // key shape `screenID|figmaNodeID` — split on the first `|` only so
      // figma ids that themselves contain `|` (rare; node-ids include `:`)
      // round-trip cleanly.
      const sep = key.indexOf("|");
      const screenID = sep === -1 ? "" : key.slice(0, sep);
      const name = resolveNodeName?.(screenID, figmaNodeID) ?? null;
      const safe = name ? sanitizeFilename(name) : figmaNodeID;
      out.push({
        key,
        screenID,
        figmaNodeID,
        preview: `${safe}.${format}`,
      });
    }
    out.sort((a, b) => a.preview.localeCompare(b.preview));
    return out;
  }, [selected, resolveNodeName, format]);

  const onExport = useCallback(async () => {
    if (rows.length === 0) return;
    setState({ kind: "minting" });
    const res: ApiResult<BulkMintAssetResponse> = await mintFn(slug, {
      leafID,
      nodeIDs: rows.map((r) => r.figmaNodeID),
      format,
      scale,
    });
    if (!res.ok) {
      setState({ kind: "error", message: res.error || `HTTP ${res.status}` });
      return;
    }
    setState({ kind: "done", url: res.data.download_url });
    triggerDownload(res.data.download_url);
  }, [rows, mintFn, slug, leafID, format, scale, triggerDownload]);

  if (rows.length === 0) return null;

  const busy = state.kind === "minting";
  return (
    <div
      className="leafcv2-bulk-panel"
      role="dialog"
      aria-label="Bulk asset export"
    >
      <div className="leafcv2-bulk-panel__row">
        <strong>Export selection ({rows.length})</strong>
        <button
          type="button"
          className="leafcv2-bulk-panel__btn leafcv2-bulk-panel__btn--ghost"
          onClick={clearBulkSelection}
          aria-label="Clear bulk selection"
        >
          Clear
        </button>
      </div>
      <div className="leafcv2-bulk-panel__row">
        <label>
          Format:&nbsp;
          <select
            value={format}
            onChange={(e) => setFormat(e.target.value as "png" | "svg")}
            disabled={busy}
          >
            <option value="svg">SVG</option>
            <option value="png">PNG</option>
          </select>
        </label>
        {format === "png" && (
          <label>
            Scale:&nbsp;
            <select
              value={scale}
              onChange={(e) =>
                setScale(Number(e.target.value) as 1 | 2 | 3)
              }
              disabled={busy}
            >
              <option value={1}>1x</option>
              <option value={2}>2x</option>
              <option value={3}>3x</option>
            </select>
          </label>
        )}
      </div>
      <ul className="leafcv2-bulk-panel__list" aria-label="Export preview">
        {rows.slice(0, 12).map((r) => (
          <li key={r.key}>{r.preview}</li>
        ))}
        {rows.length > 12 && <li>+ {rows.length - 12} more…</li>}
      </ul>
      <div className="leafcv2-bulk-panel__row">
        <button
          type="button"
          className="leafcv2-bulk-panel__btn"
          onClick={() => void onExport()}
          disabled={busy}
        >
          {busy ? "Zipping…" : "Export selection"}
        </button>
        {state.kind === "done" && <span>Download started.</span>}
      </div>
      {state.kind === "error" && (
        <div className="leafcv2-bulk-panel__error">{state.message}</div>
      )}
    </div>
  );
}

// ─── Helpers (exported for tests) ───────────────────────────────────────────

/**
 * Trim, lowercase, replace runs of non-[A-Za-z0-9_-] with `-`, dedupe
 * adjacent dashes. Safe for use in filenames across OSes; matches the
 * server's collision-resolution input domain (`name + "-N" + ext`).
 */
export function sanitizeFilename(raw: string): string {
  const trimmed = raw.trim().toLowerCase();
  const replaced = trimmed.replace(/[^a-z0-9_-]+/g, "-");
  return replaced.replace(/-+/g, "-").replace(/^-|-$/g, "") || "untitled";
}

/**
 * Build the preview filename used in the panel + sent to the server as a
 * suggestion (the server is authoritative — it appends `-1`, `-2` when
 * names collide). Exported so the test asserts the shape directly.
 */
export function buildFilenamePreview(
  name: string | null,
  figmaNodeID: string,
  format: "png" | "svg",
): string {
  const base = name ? sanitizeFilename(name) : figmaNodeID;
  return `${base}.${format}`;
}

function defaultTriggerDownload(url: string): void {
  if (typeof window === "undefined") return;
  // Setting `window.location.href` on a same-origin signed-URL response
  // with `Content-Disposition: attachment` keeps the user on the page;
  // the browser handles the file dialog. `window.open` would risk a
  // popup blocker since this runs after an awaited fetch.
  window.location.href = url;
}
