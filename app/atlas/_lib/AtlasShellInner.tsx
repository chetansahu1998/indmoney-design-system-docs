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

import { useCallback, useEffect, useState } from "react";

import { subscribeProjectEvents } from "../../../lib/projects/client";
import { subscribeGraphEvents } from "../../../lib/atlas/data-adapters";
import { useAtlas } from "../../../lib/atlas/live-store";
import { AtlasShellProvider, type AtlasShellContextShape } from "./AtlasShellContext";

// Side-effect imports — order matters; see AtlasShell for rationale.
import "./tweaks-panel";
import "./leaves";
import "./frames";
import "./real-data-bridge"; // Phase 5: must come AFTER leaves+frames
import "./atlas";
import "./leafcanvas";
import AtlasDRDEditor from "./AtlasDRDEditor"; // Phase 6 — Notion-like DRD

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
function getLeavesArray(): Array<{ id: string; flow: string; label: string }> {
  return (typeof window !== "undefined" ? (window as any).LEAVES : null) ?? [];
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

  // Esc layered close.
  useEffect(() => {
    const fn = (e: KeyboardEvent) => {
      if (e.key === "Escape" && leafID) {
        if (selectedFrameID) setSelectedFrameID(null);
        else closeLeaf();
      }
    };
    window.addEventListener("keydown", fn);
    return () => window.removeEventListener("keydown", fn);
  }, [leafID, selectedFrameID, closeLeaf]);

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
      {leaf && LeafCanvas && LeafInspector ? (
        <>
          <LeafCanvas
            key={`canvas-${leaf.id}-${slotVersion}`}
            leaf={leaf}
            onClose={closeLeaf}
            onPickFrame={setSelectedFrameID}
            selectedFrameId={selectedFrameID}
          />
          <LeafInspector
            key={`inspector-${leaf.id}-${slotVersion}`}
            leaf={leaf}
            frameId={selectedFrameID}
            onClose={closeLeaf}
            onPickFrame={setSelectedFrameID}
          />
        </>
      ) : null}
    </AtlasShellProvider>
  );
}
