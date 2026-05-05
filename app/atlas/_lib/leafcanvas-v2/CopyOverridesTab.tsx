"use client";

/**
 * CopyOverridesTab — U11.
 *
 * Per-leaf "Copy overrides" inspector tab. Lists every active +
 * orphaned text-override the open leaf knows about, with:
 *   - free-text search across original / current / screen name,
 *   - status filter (all / active / orphaned),
 *   - sort by updated_at | screen | who (asc/desc),
 *   - inline "Reset" button (DELETE on the row),
 *   - drag-to-reattach for orphaned rows (HTML5 drag-and-drop;
 *     LeafFrameRenderer wires the drop side).
 *
 * Data source: the open leaf slot's `overrides` map already populated
 * by `fetchLeafOverlays` at leaf-load time. SSE-driven cache freshness
 * comes from `applyEvent` in `live-store.ts`; this component triggers
 * an explicit `refreshLeafOverrides` on mount + when the slug/leafID
 * changes so the list reflects out-of-band edits within 1s.
 *
 * Strict TS — no `// @ts-nocheck`.
 */

import { useCallback, useEffect, useMemo, useState } from "react";
import type { CSSProperties } from "react";

import { deleteTextOverride, type TextOverride } from "../../../../lib/projects/client";
import { useAtlas } from "../../../../lib/atlas/live-store";
import type { Frame } from "../../../../lib/atlas/types";

// ─── Drag-and-drop wire format ────────────────────────────────────────────────

/**
 * MIME type used by the HTML5 drag-and-drop bridge between the
 * Copy Overrides tab and `LeafFrameRenderer`. Custom (non-standard)
 * MIME types prevent collisions with browser-native drags (file,
 * text/plain) and let the renderer cheaply detect "this drag is one
 * of ours" in `onDragOver`.
 */
export const ORPHAN_DRAG_MIME = "application/x-leafcv2-orphan-override";

/**
 * Payload serialised onto a drag event. `LeafFrameRenderer` deserialises
 * it on drop and forwards to `applyOrphanReattach`.
 */
export interface OrphanDragPayload {
  kind: "orphan-override";
  leafID: string;
  slug: string;
  /** The whole orphan row — applyOrphanReattach needs value + ids. */
  orphan: TextOverride;
}

export function encodeOrphanDrag(p: OrphanDragPayload): string {
  return JSON.stringify(p);
}

export function decodeOrphanDrag(s: string): OrphanDragPayload | null {
  try {
    const parsed = JSON.parse(s) as Partial<OrphanDragPayload>;
    if (parsed && parsed.kind === "orphan-override" && parsed.orphan) {
      return parsed as OrphanDragPayload;
    }
  } catch {
    return null;
  }
  return null;
}

// ─── Sort + filter ────────────────────────────────────────────────────────────

export type CopySort = "updated_desc" | "updated_asc" | "screen" | "who";
export type CopyStatusFilter = "all" | "active" | "orphaned";

/**
 * Pure list-pipeline: filter by status + free-text query, then sort.
 * Exposed for the unit-test harness; the component memos this.
 */
export function applyOverridePipeline(
  rows: ReadonlyArray<DisplayOverrideRow>,
  status: CopyStatusFilter,
  query: string,
  sort: CopySort,
): DisplayOverrideRow[] {
  const q = query.trim().toLowerCase();
  const filtered = rows.filter((r) => {
    if (status !== "all" && r.row.status !== status) return false;
    if (!q) return true;
    return (
      r.screenLabel.toLowerCase().includes(q) ||
      r.row.last_seen_original_text.toLowerCase().includes(q) ||
      r.row.value.toLowerCase().includes(q)
    );
  });
  return sortOverrides(filtered, sort);
}

function sortOverrides(
  rows: DisplayOverrideRow[],
  sort: CopySort,
): DisplayOverrideRow[] {
  const out = rows.slice();
  out.sort((a, b) => {
    switch (sort) {
      case "updated_desc":
        return cmpDate(b.row.updated_at, a.row.updated_at);
      case "updated_asc":
        return cmpDate(a.row.updated_at, b.row.updated_at);
      case "screen":
        return a.screenLabel.localeCompare(b.screenLabel);
      case "who":
        return a.row.updated_by_user_id.localeCompare(b.row.updated_by_user_id);
      default:
        return 0;
    }
  });
  return out;
}

function cmpDate(a: string, b: string): number {
  // Empty timestamps (synth rows from 409 conflicts) sort last so they
  // never pollute the head of the list.
  const ta = a ? Date.parse(a) : Number.NEGATIVE_INFINITY;
  const tb = b ? Date.parse(b) : Number.NEGATIVE_INFINITY;
  return ta - tb;
}

// ─── Display row shape ────────────────────────────────────────────────────────

export interface DisplayOverrideRow {
  row: TextOverride;
  /** Frame label resolved from leafSlot.frames. Falls back to screen id. */
  screenLabel: string;
}

// ─── Component ────────────────────────────────────────────────────────────────

export interface CopyOverridesTabProps {
  /** Project slug — required for refresh + delete + reattach calls. */
  slug: string;
  /** Open leaf id (= flow id post brain-products). */
  leafID: string;
}

export function CopyOverridesTab(props: CopyOverridesTabProps) {
  const { slug, leafID } = props;

  const slot = useAtlas((s) => s.leafSlots[leafID]);
  const userDirectory = useAtlas((s) => s.userDirectory);
  const refreshLeafOverrides = useAtlas((s) => s.refreshLeafOverrides);

  // ─── Refresh on mount + on identity change. SSE rebroadcasts piggyback
  //     on the same path; the live store's `applyEvent` doesn't (yet) pull
  //     overrides directly, but the user can still see fresh data within
  //     1s of an audit_complete because that path also re-runs
  //     `fetchLeafOverlays`. Until then, an explicit refresh on tab
  //     mount keeps "another user edited an override" current. ───
  useEffect(() => {
    void refreshLeafOverrides(leafID, slug);
  }, [leafID, slug, refreshLeafOverrides]);

  const rows = useMemo(
    () => collectOverrides(slot?.overrides, slot?.frames ?? []),
    [slot?.overrides, slot?.frames],
  );

  const [statusFilter, setStatusFilter] = useState<CopyStatusFilter>("all");
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<CopySort>("updated_desc");
  const [resetting, setResetting] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const visible = useMemo(
    () => applyOverridePipeline(rows, statusFilter, query, sort),
    [rows, statusFilter, query, sort],
  );

  const onReset = useCallback(
    async (r: DisplayOverrideRow) => {
      setError(null);
      setResetting(rowKey(r.row));
      const res = await deleteTextOverride(slug, r.row.screen_id, r.row.figma_node_id);
      setResetting(null);
      if (!res.ok) {
        setError(res.error || "Reset failed");
        return;
      }
      // Server-confirmed delete; refresh the cache so this row drops.
      void refreshLeafOverrides(leafID, slug);
    },
    [slug, leafID, refreshLeafOverrides],
  );

  // CSV buttons — wired but stubbed; U12 swaps in real handlers.
  const onExportCSV = useCallback(() => {
    // U12 will replace this with a GET to
    // /v1/projects/:slug/leaves/:leaf_id/text-overrides/csv that streams
    // a CSV download. Keeping the button visible now lets us wire the
    // tab into the LeafInspector with real estate already allocated.
    // eslint-disable-next-line no-console
    console.log("U12");
  }, []);
  const onImportCSV = useCallback(() => {
    // eslint-disable-next-line no-console
    console.log("U12");
  }, []);

  if (!slot) {
    return (
      <div className="lcv2-copy-empty">
        <div className="lcv2-copy-empty-h">No leaf selected</div>
      </div>
    );
  }

  return (
    <div className="lcv2-copy">
      <div className="lcv2-copy-toolbar">
        <input
          type="search"
          className="lcv2-copy-search"
          placeholder="Search overrides…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          aria-label="Search overrides"
        />
        <div className="lcv2-copy-filters">
          {(["all", "active", "orphaned"] as const).map((s) => (
            <button
              key={s}
              type="button"
              className={`lcv2-copy-filter ${statusFilter === s ? "is-active" : ""}`}
              onClick={() => setStatusFilter(s)}
            >
              {s}
              {s !== "all" && (
                <span className="lcv2-copy-filter-num">
                  {rows.filter((r) => r.row.status === s).length}
                </span>
              )}
            </button>
          ))}
        </div>
        <select
          className="lcv2-copy-sort"
          value={sort}
          onChange={(e) => setSort(e.target.value as CopySort)}
          aria-label="Sort overrides"
        >
          <option value="updated_desc">Newest first</option>
          <option value="updated_asc">Oldest first</option>
          <option value="screen">Screen A→Z</option>
          <option value="who">Editor A→Z</option>
        </select>
      </div>

      <div className="lcv2-copy-meta">
        <span>
          {visible.length} {visible.length === 1 ? "override" : "overrides"}
          {visible.length !== rows.length ? ` (of ${rows.length})` : ""}
        </span>
        <div className="lcv2-copy-csv">
          <button type="button" className="lcv2-copy-csv-btn" onClick={onExportCSV}>
            Export CSV
          </button>
          <button type="button" className="lcv2-copy-csv-btn" onClick={onImportCSV}>
            Import CSV
          </button>
        </div>
      </div>

      {error && <div className="lcv2-copy-error">{error}</div>}

      <div className="lcv2-copy-list" role="list">
        {visible.length === 0 ? (
          <div className="lcv2-copy-empty">
            <div className="lcv2-copy-empty-h">No overrides match</div>
          </div>
        ) : (
          visible.map((r) => (
            <OverrideRow
              key={rowKey(r.row)}
              row={r}
              slug={slug}
              leafID={leafID}
              busy={resetting === rowKey(r.row)}
              onReset={() => onReset(r)}
              authorLabel={resolveAuthor(userDirectory, r.row.updated_by_user_id)}
            />
          ))
        )}
      </div>
    </div>
  );
}

// ─── Row ──────────────────────────────────────────────────────────────────────

interface OverrideRowProps {
  row: DisplayOverrideRow;
  slug: string;
  leafID: string;
  busy: boolean;
  authorLabel: string;
  onReset: () => void;
}

function OverrideRow(props: OverrideRowProps) {
  const { row, slug, leafID, busy, authorLabel, onReset } = props;
  const isOrphan = row.row.status === "orphaned";

  // HTML5 drag start — only orphans are draggable. Encode the payload
  // so LeafFrameRenderer can drop it onto a TEXT atomic in the canvas.
  const onDragStart = useCallback(
    (e: React.DragEvent<HTMLDivElement>) => {
      if (!isOrphan) return;
      const payload: OrphanDragPayload = {
        kind: "orphan-override",
        leafID,
        slug,
        orphan: row.row,
      };
      const enc = encodeOrphanDrag(payload);
      e.dataTransfer.setData(ORPHAN_DRAG_MIME, enc);
      // Some browsers require `text/plain` to enable drag at all; mirror
      // the encoded payload so it stays self-describing if dropped on a
      // generic target.
      e.dataTransfer.setData("text/plain", row.row.value);
      e.dataTransfer.effectAllowed = "move";
    },
    [isOrphan, leafID, slug, row.row],
  );

  return (
    <div
      className="lcv2-copy-row"
      data-status={row.row.status}
      role="listitem"
      draggable={isOrphan}
      onDragStart={onDragStart}
    >
      <div className="lcv2-copy-row-head">
        <span className="lcv2-copy-screen">{row.screenLabel}</span>
        <span
          className="lcv2-copy-pill"
          data-status={row.row.status}
          aria-label={`Status: ${row.row.status}`}
        >
          {row.row.status}
        </span>
      </div>
      <div className="lcv2-copy-strings">
        <div className="lcv2-copy-original" title="Original Figma text">
          <span className="lcv2-copy-strings-label">orig</span>
          <span className="lcv2-copy-strings-text">
            {row.row.last_seen_original_text || <em>(empty)</em>}
          </span>
        </div>
        <div className="lcv2-copy-current" title="Current override value">
          <span className="lcv2-copy-strings-label">now</span>
          <span className="lcv2-copy-strings-text">
            {row.row.value || <em>(empty)</em>}
          </span>
        </div>
      </div>
      <div className="lcv2-copy-row-foot">
        <span className="lcv2-copy-author">{authorLabel}</span>
        <span className="lcv2-copy-dot">·</span>
        <span className="lcv2-copy-when" title={row.row.updated_at}>
          {relativeTime(row.row.updated_at)}
        </span>
        <button
          type="button"
          className="lcv2-copy-reset"
          onClick={onReset}
          disabled={busy}
          aria-label={`Reset override on ${row.screenLabel}`}
        >
          {busy ? "…" : "Reset"}
        </button>
      </div>
      {isOrphan && (
        <div
          className="lcv2-copy-orphan-hint"
          style={{ "--lcv2-copy-orphan-hint-show": "block" } as CSSProperties}
        >
          Drag onto a text layer in the canvas to re-attach.
        </div>
      )}
    </div>
  );
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function rowKey(o: TextOverride): string {
  return `${o.screen_id}:${o.figma_node_id}:${o.id || "synth"}`;
}

/**
 * Walk the per-screen Map<figmaNodeID, TextOverride> structure into a
 * flat array of display rows joined with their frame label.
 */
export function collectOverrides(
  byScreen: Record<string, Map<string, TextOverride>> | undefined,
  frames: ReadonlyArray<Frame>,
): DisplayOverrideRow[] {
  if (!byScreen) return [];
  const labelByID = new Map(frames.map((f) => [f.id, f.label]));
  const out: DisplayOverrideRow[] = [];
  for (const [screenID, m] of Object.entries(byScreen)) {
    const screenLabel = labelByID.get(screenID) ?? screenID;
    for (const ov of m.values()) {
      // `recordConflict` may have stamped a synth row with empty
      // canonical_path / id; we still surface those so users see the
      // conflicted state — guard rendering, not collection.
      out.push({ row: ov, screenLabel });
    }
  }
  return out;
}

function resolveAuthor(directory: Record<string, string>, id: string): string {
  if (!id) return "—";
  return directory[id] ?? shortenID(id);
}

function shortenID(id: string): string {
  if (id.length <= 8) return id;
  return `${id.slice(0, 4)}…${id.slice(-4)}`;
}

/**
 * Lightweight relative-time formatter — "now", "2m", "2h", "3d", or
 * a localised date when older than a week. Avoids a date library so
 * the canvas-v2 bundle stays small.
 */
export function relativeTime(iso: string, now: number = Date.now()): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "—";
  const diff = Math.max(0, now - t);
  const sec = Math.floor(diff / 1000);
  if (sec < 30) return "now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const days = Math.floor(hr / 24);
  if (days < 7) return `${days}d ago`;
  return new Date(t).toLocaleDateString();
}
