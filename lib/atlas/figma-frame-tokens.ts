"use client";

/**
 * lib/atlas/figma-frame-tokens.ts — Atlas-relocated version of the
 * useFrameThumbTokens hook previously living at
 * app/prd/[subProduct]/[subFlow]/useFrameThumbToken.ts.
 *
 * Plan 005 U2 moves the token-mint logic into lib/atlas/ so both the new
 * PRDTab (inside leafcanvas.tsx) and the future Wall mode (U7) can share
 * one implementation. The behaviour is preserved verbatim — token economics,
 * cap on per-flush mints, in-flight dedupe, and the (tenant, at) query-string
 * shape all match the original hook the /prd viewer shipped.
 *
 * Public shape:
 *   const { tokenQSFor, loading } = useFrameThumbTokens(fileKey, nodeIDs);
 *   tokenQSFor(nodeID) → "tenant=…&at=…" | "" (empty until minted)
 *
 * The empty-string fallback lets <FrameThumbnail> render its placeholder
 * glyph immediately, then upgrade to the real PNG when the token resolves.
 *
 * Also exports a useFrameThumbToken (singular) convenience wrapper used by
 * the FrameThumbnail single-node call site inside PRDStateCard.
 */

import { useEffect, useMemo, useRef, useState } from "react";

import { useAuth } from "@/lib/auth-client";

interface MintResponse {
  url?: string;
  expires_in?: number;
  error?: string;
  detail?: string;
}

const MAX_MINTS_PER_FLUSH = 100;

export function useFrameThumbTokens(
  fileKey: string | undefined,
  nodeIDs: string[],
  scale: 1 | 2 = 1,
) {
  const token = useAuth((s) => s.token);
  const [tokensByNode, setTokensByNode] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(false);

  // Track in-flight mints across renders so a re-render with the same set
  // of node ids doesn't re-issue the POST. The ref persists without
  // re-triggering the dispatch effect.
  const inflight = useRef<Set<string>>(new Set());

  // Stable join key for the deps array — sorts so a re-ordered nodeIDs
  // array doesn't churn the effect.
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
            // body.url shape: "/v1/figma/frame-png?file_key=…&node_id=…&scale=…&tenant=…&at=…".
            // <FrameThumbnail> wants only the (tenant + at) pair.
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

/**
 * Convenience single-node hook used by call sites that only need one PNG
 * (PRDStateCard frame thumbnails). Wraps useFrameThumbTokens with a
 * memoised one-item array so the dependency stays stable.
 */
export function useFrameThumbToken(
  fileKey: string | undefined,
  nodeID: string | undefined,
  scale: 1 | 2 = 1,
): string {
  const ids = useMemo(() => (nodeID ? [nodeID] : []), [nodeID]);
  const { tokenQSFor } = useFrameThumbTokens(fileKey, ids, scale);
  return nodeID ? tokenQSFor(nodeID) : "";
}
