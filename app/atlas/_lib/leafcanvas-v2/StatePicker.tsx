"use client";

/**
 * StatePicker — U14.
 *
 * Floating chip-row overlay that surfaces co-positioned design-state
 * variants (siblings sharing `(x, y, w, h)` rounded to 1 px). Mounted by
 * `LeafFrameRenderer` once per frame that has at least one state group.
 *
 * Why a chip-row at the top of the frame:
 *   - Mirrors the Tweaks-panel floating-overlay convention used elsewhere
 *     in the atlas (see `app/atlas/_lib/atlas.tsx:Tweaks panel`) — same
 *     positioning vocabulary (`position: absolute`, top-anchored, scoped
 *     z-index) so designers don't have to learn a new affordance.
 *   - Stays inside the frame wrapper so canvas pan/zoom transforms apply
 *     uniformly. Don't pin to viewport — the chip would lose context when
 *     the frame scrolls out of view.
 *
 * Click model: each chip is a `<button>` so keyboard nav (Tab + Space /
 * Enter) works for free. Active chip carries `aria-pressed="true"` and a
 * darkened background. Clicking the already-active chip is a no-op (the
 * store action is idempotent).
 *
 * Strict TS — no `// @ts-nocheck`.
 */

import { useCallback } from "react";
import type { CSSProperties, MouseEvent as ReactMouseEvent } from "react";

import { useAtlas } from "../../../../lib/atlas/live-store";

import {
  resolveActiveVariantID,
  type StateGroup,
} from "./visible-filter";

export interface StatePickerProps {
  /**
   * Frame the picker belongs to. Used to scope `activeStatesByFrame` so
   * cross-frame `groupKey` collisions never make a click in frame A shift
   * the variant inside frame B.
   */
  frameID: string;
  /** All state groups detected for this frame. Empty array → no picker. */
  groups: StateGroup[];
}

/**
 * Render a chip-row overlay with one chip per variant, stacked into rows
 * when the frame has multiple state groups. Returns null when there are
 * no groups so the renderer can mount unconditionally without a guard.
 */
export function StatePicker(props: StatePickerProps) {
  const { frameID, groups } = props;
  const picksForFrame = useAtlas((s) => s.selection.activeStatesByFrame.get(frameID));
  const setActiveState = useAtlas((s) => s.setActiveState);

  const onPick = useCallback(
    (groupKey: string, variantID: string) => {
      setActiveState(frameID, groupKey, variantID);
    },
    [frameID, setActiveState],
  );

  // Stop the click from bubbling up to the canvas pan/zoom layer or the
  // atomic-select handler in the parent renderer. The chip itself reads
  // its own data attributes for the action so we don't rely on event
  // delegation.
  const stop = useCallback((e: ReactMouseEvent<HTMLDivElement>) => {
    e.stopPropagation();
  }, []);

  if (groups.length === 0) return null;

  return (
    <div
      className="leafcv2-state-picker"
      data-frame-id={frameID}
      style={CONTAINER_STYLE}
      onClick={stop}
      onPointerDown={stop}
      onMouseDown={stop}
      onDoubleClick={stop}
      // The picker is above the wrapper's hit-testing layer but the
      // chips themselves shouldn't capture lasso starts on the gap
      // between rows — pointer-events:none on the container, "auto"
      // on the rows.
    >
      {groups.map((g) => {
        const pick = picksForFrame?.get(g.key);
        const activeID = resolveActiveVariantID(g, pick);
        return (
          <div key={g.key} className="leafcv2-state-picker__row" style={ROW_STYLE}>
            {g.variants.map((v) => {
              const isActive = v.figmaNodeID === activeID;
              return (
                <button
                  key={v.figmaNodeID}
                  type="button"
                  className="leafcv2-state-picker__chip"
                  data-active={isActive ? "true" : "false"}
                  data-group-key={g.key}
                  data-variant-id={v.figmaNodeID}
                  aria-pressed={isActive}
                  style={isActive ? ACTIVE_CHIP_STYLE : CHIP_STYLE}
                  onClick={(e) => {
                    e.stopPropagation();
                    onPick(g.key, v.figmaNodeID);
                  }}
                >
                  {v.name}
                </button>
              );
            })}
          </div>
        );
      })}
    </div>
  );
}

// ─── Styles ──────────────────────────────────────────────────────────────────
//
// Inlined so the picker stays self-contained — adding a CSS rule to
// `canvas-v2.css` would require touching the existing file's stylesheet
// authoring conventions and risk colliding with the bulk-panel block at
// the bottom (which has a known pre-existing missing brace; not in
// scope to fix here). Inline styles are fine for an overlay this small.

const CONTAINER_STYLE: CSSProperties = {
  position: "absolute",
  top: 8,
  left: 8,
  right: 8,
  // Row-stack across multiple state groups — each `g` becomes its own row.
  display: "flex",
  flexDirection: "column",
  gap: 4,
  // Sit above bulk-select outlines (z-index: 90 on the lasso) but below
  // the BulkExportPanel (z-index: 200).
  zIndex: 95,
  // Don't capture pointer events on the gaps between chips so the user
  // can still lasso-select / pan-zoom by clicking the frame whitespace.
  pointerEvents: "none",
};

const ROW_STYLE: CSSProperties = {
  display: "flex",
  gap: 4,
  flexWrap: "wrap",
  // Re-enable pointer events specifically on the row so the chips
  // themselves stay interactive.
  pointerEvents: "auto",
};

const CHIP_STYLE: CSSProperties = {
  appearance: "none",
  border: "1px solid rgba(0, 0, 0, 0.12)",
  borderRadius: 999,
  padding: "3px 10px",
  font: "500 11px/1.2 Inter, system-ui, sans-serif",
  background: "rgba(255, 255, 255, 0.92)",
  color: "rgba(0, 0, 0, 0.78)",
  cursor: "pointer",
  // Soft shadow so the chip floats over the frame surface.
  boxShadow: "0 1px 2px rgba(0, 0, 0, 0.06)",
  // Truncate long state names rather than wrapping inside one chip.
  maxWidth: 160,
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const ACTIVE_CHIP_STYLE: CSSProperties = {
  ...CHIP_STYLE,
  // Override the full `border` shorthand (not just borderColor) — mixing
  // shorthand on the inactive style with a longhand override on the active
  // style triggers React's "Removing a style property during rerender"
  // warning when toggling between the two.
  border: "1px solid rgba(20, 20, 24, 0.92)",
  background: "rgba(20, 20, 24, 0.92)",
  color: "#fff",
};
