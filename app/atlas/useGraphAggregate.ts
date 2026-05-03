"use client";

/**
 * Phase 6 — Mind-graph aggregate fetcher + SSE bust subscriber.
 *
 * Returns the latest GraphAggregate for (tenant, platform). Mounts an
 * EventSource on graph:<tenant>:<platform> and re-fetches on every
 * GraphIndexUpdated event the rebuild worker emits. The `cache_key` field
 * lets us skip no-op busts.
 */

import { useEffect, useRef, useState } from "react";

import { getToken } from "@/lib/auth-client";
import type {
  GraphAggregate,
  GraphIndexUpdatedEvent,
  GraphPlatform,
} from "./types";

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL || "http://localhost:8080";
}

interface TicketResponse {
  ticket: string;
  trace_id: string;
  platform: GraphPlatform;
  expires_in: number;
}

export interface GraphAggregateState {
  status: "loading" | "ready" | "error" | "empty";
  data: GraphAggregate | null;
  error: string | null;
  /** Re-fetch on demand (e.g., debug button or test hook). */
  refetch: () => void;
}

const EMPTY_AGGREGATE: GraphAggregate = {
  nodes: [],
  edges: [],
  generated_at: "",
  platform: "mobile",
  cache_key: "",
};

/**
 * useGraphAggregate fetches the wire-shape JSON, then keeps it fresh by
 * subscribing to graph:<tenant>:<platform> SSE busts. Returns the latest
 * snapshot plus a refetch handle.
 *
 * Reconnect strategy: on EventSource error we close + retry after 2s with
 * a fresh ticket (single-use). Backoff capped at 30s. Browser tab idle
 * suspends rAF and the EventSource together; resuming triggers a refetch.
 */
export function useGraphAggregate(platform: GraphPlatform): GraphAggregateState {
  const [data, setData] = useState<GraphAggregate | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<GraphAggregateState["status"]>("loading");
  const lastCacheKeyRef = useRef<string>("");
  const lastETagRef = useRef<string>("");

  const fetchAggregate = async (signal?: AbortSignal): Promise<void> => {
    try {
      const token = getToken();
      const headers: Record<string, string> = { Accept: "application/json" };
      if (token) headers.Authorization = `Bearer ${token}`;
      // ETag-aware re-fetch: we send If-None-Match to short-circuit no-op
      // busts. Server returns 304 with empty body when unchanged.
      if (lastETagRef.current) {
        headers["If-None-Match"] = lastETagRef.current;
      }
      const res = await fetch(
        `${dsBaseURL()}/v1/projects/graph?platform=${encodeURIComponent(platform)}`,
        { method: "GET", headers, signal },
      );
      if (res.status === 304) {
        // Aggregate unchanged — keep current data + status.
        return;
      }
      if (!res.ok) {
        const detail = await safeErrText(res);
        setError(detail);
        setStatus("error");
        return;
      }
      const etag = res.headers.get("ETag");
      if (etag) lastETagRef.current = etag;
      const next = (await res.json()) as GraphAggregate;
      lastCacheKeyRef.current = next.cache_key ?? "";
      setData(next);
      setStatus(next.nodes.length === 0 ? "empty" : "ready");
      setError(null);
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") return;
      setError(err instanceof Error ? err.message : String(err));
      setStatus("error");
    }
  };

  // Initial fetch + platform change refetch.
  useEffect(() => {
    setStatus("loading");
    setData(null);
    lastCacheKeyRef.current = "";
    lastETagRef.current = "";
    const ac = new AbortController();
    void fetchAggregate(ac.signal);
    return () => ac.abort();
    // fetchAggregate is stable enough that we don't need to memoize; the
    // platform change is the only re-trigger we want.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [platform]);

  // SSE subscription. Reconnects with backoff on error, capped at
  // MAX_ATTEMPTS so a misconfigured deployment (e.g. ds-service
  // unreachable; Chrome's PNA blocking loopback from a public origin)
  // doesn't loop forever — the page will surface its error state and
  // stop spamming the network tab. The aggregate fetch's status field
  // is what the UI reads; SSE failures here only affect live updates,
  // not the initial render.
  useEffect(() => {
    let cancelled = false;
    let es: EventSource | null = null;
    let backoffMs = 2000;
    let attempts = 0;
    const MAX_ATTEMPTS = 6; // 2 → 4 → 8 → 16 → 30 → 30 ≈ 90s total before giving up

    async function mintAndOpen(): Promise<void> {
      if (cancelled) return;
      if (attempts >= MAX_ATTEMPTS) {
        // Bail. The aggregate REST fetch's error state already tells
        // the user the backend is unreachable; no point hammering the
        // SSE endpoint on top.
        return;
      }
      attempts += 1;
      try {
        const token = getToken();
        const headers: Record<string, string> = {
          Accept: "application/json",
          "Content-Type": "application/json",
        };
        if (token) headers.Authorization = `Bearer ${token}`;
        const tres = await fetch(
          `${dsBaseURL()}/v1/projects/graph/events/ticket?platform=${encodeURIComponent(platform)}`,
          { method: "POST", headers, body: JSON.stringify({}) },
        );
        if (!tres.ok) {
          throw new Error(`ticket ${tres.status}`);
        }
        const tBody = (await tres.json()) as TicketResponse;
        if (cancelled) return;
        const url = `${dsBaseURL()}/v1/projects/graph/events?ticket=${encodeURIComponent(
          tBody.ticket,
        )}`;
        es = new EventSource(url);
        es.addEventListener("graph.index_updated", (raw) => {
          const ev = raw as MessageEvent<string>;
          try {
            const payload = JSON.parse(ev.data) as GraphIndexUpdatedEvent;
            if (payload.materialized_at && payload.materialized_at !== lastCacheKeyRef.current) {
              void fetchAggregate();
            }
          } catch {
            // ignore malformed SSE payloads
          }
        });
        es.onerror = () => {
          es?.close();
          es = null;
          if (cancelled) return;
          window.setTimeout(() => void mintAndOpen(), backoffMs);
          backoffMs = Math.min(backoffMs * 2, 30_000);
        };
        es.onopen = () => {
          // Successful open — reset the attempt counter so a transient
          // disconnect later doesn't count against the cap.
          attempts = 0;
          backoffMs = 2000;
        };
      } catch {
        if (cancelled) return;
        window.setTimeout(() => void mintAndOpen(), backoffMs);
        backoffMs = Math.min(backoffMs * 2, 30_000);
      }
    }

    void mintAndOpen();
    return () => {
      cancelled = true;
      es?.close();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [platform]);

  return {
    status,
    data: data ?? EMPTY_AGGREGATE,
    error,
    refetch: () => void fetchAggregate(),
  };
}

async function safeErrText(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: string; detail?: string };
    return body.detail ?? body.error ?? `HTTP ${res.status}`;
  } catch {
    return `HTTP ${res.status}`;
  }
}
