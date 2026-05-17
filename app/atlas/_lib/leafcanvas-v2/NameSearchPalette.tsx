"use client";

/**
 * NameSearchPalette — Cmd+F overlay that lets the user jump to any
 * named frame in the active leaf canvas (U3b).
 *
 * UX contract:
 *
 *   - Visible only when `open` is true. Mounting cost when closed is
 *     effectively zero (returns null).
 *   - Auto-focuses the search input on open so the user can type
 *     immediately without clicking.
 *   - Filters the frame list by substring match on the label
 *     (case-insensitive). Empty query shows every frame.
 *   - Arrow Up / Down move highlight by one row, with wrap-around at
 *     the ends. Home / End jump to first / last.
 *   - Enter activates the highlighted row (calls `onJumpToFrame(id)`
 *     then `onClose()`).
 *   - Escape closes the palette without jumping.
 *   - Click on a row activates it (same as Enter).
 *   - Click outside the palette closes it (caught via the backdrop
 *     div, not via document listeners — keeps the interaction local).
 *
 * Empty states:
 *
 *   - No frames at all → "No frames in this leaf." (rare; shows the
 *     palette empty so the user understands their query isn't being
 *     ignored).
 *   - Frames exist but query has no matches → "No match for '<q>'".
 *
 * The component is presentational. State (open / close / data
 * source) lives in AtlasShellInner; the palette receives `frames` as
 * a snapshot prop (refreshed each time the palette opens via
 * `listNamedFrames()` from camera-actions). Re-querying the registry
 * on every render would be wasteful and would surface stale closures
 * — open-time snapshot is the right shape.
 */

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
} from "react";

import type { NamedFrameEntry } from "./camera-actions";

export interface NameSearchPaletteProps {
  open: boolean;
  /** Snapshot taken when the palette opens — not re-read per render. */
  frames: NamedFrameEntry[];
  onClose: () => void;
  onJumpToFrame: (id: string) => void;
}

export function NameSearchPalette({
  open,
  frames,
  onClose,
  onJumpToFrame,
}: NameSearchPaletteProps): React.ReactElement | null {
  const [query, setQuery] = useState("");
  const [activeIdx, setActiveIdx] = useState(0);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);

  // Reset state on each open. Reading the snapshot fresh from props
  // means the consumer (AtlasShellInner) can also wipe the query by
  // toggling `open` off → on.
  useEffect(() => {
    if (open) {
      setQuery("");
      setActiveIdx(0);
      // Defer focus to next microtask so the input is mounted first.
      queueMicrotask(() => {
        inputRef.current?.focus();
      });
    }
  }, [open]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return frames;
    return frames.filter((f) => f.label.toLowerCase().includes(q));
  }, [frames, query]);

  // Clamp activeIdx whenever the filtered list changes underneath it.
  useEffect(() => {
    if (filtered.length === 0) {
      setActiveIdx(0);
      return;
    }
    setActiveIdx((i) => {
      if (i < 0) return 0;
      if (i >= filtered.length) return filtered.length - 1;
      return i;
    });
  }, [filtered]);

  // Scroll the highlighted row into view when activeIdx changes.
  useEffect(() => {
    if (!listRef.current) return;
    const el = listRef.current.querySelector<HTMLElement>(
      `[data-row-idx="${activeIdx}"]`,
    );
    el?.scrollIntoView({ block: "nearest" });
  }, [activeIdx]);

  const activate = useCallback(
    (idx: number) => {
      const target = filtered[idx];
      if (!target) return;
      onJumpToFrame(target.id);
      onClose();
    },
    [filtered, onJumpToFrame, onClose],
  );

  const onKeyDown = useCallback(
    (e: ReactKeyboardEvent<HTMLDivElement>) => {
      // The palette mounts as a sibling of the canvas; its own input
      // owns the keystroke, so the canvas keymap never sees these.
      // We intercept anyway to suppress bubbling to a future global
      // shortcut layer.
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        onClose();
        return;
      }
      if (e.key === "Enter") {
        e.preventDefault();
        e.stopPropagation();
        activate(activeIdx);
        return;
      }
      if (e.key === "ArrowDown") {
        e.preventDefault();
        if (filtered.length === 0) return;
        setActiveIdx((i) => (i + 1) % filtered.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        if (filtered.length === 0) return;
        setActiveIdx((i) => (i - 1 + filtered.length) % filtered.length);
        return;
      }
      if (e.key === "Home") {
        e.preventDefault();
        setActiveIdx(0);
        return;
      }
      if (e.key === "End") {
        e.preventDefault();
        if (filtered.length > 0) setActiveIdx(filtered.length - 1);
        return;
      }
    },
    [activate, activeIdx, filtered, onClose],
  );

  const onBackdropClick = useCallback(
    (e: ReactMouseEvent<HTMLDivElement>) => {
      // Only close when the click landed directly on the backdrop,
      // not on a child (the palette card). React's onClick already
      // distinguishes target vs currentTarget for us.
      if (e.target === e.currentTarget) onClose();
    },
    [onClose],
  );

  if (!open) return null;

  const showEmptyAll = frames.length === 0;
  const showNoMatch = !showEmptyAll && filtered.length === 0;

  return (
    <div
      className="leafcv2-search-palette__backdrop"
      onClick={onBackdropClick}
      onKeyDown={onKeyDown}
      role="presentation"
    >
      <div
        className="leafcv2-search-palette"
        role="dialog"
        aria-label="Find frame by name"
        aria-modal="true"
      >
        <input
          ref={inputRef}
          className="leafcv2-search-palette__input"
          type="text"
          placeholder={
            showEmptyAll
              ? "No frames in this leaf"
              : `Find a frame in ${frames.length}…`
          }
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          disabled={showEmptyAll}
          aria-autocomplete="list"
          aria-controls="leafcv2-search-palette-list"
          aria-activedescendant={
            filtered[activeIdx]
              ? `leafcv2-search-palette-row-${activeIdx}`
              : undefined
          }
          spellCheck={false}
          autoCorrect="off"
          autoCapitalize="off"
        />
        <div
          ref={listRef}
          id="leafcv2-search-palette-list"
          className="leafcv2-search-palette__list"
          role="listbox"
        >
          {showNoMatch && (
            <div className="leafcv2-search-palette__empty">
              No match for &ldquo;{query}&rdquo;.
            </div>
          )}
          {showEmptyAll && (
            <div className="leafcv2-search-palette__empty">
              No frames in this leaf.
            </div>
          )}
          {filtered.map((frame, idx) => (
            <div
              key={frame.id}
              id={`leafcv2-search-palette-row-${idx}`}
              data-row-idx={idx}
              role="option"
              aria-selected={idx === activeIdx}
              className={
                idx === activeIdx
                  ? "leafcv2-search-palette__row leafcv2-search-palette__row--active"
                  : "leafcv2-search-palette__row"
              }
              onMouseEnter={() => setActiveIdx(idx)}
              onClick={() => activate(idx)}
            >
              <span className="leafcv2-search-palette__row-label">{frame.label}</span>
              <span className="leafcv2-search-palette__row-id">{frame.id}</span>
            </div>
          ))}
        </div>
        <div className="leafcv2-search-palette__hint" aria-hidden="true">
          <kbd>↑</kbd>
          <kbd>↓</kbd>
          <span>navigate</span>
          <kbd>↵</kbd>
          <span>jump</span>
          <kbd>Esc</kbd>
          <span>close</span>
        </div>
      </div>
    </div>
  );
}
