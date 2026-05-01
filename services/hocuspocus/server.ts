/**
 * services/hocuspocus/server.ts — Phase 5 U1.
 *
 * The Hocuspocus sidecar bridges DRD collaboration to ds-service. Each
 * connecting client passes a `?ticket=` query string parameter on the
 * WebSocket handshake; we forward to ds-service's /internal/drd/auth
 * endpoint to redeem the ticket + recover the user/tenant/flow context.
 *
 * Lifecycle hooks:
 *   onAuthenticate — redeem ticket, store user/tenant/flow on the
 *                    connection context.
 *   onLoadDocument — GET /internal/drd/load to bootstrap the Y.Doc.
 *   onChange       — debounced 30s; POST /internal/drd/snapshot.
 *   onDisconnect   — when the last peer leaves a doc, snapshot
 *                    immediately so an empty doc isn't lost.
 *
 * Auth between this sidecar and ds-service uses a shared secret env
 * (DS_HOCUSPOCUS_SHARED_SECRET) sent as `X-DS-Hocuspocus-Secret` on
 * every internal call. The sidecar listens on the configured port
 * (default 7676) on a private interface.
 */

import { Server } from "@hocuspocus/server";
import * as Y from "yjs";

const HOCUSPOCUS_PORT = Number(process.env.HOCUSPOCUS_PORT ?? "7676");
const DS_SERVICE_URL = process.env.DS_SERVICE_URL ?? "http://127.0.0.1:7475";
const SHARED_SECRET = process.env.DS_HOCUSPOCUS_SHARED_SECRET ?? "";
const SNAPSHOT_DEBOUNCE_MS = Number(process.env.HOCUSPOCUS_SNAPSHOT_DEBOUNCE_MS ?? "30000");

if (!SHARED_SECRET) {
  console.warn("[hocuspocus] DS_HOCUSPOCUS_SHARED_SECRET not set — auth bridge will fail");
}

interface DRDContext {
  userID: string;
  tenantID: string;
  flowID: string;
  role: string;
}

// Per-document debounce for snapshot persistence.
const snapshotTimers = new Map<string, NodeJS.Timeout>();

async function postSnapshot(flowID: string, ctx: DRDContext, state: Uint8Array, reason: "idle" | "disconnect"): Promise<void> {
  const url = `${DS_SERVICE_URL}/internal/drd/snapshot`;
  try {
    const res = await fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/octet-stream",
        "X-DS-Hocuspocus-Secret": SHARED_SECRET,
        "X-DS-Flow-ID": flowID,
        "X-DS-Tenant-ID": ctx.tenantID,
        "X-DS-User-ID": ctx.userID,
        "X-DS-Snapshot-Reason": reason,
      },
      body: state,
    });
    if (!res.ok) {
      console.error(`[hocuspocus] snapshot failed (HTTP ${res.status}) for flow=${flowID} reason=${reason}`);
      return;
    }
    const body = (await res.json()) as { revision?: number; bytes?: number };
    console.log(`[hocuspocus] snapshot ok flow=${flowID} reason=${reason} bytes=${body.bytes} rev=${body.revision}`);
  } catch (err) {
    console.error(`[hocuspocus] snapshot error flow=${flowID}:`, err);
  }
}

const server = new Server({
  port: HOCUSPOCUS_PORT,
  name: "indmoney-ds-drd",

  async onAuthenticate(data) {
    const { token, documentName } = data;
    // Convention: documentName == flow_id (the client opens the
    // socket via WebSocket(`/v1/drd/${flow_id}?ticket=...`)).
    if (!token || !documentName) {
      throw new Error("missing token or document name");
    }
    const url = `${DS_SERVICE_URL}/internal/drd/auth`;
    const res = await fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-DS-Hocuspocus-Secret": SHARED_SECRET,
      },
      body: JSON.stringify({ ticket: token, flow_id: documentName }),
    });
    if (!res.ok) {
      throw new Error(`auth failed (HTTP ${res.status})`);
    }
    const body = (await res.json()) as DRDContext;
    return body;
  },

  async onLoadDocument(data) {
    const ctx = data.context as DRDContext;
    const url = `${DS_SERVICE_URL}/internal/drd/load?flow_id=${encodeURIComponent(data.documentName)}&tenant_id=${encodeURIComponent(ctx.tenantID)}`;
    const res = await fetch(url, {
      headers: { "X-DS-Hocuspocus-Secret": SHARED_SECRET },
    });
    if (!res.ok) {
      console.warn(`[hocuspocus] load returned HTTP ${res.status}; starting empty Y.Doc`);
      return new Y.Doc();
    }
    const buf = new Uint8Array(await res.arrayBuffer());
    const doc = new Y.Doc();
    if (buf.byteLength > 0) {
      Y.applyUpdate(doc, buf);
    }
    return doc;
  },

  async onChange(data) {
    const ctx = data.context as DRDContext;
    if (!ctx) return;
    const flowID = data.documentName;
    // Debounce: clear any pending timer for this flow + schedule a fresh one.
    const existing = snapshotTimers.get(flowID);
    if (existing) clearTimeout(existing);
    snapshotTimers.set(
      flowID,
      setTimeout(() => {
        snapshotTimers.delete(flowID);
        const state = Y.encodeStateAsUpdate(data.document);
        void postSnapshot(flowID, ctx, state, "idle");
      }, SNAPSHOT_DEBOUNCE_MS),
    );
  },

  async onDisconnect(data) {
    // On last-peer disconnect, force an immediate snapshot so a peer
    // who refreshes doesn't lose work. Hocuspocus passes
    // `clientsCount` post-disconnect; flush only when zero.
    if (data.clientsCount > 0) return;
    const ctx = data.context as DRDContext;
    if (!ctx) return;
    const flowID = data.documentName;
    const existing = snapshotTimers.get(flowID);
    if (existing) {
      clearTimeout(existing);
      snapshotTimers.delete(flowID);
    }
    const state = Y.encodeStateAsUpdate(data.document);
    await postSnapshot(flowID, ctx, state, "disconnect");
  },
});

server
  .listen()
  .then(() => {
    console.log(`[hocuspocus] listening on :${HOCUSPOCUS_PORT}, bridging to ${DS_SERVICE_URL}`);
  })
  .catch((err) => {
    console.error("[hocuspocus] failed to start:", err);
    process.exit(1);
  });
