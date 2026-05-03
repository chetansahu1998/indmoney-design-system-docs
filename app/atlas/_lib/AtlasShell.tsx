"use client";

/**
 * AtlasShell — TSX port of `INDmoney Docs/shell.jsx` + Phase 4 live-data
 * hydration.
 *
 * Lifecycle:
 *   1. Mount AtlasShellBoot. Hydrate the live store (lib/atlas/live-store)
 *      from /v1/projects/atlas/brain-nodes + /v1/projects/graph.
 *   2. Write the resolved DOMAINS / FLOWS / SYNAPSES / LEAVES into the
 *      window.__ATLAS_* globals that the ported atlas/leaves modules read at
 *      module-load.
 *   3. Dynamic-import AtlasShellInner so the side-effect imports
 *      (atlas / leafcanvas / leaves / frames / tweaks-panel) only run AFTER
 *      the data is in place.
 *   4. Subscribe to graph:<tenant>:<platform> SSE; on bust, refresh the
 *      brain slice and remount the inner shell via key={loadedAt} to pick
 *      up new flows with the entrance-bloom animation.
 *   5. The leaf canvas is opened by user click — we hydrate per-leaf data
 *      on demand (live-store.openLeaf).
 */

import dynamic from "next/dynamic";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";

import { selectFlows, selectSelection, useAtlas } from "../../../lib/atlas/live-store";
import { ATLAS_DOMAINS } from "../../../lib/atlas/taxonomy";
import NoPlatformFlows from "../NoPlatformFlows";
import {
  buildAtlasURL,
  shouldReplaceHistory,
  type AtlasURLState,
  DEFAULT_ATLAS_URL_STATE,
} from "../../../lib/atlas/url-state";

const AtlasShellInner = dynamic(() => import("./AtlasShellInner"), {
  ssr: false,
  loading: () => <div className="atlas-root atlas-root--booting" />,
});

export interface AtlasShellProps {
  /** Parsed URL state from page.tsx — drives initial selection. */
  initialURL?: AtlasURLState;
}

export default function AtlasShell({ initialURL }: AtlasShellProps = {}) {
  const platform = useAtlas((s) => s.platform);
  const setPlatform = useAtlas((s) => s.setPlatform);
  const hydrated = useAtlas((s) => s.hydrated);
  const flows = useAtlas(selectFlows);
  const synapses = useAtlas((s) => s.synapses);
  const selection = useAtlas(selectSelection);
  const leavesByFlow = useAtlas((s) => s.leavesByFlow);
  const hydrateInitial = useAtlas((s) => s.hydrateInitial);
  const refreshBrain = useAtlas((s) => s.refreshBrain);
  const openLeaf = useAtlas((s) => s.openLeaf);
  const loadLeavesForFlow = useAtlas((s) => s.loadLeavesForFlow);
  const router = useRouter();
  const pathname = usePathname();

  const url = initialURL ?? DEFAULT_ATLAS_URL_STATE;
  const previousURLRef = useRef<AtlasURLState>(url);

  // Apply ?platform from URL on first mount.
  useEffect(() => {
    if (url.platform !== platform) void setPlatform(url.platform);
  }, [url.platform, platform, setPlatform]);

  // Apply ?project / ?leaf from URL once data is hydrated. Pre-load the
  // requested project's leaves so openLeaf can resolve the leaf ID.
  useEffect(() => {
    if (!hydrated) return;
    if (!url.project) return;
    const project = flows.find((f) => f.id === url.project);
    if (!project) return;
    void loadLeavesForFlow(project.id, project.latestVersionID).then(() => {
      if (url.leaf) void openLeaf(url.leaf);
    });
  }, [hydrated, url.project, url.leaf, flows, loadLeavesForFlow, openLeaf]);

  // Push selection changes back into the URL.
  useEffect(() => {
    if (!hydrated) return;
    const next: AtlasURLState = {
      ...DEFAULT_ATLAS_URL_STATE,
      platform,
      project: selection.flowID,
      leaf: selection.leafID,
      frame: selection.frameID,
      versionID: url.versionID,
      traceID: url.traceID,
      persona: url.persona,
      from: url.from,
    };
    const prev = previousURLRef.current;
    if (
      prev.platform === next.platform &&
      prev.project === next.project &&
      prev.leaf === next.leaf &&
      prev.frame === next.frame
    ) {
      return;
    }
    const href = buildAtlasURL(next, pathname || "/atlas");
    if (shouldReplaceHistory(prev, next)) router.replace(href);
    else router.push(href);
    previousURLRef.current = next;
  }, [
    hydrated,
    platform,
    selection.flowID,
    selection.leafID,
    selection.frameID,
    pathname,
    router,
    url.versionID,
    url.traceID,
    url.persona,
    url.from,
  ]);

  // Force-remount key. Bumps every time the brain slice changes
  // structurally (length or new flow IDs). Keeps the entrance bloom firing
  // on SSE-driven additions because the ported atlas.tsx reads its
  // constants at module load.
  const [remountKey, setRemountKey] = useState(0);
  const flowsSignature = flows.map((f) => f.id).join("|");

  // 1. Cold-load
  useEffect(() => {
    if (!hydrated) void hydrateInitial();
  }, [hydrated, hydrateInitial, platform]);

  // 2. Push real data into window globals SYNCHRONOUSLY in render — atlas.tsx
  // reads __ATLAS_DOMAINS/__ATLAS_FLOWS/__ATLAS_SYNAPSES at module load
  // (top-level const), so the write must happen BEFORE AtlasShellInner is
  // dynamic-imported on first hydrated render. A useEffect here would lose
  // the race because effects run after children commit.
  //
  // We write even when flows.length === 0 (empty production DB) and set
  // __ATLAS_DATA_READY so atlas.tsx prefers the real (possibly empty)
  // arrays over the mock — otherwise tenants with no projects would see
  // the showcase mock and not realise their data isn't connected.
  if (typeof window !== "undefined" && hydrated) {
    const w = window as any;
    w.__ATLAS_DATA_READY = true;
    w.__ATLAS_DOMAINS = ATLAS_DOMAINS;
    w.__ATLAS_FLOWS = flows.map((f) => ({
      id: f.id,
      label: f.label,
      domain: f.domain,
      count: f.count,
      primary: f.primary,
    }));
    w.__ATLAS_SYNAPSES = synapses;
    const flatLeaves: any[] = [];
    const byFlow: Record<string, any[]> = {};
    for (const [, leaves] of Object.entries(leavesByFlow)) {
      for (const l of leaves) {
        const row = {
          id: l.id,
          flow: l.flow,
          label: l.label,
          frames: l.frames,
          violations: l.violations,
          frameKind: "real",
        };
        flatLeaves.push(row);
        (byFlow[l.flow] ??= []).push(row);
      }
    }
    w.LEAVES = flatLeaves;
    // Rebuild the index too — leaves.tsx builds it ONCE at module load,
    // so the brain's right-inspector and the ⌘K palette would otherwise
    // see a stale map after a project's leaves load asynchronously.
    w.LEAVES_BY_FLOW = byFlow;
  }

  // 3. SSE wiring lives in AtlasShellInner (it owns the EventSource so
  // remounts cleanly tear it down).

  // 4. Remount inner shell when the structural shape of flows changes.
  useEffect(() => {
    if (!hydrated) return;
    setRemountKey((k) => k + 1);
  }, [flowsSignature, hydrated]);

  // 5. Periodic gentle refresh as a belt-and-braces backup to SSE.
  useEffect(() => {
    if (!hydrated) return;
    const t = window.setInterval(() => void refreshBrain(), 60_000);
    return () => window.clearInterval(t);
  }, [hydrated, refreshBrain]);

  if (!hydrated) {
    return <div className="atlas-root atlas-root--booting" />;
  }

  // T9 — empty-state: no flows extracted on the active platform yet.
  // Until the plugin grows a `web` extraction target, switching to
  // ?platform=web returns zero rows for every tenant; the canvas would
  // otherwise paint as a black void with no signal that the data simply
  // isn't there. This empty state routes the user back to the populated
  // platform.
  if (flows.length === 0) {
    return (
      <NoPlatformFlows
        platform={platform}
        onSwitchPlatform={(next) => void setPlatform(next)}
      />
    );
  }

  return (
    <AtlasShellInner
      key={remountKey}
      selection={selection}
    />
  );
}
