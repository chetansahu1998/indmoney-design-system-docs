/**
 * useIconClusterURLs.ts — resolves canonical_tree icon-cluster wrappers
 * (FRAME/INSTANCE/GROUP/BOOLEAN_OPERATION whose subtree is text-free) to
 * Figma-rendered PNG URLs by minting a signed URL per cluster via the
 * existing `POST /v1/projects/:slug/assets/export-url` endpoint.
 *
 * Why per-cluster instead of bulk-zip:
 *   - <img src=…> is the fastest "tile-style" canvas paint (no JS-side
 *     ZIP decoding, no blob-URL bookkeeping, browser parallelizes 6
 *     fetches per origin).
 *   - The bulk-export endpoint exists and would save round-trips, but
 *     it returns a ZIP which the canvas would have to unzip + objectURL-
 *     ify per file — more code for a marginal speed-up at typical
 *     cluster counts (5–30 per leaf).
 *
 * Concurrency: requests fire as soon as the cluster id list resolves
 * from the canonical_tree. Browsers cap at 6 concurrent requests per
 * origin; mint calls average ~50ms each so a 30-cluster leaf warms up
 * in ~250ms. Failures (single-cluster 5xx, network blip, expired token)
 * fall through to the dashed-border placeholder; the cluster still
 * exists in the canvas, it just renders as a stub the user can click
 * to open the source frame in Figma.
 *
 * Cache: never. Tokens expire in 60s, so the next render naturally
 * remints. If the canvas remounts inside that window the same map is
 * still in zustand-store memory (LeafFrameRenderer caches via
 * `useState`), so no extra calls.
 */

import { useEffect, useState } from "react";

import type { CanonicalNode } from "./types";
import { shouldRasterize } from "./node-classifier";
import { getDeviceDPR, pickPreviewTier, type PreviewTier } from "./preview-tier";
import { getToken } from "@/lib/auth-client";
import {
  subscribeLeafAssets,
  waitForLeafStreamSettled,
} from "./leaf-asset-stream";

export const EMPTY_CLUSTER_URLS: ReadonlyMap<string, string> = new Map();

/** Walk a canonical_tree and yield every icon-cluster's id. */
export function collectClusterIDs(root: CanonicalNode | null): string[] {
  if (!root) return [];
  const out: string[] = [];
  // Skip the screen-root for the same reason as collectClusterIDsWithBBox.
  // The root is always a container in nodeToHTML; clustering it would
  // both waste a URL mint and prevent descent into real inner clusters.
  if (Array.isArray(root.children)) {
    for (const c of root.children) walk(c, out);
  }
  return out;
}

function walk(node: CanonicalNode, acc: string[]): void {
  // Visibility pruning. Mirrors the Go server-side walker
  // (services/ds-service/internal/projects/pipeline_cluster_prerender.go
  // ::walkClusters lines 121-129). Hidden / removed nodes don't render
  // (nodeToHTML respects the flags at draw time) so collecting cluster
  // URLs for them mints unused signed URLs and wastes Phase 1 Figma
  // calls during Stage 9 prerender. Pruning here matches Go-side
  // behavior — confirmed by the parity fixture at __tests__/node-
  // classifier-parity.test.ts which the test-side walker mirrors.
  const visibleField = (node as unknown as { visible?: unknown }).visible;
  if (typeof visibleField === "boolean" && !visibleField) return;
  const removedField = (node as unknown as { removed?: unknown }).removed;
  if (typeof removedField === "boolean" && removedField) return;
  if (shouldRasterize(node) && typeof node.id === "string") {
    acc.push(node.id);
    return;
  }
  if (Array.isArray(node.children)) {
    for (const c of node.children) walk(c, acc);
  }
}

/**
 * Like `collectClusterIDs` but also returns the cluster's longest-edge
 * px from `absoluteBoundingBox`. Used by the preview-tier selector to
 * pick a tier per cluster rather than one tier for the whole leaf.
 */
export interface ClusterIDWithBBox {
  id: string;
  longestEdgePx: number;
}

export function collectClusterIDsWithBBox(
  root: CanonicalNode | null,
): ClusterIDWithBBox[] {
  if (!root) return [];
  const out: ClusterIDWithBBox[] = [];
  // Skip the screen-root from clustering — it always renders as
  // container in nodeToHTML (see the isScreenRoot guard there), so
  // minting a cluster URL for it is wasted work AND would short-circuit
  // descent into real inner clusters. Walk the root's children directly.
  if (Array.isArray(root.children)) {
    for (const c of root.children) walkWithBBox(c, out);
  }
  return out;
}

function walkWithBBox(node: CanonicalNode, acc: ClusterIDWithBBox[]): void {
  // Single classification source — node-classifier.ts combines name
  // patterns (Icons/.../, Illustrations/, Yes/No/24px variants) with the
  // structural heuristic. shouldRasterize returns true for icons,
  // illustrations, and standalone shapes; layout-named containers
  // (Status Bar, OTP Input, Footer, ...) return false even when the
  // structural heuristic would tag them as clusters.
  if (shouldRasterize(node) && typeof node.id === "string") {
    const bbox = node.absoluteBoundingBox;
    const longest = bbox
      ? Math.max(bbox.width ?? 0, bbox.height ?? 0)
      : 24;
    acc.push({ id: node.id, longestEdgePx: longest });
    return;
  }
  if (Array.isArray(node.children)) {
    for (const c of node.children) walkWithBBox(c, acc);
  }
}

/**
 * Resolves cluster ids → signed `?at=…` URLs for `<img src>` use.
 *
 * Two-step warm-then-mint flow:
 *
 *   1. POST /v1/projects/<slug>/assets/warm with the full node-id list.
 *      ds-service runs RenderAssetsForLeaf which renders + caches each
 *      PNG up-front, batched against Figma's 5 req/sec budget. Without
 *      this step a leaf with 30+ clusters would race the 5-second
 *      synchronous render budget per `<img>` and most fetches would
 *      come back HTTP 425 — which browsers don't retry on, so the
 *      canvas would show broken images.
 *
 *   2. After warm completes, parallel-mint /assets/export-url tokens.
 *      Every fetch hits cache and returns instantly (no further Figma
 *      round-trips).
 *
 * @param slug     project slug (must match the leaf's owning project)
 * @param leafID   ds-service flow.id (resolved from the live store)
 * @param treeRoot canonical_tree root used for cluster discovery
 * @param scale    PNG scale (1|2|3); 2 matches the screen PNG default
 *
 * @returns a Map (`id → url`). Empty until warm + mint complete.
 */
export function useIconClusterURLs(
  slug: string,
  leafID: string | null | undefined,
  treeRoot: CanonicalNode | null,
  /** Display zoom — drives preview-tier selection per cluster. */
  zoom: number = 1,
  /** Legacy parameter; kept for callers that still pass scale=2 explicitly. */
  scale: 1 | 2 | 3 = 2,
): ReadonlyMap<string, string> {
  const [urls, setURLs] = useState<ReadonlyMap<string, string>>(EMPTY_CLUSTER_URLS);

  useEffect(() => {
    if (!treeRoot || !slug || !leafID) {
      setURLs(EMPTY_CLUSTER_URLS);
      return;
    }
    // Collect cluster ids + their bounding box widths so we can pick a
    // preview tier per cluster. Wider clusters get a bigger tier; tiny
    // 16×16 icons stay at preview-128 even at zoom=2.
    const idsWithBBox = collectClusterIDsWithBBox(treeRoot);
    if (idsWithBBox.length === 0) {
      setURLs(EMPTY_CLUSTER_URLS);
      return;
    }
    let cancelled = false;
    const dsURL = process.env.NEXT_PUBLIC_DS_SERVICE_URL || "";
    const token = getToken();
    const authHeaders: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {};
    const dpr = getDeviceDPR();
    const localIDs = new Set(idsWithBBox.map(({ id }) => id));

    // Subscribe to the leaf-level SSE stream. The store opens ONE
    // EventSource per (slug, leafID) regardless of how many frames in
    // the leaf call this hook — first subscriber kicks off the stream,
    // the rest free-ride on the same Map. As `asset-ready` events
    // arrive, the snapshot updates and we filter to this frame's local
    // cluster IDs. Whatever the stream doesn't deliver (failed events,
    // server doesn't support the endpoint, network blip) falls through
    // to the per-cluster mint pass below.
    const projectStreamFiltered = (snap: ReturnType<typeof subscribeLeafAssets>["snapshot"]): Map<string, string> => {
      const filtered = new Map<string, string>();
      for (const id of localIDs) {
        const u = snap().urls.get(id);
        if (typeof u === "string" && u.length > 0) {
          filtered.set(id, u.startsWith("http") ? u : `${dsURL}${u}`);
        }
      }
      return filtered;
    };

    let lastEmittedSize = 0;
    const sub = subscribeLeafAssets(slug, leafID, () => {
      if (cancelled) return;
      const filtered = projectStreamFiltered(sub.snapshot);
      // Only re-render when our frame's slice changed. Avoids hundreds
      // of LeafFrameRenderers re-rendering for every other frame's
      // asset-ready event.
      if (filtered.size !== lastEmittedSize) {
        lastEmittedSize = filtered.size;
        setURLs(filtered);
      }
    });
    // Emit current snapshot immediately so cache hits or re-mounts
    // pick up state without waiting for the next event.
    {
      const initial = projectStreamFiltered(sub.snapshot);
      if (initial.size > 0) {
        lastEmittedSize = initial.size;
        setURLs(initial);
      }
    }

    // Residual mint pass — runs after the stream settles (`complete` or
    // `failed`). Picks up any cluster IDs the stream didn't deliver
    // (older deploys without /asset-stream, transient 5xx, server-side
    // render failure that surfaced as asset-failed). Mirrors the
    // pre-2026-05-09 Promise.all path so we never regress when the
    // stream is unavailable.
    (async () => {
      await waitForLeafStreamSettled(slug, leafID);
      if (cancelled) return;
      const have = projectStreamFiltered(sub.snapshot);
      const missing = idsWithBBox.filter(({ id }) => !have.has(id) && !sub.snapshot().failedIDs.has(id));
      if (missing.length === 0) {
        // Stream covered everything — final state already pushed via
        // the subscriber callback. Nothing left to do.
        return;
      }
      const next = new Map(have);
      const failures: string[] = [];
      await Promise.all(
        missing.map(async ({ id, longestEdgePx }) => {
          try {
            const tier: PreviewTier = pickPreviewTier(longestEdgePx, zoom, dpr);
            const res = await fetch(
              `${dsURL}/v1/projects/${encodeURIComponent(slug)}/assets/export-url`,
              {
                method: "POST",
                headers: { "Content-Type": "application/json", ...authHeaders },
                body: JSON.stringify({
                  node_id: id,
                  format: `preview-${tier}`,
                  scale,
                }),
              },
            );
            if (!res.ok) {
              failures.push(`${id}: HTTP ${res.status}`);
              return;
            }
            const body = (await res.json()) as { url?: string };
            if (typeof body.url === "string" && body.url.length > 0) {
              next.set(id, body.url.startsWith("http") ? body.url : `${dsURL}${body.url}`);
            }
          } catch (err) {
            failures.push(`${id}: ${err instanceof Error ? err.message : String(err)}`);
          }
        }),
      );
      if (cancelled) return;
      if (failures.length > 0) {
        // eslint-disable-next-line no-console
        console.warn(
          `[icon-cluster] residual mint: ${failures.length}/${missing.length} failed:`,
          failures.slice(0, 5),
        );
      }
      setURLs(next);
    })();

    return () => {
      cancelled = true;
      sub.unsubscribe();
    };
    // `zoom` IS a dep so a zoom change that flips a cluster into a new
    // tier band triggers a fresh re-evaluate. Stream URLs are tier-
    // agnostic (server emits preview-128); zoom-up uses the residual
    // mint path which hits cache (PreviewPyramidGenerator wrote all 4
    // tiers in one Figma call when the stream rendered each cluster).
  }, [slug, leafID, treeRoot, zoom, scale]);

  return urls;
}
