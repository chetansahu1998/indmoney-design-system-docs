"use client";

/**
 * CanonicalRenderClient — client-side render harness for fidelity audit.
 *
 * Mirrors LeafFrameRenderer's hydration contract for a single
 * canonical_tree:
 *   - useImageRefs(slug, leafID)        → ImageRefMap for raster fills
 *   - useIconClusterURLs(slug, leafID)  → { urls, failedIDs } for icons
 *
 * Then walks the tree through nodeToHTML with hydrated maps. Once the
 * stream's status flips to `complete` (or failed) AND any in-flight
 * imageRefs fetch settles, the wrapper exposes `data-render-ready="true"`
 * so Chrome MCP can hold off the screenshot until pixels are stable.
 *
 * If `slug` is null (the caller didn't pass it) we render with empty
 * maps — same fall-through as before. That mode is fine for layout-only
 * audits but icon-cluster fidelity questions stay invisible.
 */

import { useEffect, useMemo, useState } from "react";

import { nodeToHTML } from "../../_lib/leafcanvas-v2/nodeToHTML";
import { filterVisible } from "../../_lib/leafcanvas-v2/visible-filter";
import { useImageRefs } from "../../_lib/leafcanvas-v2/useImageRefs";
import { useIconClusterURLs } from "../../_lib/leafcanvas-v2/useIconClusterURLs";
import type {
  BoundingBox,
  CanonicalNode,
  ImageRefMap,
} from "../../_lib/leafcanvas-v2/types";

const EMPTY_IMAGE_REFS: ImageRefMap = Object.freeze({}) as ImageRefMap;
const EMPTY_CLUSTER_URLS: ReadonlyMap<string, string> = new Map();
const EMPTY_FAILED_IDS: ReadonlySet<string> = new Set();

interface Props {
  tree: CanonicalNode;
  slug: string | null;
  leafID: string | null;
  source: string;
}

export function CanonicalRenderClient({ tree, slug, leafID, source }: Props) {
  const filtered = useMemo(() => filterVisible(tree), [tree]);
  const bbox: BoundingBox | null =
    filtered?.absoluteBoundingBox ?? null;

  // Production hooks — empty arms when no slug so we fall through to
  // the dashed-placeholder mode without crashing. The hooks themselves
  // bail early when slug/leafID are falsy.
  const hookSlug = slug ?? "";
  const hookLeafID = slug ? (leafID ?? slug) : null;
  const liveImageRefs = useImageRefs(hookSlug, hookLeafID);
  const liveClusterResult = useIconClusterURLs(
    hookSlug,
    hookLeafID,
    filtered ?? null,
    1, // zoom — audit is at 1:1, no tier escalation needed
    2,
  );

  const imageRefs: ImageRefMap = slug ? liveImageRefs : EMPTY_IMAGE_REFS;
  const clusterURLs: ReadonlyMap<string, string> = slug
    ? liveClusterResult.urls
    : EMPTY_CLUSTER_URLS;
  const clusterFailedIDs: ReadonlySet<string> = slug
    ? liveClusterResult.failedIDs
    : EMPTY_FAILED_IDS;

  // Readiness: when slug is provided, wait for the asset stream to
  // settle (urls + failedIDs collectively account for every cluster
  // we asked about). When no slug, ready immediately.
  //
  // Heuristic: we don't have a direct "stream complete" signal exposed
  // by useIconClusterURLs; instead we sample the result's combined
  // size after a debounce. When it stops growing for ~750ms, we call
  // it ready. That mirrors how a human would judge "the icons stopped
  // popping in." Image-refs is a one-shot fetch — `Object.keys`
  // length flipping non-zero is enough.
  const [ready, setReady] = useState<boolean>(!slug);
  useEffect(() => {
    if (!slug) {
      setReady(true);
      return;
    }
    setReady(false);
    let last = -1;
    let stableTicks = 0;
    const id = window.setInterval(() => {
      const size =
        clusterURLs.size + clusterFailedIDs.size + Object.keys(imageRefs).length;
      if (size === last) {
        stableTicks++;
      } else {
        stableTicks = 0;
        last = size;
      }
      // 5 ticks × 150ms = 750ms of stability.
      if (stableTicks >= 5 && size > 0) {
        window.clearInterval(id);
        setReady(true);
      }
    }, 150);
    // Safety: never wait forever. After 30s, declare ready regardless
    // — the screenshot will show whatever's hydrated. Audit can still
    // proceed; we'll note partial-hydration cases in the report.
    const timeout = window.setTimeout(() => {
      window.clearInterval(id);
      setReady(true);
    }, 30_000);
    return () => {
      window.clearInterval(id);
      window.clearTimeout(timeout);
    };
  }, [slug, clusterURLs, clusterFailedIDs, imageRefs]);

  if (!filtered || !bbox) {
    return (
      <div
        data-render-root="true"
        data-render-error="true"
        data-render-ready="true"
        data-render-source={source}
        style={{
          position: "relative",
          padding: 16,
          background: "#fff",
          color: "#900",
          fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
          fontSize: 12,
        }}
      >
        canonical-render: tree loaded but missing absoluteBoundingBox at root.
      </div>
    );
  }

  const rendered = nodeToHTML(
    filtered,
    bbox,
    null,
    { imageRefs, clusterURLs, clusterFailedIDs },
    "root",
  );

  // 2026-05-12 round-2 audit P14: when the rendered subject is shorter
  // than the screenshot viewport (bottomsheets, modals, partial-screen
  // frames), the atlas layout's default page background bleeds through
  // below the render-root and screenshots as opaque black. Force a
  // white viewport host so partial-screen subjects sit on a clean
  // white background — both layout-only audits AND production-style
  // hydrated captures look correct without an additional CSS reset
  // outside this route.
  return (
    <>
      <style jsx global>{`
        html, body { background: #fff !important; margin: 0 !important; padding: 0 !important; }
      `}</style>
      <div
        data-render-root="true"
        data-render-ready={ready ? "true" : "false"}
        data-render-source={source}
        data-render-width={bbox.width}
        data-render-height={bbox.height}
        data-render-clusters-resolved={clusterURLs.size}
        data-render-clusters-failed={clusterFailedIDs.size}
        data-render-imagerefs={Object.keys(imageRefs).length}
        style={{
          position: "relative",
          width: `${bbox.width}px`,
          height: `${bbox.height}px`,
          background: "#fff",
          overflow: "hidden",
        }}
      >
        {rendered}
      </div>
    </>
  );
}
