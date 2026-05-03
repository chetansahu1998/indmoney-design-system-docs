"use client";

/**
 * lib/drd/collab.ts — Phase 5.1 P1.
 *
 * Mints a single-use Hocuspocus ticket + builds a HocuspocusProvider
 * pointed at the sidecar's WebSocket endpoint. The provider's Y.Doc is
 * the same one BlockNote consumes via its collaboration extension.
 *
 * Concurrent editors land in the same Y.Doc; CRDT merge produces a
 * convergent state across all peers. Awareness states (cursors,
 * presence) ride alongside Y.Doc updates on the same socket.
 */

import { HocuspocusProvider } from "@hocuspocus/provider";
import * as Y from "yjs";
import { getToken } from "../auth-client";

export interface DRDTicketResponse {
  ticket: string;
  trace_id: string;
  flow_id: string;
  tenant_id: string;
  user_id: string;
  role: string;
  expires_in: number;
}

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL || "http://localhost:8080";
}

function hocuspocusURL(): string {
  // Default to ws://localhost:7676 for local dev; production reverse-
  // proxies the sidecar at /drd/ws or similar.
  return process.env.NEXT_PUBLIC_HOCUSPOCUS_URL ?? "ws://localhost:7676";
}

/**
 * mintDRDTicket calls POST /v1/projects/:slug/flows/:flow_id/drd/ticket
 * to receive a 60s single-use ticket bound to (user, tenant, flow).
 */
export async function mintDRDTicket(
  slug: string,
  flowID: string,
): Promise<{ ok: true; data: DRDTicketResponse } | { ok: false; error: string; status: number }> {
  try {
    const token = getToken();
    if (!token) return { ok: false, status: 401, error: "no auth token" };
    const res = await fetch(
      `${dsBaseURL()}/v1/projects/${encodeURIComponent(slug)}/flows/${encodeURIComponent(flowID)}/drd/ticket`,
      {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: "{}",
      },
    );
    if (!res.ok) {
      let msg = `HTTP ${res.status}`;
      try {
        const body = (await res.json()) as { error?: string; detail?: string };
        msg = body.detail ?? body.error ?? msg;
      } catch {}
      return { ok: false, status: res.status, error: msg };
    }
    const data = (await res.json()) as DRDTicketResponse;
    return { ok: true, data };
  } catch (err) {
    return {
      ok: false,
      status: 0,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

/**
 * createDRDProvider opens a Hocuspocus WebSocket bound to a flow. The
 * returned bundle exposes the underlying Y.Doc so BlockNote can be
 * configured with the collaboration extension. Caller must invoke
 * destroy() on unmount to close the socket cleanly.
 *
 * Reconnect: Hocuspocus auto-reconnects on transient disconnects, but
 * the ticket is single-use — on a stale-ticket reconnect the sidecar
 * will reject the handshake and the provider fires `onAuthFailure`.
 * Callers are expected to re-mint a ticket + build a fresh provider
 * when that happens; lib doesn't auto-loop.
 */
export interface DRDCollabBundle {
  provider: HocuspocusProvider;
  doc: Y.Doc;
  destroy: () => void;
}

export function createDRDProvider(args: {
  flowID: string;
  ticket: string;
  user: { id: string; name?: string; color?: string };
  onAuthFailure?: () => void;
  onSync?: (synced: boolean) => void;
}): DRDCollabBundle {
  const doc = new Y.Doc();
  const provider = new HocuspocusProvider({
    url: hocuspocusURL(),
    name: args.flowID,
    document: doc,
    token: args.ticket,
    onAuthenticationFailed: () => {
      args.onAuthFailure?.();
    },
    onSynced: () => {
      args.onSync?.(true);
    },
  });

  // Set the local awareness state so other peers see who we are.
  // Phase 5.1 P3 wires the cursor render path; this just publishes
  // the identity each peer broadcasts.
  provider.setAwarenessField("user", {
    id: args.user.id,
    name: args.user.name ?? args.user.id.slice(0, 8),
    color: args.user.color ?? userColor(args.user.id),
  });

  return {
    provider,
    doc,
    destroy: () => {
      provider.destroy();
      doc.destroy();
    },
  };
}

/**
 * userColor — deterministic 12-tint palette assignment by user_id hash.
 * Same algorithm the Phase 5 plan describes for cursor coloring; lifted
 * here so multiple components agree on the mapping.
 */
const PALETTE = [
  "#dc2626",
  "#ea580c",
  "#ca8a04",
  "#16a34a",
  "#0891b2",
  "#2563eb",
  "#7c3aed",
  "#c026d3",
  "#db2777",
  "#475569",
  "#65a30d",
  "#0d9488",
];

export function userColor(userID: string): string {
  let h = 0;
  for (let i = 0; i < userID.length; i++) {
    h = (h * 31 + userID.charCodeAt(i)) >>> 0;
  }
  return PALETTE[h % PALETTE.length];
}
