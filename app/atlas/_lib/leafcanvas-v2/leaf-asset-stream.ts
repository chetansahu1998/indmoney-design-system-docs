/**
 * leaf-asset-stream.ts — module-level singleton that drives one
 * SSE-per-leaf to hydrate cluster `<img>` URLs progressively.
 *
 * Why a singleton: useIconClusterURLs is called per-frame inside
 * LeafFrameRenderer. A leaf with 80 frames would otherwise open 80
 * EventSource connections — far past Chrome's HTTP/1.1 6-conn cap and
 * pointless duplicate work server-side. We hoist the stream up: the
 * first subscriber for a (slug, leafID) triggers ticket+stream
 * acquisition; later subscribers (other frames in the same leaf) attach
 * to the existing store and get the same Map.
 *
 * Lifecycle:
 *   1. subscribe(slug, leafID, callback) — returns current Map<id,url>
 *      + registers callback for future updates. First subscribe per leaf
 *      kicks off acquireStream().
 *   2. acquireStream() — POSTs /asset-stream/ticket, opens an
 *      EventSource on the GET stream, parses asset-ready / asset-failed /
 *      complete events into the Map.
 *   3. Last unsubscribe closes the EventSource (with a 5s debounce so
 *      transient unmount/remount doesn't churn).
 *
 * Failure modes (fall through to caller's mint pass):
 *   - ticket POST fails (404, 401, 500): mark stream as `failed`,
 *     callbacks fire once with current Map (likely empty), subscribers
 *     trigger their existing Promise.all path.
 *   - EventSource onerror after open: same — mark failed, notify.
 *   - `complete` event: stream's done, but callers can still mint any
 *     IDs not seen as a residual pass.
 */

import { getToken } from "@/lib/auth-client";

interface AssetReadyEvent {
  node_id: string;
  format: string;
  url: string;
}

interface AssetFailedEvent {
  node_id: string;
  reason: string;
}

interface AssetCompleteEvent {
  total: number;
  rendered: number;
  failed: number;
}

/** State per (slug, leafID) singleton entry. */
interface LeafStreamState {
  slug: string;
  leafID: string;
  /** Map<nodeID, signed-URL>. Mutated as events arrive. */
  urls: Map<string, string>;
  /** Set<nodeID> for which an asset-failed event arrived. Frontend uses
   * this to skip retry attempts on the residual mint pass. */
  failedIDs: Set<string>;
  /** Subscribers fire whenever urls/failedIDs/status changes. */
  subscribers: Set<() => void>;
  /** Status drives waitForCompletion + UI gating. */
  status: "idle" | "opening" | "open" | "complete" | "failed";
  /** EventSource handle so unsubscribe can close. */
  source: EventSource | null;
  /** Pending close timer so transient unmount/remount doesn't tear down
   * an in-flight stream and re-open it 50ms later. */
  closeTimer: ReturnType<typeof setTimeout> | null;
  /** Resolves on `complete` or `failed` for await-style consumers. */
  donePromise: Promise<void>;
  resolveDone: () => void;
}

const KEY_SEP = "::";
const streams = new Map<string, LeafStreamState>();

/** Debounce window for close-on-last-unsubscribe. */
const CLOSE_DEBOUNCE_MS = 5000;

function streamKey(slug: string, leafID: string): string {
  return slug + KEY_SEP + leafID;
}

/** Shape returned to callers. Read-only Map snapshot is intentional —
 * subscribers should treat the urls and failedIDs as immutable per-tick. */
export interface LeafAssetSnapshot {
  urls: ReadonlyMap<string, string>;
  failedIDs: ReadonlySet<string>;
  status: LeafStreamState["status"];
}

/** Subscribe to leaf-level cluster URLs. Returns the current snapshot
 * plus an `unsubscribe` thunk the caller MUST invoke on cleanup.
 *
 * Multiple subscribers for the same leaf share state — the first opens
 * the stream, the rest free-ride. */
export function subscribeLeafAssets(
  slug: string,
  leafID: string,
  onUpdate: () => void,
): { snapshot: () => LeafAssetSnapshot; unsubscribe: () => void } {
  const key = streamKey(slug, leafID);
  let state = streams.get(key);
  if (!state) {
    state = createState(slug, leafID);
    streams.set(key, state);
  }
  // Cancel any pending close — a fresh subscriber means we're not idle.
  if (state.closeTimer) {
    clearTimeout(state.closeTimer);
    state.closeTimer = null;
  }
  state.subscribers.add(onUpdate);
  if (state.status === "idle") {
    void acquireStream(state);
  }

  const ourState = state;
  return {
    snapshot: () => ({
      urls: ourState.urls,
      failedIDs: ourState.failedIDs,
      status: ourState.status,
    }),
    unsubscribe: () => {
      ourState.subscribers.delete(onUpdate);
      if (ourState.subscribers.size === 0) {
        scheduleClose(ourState);
      }
    },
  };
}

/** Resolve when the stream emits `complete` or transitions to `failed`.
 * Used by the residual-mint fallback in useIconClusterURLs to know when
 * it's safe to mint anything not yet seen. */
export function waitForLeafStreamSettled(slug: string, leafID: string): Promise<void> {
  const state = streams.get(streamKey(slug, leafID));
  if (!state) return Promise.resolve();
  if (state.status === "complete" || state.status === "failed") {
    return Promise.resolve();
  }
  return state.donePromise;
}

function createState(slug: string, leafID: string): LeafStreamState {
  let resolveDone!: () => void;
  const donePromise = new Promise<void>((resolve) => {
    resolveDone = resolve;
  });
  return {
    slug,
    leafID,
    urls: new Map(),
    failedIDs: new Set(),
    subscribers: new Set(),
    status: "idle",
    source: null,
    closeTimer: null,
    donePromise,
    resolveDone,
  };
}

function scheduleClose(state: LeafStreamState): void {
  if (state.closeTimer) return;
  state.closeTimer = setTimeout(() => {
    state.closeTimer = null;
    if (state.subscribers.size > 0) return; // raced — new subscriber
    closeAndDispose(state);
  }, CLOSE_DEBOUNCE_MS);
}

function closeAndDispose(state: LeafStreamState): void {
  if (state.source) {
    state.source.close();
    state.source = null;
  }
  // Drop from the registry so a future subscriber gets a clean slate
  // (fresh tickets, fresh stream). We intentionally do NOT preserve the
  // urls Map across close — token TTL is 60min but a project export in
  // the meantime would invalidate cached URLs anyway.
  streams.delete(streamKey(state.slug, state.leafID));
}

function notify(state: LeafStreamState): void {
  for (const cb of state.subscribers) {
    try {
      cb();
    } catch {
      // Subscriber threw — don't let one bad listener tank the rest.
    }
  }
}

function setStatus(state: LeafStreamState, next: LeafStreamState["status"]): void {
  if (state.status === next) return;
  state.status = next;
  if (next === "complete" || next === "failed") {
    state.resolveDone();
  }
  notify(state);
}

async function acquireStream(state: LeafStreamState): Promise<void> {
  setStatus(state, "opening");
  const dsURL = process.env.NEXT_PUBLIC_DS_SERVICE_URL || "";
  const token = getToken();
  if (!token) {
    // Without a JWT we can't even mint a ticket. Fall through to failed
    // so subscribers don't wait on a stream that'll never open.
    setStatus(state, "failed");
    return;
  }
  let ticket = "";
  try {
    const res = await fetch(
      `${dsURL}/v1/projects/${encodeURIComponent(state.slug)}/leaves/${encodeURIComponent(state.leafID)}/asset-stream/ticket`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: "{}",
      },
    );
    if (!res.ok) {
      // 404 here is the most likely shape on older deploys — endpoint not
      // wired. Don't spam console with 404 stack traces.
      if (res.status !== 404) {
        // eslint-disable-next-line no-console
        console.warn(`[leaf-asset-stream] ticket failed: HTTP ${res.status}`);
      }
      setStatus(state, "failed");
      return;
    }
    const body = (await res.json()) as { ticket?: string };
    ticket = body.ticket ?? "";
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn("[leaf-asset-stream] ticket fetch error:", err);
    setStatus(state, "failed");
    return;
  }
  if (!ticket) {
    setStatus(state, "failed");
    return;
  }
  // EventSource follows the same-origin policy; dsURL may be a different
  // origin in dev, so ensure CORS is configured server-side. (The
  // existing /v1/projects/graph/events endpoint already proves this works
  // in this deployment.)
  const streamURL = `${dsURL}/v1/projects/${encodeURIComponent(state.slug)}/leaves/${encodeURIComponent(state.leafID)}/asset-stream?ticket=${encodeURIComponent(ticket)}`;
  let source: EventSource;
  try {
    source = new EventSource(streamURL);
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn("[leaf-asset-stream] EventSource construct error:", err);
    setStatus(state, "failed");
    return;
  }
  state.source = source;
  setStatus(state, "open");

  source.addEventListener("asset-ready", (ev) => {
    try {
      const data = JSON.parse((ev as MessageEvent).data) as AssetReadyEvent;
      if (data.node_id && data.url) {
        state.urls.set(data.node_id, data.url);
        notify(state);
      }
    } catch {
      // Malformed event — skip.
    }
  });

  source.addEventListener("asset-failed", (ev) => {
    try {
      const data = JSON.parse((ev as MessageEvent).data) as AssetFailedEvent;
      if (data.node_id) {
        state.failedIDs.add(data.node_id);
        notify(state);
      }
    } catch {
      // Skip.
    }
  });

  source.addEventListener("complete", (ev) => {
    try {
      JSON.parse((ev as MessageEvent).data) as AssetCompleteEvent;
    } catch {
      // Don't care about malformed complete payload — the event itself
      // is the signal.
    }
    setStatus(state, "complete");
    // Server closes after `complete`; we close client-side too so the
    // browser stops holding the connection.
    if (state.source) {
      state.source.close();
      state.source = null;
    }
  });

  source.onerror = () => {
    // EventSource auto-reconnects on transient errors. We treat error AS
    // failure only when the readyState is CLOSED (Chrome reports this on
    // server-initiated close after `complete`, which we already
    // handled — so a subsequent CLOSED is benign). Mark failed only when
    // we haven't seen `complete`.
    if (source.readyState === EventSource.CLOSED && state.status !== "complete") {
      setStatus(state, "failed");
    }
  };
}

/** Test helper: clear all in-flight streams and drop the registry. NOT
 * for production code paths. */
export function __resetLeafAssetStreamForTests(): void {
  for (const state of streams.values()) {
    if (state.source) state.source.close();
    if (state.closeTimer) clearTimeout(state.closeTimer);
  }
  streams.clear();
}
