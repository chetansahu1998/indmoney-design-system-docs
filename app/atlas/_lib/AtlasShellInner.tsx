"use client";

/**
 * AtlasShellInner — actually mounts the ported atlas/leafcanvas modules.
 *
 * Split out from AtlasShell so that:
 *   - The expensive side-effect imports (atlas/leafcanvas/leaves/frames/
 *     tweaks-panel) only resolve AFTER the live store has hydrated and the
 *     window.__ATLAS_* globals are populated.
 *   - The outer AtlasShell can force a remount via key= to make atlas.tsx
 *     re-read its module-load constants (DOMAINS / FLOWS / SYNAPSES).
 *
 * Owns the SSE subscription for graph + project events. Tears down on unmount.
 */

import { useCallback, useEffect, useRef, useState } from "react";

import { subscribeProjectEvents } from "../../../lib/projects/client";
import { subscribeGraphEvents } from "../../../lib/atlas/data-adapters";
import { useAtlas } from "../../../lib/atlas/live-store";
import type { Leaf } from "../../../lib/atlas/types";
import { AtlasShellProvider, type AtlasShellContextShape } from "./AtlasShellContext";
import { AtomicChildInspector, findByFigmaID } from "./leafcanvas-v2/AtomicChildInspector";
import { requestCameraSnap } from "./leafcanvas-v2/camera-snap";
import { getHoveredAtomicChild, setHoveredAtomicChild } from "./leafcanvas-v2/hover-signal";
// U3 — centralized keymap. Replaces the previously-scattered Shift+2
// and Esc useEffects with a single action-table + installKeymap call.
// The keymap module gates on canvas focus (.lc-stage subtree) and
// rejects editable targets, so Cmd+A inside an InlineTextEditor still
// behaves natively.
import { installKeymap, registerKeymap, type ActionTable } from "./leafcanvas-v2/keymap";
import { getCameraActions, type NamedFrameEntry } from "./leafcanvas-v2/camera-actions";
import { toggleDevMode } from "./leafcanvas-v2/dev-mode-state";
import { NameSearchPalette } from "./leafcanvas-v2/NameSearchPalette";
// U4 — selection cycle helpers. Used by the Enter / Shift+Enter / Tab /
// Cmd+A keymap actions to walk the current canonical_tree relative to
// the active selection.
import {
  collectIdsByType,
  findFirstChildId,
  findNextSiblingId,
  findParentId,
  findPrevSiblingId,
} from "./leafcanvas-v2/selection-cycle";

// Side-effect imports — order matters; see AtlasShell for rationale.
import "./tweaks-panel";
import "./leaves";
import "./frames";
import "./real-data-bridge"; // Phase 5: must come AFTER leaves+frames
import "./atlas";
import "./leafcanvas";
import AtlasDRDEditor from "./AtlasDRDEditor"; // Phase 6 — Notion-like DRD
import { PrototypeCanvas } from "./PrototypeCanvas"; // Plan 005 U6
import { Wall } from "./Wall"; // Plan 005 U7

// Inject the DRD editor into the global namespace the ported leafcanvas
// looks up. Doing this at module-load means LeafInspector can call it on
// first render without any extra plumbing.
if (typeof window !== "undefined") {
  (window as any).__AtlasDRDEditor = AtlasDRDEditor;
}

function getAtlasApp(): React.FC | null {
  if (typeof window === "undefined") return null;
  return ((window as any).AtlasApp as React.FC | undefined) ?? null;
}
function getLeafCanvas(): React.FC<any> | null {
  if (typeof window === "undefined") return null;
  return ((window as any).LeafCanvas as React.FC<any> | undefined) ?? null;
}
function getLeafInspector(): React.FC<any> | null {
  if (typeof window === "undefined") return null;
  return ((window as any).LeafInspector as React.FC<any> | undefined) ?? null;
}
function getLeavesArray(): Leaf[] {
  // `window.LEAVES` is the runtime mirror of the live store's leaves (see
  // leaves.tsx) — the canonical Leaf type lives in lib/atlas/types.ts and
  // carries `subFlow?: SubFlowSummary` (plan 005 U1). Returning the full
  // Leaf shape lets U6's PrototypeCanvas swap + the inspector both
  // dereference `leaf.subFlow.*` without local-narrow-type collisions.
  return (typeof window !== "undefined" ? (window as any).LEAVES : null) ?? [];
}

// CenterPaneSwitch — picks the center-pane renderer per leaf.
//
// Priority:
//   1. Wall mode (plan 005 U7) — user-triggered via LeafInspector toggle.
//   2. Prototype iframe (plan 005 U6) — when sub_flow lifecycle is proto-*
//      AND a prototypeURL is set.
//   3. LeafCanvas — default. Legacy leaves always land here.
function CenterPaneSwitch({
  leaf,
  slotVersion,
  selectedFrameID,
  setSelectedFrameID,
  closeLeaf,
  LeafCanvas,
}: {
  leaf: Leaf;
  slotVersion: number;
  selectedFrameID: string | null;
  setSelectedFrameID: (id: string | null) => void;
  closeLeaf: () => void;
  LeafCanvas: React.FC<any>;
}) {
  const mode = useAtlas((s) => s.leafMode[leaf.id] ?? "canvas");
  const wall = useAtlas((s) => s.wallByLeaf[leaf.id]);
  const setLeafMode = useAtlas((s) => s.setLeafMode);
  if (mode === "wall" && leaf.subFlow) {
    // Wall.tsx tolerates a null/undefined wall — we render a tiny "loading"
    // placeholder until loadLeafWall lands the data.
    if (wall) {
      return (
        <Wall
          key={`wall-${leaf.id}`}
          data={wall}
          slug={leaf.id}
          fileKey={leaf.subFlow.figmaFileKey ?? undefined}
          onFrameClick={(figmaNodeID) => {
            setLeafMode(leaf.id, "canvas");
            setSelectedFrameID(figmaNodeID);
          }}
        />
      );
    }
    return (
      <div className="lc-wall lc-wall-empty" style={{ minHeight: 240 }}>
        <strong>Loading wall…</strong>
        <span>Pulling frames + coverage from /api/prd/{leaf.subFlow.fullSlug}</span>
      </div>
    );
  }
  const lifecycle = leaf.subFlow?.canvasLifecycle;
  const protoURL = leaf.subFlow?.prototypeURL;
  const useProto =
    (lifecycle === "proto-only" || lifecycle === "proto-wip") && !!protoURL;
  if (useProto) {
    return (
      <PrototypeCanvas
        key={`proto-${leaf.id}-${protoURL}`}
        url={protoURL!}
        title={leaf.subFlow?.prototypeTitle ?? leaf.label}
        subFlowSlug={leaf.subFlow?.fullSlug ?? null}
        banner={
          lifecycle === "proto-wip"
            ? "Designer is working on the section — not yet on the Final Designs page."
            : null
        }
      />
    );
  }
  return (
    <LeafCanvas
      key={`canvas-${leaf.id}-${slotVersion}`}
      leaf={leaf}
      onClose={closeLeaf}
      onPickFrame={setSelectedFrameID}
      selectedFrameId={selectedFrameID}
    />
  );
}

export interface AtlasShellInnerProps {
  selection: { flowID: string | null; leafID: string | null; frameID: string | null };
}

export default function AtlasShellInner(_props: AtlasShellInnerProps) {
  const flows = useAtlas((s) => s.flows);
  const openLeafFromStore = useAtlas((s) => s.openLeaf);
  const closeLeafFromStore = useAtlas((s) => s.closeLeaf);
  const loadLeavesForFlow = useAtlas((s) => s.loadLeavesForFlow);
  const applyEvent = useAtlas((s) => s.applyEvent);
  const refreshBrain = useAtlas((s) => s.refreshBrain);

  const [leafID, setLeafID] = useState<string | null>(null);
  const [selectedFrameID, setSelectedFrameID] = useState<string | null>(null);

  // Plan 005 — sync local leafID with the store's selection.leafID so that
  // URL deep-link openLeaf (fired by AtlasShell.tsx's URL effect against
  // the store action, not this component's local callback) actually mounts
  // the leaf shell. Without this mirror, the store flips selection.leafID
  // but AtlasShellInner.leafID stays null and the shell never renders.
  const storeLeafID = useAtlas((s) => s.selection.leafID);
  useEffect(() => {
    if (storeLeafID !== leafID) setLeafID(storeLeafID);
  }, [storeLeafID, leafID]);

  // ─── Atomic-child selection wiring (D5/D8/D10 — Zeplin v1) ────────────────
  // The leaf-canvas-v2 renderer fires `selectAtomicChild` on click; the
  // store updates `selection.selectedAtomicChild`. This pane mounts the
  // Zeplin-style sidebar against the selected node and routes Esc / close
  // back to clearing the store. Pre-2026-05-08 these signals were dropped
  // on the floor — the inspector component existed but was never imported.
  const selectedAtomicChild = useAtlas((s) => s.selection.selectedAtomicChild);
  const selectAtomicChild = useAtlas((s) => s.selectAtomicChild);
  // U4 — Cmd+A bulk-add. Wired through the keymap action below.
  const addToBulkSelection = useAtlas((s) => s.addToBulkSelection);
  const removeOverride = useAtlas((s) => s.removeOverride);
  const atomicCanonicalTree = useAtlas((s) => {
    const sel = s.selection.selectedAtomicChild;
    if (!sel) return null;
    const slot = leafID ? s.leafSlots[leafID] : null;
    if (!slot) return null;
    return slot.canonicalTreeByScreenID?.[sel.screenID] ?? null;
  });
  const atomicOverride = useAtlas((s) => {
    const sel = s.selection.selectedAtomicChild;
    if (!sel) return null;
    const slot = leafID ? s.leafSlots[leafID] : null;
    if (!slot) return null;
    return slot.overrides?.[sel.screenID]?.get(sel.figmaNodeID) ?? null;
  });

  const closeAtomicInspector = useCallback(() => {
    selectAtomicChild("", "");
  }, [selectAtomicChild]);

  const handleAtomicOverrideReset = useCallback(() => {
    if (!leafID || !selectedAtomicChild) return;
    removeOverride(leafID, selectedAtomicChild.screenID, selectedAtomicChild.figmaNodeID);
  }, [leafID, selectedAtomicChild, removeOverride]);

  // Atomic-inspector close is layered into the existing Esc handler
  // below — see "Esc layered close" useEffect.

  // U7 — Shift+2 camera snap to selected atomic. Mirrors Figma's
  // "Zoom to Selection" shortcut. No-op when no atomic is selected.
  // Look up bbox from the cached canonical_tree slot and request a
  // snap via the module-level channel; LeafCanvas (which registers
  // the snap target on mount) handles the rAF animation.
  const requestSnapToSelected = useCallback(() => {
    if (!selectedAtomicChild || !atomicCanonicalTree) return;
    const found = findByFigmaID(atomicCanonicalTree, selectedAtomicChild.figmaNodeID);
    if (!found || !found.node.absoluteBoundingBox) return;
    const bb = found.node.absoluteBoundingBox;
    requestCameraSnap({ x: bb.x, y: bb.y, width: bb.width, height: bb.height });
  }, [selectedAtomicChild, atomicCanonicalTree]);

  // U3b — Cmd+F name-search palette state. The palette is mounted as
  // a sibling of LeafCanvas (outside .lc-stage) so its input focus
  // disables canvas hotkeys via the keymap focus gate. paletteFrames
  // is snapshotted at open-time so re-renders don't churn the
  // filtered list with a new array reference.
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [paletteFrames, setPaletteFrames] = useState<NamedFrameEntry[]>([]);
  // Ref mirror so the keymap action handlers can read the current
  // palette state synchronously without forcing the keymap effect
  // to re-register on every open/close.
  const paletteOpenRef = useRef(false);
  const openPalette = useCallback(() => {
    const frames = getCameraActions()?.listNamedFrames() ?? [];
    setPaletteFrames(frames);
    paletteOpenRef.current = true;
    setPaletteOpen(true);
  }, []);
  const closePalette = useCallback(() => {
    paletteOpenRef.current = false;
    setPaletteOpen(false);
  }, []);
  const paletteJumpToFrame = useCallback((id: string) => {
    getCameraActions()?.jumpToFrame(id);
  }, []);

  // U3 — Shift+2 is now dispatched through the keymap action table
  // below (`canvas.fit-selection`). The previous standalone effect
  // here is folded into the combined keymap registration further
  // down so we register one window listener, not many. The
  // requestSnapToSelected callback remains — the inline
  // "Scroll into view" button in AtomicChildInspector still calls it
  // directly via requestCameraSnap.

  // Open a leaf — awaits the leaf-slot load BEFORE flipping local state
  // so LeafCanvas mounts with the data already present in the live store.
  // Otherwise the bridge falls through to mock for one render frame and
  // the inspector tabs flicker.
  const openLeaf = useCallback(
    async (id: string) => {
      let leaves = getLeavesArray();
      let found = leaves.find((l) => l.id === id);
      if (!found) {
        // Pre-warm leaves for every brain node so the lookup succeeds.
        await Promise.all(flows.map((f) => loadLeavesForFlow(f.id, f.latestVersionID)));
        leaves = getLeavesArray();
        found = leaves.find((l) => l.id === id);
      }
      setSelectedFrameID(null);
      // Kick off the per-leaf overlay fetch (frames + violations + decisions
      // + activity + comments + drd) and await it before mounting LeafCanvas.
      await openLeafFromStore(id);
      setLeafID(id);
    },
    [flows, loadLeavesForFlow, openLeafFromStore],
  );

  const closeLeaf = useCallback(() => {
    setLeafID(null);
    setSelectedFrameID(null);
    closeLeafFromStore();
  }, [closeLeafFromStore]);

  // U3 — Esc layered close + full Figma keymap. Previously this lived
  // as a standalone useEffect that owned its own window keydown
  // listener; the layered close logic is now the action handler for
  // `selection.escape-layered` and registers alongside the rest of
  // the camera + mode + search hotkeys.
  //
  // Order of Esc layers (innermost → outermost), preserved verbatim:
  //   1. hover state  → clear it
  //   2. atomic-child selection → clear it (D8 spec)
  //   3. selected frame → deselect
  //   4. leaf open → close leaf
  // Each Esc press consumes one layer; the user must press again to
  // pop the next.
  useEffect(() => {
    if (!leafID) return;

    const table: ActionTable = {
      // Camera — delegates to leafcanvas-registered actions. The
      // registry can be null if the LeafCanvas hasn't mounted yet
      // (race with the leafID flip), so each handler null-checks.
      "canvas.fit-all": () => getCameraActions()?.fitAll(),
      "canvas.fit-selection": () => requestSnapToSelected(),
      "canvas.zoom-100": () => getCameraActions()?.zoom100(),
      "canvas.zoom-in": () => getCameraActions()?.zoomIn(),
      "canvas.zoom-out": () => getCameraActions()?.zoomOut(),
      "canvas.next-named-frame": () => getCameraActions()?.nextNamedFrame(),
      "canvas.prev-named-frame": () => getCameraActions()?.prevNamedFrame(),

      // Mode flag (U9 paints the annotations; U3 just toggles).
      "mode.toggle-dev-mode": () => toggleDevMode(),

      // Layered close (ported from the prior useEffect, with the U3b
      // palette-close layer prepended). Esc layers, innermost first:
      //   0. name-search palette open → close palette
      //   1. hover state → clear
      //   2. atomic-child selection → clear
      //   3. selected frame → deselect
      //   4. leaf open → close leaf
      "selection.escape-layered": () => {
        if (paletteOpenRef.current) {
          closePalette();
          return;
        }
        if (getHoveredAtomicChild() !== null) {
          setHoveredAtomicChild(null);
          return;
        }
        if (selectedAtomicChild) {
          closeAtomicInspector();
          return;
        }
        if (selectedFrameID) {
          setSelectedFrameID(null);
          return;
        }
        closeLeaf();
      },

      // U4 — selection navigation via the active canonical_tree.
      // All five actions read `selectedAtomicChild` and walk the
      // currently-loaded `atomicCanonicalTree`. When no selection is
      // active or the helper returns no candidate, the action is a
      // no-op (consistent with the brainstorm AE for "Enter with no
      // selection" — no-op).
      "selection.descend": () => {
        if (!selectedAtomicChild || !atomicCanonicalTree) return;
        const child = findFirstChildId(
          atomicCanonicalTree,
          selectedAtomicChild.figmaNodeID,
        );
        if (!child) return;
        selectAtomicChild(selectedAtomicChild.screenID, child);
      },
      "selection.ascend": () => {
        if (!selectedAtomicChild || !atomicCanonicalTree) return;
        const parent = findParentId(
          atomicCanonicalTree,
          selectedAtomicChild.figmaNodeID,
        );
        if (!parent) return;
        selectAtomicChild(selectedAtomicChild.screenID, parent);
      },
      "selection.next-sibling": () => {
        if (!selectedAtomicChild || !atomicCanonicalTree) return;
        const next = findNextSiblingId(
          atomicCanonicalTree,
          selectedAtomicChild.figmaNodeID,
        );
        if (!next) return;
        selectAtomicChild(selectedAtomicChild.screenID, next);
      },
      "selection.prev-sibling": () => {
        if (!selectedAtomicChild || !atomicCanonicalTree) return;
        const prev = findPrevSiblingId(
          atomicCanonicalTree,
          selectedAtomicChild.figmaNodeID,
        );
        if (!prev) return;
        selectAtomicChild(selectedAtomicChild.screenID, prev);
      },
      "selection.select-all": () => {
        // Selects every FRAME-class node in the current screen.
        // Falls back gracefully when no atomicCanonicalTree is loaded
        // (e.g., user hits Cmd+A before picking anything).
        if (!selectedAtomicChild || !atomicCanonicalTree) return;
        const FRAME_TYPES_SET = new Set([
          "FRAME",
          "COMPONENT",
          "INSTANCE",
          "GROUP",
        ]);
        const ids = collectIdsByType(atomicCanonicalTree, FRAME_TYPES_SET);
        for (const id of ids) {
          addToBulkSelection(selectedAtomicChild.screenID, id);
        }
      },

      // Hand tool toggle — defers to follow-up; canvas already pans
      // on default pointer-drag, so H toggling doesn't change behavior
      // until we add a "frame-select" mode separately.
      "mode.toggle-hand-tool": () => {
        /* follow-up */
      },

      // Cmd+F — open the name-search palette with a fresh frame
      // snapshot. Idempotent: opening when already open re-snapshots
      // (useful if the user navigated to a new frame between opens).
      "search.open-palette": () => openPalette(),
    };

    const unregisterTable = registerKeymap(table);
    const uninstall = installKeymap();
    return () => {
      unregisterTable();
      uninstall();
    };
  }, [
    leafID,
    requestSnapToSelected,
    selectedFrameID,
    selectedAtomicChild,
    closeLeaf,
    closeAtomicInspector,
    openPalette,
    closePalette,
    atomicCanonicalTree,
    selectAtomicChild,
    addToBulkSelection,
  ]);

  // window globals for backward compat with the ported modules.
  useEffect(() => {
    (window as any).__openLeaf = openLeaf;
    return () => {
      delete (window as any).__openLeaf;
    };
  }, [openLeaf]);
  useEffect(() => {
    (window as any).__leafOpen = !!leafID;
    return () => {
      (window as any).__leafOpen = false;
    };
  }, [leafID]);

  // Eagerly load leaves for every brain node — keeps the inspector list
  // populated as users mouse over flows on the brain. The fetches are
  // ETag-cached so this is cheap on repeats.
  useEffect(() => {
    flows.forEach((f) => void loadLeavesForFlow(f.id, f.latestVersionID));
  }, [flows, loadLeavesForFlow]);

  // ── Brain-level SSE: subscribe to the graph:<tenant>:<platform> channel
  // so the brain repaints whenever the rebuild worker flushes (new project
  // exported, audit completed, decisions/violations counts changed). Mount
  // once for the lifetime of AtlasShellInner — independent of which leaf
  // is open.
  const platform = useAtlas((s) => s.platform);
  useEffect(() => {
    const unsub = subscribeGraphEvents(platform, () => {
      void refreshBrain();
      // Also refresh the open leaf's overlays in case violations/decisions
      // changed counts during the same rebuild cycle.
      const sel = useAtlas.getState().selection;
      if (sel.leafID && sel.flowID) {
        applyEvent({ type: "view_ready", slug: sel.flowID });
      }
    });
    return () => unsub();
  }, [platform, refreshBrain, applyEvent]);

  // ── Per-pipeline SSE: only when the URL carried a ?trace=<traceID>
  // (Figma plugin deeplink case). Without a trace_id the project events
  // channel would emit only heartbeats, so we skip it entirely for passive
  // viewing — the graph SSE above already covers the visible signals.
  const urlTrace = typeof window !== "undefined"
    ? new URLSearchParams(window.location.search).get("trace")
    : null;
  useEffect(() => {
    if (!leafID || !urlTrace) return;
    const leaf = getLeavesArray().find((l) => l.id === leafID);
    if (!leaf?.flow) return;
    const slug = leaf.flow;
    const unsub = subscribeProjectEvents(slug, urlTrace, (evt) => {
      switch (evt.type) {
        case "view_ready":
          applyEvent({ type: "view_ready", slug });
          void refreshBrain();
          break;
        case "audit_complete":
          applyEvent({ type: "audit_complete", slug });
          break;
        case "audit_failed":
          applyEvent({ type: "audit_failed", slug });
          break;
        case "audit_progress":
          applyEvent({ type: "audit_progress", slug });
          break;
        case "violation_lifecycle_changed": {
          const data = (evt.data ?? {}) as { violation_id?: string; status?: string };
          if (data.violation_id && data.status) {
            applyEvent({
              type: "violation_lifecycle_changed",
              violationID: data.violation_id,
              status: data.status as any,
            });
          }
          break;
        }
        default:
          break;
      }
    });
    return () => unsub();
  }, [leafID, urlTrace, applyEvent, refreshBrain]);

  const ctx: AtlasShellContextShape = { leafOpen: !!leafID, openLeaf, closeLeaf };

  const AtlasApp = getAtlasApp();
  const LeafCanvas = getLeafCanvas();
  const LeafInspector = getLeafInspector();
  const leaf = leafID ? getLeavesArray().find((l) => l.id === leafID) ?? null : null;

  // Mount-with-transition: keep the leaf shell mounted long enough for the
  // exit animation to complete. `leafSticky` is the leaf object that drives
  // the rendered canvas/inspector during both entering and exiting phases.
  // `phase` orchestrates the CSS animation.
  const [phase, setPhase] = useState<"closed" | "entering" | "open" | "exiting">(
    leaf ? "open" : "closed",
  );
  const [leafSticky, setLeafSticky] = useState(leaf);
  useEffect(() => {
    if (leaf && leaf.id !== leafSticky?.id) {
      // New leaf opened (or first open). Promote sticky and play entry.
      setLeafSticky(leaf);
      setPhase("entering");
      const id = window.setTimeout(() => setPhase("open"), 360);
      return () => window.clearTimeout(id);
    }
    if (leaf && leafSticky && leaf.id === leafSticky.id && leaf !== leafSticky) {
      // Same leaf, fresh reference — e.g. plan 005 U1 attached the sub_flow
      // after openLeaf already promoted sticky, or U6/U7's SSE refetch
      // replaced the leaf row. Refresh sticky in place WITHOUT re-triggering
      // the entry animation so CenterPaneSwitch + LeafInspector see the
      // new leaf.subFlow / leaf.violations / leaf.frames values.
      setLeafSticky(leaf);
    }
    if (!leaf && leafSticky) {
      // Closed — kick off exit; sticky drops once the animation ends.
      setPhase("exiting");
      const id = window.setTimeout(() => {
        setPhase("closed");
        setLeafSticky(null);
      }, 280);
      return () => window.clearTimeout(id);
    }
  }, [leaf, leafSticky]);
  // No brain-side CSS effects — the reference UI keeps the brain visually
  // untouched while the leaf overlay sits on top. Touching the brain's
  // CSS (opacity/transform/touch-action) breaks its canvas wheel + pointer
  // chain. Animation lives entirely on the leaf shell.

  // ── Resizable right inspector — persisted to localStorage so designers
  // who prefer a wider panel for DRD reading don't lose it across sessions.
  // Width is applied via a CSS variable on the inspector wrap; the handle
  // captures pointer events to drive the live resize.
  const STORAGE_KEY = "atlas:inspector:width";
  const MIN_WIDTH = 320;
  const MAX_WIDTH = 760;
  const [inspectorWidth, setInspectorWidth] = useState<number>(() => {
    if (typeof window === "undefined") return 460;
    const v = window.localStorage?.getItem(STORAGE_KEY);
    const n = v ? Number(v) : NaN;
    return Number.isFinite(n) && n >= MIN_WIDTH && n <= MAX_WIDTH ? n : 460;
  });
  useEffect(() => {
    if (typeof window === "undefined") return;
    try { window.localStorage.setItem(STORAGE_KEY, String(inspectorWidth)); } catch {}
  }, [inspectorWidth]);

  const onResizeStart = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.preventDefault();
    const startX = e.clientX;
    const startW = inspectorWidth;
    const handle = e.currentTarget;
    handle.classList.add("is-dragging");
    document.body.classList.add("atlas-inspector-dragging");
    const move = (mv: PointerEvent) => {
      // Right-anchored panel: dragging left widens it, dragging right shrinks.
      const delta = startX - mv.clientX;
      const next = Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, startW + delta));
      setInspectorWidth(next);
    };
    const up = () => {
      handle.classList.remove("is-dragging");
      document.body.classList.remove("atlas-inspector-dragging");
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
  }, [inspectorWidth]);

  // Subscribe to the open leaf's slot version. Each SSE-driven overlay
  // refresh bumps `loadedAt` on the slot (live-store.applyEvent →
  // fetchLeafOverlays → set leafSlots[id]). LeafCanvas + LeafInspector
  // memoize their data via `useMemo(() => window.buildXXX(leaf), [leaf.id])`
  // — keying them on the slot version forces a remount so memos refresh.
  const slotVersion = useAtlas((s) => (leafID ? s.leafSlots[leafID]?.loadedAt ?? 0 : 0));

  if (!AtlasApp) {
    return <div className="atlas-root atlas-root--booting" />;
  }

  return (
    <AtlasShellProvider value={ctx}>
      <AtlasApp />
      {leafSticky && LeafCanvas && LeafInspector ? (
        <div
          className="atlas-leaf-shell"
          data-leaf-phase={phase}
          style={{ ["--atlas-inspector-width" as any]: `${inspectorWidth}px` }}
        >
          <div className="atlas-leaf-canvas-wrap">
            <CenterPaneSwitch
              leaf={leafSticky}
              slotVersion={slotVersion}
              selectedFrameID={selectedFrameID}
              setSelectedFrameID={setSelectedFrameID}
              closeLeaf={closeLeaf}
              LeafCanvas={LeafCanvas}
            />
          </div>
          <div className="atlas-leaf-inspector-wrap">
            {selectedAtomicChild ? (
              <AtomicChildInspector
                key={`atomic-${selectedAtomicChild.screenID}-${selectedAtomicChild.figmaNodeID}`}
                screenID={selectedAtomicChild.screenID}
                figmaNodeID={selectedAtomicChild.figmaNodeID}
                canonicalTree={atomicCanonicalTree}
                override={atomicOverride}
                slug={leafSticky.id}
                onClose={closeAtomicInspector}
                onOverrideReset={handleAtomicOverrideReset}
              />
            ) : (
              <LeafInspector
                key={`inspector-${leafSticky.id}-${slotVersion}`}
                leaf={leafSticky}
                frameId={selectedFrameID}
                onClose={closeLeaf}
                onPickFrame={setSelectedFrameID}
              />
            )}
            {/* Resize handle — sits on the LEFT edge of the inspector so
               the user can drag it inward/outward to set width. Position
               is computed off the inspector's right-anchored layout. */}
            <div
              className="atlas-inspector-resize-handle"
              style={{ right: `${inspectorWidth}px` }}
              onPointerDown={onResizeStart}
              role="separator"
              aria-orientation="vertical"
              aria-label="Resize inspector"
              title="Drag to resize"
            />
          </div>
          {/* U3b — Cmd+F name-search palette. Mounted as a sibling of
              .lc-stage so its input focus correctly fails the canvas
              keymap's focus gate (closest('.lc-stage') is null). The
              palette is conditionally null when closed; opening
              snapshots frames once so the filtered list is stable. */}
          <NameSearchPalette
            open={paletteOpen}
            frames={paletteFrames}
            onClose={closePalette}
            onJumpToFrame={paletteJumpToFrame}
          />
        </div>
      ) : null}
    </AtlasShellProvider>
  );
}
