"use client";

/**
 * LeafFrameRenderer — canvas v2.
 *
 * Replaces the flat-PNG rendering of `<window.PhoneFrame>` with a strict-TS
 * walker that converts each frame's `canonical_tree` into HTML+CSS using
 * `nodeToHTML`. Mounts per-frame and lazy-fetches the canonical_tree on
 * first intersection with the viewport (the existing `lib/atlas/data-
 * adapters.ts` only fetches the first 20 trees as part of edge inference,
 * so the rest arrive on demand here).
 *
 * Virtualization scaffold: an IntersectionObserver gates the fetch +
 * mount of expensive subtrees so a 100-frame leaf doesn't render every
 * canonical_tree at once. Off-screen frames render a skeleton at the
 * correct bbox so the canvas's auto-fit math stays stable.
 *
 * Strict TS: no `// @ts-nocheck`.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties, MouseEvent as ReactMouseEvent } from "react";

import {
  lazyFetchCanonicalTree,
  type TextOverride,
} from "../../../../lib/projects/client";
import { useAtlas } from "../../../../lib/atlas/live-store";

import { findByFigmaID } from "./AtomicChildInspector";
import { BulkExportPanel } from "./BulkExportPanel";
import { InlineTextEditor } from "./InlineTextEditor";
import { nodeToHTML } from "./nodeToHTML";
import type { CanonicalNode, ImageRefMap } from "./types";
import { filterVisible } from "./visible-filter";

import "./canvas-v2.css";

/**
 * Node types that count as "atomic" for inspector selection. Frame
 * containers stay pass-through per D5 — clicking them defers to the
 * nearest atomic descendant under the click target (handled by the
 * DOM walk in `findAtomicTarget`).
 */
const ATOMIC_TYPES: ReadonlySet<string> = new Set([
  "TEXT",
  "RECTANGLE",
  "ELLIPSE",
  "VECTOR",
  "STAR",
  "POLYGON",
  "LINE",
]);

export interface LeafFrameRendererProps {
  /** ds-service project slug — leaf id post brain-products migration. */
  slug: string;
  /** screens.id (= frame.id). */
  screenID: string;
  /** Display label (used when fallback / skeleton renders). */
  label?: string;
  /** Frame dimensions for skeleton sizing. */
  width: number;
  height: number;
}

interface CanonicalState {
  status: "idle" | "loading" | "ready" | "error" | "empty";
  tree?: CanonicalNode;
  error?: string;
}

const INITIAL_STATE: CanonicalState = { status: "idle" };

export function LeafFrameRenderer(props: LeafFrameRendererProps) {
  const { slug, screenID, label, width, height } = props;

  // Pull any pre-fetched tree from the live store so frames already
  // hydrated by data-adapters.ts skip the network round-trip.
  const cached = useAtlas((s) => {
    const slot = s.leafSlots[slug];
    if (!slot) return undefined;
    const map = slot.canonicalTreeByScreenID;
    if (!map) return undefined;
    return map[screenID];
  });

  const [state, setState] = useState<CanonicalState>(INITIAL_STATE);
  const [intersected, setIntersected] = useState(false);
  const wrapperRef = useRef<HTMLDivElement | null>(null);

  // ─── Atomic-child selection (U7 + U9) ─────────────────────────────────────
  // Single-click any TEXT / icon-cluster / RECTANGLE / ELLIPSE / VECTOR
  // atomic emits `selectAtomicChild`. Frame containers (FRAME) keep their
  // pass-through behaviour: clicking a frame walks up from the click
  // target to the nearest atomic descendant (D5).
  //
  // Shift-click (U9): toggles the atomic in `selectedAtomicChildren`
  // instead of replacing the single-select. The store auto-clears the
  // single selection once the bulk set grows past 1 so the inspector
  // and BulkExportPanel never overlap.
  const selectAtomicChild = useAtlas((s) => s.selectAtomicChild);
  const addToBulkSelection = useAtlas((s) => s.addToBulkSelection);
  const removeFromBulkSelection = useAtlas((s) => s.removeFromBulkSelection);
  const bulkSelected = useAtlas((s) => s.selection.selectedAtomicChildren);

  const handleClick = useCallback(
    (e: ReactMouseEvent<HTMLDivElement>) => {
      const target = e.target as HTMLElement | null;
      if (!target) return;
      const figmaID = findAtomicTarget(target, wrapperRef.current);
      if (!figmaID) return;
      // Stop propagation so the outer canvas pan/zoom layer doesn't also
      // interpret this as a frame focus event.
      e.stopPropagation();
      if (e.shiftKey) {
        const key = `${screenID}|${figmaID}`;
        if (bulkSelected.has(key)) {
          removeFromBulkSelection(screenID, figmaID);
        } else {
          addToBulkSelection(screenID, figmaID);
        }
        return;
      }
      selectAtomicChild(screenID, figmaID);
    },
    [
      screenID,
      selectAtomicChild,
      addToBulkSelection,
      removeFromBulkSelection,
      bulkSelected,
    ],
  );

  // ─── Atomic-child editing (U8) ────────────────────────────────────────────
  // Double-click a TEXT atomic → activates the InlineTextEditor over it.
  // Non-TEXT atomics (icons, shapes, frames) are no-ops per D2.
  const setOverride = useAtlas((s) => s.setOverride);
  const recordConflict = useAtlas((s) => s.recordConflict);
  const openLeafID = useAtlas((s) => s.selection.leafID);
  const [editing, setEditing] = useState<EditingTarget | null>(null);

  const handleDoubleClick = useCallback(
    (e: ReactMouseEvent<HTMLDivElement>) => {
      const target = e.target as HTMLElement | null;
      if (!target) return;
      // Walk up to the nearest TEXT element specifically — non-TEXT
      // atomics (RECTANGLE / cluster / VECTOR) are no-ops in v1 per D2.
      const textEl = findTextAtomic(target, wrapperRef.current);
      if (!textEl) return;
      const figmaID = textEl.getAttribute("data-figma-id");
      if (!figmaID) return;
      e.stopPropagation();
      e.preventDefault();
      // Look up the override + canonical_path from the cached tree so the
      // PUT body can carry both. Tree may be unavailable (lazy slot, error)
      // — bail out softly so dblclick doesn't crash on unhydrated frames.
      if (state.status !== "ready" || !state.tree) return;
      const found = findByFigmaID(state.tree, figmaID);
      if (!found || found.node.type !== "TEXT") return;
      const rect = textEl.getBoundingClientRect();
      const wrapperRect = wrapperRef.current?.getBoundingClientRect();
      // Position is local to the wrapper so CSS transforms (canvas zoom)
      // pass through cleanly.
      const left = wrapperRect ? rect.left - wrapperRect.left : rect.left;
      const top = wrapperRect ? rect.top - wrapperRect.top : rect.top;
      setEditing({
        figmaNodeID: figmaID,
        originalText: found.node.characters ?? "",
        canonicalPath: buildCanonicalPath(state.tree, figmaID),
        currentRevision: 0,
        bbox: { left, top, width: rect.width, height: rect.height },
        textStyle: extractTextStyle(textEl),
      });
    },
    [state],
  );

  const onEditorClose = useCallback(() => setEditing(null), []);
  const onSavedOverride = useCallback(
    (ov: TextOverride) => {
      if (!openLeafID) return;
      setOverride(openLeafID, ov);
    },
    [openLeafID, setOverride],
  );
  const onEditorConflict = useCallback(
    (currentRevision: number, currentValue: string) => {
      if (!openLeafID || !editing) return;
      recordConflict(
        openLeafID,
        screenID,
        editing.figmaNodeID,
        currentRevision,
        currentValue,
      );
    },
    [editing, openLeafID, recordConflict, screenID],
  );

  // ─── Intersection-based virtualization ────────────────────────────────────
  useEffect(() => {
    if (intersected) return;
    const el = wrapperRef.current;
    if (!el) return;
    if (typeof IntersectionObserver === "undefined") {
      setIntersected(true);
      return;
    }
    const obs = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) {
            setIntersected(true);
            obs.disconnect();
            break;
          }
        }
      },
      { rootMargin: "200px" },
    );
    obs.observe(el);
    return () => obs.disconnect();
  }, [intersected]);

  // ─── Tree resolution ──────────────────────────────────────────────────────
  useEffect(() => {
    if (!intersected) return;
    if (state.status === "loading" || state.status === "ready" || state.status === "empty") {
      return;
    }
    // Cached path: live-store has the tree already → use it without fetch.
    if (cached !== undefined) {
      if (cached === null) {
        setState({ status: "empty" });
      } else {
        setState({ status: "ready", tree: cached as CanonicalNode });
      }
      return;
    }

    let cancelled = false;
    setState({ status: "loading" });
    lazyFetchCanonicalTree(slug, screenID)
      .then((res) => {
        if (cancelled) return;
        if (!res.ok) {
          // 404 / 410 / 5xx — fall through to PNG path. Plan: empty state
          // here means "the renderer has no work to do" and the bridge is
          // expected to render PNG when canonical_tree is null.
          setState({ status: "empty" });
          return;
        }
        const tree = res.data.canonical_tree;
        if (!tree || typeof tree !== "object") {
          setState({ status: "empty" });
          return;
        }
        setState({ status: "ready", tree: tree as CanonicalNode });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setState({
          status: "error",
          error: err instanceof Error ? err.message : String(err),
        });
      });
    return () => {
      cancelled = true;
    };
  }, [intersected, slug, screenID, cached, state.status]);

  // ─── Filter visibility + tag co-positioned siblings (memoized) ────────────
  const filtered = useMemo(() => {
    if (state.status !== "ready" || !state.tree) return null;
    return filterVisible(state.tree);
  }, [state]);

  const imageRefs: ImageRefMap = useEmptyImageRefs();
  const rendered = useMemo(() => {
    if (!filtered || !filtered.absoluteBoundingBox) return null;
    return nodeToHTML(filtered, filtered.absoluteBoundingBox, null, { imageRefs }, "root");
  }, [filtered, imageRefs]);

  // ─── Lasso selection (U9) ────────────────────────────────────────────────
  // Pointer-down on canvas whitespace (an element that is NOT inside an
  // atomic) starts a selection rectangle. Pointer-move expands it;
  // pointer-up commits every atomic whose DOM bbox intersects the lasso
  // rect to `selectedAtomicChildren`. Frames are never added — only their
  // atomic descendants — so a lasso that covers a FRAME picks up only
  // the icons/texts inside it.
  const [lasso, setLasso] = useState<LassoRect | null>(null);
  const lassoStartRef = useRef<{ x: number; y: number } | null>(null);

  const onPointerDown = useCallback(
    (e: ReactMouseEvent<HTMLDivElement>) => {
      // Only the primary button starts a lasso; right-click / middle pan
      // are owned by the outer canvas shell.
      if (e.button !== 0) return;
      const target = e.target as HTMLElement | null;
      if (!target) return;
      // If the user clicked through to an atomic, the click handler owns
      // the interaction (single-click select / shift-click toggle). Lasso
      // only kicks in on whitespace.
      if (findAtomicTarget(target, wrapperRef.current)) return;
      const wrapper = wrapperRef.current;
      if (!wrapper) return;
      const rect = wrapper.getBoundingClientRect();
      const x = e.clientX - rect.left;
      const y = e.clientY - rect.top;
      lassoStartRef.current = { x, y };
      setLasso({ left: x, top: y, width: 0, height: 0 });
    },
    [],
  );

  const onPointerMove = useCallback(
    (e: ReactMouseEvent<HTMLDivElement>) => {
      const start = lassoStartRef.current;
      if (!start) return;
      const wrapper = wrapperRef.current;
      if (!wrapper) return;
      const rect = wrapper.getBoundingClientRect();
      const x = e.clientX - rect.left;
      const y = e.clientY - rect.top;
      setLasso({
        left: Math.min(start.x, x),
        top: Math.min(start.y, y),
        width: Math.abs(x - start.x),
        height: Math.abs(y - start.y),
      });
    },
    [],
  );

  const onPointerUp = useCallback(() => {
    const start = lassoStartRef.current;
    lassoStartRef.current = null;
    if (!start || !lasso) {
      setLasso(null);
      return;
    }
    // Treat anything smaller than ~4px as a click, not a drag.
    if (lasso.width < 4 && lasso.height < 4) {
      setLasso(null);
      return;
    }
    const wrapper = wrapperRef.current;
    if (!wrapper) {
      setLasso(null);
      return;
    }
    const wrapperRect = wrapper.getBoundingClientRect();
    // Convert lasso (wrapper-local) to viewport coords for getBoundingClientRect comparison.
    const lassoVp = {
      left: lasso.left + wrapperRect.left,
      top: lasso.top + wrapperRect.top,
      right: lasso.left + lasso.width + wrapperRect.left,
      bottom: lasso.top + lasso.height + wrapperRect.top,
    };
    // Walk every atomic-tagged DOM node under the wrapper. We rely on
    // `data-figma-type` markup that nodeToHTML emits for atomic types, plus
    // `data-cluster="true"` for icon clusters. FRAMEs are excluded by
    // construction — they don't get selected even if they're inside the
    // lasso.
    const candidates = wrapper.querySelectorAll<HTMLElement>(
      `[data-figma-type="TEXT"],` +
        `[data-figma-type="RECTANGLE"],` +
        `[data-figma-type="ELLIPSE"],` +
        `[data-figma-type="VECTOR"],` +
        `[data-figma-type="STAR"],` +
        `[data-figma-type="POLYGON"],` +
        `[data-figma-type="LINE"],` +
        `[data-cluster="true"],[data-cluster-pending="true"]`,
    );
    for (const el of Array.from(candidates)) {
      const figmaID = el.getAttribute("data-figma-id");
      if (!figmaID) continue;
      const r = el.getBoundingClientRect();
      if (
        r.right >= lassoVp.left &&
        r.left <= lassoVp.right &&
        r.bottom >= lassoVp.top &&
        r.top <= lassoVp.bottom
      ) {
        addToBulkSelection(screenID, figmaID);
      }
    }
    setLasso(null);
  }, [lasso, screenID, addToBulkSelection]);

  // ─── Paint bulk-selected outlines onto matching atomics ──────────────────
  // We tag DOM nodes with `data-bulk-selected="true"` after each render so
  // CSS can show the highlight without React owning the per-atomic
  // markup. Ref-driven — survives re-renders triggered by the bulk Map.
  useEffect(() => {
    const wrapper = wrapperRef.current;
    if (!wrapper) return;
    // Clear stale flags first.
    const stale = wrapper.querySelectorAll<HTMLElement>('[data-bulk-selected="true"]');
    for (const el of Array.from(stale)) {
      el.removeAttribute("data-bulk-selected");
    }
    if (bulkSelected.size === 0) return;
    for (const [key, figmaNodeID] of bulkSelected) {
      const sep = key.indexOf("|");
      const sid = sep === -1 ? "" : key.slice(0, sep);
      if (sid !== screenID) continue;
      const el = wrapper.querySelector<HTMLElement>(
        `[data-figma-id="${cssEscapeAttr(figmaNodeID)}"]`,
      );
      if (el) el.setAttribute("data-bulk-selected", "true");
    }
  }, [bulkSelected, screenID, rendered]);

  // ─── Render ──────────────────────────────────────────────────────────────
  // Outer wrapper — sized to the frame's PNG bbox so the canvas's auto-fit
  // math stays stable whether we're showing the skeleton, the rendered
  // tree, or an error.
  return (
    <div
      ref={wrapperRef}
      className="leafcv2-frame"
      data-screen-id={screenID}
      data-status={state.status}
      style={{ width, height, position: "relative" }}
      onClick={handleClick}
      onDoubleClick={handleDoubleClick}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
    >
      {rendered ?? <Skeleton label={label} status={state.status} />}
      {editing && openLeafID && (
        <div
          className="leafcv2-inline-editor-host"
          style={{
            position: "absolute",
            left: editing.bbox.left,
            top: editing.bbox.top,
            width: editing.bbox.width,
            height: editing.bbox.height,
            zIndex: 100,
          }}
        >
          <InlineTextEditor
            slug={slug}
            leafID={openLeafID}
            screenID={screenID}
            figmaNodeID={editing.figmaNodeID}
            originalText={editing.originalText}
            canonicalPath={editing.canonicalPath}
            currentRevision={editing.currentRevision}
            textStyle={editing.textStyle}
            onSavedOverride={onSavedOverride}
            onConflict={onEditorConflict}
            onClose={onEditorClose}
          />
        </div>
      )}
      {lasso && (
        <div
          className="leafcv2-lasso"
          style={{
            left: lasso.left,
            top: lasso.top,
            width: lasso.width,
            height: lasso.height,
          }}
          aria-hidden="true"
        />
      )}
      {openLeafID && (
        <BulkExportPanel
          slug={slug}
          leafID={openLeafID}
          resolveNodeName={(sid, fid) => {
            // Only resolve names for our own screen — cross-screen rows
            // (rare; lasso is per-frame) fall through to the figmaNodeID
            // preview gracefully.
            if (sid !== screenID) return null;
            if (state.status !== "ready" || !state.tree) return null;
            const found = findByFigmaID(state.tree, fid);
            return found?.node.name ?? null;
          }}
        />
      )}
    </div>
  );
}

/**
 * Lasso rectangle in wrapper-local coordinates. `pointermove` widens it;
 * `pointerup` consumes it for the intersection test then clears.
 */
interface LassoRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

/**
 * Minimal CSSOM `CSS.escape` polyfill scoped to attribute values. Figma
 * node ids contain `:` which is a CSS pseudo-selector marker; without
 * escaping, `querySelector('[data-figma-id="123:456"]')` throws.
 */
function cssEscapeAttr(s: string): string {
  return s.replace(/(["\\])/g, "\\$1");
}

interface EditingTarget {
  figmaNodeID: string;
  originalText: string;
  canonicalPath: string;
  currentRevision: number;
  bbox: { left: number; top: number; width: number; height: number };
  textStyle: CSSProperties;
}

/**
 * Find the nearest TEXT atomic from a click target. Mirrors
 * `findAtomicTarget` but narrows to TEXT only — clusters / shapes are
 * no-ops for the editor per D2.
 */
function findTextAtomic(
  el: HTMLElement,
  wrapper: HTMLDivElement | null,
): HTMLElement | null {
  let cur: HTMLElement | null = el;
  while (cur && cur !== wrapper) {
    const type = cur.getAttribute("data-figma-type");
    if (type === "TEXT") return cur;
    cur = cur.parentElement;
  }
  return null;
}

/**
 * Walk the canonical_tree building "Frame/Section/Label" style ancestor
 * paths so the override PUT body carries enough context for the U2
 * reattach logic. Mirrors `services/ds-service/internal/projects/
 * canonical_tree_index.go:buildPath` — joins ancestor `name` fields with
 * "/". Falls back to "" when the node can't be located.
 */
function buildCanonicalPath(tree: CanonicalNode, targetID: string): string {
  const trail: string[] = [];
  function walk(node: CanonicalNode, parents: string[]): boolean {
    const here = [...parents, node.name ?? node.id ?? ""];
    if (node.id === targetID) {
      trail.push(...here);
      return true;
    }
    const kids = Array.isArray(node.children) ? node.children : [];
    for (const c of kids) {
      if (!c || typeof c !== "object") continue;
      if (walk(c as CanonicalNode, here)) return true;
    }
    return false;
  }
  walk(tree, []);
  return trail.join("/");
}

/**
 * Capture the rendered text style off the live span so the editor can
 * paint the contenteditable replica with byte-identical typography. We
 * read computed styles instead of inline style because `nodeToHTML` can
 * cascade some properties via parent rules.
 */
function extractTextStyle(el: HTMLElement): CSSProperties {
  const cs = window.getComputedStyle(el);
  return {
    fontFamily: cs.fontFamily,
    fontSize: cs.fontSize,
    fontWeight: cs.fontWeight,
    color: cs.color,
    lineHeight: cs.lineHeight,
    letterSpacing: cs.letterSpacing,
    textAlign: cs.textAlign as CSSProperties["textAlign"],
    fontStyle: cs.fontStyle,
    whiteSpace: cs.whiteSpace as CSSProperties["whiteSpace"],
  };
}

function Skeleton({
  label,
  status,
}: {
  label?: string;
  status: CanonicalState["status"];
}) {
  if (status === "error") {
    return (
      <div className="leafcv2-skeleton leafcv2-skeleton--error">
        <div className="leafcv2-skeleton__label">{label ?? "Frame"}</div>
        <div className="leafcv2-skeleton__sub">render failed</div>
      </div>
    );
  }
  if (status === "empty") {
    // No canonical_tree available — the bridge will render the PNG path.
    // We render an invisible spacer so the canvas math stays right; the
    // PNG sits behind / above this layer per real-data-bridge wiring.
    return <div className="leafcv2-skeleton leafcv2-skeleton--empty" aria-hidden="true" />;
  }
  return (
    <div className="leafcv2-skeleton" aria-hidden="true">
      <div className="leafcv2-skeleton__shimmer" />
    </div>
  );
}

// Stub until U7 wires the asset-export client. Memoized to a stable
// reference so the `useMemo` for `rendered` doesn't churn each render.
function useEmptyImageRefs(): ImageRefMap {
  return EMPTY_IMAGE_REFS;
}

const EMPTY_IMAGE_REFS: ImageRefMap = Object.freeze({}) as ImageRefMap;

/**
 * Walk up from the click target to the nearest atomic node. Returns the
 * Figma node id or null when the user clicked through to a non-atomic
 * (e.g. a transparent FRAME edge with no shape under the cursor).
 *
 * Resolution order:
 *   1. Climb until we hit a `data-figma-type` whose value is in
 *      ATOMIC_TYPES (TEXT, shapes, etc.) — single click selects that.
 *   2. Or until we hit a `data-cluster="true"` / `data-cluster-pending`
 *      element — icon-cluster wrappers count as atomic per the plan.
 *   3. We never select FRAMEs (D5 — frames pass-through), so if we walk
 *      out of the wrapper without hitting either, return null.
 */
function findAtomicTarget(
  el: HTMLElement,
  wrapper: HTMLDivElement | null,
): string | null {
  let cur: HTMLElement | null = el;
  while (cur && cur !== wrapper) {
    const cluster = cur.getAttribute("data-cluster");
    const clusterPending = cur.getAttribute("data-cluster-pending");
    if (cluster === "true" || clusterPending === "true") {
      const id = cur.getAttribute("data-figma-id");
      if (id) return id;
    }
    const type = cur.getAttribute("data-figma-type");
    if (type && ATOMIC_TYPES.has(type)) {
      const id = cur.getAttribute("data-figma-id");
      if (id) return id;
    }
    cur = cur.parentElement;
  }
  return null;
}
