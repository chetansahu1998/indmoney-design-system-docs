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
import { useAtlas } from "@/lib/atlas/live-store";
import { getToken } from "@/lib/auth-client";

/**
 * UUID v4 sniffer — accepts the canonical 8-4-4-4-12 hex shape. The
 * backend's /image-refs endpoint expects a flow.id (UUID); the URL
 * contract documented in lib/atlas/url-state.ts:7 says `?leaf=<flow_id>`,
 * but the brain canvas sometimes hands us a project slug instead. When
 * that happens the image-refs hook has no real flow id to use, so we
 * resolve via the live store before firing the request.
 */
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function isUUID(s: string | null | undefined): s is string {
  return typeof s === "string" && UUID_RE.test(s);
}

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

  // Pull the leaves catalog so we can repair a non-UUID leafID (the
  // brain canvas sometimes routes via project slug instead of flow.id).
  // Returning the same array instance unless leaves changed keeps the
  // useEffect dep stable.
  const leavesByFlow = useAtlas((s) => s.leavesByFlow);

  useEffect(() => {
    if (!slug || !leafID) {
      setRefs(EMPTY_IMAGE_REFS);
      return;
    }

    // The /image-refs endpoint resolves leafID → (file_id, version_index)
    // via flows.id. If the supplied leafID isn't a valid UUID, search
    // the live-store's leavesByFlow for any leaf belonging to this
    // slug/product and use its real flow.id. Image-fill cache is keyed
    // on (file_id, version_index) — every leaf in the same file shares
    // the same map, so any flow.id from the right product works.
    let resolvedLeafID: string = leafID;
    if (!isUUID(leafID)) {
      let found: string | null = null;
      for (const leaves of Object.values(leavesByFlow)) {
        if (!Array.isArray(leaves)) continue;
        const match = leaves.find((l) => l.flow === slug || l.id === leafID);
        if (match) {
          found = match.id;
          break;
        }
      }
      if (!found) {
        // No leaf cataloged yet for this slug — skip the fetch. Returning
        // the empty map means image fills render as grey-checker placeholders
        // (same as before this hook existed). Will retry on next remount
        // once the live-store has populated.
        setRefs(EMPTY_IMAGE_REFS);
        return;
      }
      resolvedLeafID = found;
    }

    let cancelled = false;
    const dsURL = process.env.NEXT_PUBLIC_DS_SERVICE_URL || "";
    const token = getToken();
    const headers: Record<string, string> = token
      ? { Authorization: `Bearer ${token}` }
      : {};

    fetch(
      `${dsURL}/v1/projects/${encodeURIComponent(slug)}/leaves/${encodeURIComponent(
        resolvedLeafID,
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
  }, [slug, leafID, leavesByFlow]);

  return refs;
}
