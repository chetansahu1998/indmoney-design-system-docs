// @ts-nocheck
//
// STAGED-REMOVAL CONTEXT (plan 2026-05-06-003 follow-up):
//
// This @ts-nocheck is preserved deliberately. The model types,
// declare-global window augmentations, and component prop interfaces
// below are real — they typecheck under tsc and they document the
// shape this file consumes. They were added in the U7 follow-up as
// scaffolding for an eventual full removal of this directive.
//
// Why @ts-nocheck still ships: removing it surfaces ~60 mechanical
// type errors across 8 sub-components (LeafCanvas, LeafTopBar,
// LeafInspector, OverviewTab, DecisionsTab, ActivityTab, CommentsTab,
// PhoneFrame) — implicit-any event handlers, prop binding elements,
// window.PhoneFrame JSX usage, and several "possibly undefined" call
// sites on the legacy window.build* builders. Each fix is small but
// the surface is large enough that landing it as one commit risks
// silent regression in the canvas's RAF/IO/camera pipeline (where the
// recent canvas-refresh fix lives, commit b9b4377). The right shape
// is a focused PR with manual smoke testing of the canvas.
//
// Tracker: see GitHub issue (filed alongside this commit) for the
// staged-removal checklist. Each sub-component is its own item.
"use client";
import React, { useEffect, useLayoutEffect, useRef, useState, useMemo, useCallback } from "react";
import { CopyOverridesTab } from "./leafcanvas-v2/CopyOverridesTab";
import { setLeafZoom } from "./leafcanvas-v2/leaf-zoom-signal";
import { canvasGestureTracker } from "./leafcanvas-v2/gesture-tracker";
import { clog } from "./leafcanvas-v2/canvas-log";
import {
  animateCamera,
  computeFitCamera,
  registerSnapTarget,
  SNAP_DURATION_MS,
  type CancelToken,
  type SnapBBox,
} from "./leafcanvas-v2/camera-snap";

// ─── Loose model types ──────────────────────────────────────────────────
// These describe only the fields this file reads. The upstream brain /
// canvas builders (window.buildLeafCanvas etc.) produce richer objects;
// we deliberately don't import their full DTOs because they're undeclared
// in TS-land (the legacy JS path predates typing).
// Loose model types. The bag uses `any` (not `unknown`) intentionally:
// the legacy builders (window.buildLeafCanvas, window.buildViolations)
// produce ad-hoc shapes with many one-off fields. Forcing every field
// to be declared here would be a much larger refactor than the value
// it delivers — typos in required fields below still fail tsc, which is
// where most regression risk actually lives.
//
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- intentional bag type, see comment
type AnyBag = { [k: string]: any };
type Leaf = { id: string } & AnyBag;
type Frame = {
  id: string;
  x: number;
  y: number;
  // FW/FH defaults at the top of this file mean buildLeafCanvas always
  // returns w/h populated; required for arithmetic without `?? 0` noise.
  w: number;
  h: number;
} & AnyBag;
type LeafEdge = { source: string; target: string } & AnyBag;
type LeafLayout = { frames: Frame[]; edges: LeafEdge[] } & AnyBag;
type Violation = { frameId?: string } & AnyBag;
type ViolationsByFrame = Record<string, Violation[]>;

// ─── Global window augmentations ────────────────────────────────────────
// The legacy JS canvas pipeline lives on `window`. None of these are
// installed by this file — they're set elsewhere (data loaders, the
// legacy app entrypoint). Declaring them here lets us read them under
// strict TS without per-call casts.
declare global {
  interface Window {
    LeafCanvas?: React.FC<LeafCanvasProps>;
    LeafInspector?: React.FC<LeafInspectorProps>;
    PhoneFrame?: React.FC<PhoneFrameProps>;
    buildLeafCanvas?: (leaf: Leaf) => LeafLayout;
    buildViolations?: (leaf: Leaf) => Violation[];
    buildDecisions?: (leaf: Leaf) => unknown[];
    buildActivity?: (leaf: Leaf) => unknown[];
    buildComments?: (leaf: Leaf) => unknown[];
    FLOWS_BY_ID?: Record<string, unknown>;
    LEAVES?: Record<string, Leaf[]>;
    __openLeaf?: (id: string) => void;
    __LC_MOUNTS?: number;
    __LC_UNMOUNTS?: number;
  }
}

// ─── Component prop shapes ──────────────────────────────────────────────
type LeafCanvasProps = {
  leaf: Leaf;
  onClose?: () => void;
  onPickFrame?: (id: string | null) => void;
  selectedFrameId?: string | null;
};
type LeafInspectorProps = {
  leaf: Leaf;
  frameId?: string | null;
  onClose?: () => void;
  onPickFrame?: (id: string | null) => void;
};
type PhoneFrameProps = {
  leaf: Leaf;
  frame: Frame;
} & Record<string, unknown>;
// ============================================================
// LEAF CANVAS — Figma-like infinite board for a single sub-flow.
// Renders an array of "frames" (phone-mockup screens) on a
// pannable / zoomable canvas with curved connectors and a
// frame-counter overlay. Click a frame to open the inspector
// pinned to it; click empty canvas to deselect.
// ============================================================

// frame width/height (matches leaves.jsx FW/FH)
const FW = 280, FH = 580;

window.LeafCanvas = function LeafCanvas({ leaf, onClose, onPickFrame, selectedFrameId }: LeafCanvasProps) {
  const layout = useMemo<LeafLayout>(
    () => (window.buildLeafCanvas ? window.buildLeafCanvas(leaf) : { frames: [], edges: [] }),
    [leaf.id],
  );
  const stageRef = useRef<HTMLDivElement | null>(null);

  // Diagnostic — mount/unmount counter so we can SEE if the canvas is
  // remounting on every SSE event / overlay refresh / etc.
  useEffect(() => {
    if (typeof window !== "undefined") {
      window.__LC_MOUNTS = (window.__LC_MOUNTS || 0) + 1;
      clog("camera", "LeafCanvas MOUNT", { leafId: leaf.id, n: window.__LC_MOUNTS });
    }
    return () => {
      if (typeof window !== "undefined") {
        window.__LC_UNMOUNTS = (window.__LC_UNMOUNTS || 0) + 1;
        clog("camera", "LeafCanvas UNMOUNT", { leafId: leaf.id, n: window.__LC_UNMOUNTS });
      }
    };
  }, [leaf.id]);

  // ---- camera (pan + zoom) — RAF-driven outside React
  //
  // Camera state lives in a ref, not React state. Wheel and pointer
  // handlers mutate the ref and schedule a single RAF tick that:
  //   (a) writes `transform` directly to the .lc-world DOM node
  //   (b) writes the dotted-grid offsets to the .lc-stage CSS vars
  //   (c) calls setLeafZoom(z) for the live zoom signal (cheap)
  //   (d) calls canvasGestureTracker.tick() so consumers know we're
  //       mid-gesture (LeafFrameRenderer queues mounts; the settled
  //       zoom signal holds back tier transitions until we settle)
  //
  // React only re-renders for the bottom-bar zoom % display — and
  // that's debounced to fire once per gesture-end via the gesture
  // tracker's settle callback. That removes the 60-Hz reconciliation
  // pass over 87 frames during a zoom gesture, which was the core
  // cause of "messed up zoom interactions during loading".
  // ------------------------------------------------------------------
  const camRef = useRef<{ x: number; y: number; z: number }>({ x: 0, y: 0, z: 0.6 });
  // U7 — running snap-animation token; cancelled on user input.
  const snapAnimRef = useRef<CancelToken | null>(null);
  const worldRef = useRef<HTMLDivElement | null>(null);
  const rafPendingRef = useRef<number>(0);
  const [zoomPct, setZoomPct] = useState(60);

  // Apply the current camRef to the DOM. Cheap: two style writes.
  // Called from RAF or directly from one-shot setters (fitAll etc).
  const applyCameraToDOM = useCallback(() => {
    const c = camRef.current;
    const world = worldRef.current;
    if (world) {
      world.style.transform = `scale(${c.z}) translate(${-c.x}px, ${-c.y}px)`;
    }
    const stage = stageRef.current;
    if (stage) {
      stage.style.backgroundSize = `${24 * c.z}px ${24 * c.z}px`;
      stage.style.backgroundPosition = `calc(50% - ${c.x * c.z}px) calc(50% - ${c.y * c.z}px)`;
    }
    setLeafZoom(c.z);
    clog("camera", "apply", { x: c.x, y: c.y, z: c.z, hasWorld: !!world });
  }, []);

  // Schedule a RAF flush. Coalesces N wheel events per frame into 1
  // DOM write. willChange: transform (in CSS) keeps the GPU layer hot.
  const scheduleCameraFlush = useCallback(() => {
    if (rafPendingRef.current) return;
    rafPendingRef.current = requestAnimationFrame(() => {
      rafPendingRef.current = 0;
      applyCameraToDOM();
    });
  }, [applyCameraToDOM]);

  // U7 — snap-to-bbox. Pure animation; the camera ref + DOM transform
  // are written on each rAF tick by the helper. Cancels any in-flight
  // snap before starting a new one (rapid Shift+2 presses are
  // common). Cancellation on user input lives in the pointer/wheel
  // handlers — they call snapAnimRef.current?.cancel() before
  // mutating the camera themselves, so a drag mid-snap aborts the
  // animation cleanly.
  const snapToBBox = useCallback(
    (bbox: SnapBBox) => {
      const stage = stageRef.current;
      if (!stage) return;
      const rect = stage.getBoundingClientRect();
      // Reserve 380px on the right for the inspector pane + 100px
      // chrome on top and bottom — same constants the auto-fit
      // useLayoutEffect uses, so the framing matches.
      const inspectorReserve = 380;
      const chromeReserve = 100;
      const usableW = Math.max(1, rect.width - inspectorReserve);
      const usableH = Math.max(1, rect.height - chromeReserve);
      const target = computeFitCamera(bbox, { width: usableW, height: usableH });
      if (!target) return;
      // Cancel any currently running snap before starting a new one.
      snapAnimRef.current?.cancel();
      const from = { ...camRef.current };
      const onTick = (s: { x: number; y: number; z: number }) => {
        camRef.current = { x: s.x, y: s.y, z: s.z };
        applyCameraToDOM();
        // Mark the canvas as gesturing so MeasurementOverlay etc.
        // know to suppress paint until the snap settles. Tick
        // also prevents the gesture-tracker idle from firing a
        // settle while the camera is still moving.
        canvasGestureTracker.tick();
      };
      const onDone = () => {
        setZoomPct(Math.round(camRef.current.z * 100));
        clog("camera", "snap-done", { ...camRef.current });
        snapAnimRef.current = null;
      };
      clog("camera", "snap-start", { from, to: target, bbox });
      snapAnimRef.current = animateCamera(
        from,
        target,
        SNAP_DURATION_MS,
        onTick,
        onDone,
      );
    },
    [applyCameraToDOM],
  );

  // Register/unregister the snap target on mount so external callers
  // (Shift+2 in AtlasShellInner, "Scroll into view" button in
  // AtomicChildInspector) can request a snap without prop-drilling.
  useEffect(() => {
    const off = registerSnapTarget(snapToBBox);
    return off;
  }, [snapToBBox]);

  // Debounced React-state sync — fires when the gesture-tracker
  // reports settle (~150 ms after the last wheel/pan event). Updates
  // the bottom-bar zoom percentage exactly once per gesture.
  useEffect(() => {
    const off = canvasGestureTracker.subscribe((gesturing) => {
      if (gesturing) return;
      const next = Math.round(camRef.current.z * 100);
      setZoomPct((prev) => (prev === next ? prev : next));
    });
    return off;
  }, []);

  // Auto-fit to layout on mount. Guards an empty `frames` array (real-data
  // case where the project has no rendered screens yet) so Math.min/max of
  // an empty spread doesn't yield -Infinity → NaN camera.
  //
  // Auto-fit MUST NOT re-run after the user has interacted with this leaf,
  // otherwise the camera snaps back to the landing zone whenever this effect
  // re-fires (e.g. on layout.frames.length changing because data streams in
  // late, or — the bug we just hit — on dep-identity churn). Track which
  // leaf we've fit for; only fit once per leaf.
  //
  // Critical: useLayoutEffect (not useEffect) so the world transform is
  // applied BEFORE first paint. With useEffect the browser paints once
  // with no transform (frames at world coords, way off-screen), the
  // IntersectionObserver fires `isIntersecting:false` for everything,
  // and 71/79 frames stay stuck on shimmer until the user wiggles the
  // viewport. (Confirmed via headless audit 2026-05-06.)
  const fitDoneForLeafRef = useRef<string | null>(null);
  useLayoutEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    if (fitDoneForLeafRef.current === leaf.id) {
      clog("camera", "auto-fit skipped (already fit this leaf)", { leafId: leaf.id });
      return;
    }
    if (!layout.frames || layout.frames.length === 0) {
      clog("camera", "auto-fit (empty layout) → 0,0,0.6");
      camRef.current = { x: 0, y: 0, z: 0.6 };
      applyCameraToDOM();
      setZoomPct(60);
      // Don't mark fit-done for empty layout — a later effect run with
      // populated frames should still fit.
      return;
    }
    const rect = stage.getBoundingClientRect();
    const minX = Math.min(...layout.frames.map(f => f.x));
    const maxX = Math.max(...layout.frames.map(f => f.x + f.w));
    const minY = Math.min(...layout.frames.map(f => f.y));
    const maxY = Math.max(...layout.frames.map(f => f.y + f.h));
    const w = Math.max(1, maxX - minX), h = Math.max(1, maxY - minY);
    const padding = 120;
    const zx = (rect.width - 380 - padding * 2) / w;   // leave room for inspector
    const zy = (rect.height - 100 - padding * 2) / h;
    const z = Math.max(0.18, Math.min(1.0, Math.min(zx, zy)));
    const cx = (minX + maxX) / 2;
    const cy = (minY + maxY) / 2;
    camRef.current = { x: cx, y: cy, z };
    applyCameraToDOM();
    setZoomPct(Math.round(z * 100));
    fitDoneForLeafRef.current = leaf.id;
    clog("camera", "auto-fit (initial)", {
      cx, cy, z, frames: layout.frames.length,
      stageW: rect.width, stageH: rect.height,
    });
  }, [leaf.id, layout.frames.length, applyCameraToDOM]);

  // ---- pan/zoom event handlers
  const dragRef = useRef({ active: false, startX: 0, startY: 0, camX: 0, camY: 0, moved: false });
  const onPointerDown = (e) => {
    if (e.target.closest(".lc-frame")) return; // let frame click bubble
    // Cancel any in-flight camera snap so the user's drag wins.
    snapAnimRef.current?.cancel();
    snapAnimRef.current = null;
    dragRef.current = {
      active: true,
      startX: e.clientX, startY: e.clientY,
      camX: camRef.current.x, camY: camRef.current.y,
      moved: false,
    };
    e.currentTarget.setPointerCapture?.(e.pointerId);
  };
  const onPointerMove = (e) => {
    if (!dragRef.current.active) return;
    const dx = e.clientX - dragRef.current.startX;
    const dy = e.clientY - dragRef.current.startY;
    if (Math.hypot(dx, dy) > 3) dragRef.current.moved = true;
    const z = camRef.current.z;
    camRef.current = {
      ...camRef.current,
      x: dragRef.current.camX - dx / z,
      y: dragRef.current.camY - dy / z,
    };
    canvasGestureTracker.tick();
    scheduleCameraFlush();
  };
  const onPointerUp = (e) => {
    const wasMoved = dragRef.current.moved;
    dragRef.current.active = false;
    try { e.currentTarget.releasePointerCapture?.(e.pointerId); } catch {}
    if (!wasMoved && !e.target.closest(".lc-frame")) {
      onPickFrame?.(null);
    }
  };
  // useCallback with empty deps because the body only reads stable refs
  // (stageRef, camRef) and module-level singletons (canvasGestureTracker,
  // scheduleCameraFlush, clog). Without the wrap, onWheel was recreated
  // every render but the useEffect at line ~250 had `[]` deps — so
  // addEventListener captured first-render onWheel and removeEventListener
  // referenced the latest identity, producing a no-op cleanup. Currently
  // benign because the effect runs once and the DOM goes away on unmount,
  // but a maintenance trap if any indirect dep becomes non-stable.
  const onWheel = useCallback((e) => {
    e.preventDefault();
    // Cancel any in-flight camera snap so the user's wheel input wins.
    snapAnimRef.current?.cancel();
    snapAnimRef.current = null;
    const stage = stageRef.current;
    const rect = stage.getBoundingClientRect();
    const sx = e.clientX - rect.left;
    const sy = e.clientY - rect.top;
    const c = camRef.current;

    // ---- Trackpad-friendly wheel routing ------------------------------
    // Browsers send 3 different "wheel" event shapes; we route them:
    //
    //   (1) Pinch-to-zoom on a trackpad → wheel with ctrlKey=true,
    //       small deltaY (synthetic ctrl, not a real Ctrl press).
    //   (2) Two-finger SCROLL on a trackpad → wheel with non-zero deltaX
    //       and/or small fractional deltaY. We treat this as PAN.
    //   (3) Mouse wheel (line-mode) → deltaMode === 1 OR large integer
    //       deltaY with deltaX === 0. We treat this as ZOOM.
    //   (4) User-held Cmd/Meta + scroll → ZOOM (explicit).
    //
    // The detection: ctrlKey OR metaKey OR (deltaX === 0 AND deltaY is a
    // large integer with no x-component) → zoom. Everything else → pan.
    // ------------------------------------------------------------------
    const isPinch = e.ctrlKey; // trackpad pinch sets ctrlKey
    const isCmdZoom = e.metaKey;
    const looksLikeMouseWheel =
      e.deltaMode === 1 || // line mode
      (e.deltaX === 0 && Math.abs(e.deltaY) >= 50 && Number.isInteger(e.deltaY));
    const shouldZoom = isPinch || isCmdZoom || looksLikeMouseWheel;

    if (shouldZoom) {
      // world point under cursor
      const wx = c.x + (sx - rect.width / 2) / c.z;
      const wy = c.y + (sy - rect.height / 2) / c.z;
      // smoother continuous zoom: exponential of -deltaY scaled small for trackpad
      // pinch (which fires many small events) and large for mouse wheel.
      const k = isPinch ? 0.01 : isCmdZoom ? 0.005 : 0.002;
      const factor = Math.exp(-e.deltaY * k);
      const z = Math.max(0.18, Math.min(2.0, c.z * factor));
      const nx = wx - (sx - rect.width / 2) / z;
      const ny = wy - (sy - rect.height / 2) / z;
      camRef.current = { x: nx, y: ny, z };
    } else {
      // Two-finger PAN — translate camera in world space.
      // Shift+wheel on a mouse swaps axes (browser convention) — already
      // reflected in deltaX, so we just consume both axes directly.
      camRef.current = {
        x: c.x + e.deltaX / c.z,
        y: c.y + e.deltaY / c.z,
        z: c.z,
      };
    }
    canvasGestureTracker.tick();
    scheduleCameraFlush();
    clog("camera", shouldZoom ? "wheel-zoom" : "wheel-pan", {
      dx: e.deltaX, dy: e.deltaY, ctrl: e.ctrlKey, meta: e.metaKey,
      next: { ...camRef.current },
    });
  }, []);
  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    stage.addEventListener("wheel", onWheel, { passive: false });
    return () => {
      stage.removeEventListener("wheel", onWheel);
      if (rafPendingRef.current) {
        cancelAnimationFrame(rafPendingRef.current);
        rafPendingRef.current = 0;
      }
    };
  }, [onWheel]);

  // ---- connectors (SVG) — drawn in world space, so put SVG at world coords
  // Compute bounding world box for SVG
  const worldBounds = useMemo(() => {
    if (!layout.frames || layout.frames.length === 0) {
      return { minX: -400, minY: -400, w: 800, h: 800 };
    }
    const minX = Math.min(...layout.frames.map(f => f.x)) - 200;
    const maxX = Math.max(...layout.frames.map(f => f.x + f.w)) + 200;
    const minY = Math.min(...layout.frames.map(f => f.y)) - 200;
    const maxY = Math.max(...layout.frames.map(f => f.y + f.h)) + 200;
    return { minX, minY, w: maxX - minX, h: maxY - minY };
  }, [layout]);

  const violations = useMemo(() => window.buildViolations(leaf), [leaf.id]);
  // group violations by frameIdx for badges
  const violationsByFrame = useMemo(() => {
    const m = {};
    violations.forEach(v => {
      (m[v.frameIdx] ??= []).push(v);
    });
    return m;
  }, [violations]);

  // ---- helpers — one-shot camera writes (buttons, focus, etc.)
  // These set camRef + flush DOM + sync zoomPct synchronously since
  // they fire on a single user click, not a continuous gesture.
  const writeCamera = useCallback((next) => {
    camRef.current = next;
    applyCameraToDOM();
    setZoomPct(Math.round(next.z * 100));
  }, [applyCameraToDOM]);

  const fitAll = () => {
    const stage = stageRef.current;
    if (!stage) return;
    if (!layout.frames || layout.frames.length === 0) {
      writeCamera({ x: 0, y: 0, z: 0.6 });
      return;
    }
    const rect = stage.getBoundingClientRect();
    const minX = Math.min(...layout.frames.map(f => f.x));
    const maxX = Math.max(...layout.frames.map(f => f.x + f.w));
    const minY = Math.min(...layout.frames.map(f => f.y));
    const maxY = Math.max(...layout.frames.map(f => f.y + f.h));
    const w = Math.max(1, maxX - minX), h = Math.max(1, maxY - minY);
    const padding = 120;
    const zx = (rect.width - 380 - padding * 2) / w;
    const zy = (rect.height - 100 - padding * 2) / h;
    const z = Math.max(0.18, Math.min(1.0, Math.min(zx, zy)));
    writeCamera({ x: (minX + maxX) / 2, y: (minY + maxY) / 2, z });
  };
  const zoomIn = () => {
    const c = camRef.current;
    writeCamera({ ...c, z: Math.min(2.0, c.z * 1.25) });
  };
  const zoomOut = () => {
    const c = camRef.current;
    writeCamera({ ...c, z: Math.max(0.18, c.z / 1.25) });
  };
  const focusOnFrame = (id) => {
    const f = layout.frames.find(x => x.id === id);
    if (!f) return;
    writeCamera({ x: f.x + f.w / 2, y: f.y + f.h / 2, z: 0.85 });
  };

  // ---- when a frame becomes selected externally, focus on it
  useEffect(() => {
    if (selectedFrameId) focusOnFrame(selectedFrameId);
  }, [selectedFrameId]);

  return (
    <div className="leafcanvas">
      <LeafTopBar leaf={leaf} onClose={onClose} onPickLeaf={(id) => { window.__openLeaf?.(id); }} violations={violations.length} />
      <div
        className="lc-stage"
        ref={stageRef}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        style={{
          backgroundImage:
            "radial-gradient(rgba(255,255,255,0.045) 1px, transparent 1px)",
          // backgroundSize / backgroundPosition are written imperatively
          // via applyCameraToDOM (RAF-driven). Initial values get set by
          // the auto-fit effect on mount before first paint.
        }}
      >
        <div
          ref={worldRef}
          className="lc-world"
          style={{ transformOrigin: "0 0", willChange: "transform" }}
        >
          {/* SVG connectors layer */}
          <svg
            className="lc-edges"
            style={{
              position: "absolute",
              left: worldBounds.minX,
              top: worldBounds.minY,
              width: worldBounds.w,
              height: worldBounds.h,
              pointerEvents: "none",
              overflow: "visible",
            }}
            viewBox={`${worldBounds.minX} ${worldBounds.minY} ${worldBounds.w} ${worldBounds.h}`}
            preserveAspectRatio="none"
          >
            <defs>
              <marker id="lc-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
                <path d="M0 0 L10 5 L0 10 z" fill="rgba(126,184,255,0.7)" />
              </marker>
              <marker id="lc-arrow-back" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
                <path d="M0 0 L10 5 L0 10 z" fill="rgba(255,180,120,0.7)" />
              </marker>
            </defs>
            {layout.edges.map((e, i) => {
              const A = layout.frames.find(f => f.id === e.from);
              const B = layout.frames.find(f => f.id === e.to);
              if (!A || !B) return null;
              const ax = A.x + A.w, ay = A.y + A.h / 2;
              const bx = B.x,        by = B.y + B.h / 2;
              const dx = bx - ax;
              // gentle horizontal cubic
              const c1x = ax + Math.abs(dx) * 0.45;
              const c2x = bx - Math.abs(dx) * 0.45;
              const path = `M ${ax} ${ay} C ${c1x} ${ay}, ${c2x} ${by}, ${bx} ${by}`;
              const isBack = e.kind === "back";
              const stroke = isBack ? "rgba(255,180,120,0.55)" : "rgba(126,184,255,0.55)";
              const dasharray = isBack ? "6 4" : "none";
              return (
                <path
                  key={i}
                  d={path}
                  fill="none"
                  stroke={stroke}
                  strokeWidth="1.6"
                  strokeDasharray={dasharray}
                  markerEnd={isBack ? "url(#lc-arrow-back)" : "url(#lc-arrow)"}
                />
              );
            })}
          </svg>

          {/* Frames */}
          {layout.frames.map((f) => {
            const fv = violationsByFrame[f.idx] || [];
            const isSel = selectedFrameId === f.id;
            return (
              <div
                key={f.id}
                className={`lc-frame ${isSel ? "is-sel" : ""}`}
                style={{ left: f.x, top: f.y, width: f.w, height: f.h }}
                onClick={(e) => { e.stopPropagation(); onPickFrame?.(f.id); }}
              >
                <div className="lc-frame-tab">
                  <span className="lc-frame-num">{String(f.idx + 1).padStart(2, "0")}</span>
                  <span className="lc-frame-name">{f.label}</span>
                  {fv.length > 0 && (
                    <span className={`lc-frame-badge sev-${
                      fv.some(v => v.severity === "error") ? "error"
                      : fv.some(v => v.severity === "warning") ? "warning"
                      : "info"
                    }`}>{fv.length}</span>
                  )}
                </div>
                <div className="lc-frame-body">
                  {/* Phase 5: PhoneFrame wrapper checks frame.pngUrl first
                      and renders the real exported screen; falls back to
                      the original mock phone-screen dispatcher for the
                      standalone-HTML preview. */}
                  <window.PhoneFrame
                    frame={f}
                    kind={f.kind}
                    idx={f.idx}
                    label={f.label}
                    total={layout.frames.length}
                  />
                </div>
                {/* violation pins inside the frame */}
                <div className="lc-pins">
                  {fv.slice(0, 4).map((v, i) => (
                    <span
                      key={v.id}
                      className={`lc-pin sev-${v.severity}`}
                      style={{
                        left: 30 + (i % 2) * 180,
                        top: 80 + Math.floor(i / 2) * 220,
                      }}
                      title={`${v.rule}\n${v.layer}`}
                    >{i + 1}</span>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      </div>

      {/* Bottom-left zoom & nav */}
      <div className="lc-zoom">
        <button onClick={zoomOut} title="Zoom out">−</button>
        <button className="lc-zoom-num" onClick={fitAll} title="Fit to canvas">{zoomPct}%</button>
        <button onClick={zoomIn} title="Zoom in">+</button>
        <span className="lc-zoom-sep" />
        <button onClick={fitAll} title="Fit all">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/></svg>
        </button>
      </div>

      {/* Frame strip — gives an overview & quick jump */}
      <div className="lc-strip">
        {layout.frames.map((f, i) => {
          const fv = violationsByFrame[f.idx] || [];
          return (
            <button
              key={f.id}
              className={`lc-strip-cell ${selectedFrameId === f.id ? "is-sel" : ""}`}
              onClick={() => onPickFrame?.(f.id)}
              title={f.label}
            >
              <span className="lc-strip-num">{String(i + 1).padStart(2, "0")}</span>
              <span className="lc-strip-label">{f.label}</span>
              {fv.length > 0 && (
                <span className={`lc-strip-dot sev-${
                  fv.some(v => v.severity === "error") ? "error"
                  : fv.some(v => v.severity === "warning") ? "warning"
                  : "info"
                }`} />
              )}
            </button>
          );
        })}
      </div>
    </div>
  );
};

// ============================================================
// LeafTopBar — sticky header for the leaf canvas
//   - Back button → returns to Atlas (preserves selection)
//   - Flow name dropdown → jump to ANY flow's first sub-flow
//   - Sub-flow name dropdown → jump to a sibling sub-flow
//   - Prev / Next arrows → cycle through siblings
//   - Frames + violations stats on the right
// ============================================================
function LeafTopBar({ leaf, onClose, onPickLeaf, violations }) {
  const flow = window.FLOWS_BY_ID?.[leaf.flow];
  const allLeaves = window.LEAVES || [];
  // siblings = sub-flows under the same parent flow
  const siblings = useMemo(() => allLeaves.filter(l => l.flow === leaf.flow), [leaf.flow]);
  const sibIdx = siblings.findIndex(l => l.id === leaf.id);

  const [flowMenu, setFlowMenu] = useState(false);
  const [subMenu, setSubMenu] = useState(false);

  // Close menus on outside click / esc
  useEffect(() => {
    if (!flowMenu && !subMenu) return;
    const onDown = (e) => {
      if (!e.target.closest?.(".lc-menu") && !e.target.closest?.(".lc-crumb-btn")) {
        setFlowMenu(false); setSubMenu(false);
      }
    };
    const onKey = (e) => { if (e.key === "Escape") { setFlowMenu(false); setSubMenu(false); } };
    window.addEventListener("pointerdown", onDown);
    window.addEventListener("keydown", onKey, true);
    return () => {
      window.removeEventListener("pointerdown", onDown);
      window.removeEventListener("keydown", onKey, true);
    };
  }, [flowMenu, subMenu]);

  // Group all leaves by flow for the flow-picker menu
  const grouped = useMemo(() => {
    const m = new Map();
    for (const l of allLeaves) {
      if (!m.has(l.flow)) m.set(l.flow, []);
      m.get(l.flow).push(l);
    }
    return [...m.entries()];
  }, []);

  const goSibling = (delta) => {
    const next = siblings[(sibIdx + delta + siblings.length) % siblings.length];
    if (next && next.id !== leaf.id) onPickLeaf(next.id);
  };

  return (
    <div className="lc-top">
      <button className="lc-back" onClick={onClose} title="Back to Atlas (Esc)">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M19 12H5M12 19l-7-7 7-7"/></svg>
        <span>Back to Atlas</span>
      </button>

      <div className="lc-top-title">
        <div className="lc-top-eyebrow">
          {/* Flow dropdown */}
          <button
            className="lc-crumb-btn"
            onClick={(e) => { e.stopPropagation(); setFlowMenu(v => !v); setSubMenu(false); }}
          >
            {flow?.label || leaf.flow}
            <svg className="lc-caret" width="10" height="10" viewBox="0 0 12 12"><path d="M2 4l4 4 4-4" stroke="currentColor" strokeWidth="1.5" fill="none" strokeLinecap="round" strokeLinejoin="round"/></svg>
          </button>
          {flowMenu && (
            <div className="lc-menu lc-menu-flows">
              <div className="lc-menu-head">Jump to flow</div>
              {grouped.map(([flowId, leaves]) => {
                const f = window.FLOWS_BY_ID?.[flowId];
                return (
                  <div key={flowId} className="lc-menu-group">
                    <div className="lc-menu-group-label">{f?.label || flowId}</div>
                    {leaves.map(l => (
                      <button
                        key={l.id}
                        className={`lc-menu-item ${l.id === leaf.id ? "is-current" : ""}`}
                        onClick={() => { setFlowMenu(false); if (l.id !== leaf.id) onPickLeaf(l.id); }}
                      >
                        <span className="lc-menu-item-label">{l.label}</span>
                        <span className="lc-menu-item-meta">
                          {l.frames}
                          {l.violations > 0 && <span className="lc-menu-item-warn"> · {l.violations}</span>}
                        </span>
                      </button>
                    ))}
                  </div>
                );
              })}
            </div>
          )}
          <span className="lc-top-sep">›</span>
          <span className="lc-crumb-static">Sub-flow</span>
        </div>

        <div className="lc-top-name-row">
          {/* Prev sibling */}
          <button
            className="lc-sib-arrow"
            onClick={() => goSibling(-1)}
            title="Previous sub-flow"
            disabled={siblings.length < 2}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M15 18l-6-6 6-6"/></svg>
          </button>

          {/* Sub-flow name dropdown */}
          <button
            className="lc-top-name lc-crumb-btn"
            onClick={(e) => { e.stopPropagation(); setSubMenu(v => !v); setFlowMenu(false); }}
          >
            {leaf.label}
            <svg className="lc-caret-lg" width="12" height="12" viewBox="0 0 12 12"><path d="M2 4l4 4 4-4" stroke="currentColor" strokeWidth="1.6" fill="none" strokeLinecap="round" strokeLinejoin="round"/></svg>
          </button>

          {/* Next sibling */}
          <button
            className="lc-sib-arrow"
            onClick={() => goSibling(1)}
            title="Next sub-flow"
            disabled={siblings.length < 2}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M9 18l6-6-6-6"/></svg>
          </button>

          <span className="lc-sib-pos">{sibIdx + 1} / {siblings.length}</span>

          {subMenu && (
            <div className="lc-menu lc-menu-subs">
              <div className="lc-menu-head">{flow?.label || leaf.flow} · sub-flows</div>
              {siblings.map(l => (
                <button
                  key={l.id}
                  className={`lc-menu-item ${l.id === leaf.id ? "is-current" : ""}`}
                  onClick={() => { setSubMenu(false); if (l.id !== leaf.id) onPickLeaf(l.id); }}
                >
                  <span className="lc-menu-item-label">{l.label}</span>
                  <span className="lc-menu-item-meta">
                    {l.frames}
                    {l.violations > 0 && <span className="lc-menu-item-warn"> · {l.violations}</span>}
                  </span>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      <div className="lc-top-meta">
        <div className="lc-top-stat">
          <span className="lc-top-stat-num">{leaf.frames}</span>
          <span className="lc-top-stat-lbl">frames</span>
        </div>
        <div className="lc-top-stat">
          <span className={`lc-top-stat-num ${violations > 0 ? "is-warn" : ""}`}>{violations}</span>
          <span className="lc-top-stat-lbl">violations</span>
        </div>
      </div>
    </div>
  );
}

// ============================================================
// LeafInspector — DRD / decisions / violations / activity tabs
// ============================================================
window.LeafInspector = function LeafInspector({ leaf, frameId, onClose, onPickFrame }) {
  const [tab, setTab] = useState("drd");
  const violations = useMemo(() => window.buildViolations(leaf), [leaf.id]);
  const decisions = useMemo(() => window.buildDecisions(leaf), [leaf.id]);
  const activity = useMemo(() => window.buildActivity(leaf), [leaf.id]);
  const comments = useMemo(() => window.buildComments(leaf), [leaf.id]);

  const frame = frameId
    ? window.buildLeafCanvas(leaf).frames.find(f => f.id === frameId)
    : null;

  return (
    <div className="lc-ins">
      <div className="lc-ins-head">
        <div>
          <div className="lc-ins-eyebrow">{frame ? "Frame" : "Sub-flow"}</div>
          <div className="lc-ins-name">{frame ? frame.label : leaf.label}</div>
          {frame && <div className="lc-ins-meta">Frame {frame.idx + 1} of {leaf.frames} · {leaf.label}</div>}
          {!frame && <div className="lc-ins-meta">{leaf.frames} frames · {violations.length} violations · {decisions.length} decisions</div>}
        </div>
        <button className="lc-ins-close" onClick={onClose}>✕</button>
      </div>
      <div className="lc-ins-tabs">
        {["drd", "violations", "decisions", "copy", "activity", "comments"].map(t => (
          <button
            key={t}
            className={`lc-ins-tab ${tab === t ? "is-active" : ""}`}
            onClick={() => setTab(t)}
          >
            {t === "drd" ? "DRD" : t === "copy" ? "Copy" : t.charAt(0).toUpperCase() + t.slice(1)}
            {t === "violations" && violations.length > 0 && (
              <span className="lc-tab-pill">{violations.length}</span>
            )}
            {t === "decisions" && decisions.length > 0 && (
              <span className="lc-tab-pill">{decisions.length}</span>
            )}
          </button>
        ))}
      </div>
      <div className="lc-ins-body">
        {tab === "drd" && <DRDTab leaf={leaf} frame={frame} />}
        {tab === "violations" && (
          <ViolationsTab
            violations={frame ? violations.filter(v => v.frameIdx === frame.idx) : violations}
            onPickFrame={onPickFrame}
            leaf={leaf}
          />
        )}
        {tab === "decisions" && <DecisionsTab decisions={decisions} leaf={leaf} onPickFrame={onPickFrame} />}
        {tab === "copy" && <CopyOverridesTab slug={leaf.id} leafID={leaf.id} />}
        {tab === "activity" && <ActivityTab activity={activity} />}
        {tab === "comments" && <CommentsTab comments={comments} />}
      </div>
    </div>
  );
};

function DRDTab({ leaf, frame }) {
  // Phase 6 — Notion-like editor wired through AtlasDRDEditor (BlockNote +
  // Hocuspocus collab + REST autosave fallback). The slug/flowID props
  // come from the leaf object: leaf.flow is the parent project slug, and
  // leaf.id is the flows.id row in our DB.
  const Editor = (typeof window !== "undefined" ? (window as any).__AtlasDRDEditor : null);
  if (Editor && leaf?.id) {
    // Post brain-products: leaf.id is the ds-service project slug; the
    // DRD endpoint is keyed by (project_slug, flow_uuid). The editor
    // resolves the project's first flow itself when flowID is empty.
    return <Editor slug={leaf.id} flowID="" />;
  }
  // Fallback skeleton — rendered for the standalone HTML preview (no
  // window.__AtlasDRDEditor injection) or if the editor module fails to
  // load. Visually consistent with the editor host so layout doesn't jump.
  return (
    <div className="lc-drd lc-drd--placeholder">
      <div className="lc-drd-section">
        <div className="lc-drd-h">Purpose</div>
        <p>{frame ? `Handles the "${frame.label}" step of the ${leaf.label} flow.` : `${leaf.label} — Design Requirement Doc.`}</p>
      </div>
    </div>
  );
}

function ViolationsTab({ violations, onPickFrame, leaf }) {
  const [filter, setFilter] = useState("active");
  const filtered = violations.filter(v => filter === "all" || v.status === filter);
  const layout = useMemo(() => window.buildLeafCanvas(leaf), [leaf.id]);
  if (violations.length === 0) {
    return (
      <div className="lc-empty">
        <div className="lc-empty-icon">✓</div>
        <div className="lc-empty-h">No violations</div>
        <div className="lc-empty-sub">This sub-flow passes all design system checks.</div>
      </div>
    );
  }
  return (
    <div className="lc-vio">
      <div className="lc-vio-filter">
        {["active", "acknowledged", "fixed", "all"].map(s => (
          <button key={s} className={`lc-vio-fil ${filter === s ? "is-active" : ""}`} onClick={() => setFilter(s)}>
            {s} {s !== "all" && <span className="lc-vio-fil-num">{violations.filter(v => v.status === s).length}</span>}
          </button>
        ))}
      </div>
      <div className="lc-vio-list">
        {filtered.map(v => {
          const f = layout.frames.find(fr => fr.idx === v.frameIdx);
          return (
            <div key={v.id} className={`lc-vio-row sev-${v.severity}`}>
              <div className="lc-vio-row-head">
                <span className={`lc-vio-sev sev-${v.severity}`}>
                  {v.severity === "error" ? "✕" : v.severity === "warning" ? "!" : "i"}
                </span>
                <span className="lc-vio-rule">{v.rule}</span>
                <span className="lc-vio-ago">{v.ago}</span>
              </div>
              <div className="lc-vio-detail">{v.detail}</div>
              <div className="lc-vio-meta">
                <button className="lc-vio-frameref" onClick={() => onPickFrame?.(f?.id)}>
                  → Frame {v.frameIdx + 1} · {f?.label}
                </button>
                <span className="lc-vio-layer">{v.layer}</span>
                <span className={`lc-vio-status status-${v.status}`}>{v.status}</span>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function DecisionsTab({ decisions, leaf, onPickFrame }) {
  const layout = useMemo(() => window.buildLeafCanvas(leaf), [leaf.id]);
  if (decisions.length === 0) {
    return <div className="lc-empty"><div className="lc-empty-h">No decisions logged</div></div>;
  }
  return (
    <div className="lc-dec">
      {decisions.map(d => {
        const f = d.linksTo != null ? layout.frames[d.linksTo] : null;
        return (
          <div key={d.id} className="lc-dec-row">
            <div className="lc-dec-marker" />
            <div className="lc-dec-content">
              <div className="lc-dec-title">{d.title}</div>
              <div className="lc-dec-body">{d.body}</div>
              <div className="lc-dec-foot">
                <span>{d.author}</span>
                <span className="lc-dec-dot">·</span>
                <span>{d.ago}</span>
                {f && (
                  <>
                    <span className="lc-dec-dot">·</span>
                    <button className="lc-vio-frameref" onClick={() => onPickFrame?.(f.id)}>
                      → Frame {d.linksTo + 1}
                    </button>
                  </>
                )}
              </div>
            </div>
          </div>
        );
      })}
    </div>
  );
}

function ActivityTab({ activity }) {
  return (
    <div className="lc-act">
      {activity.map((a, i) => (
        <div key={i} className="lc-act-row">
          <div className={`lc-act-icon kind-${a.kind}`} />
          <div className="lc-act-body">
            <div><b>{a.who}</b> {a.what}</div>
            <div className="lc-act-ago">{a.ago}</div>
          </div>
        </div>
      ))}
    </div>
  );
}

function CommentsTab({ comments }) {
  return (
    <div className="lc-com">
      {comments.map((c, i) => (
        <div key={i} className="lc-com-row">
          <div className="lc-com-avatar" style={{ background: `hsl(${(i + 1) * 73}, 30%, 60%)` }}>{c.who[0]}</div>
          <div className="lc-com-body">
            <div className="lc-com-head"><b>{c.who}</b><span className="lc-com-ago">{c.ago}</span></div>
            <div className="lc-com-text">{c.body}</div>
            {c.reactions > 0 && <div className="lc-com-react">👍 {c.reactions}</div>}
          </div>
        </div>
      ))}
      <div className="lc-com-input">
        <div className="lc-com-input-field">Reply…</div>
      </div>
    </div>
  );
}
