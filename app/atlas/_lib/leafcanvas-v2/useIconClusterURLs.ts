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
import { isIconCluster } from "./icon-cluster-resolver";
import { getDeviceDPR, pickPreviewTier, type PreviewTier } from "./preview-tier";
import { getToken } from "@/lib/auth-client";

export const EMPTY_CLUSTER_URLS: ReadonlyMap<string, string> = new Map();

/** Walk a canonical_tree and yield every icon-cluster's id. */
export function collectClusterIDs(root: CanonicalNode | null): string[] {
  if (!root) return [];
  const out: string[] = [];
  walk(root, out);
  return out;
}

function walk(node: CanonicalNode, acc: string[]): void {
  if (isIconCluster(node) && typeof node.id === "string") {
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
  walkWithBBox(root, out);
  return out;
}

function walkWithBBox(node: CanonicalNode, acc: ClusterIDWithBBox[]): void {
  if (isIconCluster(node) && typeof node.id === "string") {
    const bbox = node.absoluteBoundingBox;
    const longest = bbox
      ? Math.max(bbox.width ?? 0, bbox.height ?? 0)
      : 64; // sane default for clusters without bbox metadata
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

    (async () => {
      // Mint signed URLs per cluster. Each `<img>` fetch synchronously
      // renders + caches on first load (5s budget); subsequent loads
      // hit cache and return instantly. If a render times out (HTTP
      // 425), nodeToHTML.ts's onError handler swaps to the dashed
      // placeholder. After the first user interaction with a leaf,
      // the cache is warm and reloads paint the full canvas.
      //
      // A pre-warm batch endpoint was prototyped (POST /assets/warm)
      // but removed pending investigation of an intermittent mux 404
      // with the AdaptAuthMiddleware wrapper. See git log e62a935..
      // for the work-in-progress; safe to re-introduce when the
      // routing issue is resolved.
      const next = new Map<string, string>();
      const failures: string[] = [];
      await Promise.all(
        idsWithBBox.map(async ({ id, longestEdgePx }) => {
          try {
            const tier: PreviewTier = pickPreviewTier(longestEdgePx, zoom, dpr);
            const res = await fetch(
              `${dsURL}/v1/projects/${encodeURIComponent(slug)}/assets/export-url`,
              {
                method: "POST",
                headers: { "Content-Type": "application/json", ...authHeaders },
                // Preview-tier formats coerce scale to 1 server-side, but we
                // still send the legacy `scale` so older deploys (pre-7e97cf2)
                // don't 400 on a missing field.
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
        console.warn(
          `[icon-cluster] ${failures.length}/${idsWithBBox.length} mints failed:`,
          failures.slice(0, 5),
        );
      }
      setURLs(next);
    })();

    return () => {
      cancelled = true;
    };
    // `zoom` IS a dep so a zoom change that flips a cluster into a new
    // tier band triggers a fresh mint with the higher-resolution PNG.
    // Mint calls hit ds-service cache (no Figma round-trip), so the
    // re-mint cost is bounded by ~5ms per cluster × N clusters per
    // zoom-stop event.
  }, [slug, leafID, treeRoot, zoom, scale]);

  return urls;
}
