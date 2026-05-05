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
    // Don't recurse into a cluster: nested clusters render as part of
    // the outer cluster's PNG. (If we minted both, the inner would
    // double-paint over its parent.)
    return;
  }
  if (Array.isArray(node.children)) {
    for (const c of node.children) walk(c, acc);
  }
}

/**
 * Resolves cluster ids → signed `?at=…` URLs for `<img src>` use.
 *
 * @param slug    project slug (must match the leaf's owning project)
 * @param treeRoot canonical_tree root used for cluster discovery
 * @param scale   PNG scale (1|2|3); 2 matches the screen PNG default
 *
 * @returns a Map (`id → url`). Empty until the first batch resolves.
 */
export function useIconClusterURLs(
  slug: string,
  treeRoot: CanonicalNode | null,
  scale: 1 | 2 | 3 = 2,
): ReadonlyMap<string, string> {
  const [urls, setURLs] = useState<ReadonlyMap<string, string>>(EMPTY_CLUSTER_URLS);

  useEffect(() => {
    if (!treeRoot || !slug) {
      setURLs(EMPTY_CLUSTER_URLS);
      return;
    }
    const ids = collectClusterIDs(treeRoot);
    if (ids.length === 0) {
      setURLs(EMPTY_CLUSTER_URLS);
      return;
    }
    let cancelled = false;
    const dsURL = process.env.NEXT_PUBLIC_DS_SERVICE_URL || "";
    const token = getToken();
    const auth = token ? { Authorization: `Bearer ${token}` } : ({} as Record<string, string>);

    const next = new Map<string, string>();
    Promise.all(
      ids.map(async (id) => {
        try {
          const res = await fetch(
            `${dsURL}/v1/projects/${encodeURIComponent(slug)}/assets/export-url`,
            {
              method: "POST",
              headers: { "Content-Type": "application/json", ...auth },
              body: JSON.stringify({ node_id: id, format: "png", scale }),
            },
          );
          if (!res.ok) return;
          const body = (await res.json()) as { url?: string };
          if (typeof body.url === "string" && body.url.length > 0) {
            // The mint endpoint returns a relative URL — prepend dsURL
            // so the browser hits ds-service directly (the same origin
            // the canonical_tree fetch already uses).
            next.set(id, body.url.startsWith("http") ? body.url : `${dsURL}${body.url}`);
          }
        } catch {
          // Single-cluster failure → skip; placeholder renders.
        }
      }),
    ).then(() => {
      if (!cancelled) setURLs(next);
    });

    return () => {
      cancelled = true;
    };
  }, [slug, treeRoot, scale]);

  return urls;
}
