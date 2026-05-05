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
import {
  ORPHAN_DRAG_MIME,
  decodeOrphanDrag,
  type OrphanDragPayload,
} from "./CopyOverridesTab";
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
  /**
   * U11 — fired when an orphan-override row is dropped onto a TEXT
   * atomic inside this frame. The host wires this to
   * `applyOrphanReattach` so the source row is deleted + a fresh active
   * row is created at the drop target. Non-TEXT atomics no-op (cursor
   * shows "no-drop" via `dropEffect = "none"`).
   */
  onDropOrphanOntoAtomic?: (
    screenID: string,
    figmaNodeID: string,
    payload: OrphanDragPayload,
    canonicalPath: string,
  ) => void;
}

interface CanonicalState {
  status: "idle" | "loading" | "ready" | "error" | "empty";
  tree?: CanonicalNode;
  error?: string;
}

const INITIAL_STATE: CanonicalState = { status: "idle" };

export function LeafFrameRenderer(props: LeafFrameRendererProps) {
  const { slug, screenID, label, width, height, onDropOrphanOntoAtomic } = props;

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

  // ─── Atomic-child selection (U7) ──────────────────────────────────────────
  // Single-click any TEXT / icon-cluster / RECTANGLE / ELLIPSE / VECTOR
  // atomic emits `selectAtomicChild`. Frame containers (FRAME) keep their
  // pass-through behaviour: clicking a frame walks up from the click
  // target to the nearest atomic descendant (D5).
  const selectAtomicChild = useAtlas((s) => s.selectAtomicChild);
  const handleClick = useCallback(
    (e: ReactMouseEvent<HTMLDivElement>) => {
      const target = e.target as HTMLElement | null;
      if (!target) return;
      const figmaID = findAtomicTarget(target, wrapperRef.current);
      if (!figmaID) return;
      // Stop propagation so the outer canvas pan/zoom layer doesn't also
      // interpret this as a frame focus event.
      e.stopPropagation();
      selectAtomicChild(screenID, figmaID);
    },
    [screenID, selectAtomicChild],
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

  // ─── U11 — orphan re-attach drop target ───────────────────────────────────
  // Drag-over: walk the click target up to the nearest TEXT atomic. If
  // we land on one, allow the drop; otherwise mark the cursor "no-drop".
  // We only react when the drag carries our custom MIME so the canvas
  // pan/zoom layer's drag detection isn't disturbed by foreign drags.
  const isOrphanDrag = useCallback((dt: DataTransfer | null): boolean => {
    if (!dt) return false;
    const types = dt.types;
    for (let i = 0; i < types.length; i++) {
      if (types[i] === ORPHAN_DRAG_MIME) return true;
    }
    return false;
  }, []);

  const handleDragOver = useCallback(
    (e: React.DragEvent<HTMLDivElement>) => {
      if (!onDropOrphanOntoAtomic) return;
      if (!isOrphanDrag(e.dataTransfer)) return;
      const target = e.target as HTMLElement | null;
      const textEl = target ? findTextAtomic(target, wrapperRef.current) : null;
      if (textEl) {
        e.preventDefault(); // signal "drop allowed here"
        e.dataTransfer.dropEffect = "move";
      } else {
        e.dataTransfer.dropEffect = "none";
      }
    },
    [isOrphanDrag, onDropOrphanOntoAtomic],
  );

  const handleDrop = useCallback(
    (e: React.DragEvent<HTMLDivElement>) => {
      if (!onDropOrphanOntoAtomic) return;
      if (!isOrphanDrag(e.dataTransfer)) return;
      const target = e.target as HTMLElement | null;
      const textEl = target ? findTextAtomic(target, wrapperRef.current) : null;
      if (!textEl) return; // non-TEXT atomic → no-op (Edge case in plan)
      const figmaID = textEl.getAttribute("data-figma-id");
      if (!figmaID) return;
      const raw = e.dataTransfer.getData(ORPHAN_DRAG_MIME);
      const payload = decodeOrphanDrag(raw);
      if (!payload) return;
      e.preventDefault();
      e.stopPropagation();
      // canonical_path is best-effort: the renderer may not have the
      // tree yet, in which case we pass an empty string and the host
      // (applyOrphanReattach) lets the server fall back on the existing
      // row's path.
      const path =
        state.status === "ready" && state.tree
          ? buildCanonicalPath(state.tree, figmaID)
          : "";
      onDropOrphanOntoAtomic(screenID, figmaID, payload, path);
    },
    [isOrphanDrag, onDropOrphanOntoAtomic, screenID, state],
  );

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
      style={{ width, height }}
      onClick={handleClick}
      onDoubleClick={handleDoubleClick}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
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
    </div>
  );
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
