/**
 * useImageRefs.ts — fetches the per-leaf imageRef → cached-blob URL map
 * from ds-service so the canvas-v2 LeafFrameRenderer can paint raster
 * fills (background photos, illustrations, raster icons) instead of
 * grey-checker placeholders.
 *
 * Backend endpoint:
 *
 *   GET /v1/projects/:slug/leaves/:leaf_id/image-refs
 *
 * Response shape:
 *   { "image_refs": { "<imageRef>": { url, mime, bytes } } }
 *
 * The server walks every screen's canonical_tree, batches the imageRefs
 * through Figma's `/v1/files/<key>/images` once per (file, version),
 * downloads bytes, caches them under data/image-fills/, and returns
 * proxy URLs that ds-service serves with immutable cache headers.
 *
 * One fetch per leaf, not per screen — even for an 80-screen leaf with
 * 200 distinct imageRefs the warm-up is one round-trip.
 */

import { useEffect, useState } from "react";

import type { ImageRefMap } from "./types";
import { getToken } from "@/lib/auth-client";

export const EMPTY_IMAGE_REFS: ImageRefMap = Object.freeze({}) as ImageRefMap;

interface ImageRefEntry {
  url: string;
  mime?: string;
  bytes?: number;
}

interface ImageRefsResponse {
  image_refs?: Record<string, ImageRefEntry>;
}

/**
 * Resolves canonical_tree imageRef hashes to served URLs for the
 * given leaf. Returns the empty frozen map until the first fetch
 * resolves. Fetches once per (slug, leafID) pair; if either changes
 * the prior in-flight request is cancelled.
 */
export function useImageRefs(
  slug: string,
  leafID: string | null | undefined,
): ImageRefMap {
  const [refs, setRefs] = useState<ImageRefMap>(EMPTY_IMAGE_REFS);

  useEffect(() => {
    if (!slug || !leafID) {
      setRefs(EMPTY_IMAGE_REFS);
      return;
    }
    let cancelled = false;
    const dsURL = process.env.NEXT_PUBLIC_DS_SERVICE_URL || "";
    const token = getToken();
    const headers: Record<string, string> = token
      ? { Authorization: `Bearer ${token}` }
      : {};

    fetch(
      `${dsURL}/v1/projects/${encodeURIComponent(slug)}/leaves/${encodeURIComponent(
        leafID,
      )}/image-refs`,
      { headers },
    )
      .then((res) => {
        if (!res.ok) return null;
        return res.json() as Promise<ImageRefsResponse>;
      })
      .then((body) => {
        if (cancelled || !body || !body.image_refs) return;
        const map: Record<string, string> = {};
        for (const [hash, entry] of Object.entries(body.image_refs)) {
          if (entry && typeof entry.url === "string" && entry.url.length > 0) {
            // Server returns relative URLs; prepend the dsURL origin so
            // <img src> hits ds-service directly (same origin already
            // used by canonical_tree fetches).
            map[hash] = entry.url.startsWith("http")
              ? entry.url
              : `${dsURL}${entry.url}`;
          }
        }
        setRefs(map);
      })
      .catch(() => {
        // Network blip — leave the placeholder rendering. A retry on
        // remount will re-fetch.
      });

    return () => {
      cancelled = true;
    };
  }, [slug, leafID]);

  return refs;
}
