"use client";

/**
 * Phase 8 U10 — debounced server-side search hook.
 *
 * Calls GET /v1/search?q=…&scope=… on the ds-service. Debounce is 250ms;
 * inflight requests for stale queries are aborted so the UI never flickers
 * to a stale result set when the user keeps typing.
 *
 * Usage:
 *   const { results, status } = useSearch(query, { scope: "all" });
 */

import { useEffect, useRef, useState } from "react";

import { getToken } from "@/lib/auth-client";

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL ?? "http://localhost:8080";
}

export interface SearchHit {
  kind: "flow" | "drd" | "decision" | "persona" | "component";
  id: string;
  title: string;
  snippet: string;
  open_url?: string;
  score: number;
}

export type SearchStatus = "idle" | "loading" | "ready" | "error";

interface UseSearchOpts {
  scope?: "all" | "mind-graph";
  limit?: number;
  /** Override debounce (ms). Default 250. */
  debounceMs?: number;
}

export function useSearch(query: string, opts: UseSearchOpts = {}) {
  const [results, setResults] = useState<SearchHit[]>([]);
  const [status, setStatus] = useState<SearchStatus>("idle");
  const [error, setError] = useState<string | null>(null);
  const inflightRef = useRef<AbortController | null>(null);

  const trimmed = query.trim();
  const debounceMs = opts.debounceMs ?? 250;
  const scope = opts.scope ?? "all";
  const limit = opts.limit ?? 20;

  useEffect(() => {
    if (trimmed === "") {
      setResults([]);
      setStatus("idle");
      setError(null);
      return;
    }
    setStatus("loading");
    const handle = window.setTimeout(() => {
      // Abort any inflight request — we only care about the latest query.
      inflightRef.current?.abort();
      const ac = new AbortController();
      inflightRef.current = ac;

      const url = `${dsBaseURL()}/v1/search?q=${encodeURIComponent(trimmed)}&scope=${encodeURIComponent(
        scope,
      )}&limit=${limit}`;
      const headers: Record<string, string> = { Accept: "application/json" };
      const token = getToken();
      if (token) headers.Authorization = `Bearer ${token}`;

      fetch(url, { signal: ac.signal, headers })
        .then(async (res) => {
          if (!res.ok) throw new Error(`HTTP ${res.status}`);
          return (await res.json()) as { results: SearchHit[] };
        })
        .then((body) => {
          if (ac.signal.aborted) return;
          setResults(body.results ?? []);
          setStatus("ready");
          setError(null);
        })
        .catch((err: unknown) => {
          if (err instanceof DOMException && err.name === "AbortError") return;
          setError(err instanceof Error ? err.message : String(err));
          setStatus("error");
        });
    }, debounceMs);
    return () => window.clearTimeout(handle);
  }, [trimmed, scope, limit, debounceMs]);

  return { results, status, error };
}
