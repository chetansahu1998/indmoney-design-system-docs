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

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties } from "react";

import {
  deleteTextOverride,
  exportLeafOverridesCSV,
  importLeafOverridesCSV,
  type CSVImportConflict,
  type CSVImportResponse,
  type TextOverride,
} from "../../../../lib/projects/client";
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

  // ─── U12: CSV export / import ──────────────────────────────────────────
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [csvBusy, setCsvBusy] = useState<"idle" | "exporting" | "importing">("idle");
  const [conflictModal, setConflictModal] = useState<{
    csv: string;
    conflicts: CSVImportConflict[];
  } | null>(null);

  const onExportCSV = useCallback(async () => {
    setError(null);
    setCsvBusy("exporting");
    const res = await exportLeafOverridesCSV(slug, leafID);
    setCsvBusy("idle");
    if (!res.ok) {
      setError(res.error || "CSV export failed");
      return;
    }
    // Trigger a download via Blob + URL.createObjectURL so the user
    // doesn't bounce to a new tab. Filename includes the leaf id so
    // multiple exports don't clobber each other in Downloads/.
    const blob = new Blob([res.data.csv], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `overrides-${leafID}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    // Defer revoke so Safari finishes the download before the URL is
    // invalidated.
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }, [slug, leafID]);

  const onImportCSV = useCallback(() => {
    setError(null);
    fileInputRef.current?.click();
  }, []);

  const handleFilePicked = useCallback(
    async (e: React.ChangeEvent<HTMLInputElement>) => {
      const file = e.target.files?.[0];
      // Reset the input so the user can re-pick the same file after a
      // failed import.
      if (fileInputRef.current) fileInputRef.current.value = "";
      if (!file) return;
      setCsvBusy("importing");
      const csvText = await file.text();
      const res = await importLeafOverridesCSV(slug, leafID, csvText);
      setCsvBusy("idle");
      if (!res.ok) {
        setError(res.error || "CSV import failed");
        return;
      }
      // Conflicts present → ask the user. Apply-all requires a second
      // round-trip with force=true; skip-all dismisses without applying.
      if (res.data.conflicts && res.data.conflicts.length > 0 && res.data.applied === 0) {
        setConflictModal({ csv: csvText, conflicts: res.data.conflicts });
        return;
      }
      // Refresh + report.
      void refreshLeafOverrides(leafID, slug);
      reportImportResult(res.data, setError);
    },
    [slug, leafID, refreshLeafOverrides],
  );

  const onConfirmApplyAll = useCallback(async () => {
    if (!conflictModal) return;
    setCsvBusy("importing");
    const res = await importLeafOverridesCSV(slug, leafID, conflictModal.csv, {
      force: true,
    });
    setCsvBusy("idle");
    setConflictModal(null);
    if (!res.ok) {
      setError(res.error || "CSV import failed");
      return;
    }
    void refreshLeafOverrides(leafID, slug);
    reportImportResult(res.data, setError);
  }, [conflictModal, slug, leafID, refreshLeafOverrides]);

  const onConfirmSkipAll = useCallback(() => {
    setConflictModal(null);
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
          <button
            type="button"
            className="lcv2-copy-csv-btn"
            onClick={onExportCSV}
            disabled={csvBusy !== "idle"}
          >
            {csvBusy === "exporting" ? "Exporting…" : "Export CSV"}
          </button>
          <button
            type="button"
            className="lcv2-copy-csv-btn"
            onClick={onImportCSV}
            disabled={csvBusy !== "idle"}
          >
            {csvBusy === "importing" ? "Importing…" : "Import CSV"}
          </button>
          <input
            ref={fileInputRef}
            type="file"
            accept=".csv,text/csv"
            style={{ display: "none" }}
            onChange={handleFilePicked}
            aria-hidden="true"
          />
        </div>
      </div>

      {conflictModal && (
        <CSVConflictModal
          conflicts={conflictModal.conflicts}
          onApplyAll={onConfirmApplyAll}
          onSkipAll={onConfirmSkipAll}
          busy={csvBusy === "importing"}
        />
      )}

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
 * Surface a CSV-import result to the user via the existing error pane.
 * "Errors" here are non-fatal — the server applied what it could and
 * flagged the rest with a per-row reason.
 */
function reportImportResult(
  data: CSVImportResponse,
  setError: (msg: string | null) => void,
): void {
  if (data.errors && data.errors.length > 0) {
    setError(
      `CSV import: ${data.applied} applied, ${data.errors.length} skipped (${data.errors[0].reason})`,
    );
    return;
  }
  setError(null);
}

// ─── CSV conflict confirmation modal (U12) ──────────────────────────────────
//
// Inline component (per the U12 plan: no new file). Renders when the import
// path returned `conflicts.length > 0`. Two actions:
//   - Apply all → second import call with force=true (last-write-wins)
//   - Skip all  → close the modal; nothing applied
//
// Per-row apply/skip is intentionally left out — translators almost always
// want the whole batch one way or the other, and per-row reconciliation
// adds a state machine the brainstorm v1 explicitly defers.

interface CSVConflictModalProps {
  conflicts: CSVImportConflict[];
  onApplyAll: () => void;
  onSkipAll: () => void;
  busy: boolean;
}

function CSVConflictModal(props: CSVConflictModalProps) {
  const { conflicts, onApplyAll, onSkipAll, busy } = props;
  return (
    <div
      className="lcv2-copy-modal-backdrop"
      role="dialog"
      aria-modal="true"
      aria-labelledby="lcv2-csv-conflict-title"
    >
      <div className="lcv2-copy-modal">
        <div className="lcv2-copy-modal-head">
          <h3 id="lcv2-csv-conflict-title" className="lcv2-copy-modal-title">
            {conflicts.length} {conflicts.length === 1 ? "row" : "rows"} changed since
            you exported
          </h3>
          <p className="lcv2-copy-modal-sub">
            The DB version is newer than your CSV. Apply your changes anyway, or
            skip them all and keep the live values.
          </p>
        </div>
        <div className="lcv2-copy-modal-list" role="list">
          {conflicts.slice(0, 50).map((c) => (
            <div key={`${c.screen_id}:${c.figma_node_id}`} className="lcv2-copy-modal-row" role="listitem">
              <div className="lcv2-copy-modal-row-head">
                <span className="lcv2-copy-modal-row-id">{c.figma_node_id}</span>
                <span className="lcv2-copy-modal-row-idx">row {c.row_index}</span>
              </div>
              <div className="lcv2-copy-modal-row-strings">
                <div className="lcv2-copy-modal-row-csv">
                  <span className="lcv2-copy-modal-row-label">your CSV</span>
                  <span className="lcv2-copy-modal-row-text">{c.csv_value}</span>
                </div>
                <div className="lcv2-copy-modal-row-cur">
                  <span className="lcv2-copy-modal-row-label">live now</span>
                  <span className="lcv2-copy-modal-row-text">{c.current_value}</span>
                </div>
              </div>
            </div>
          ))}
          {conflicts.length > 50 && (
            <div className="lcv2-copy-modal-more">
              … and {conflicts.length - 50} more
            </div>
          )}
        </div>
        <div className="lcv2-copy-modal-actions">
          <button
            type="button"
            className="lcv2-copy-modal-btn"
            onClick={onSkipAll}
            disabled={busy}
          >
            Skip all
          </button>
          <button
            type="button"
            className="lcv2-copy-modal-btn lcv2-copy-modal-btn-primary"
            onClick={onApplyAll}
            disabled={busy}
          >
            {busy ? "Applying…" : "Apply all"}
          </button>
        </div>
      </div>
    </div>
  );
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
