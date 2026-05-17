"use client";

/**
 * useFrameThumbTokens — mints + memoises the per-frame asset tokens that
 * <FrameThumbnail> needs to load PNGs through /api/figma/frame-png.
 *
 * Token economics (U1 of plan 2026-05-17-004):
 *   - Each (file_key, node_id, scale) needs its own MAC-bound token; the
 *     ds-service signer's resource_id slot encodes the triple.
 *   - The mint endpoint returns a `url` that already includes ?file_key=…
 *     &node_id=…&scale=…&tenant=…&at=…. We slice off the path prefix and
 *     keep just the query string portion the <FrameThumbnail> consumes.
 *   - Tokens live for 10 min (FigmaFramePNGAssetTokenTTL on the server).
 *     The Wall + FrameGrid stay open well within that window; we refresh
 *     when the parent re-fetches section.inspect (which happens on SSE).
 *
 * Public shape:
 *   const { tokenQSFor, loading } = useFrameThumbTokens(fileKey, nodeIDs);
 *   tokenQSFor(nodeID) → "tenant=…&at=…" | "" (empty until minted)
 *
 * The empty-string fallback lets <FrameThumbnail> render its placeholder
 * glyph immediately, then upgrade to the real PNG when the token resolves.
 */

import { useEffect, useMemo, useRef, useState } from "react";
import { useAuth } from "@/lib/auth-client";

interface MintResponse {
  url?: string;
  expires_in?: number;
  error?: string;
  detail?: string;
}

export function useFrameThumbTokens(
  fileKey: string | undefined,
  nodeIDs: string[],
  scale: 1 | 2 = 1,
) {
  const token = useAuth((s) => s.token);
  const [tokensByNode, setTokensByNode] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(false);

  // Track which (fileKey, nodeID, scale) tuples have an in-flight mint so
  // a re-render with the same nodeIDs doesn't kick off duplicate requests.
  // The ref persists across renders without triggering re-runs of the
  // dispatch effect.
  const inflight = useRef<Set<string>>(new Set());

  // Cap the number of distinct node-ids we'll request tokens for in one
  // useEffect cycle. The Wall surface is bounded by Figma section size
  // (~70 frames in the largest INDmoney files) and the mint endpoint is
  // not rate-limited per-tenant, but a defensive cap prevents pathological
  // sub_flows from drowning the API.
  const MAX_MINTS_PER_FLUSH = 100;

  // Sort the node ids so the join key is stable across renders even when
  // the parent passes a re-ordered array; otherwise we'd re-run on every
  // render of the parent.
  const keyList = useMemo(() => [...nodeIDs].sort().join(","), [nodeIDs]);

  useEffect(() => {
    if (!token || !fileKey || nodeIDs.length === 0) return;
    let cancelled = false;

    const todo: string[] = [];
    for (const nid of nodeIDs) {
      if (todo.length >= MAX_MINTS_PER_FLUSH) break;
      const cacheKey = `${fileKey}|${nid}|${scale}`;
      if (tokensByNode[nid]) continue;
      if (inflight.current.has(cacheKey)) continue;
      inflight.current.add(cacheKey);
      todo.push(nid);
    }
    if (todo.length === 0) return;

    setLoading(true);
    (async () => {
      // We mint one token per node id. The server-side mint endpoint is
      // small (HMAC + AssetSigner.Mint, no Figma calls); a parallel
      // Promise.all is cheap. Switching to a batch endpoint is a future
      // optimisation if mint cost ever becomes a hot spot.
      const results = await Promise.all(
        todo.map(async (nid) => {
          try {
            const res = await fetch("/api/figma/frame-png-token", {
              method: "POST",
              headers: {
                "Content-Type": "application/json",
                Authorization: `Bearer ${token}`,
              },
              body: JSON.stringify({
                file_key: fileKey,
                node_id: nid,
                scale,
              }),
            });
            const body = (await res.json().catch(() => ({}))) as MintResponse;
            if (!res.ok || !body.url) return [nid, ""] as const;
            // body.url is "/v1/figma/frame-png?file_key=…&node_id=…&scale=…&tenant=…&at=…".
            // <FrameThumbnail> wants only the (tenant + at) pair; the
            // component reattaches file_key/node_id/scale via the props
            // it already holds, so a URL mismatch is impossible.
            const qIdx = body.url.indexOf("?");
            if (qIdx < 0) return [nid, ""] as const;
            const params = new URLSearchParams(body.url.slice(qIdx + 1));
            const tenant = params.get("tenant") ?? "";
            const at = params.get("at") ?? "";
            if (!at) return [nid, ""] as const;
            const qs = tenant
              ? `tenant=${encodeURIComponent(tenant)}&at=${encodeURIComponent(at)}`
              : `at=${encodeURIComponent(at)}`;
            return [nid, qs] as const;
          } catch {
            return [nid, ""] as const;
          } finally {
            inflight.current.delete(`${fileKey}|${nid}|${scale}`);
          }
        }),
      );

      if (cancelled) return;
      setTokensByNode((prev) => {
        const next = { ...prev };
        for (const [nid, qs] of results) {
          if (qs) next[nid] = qs;
        }
        return next;
      });
      setLoading(false);
    })();

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token, fileKey, keyList, scale]);

  const tokenQSFor = (nodeID: string): string => tokensByNode[nodeID] ?? "";

  return { tokenQSFor, loading };
}
