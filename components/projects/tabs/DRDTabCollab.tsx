"use client";

/**
 * DRDTabCollab — Phase 5.1 P1.
 *
 * Yjs-backed collaborative DRD editor. Mounted by DRDTab when
 * NEXT_PUBLIC_DRD_COLLAB === "1" (default false during the cutover).
 * Falls back gracefully: if the ticket mint OR provider connection
 * fails, surfaces the error inline + offers a reload — single-author
 * REST mode remains available via the dispatcher.
 *
 * Lifecycle:
 *   1. Mount → POST /v1/projects/:slug/flows/:flow_id/drd/ticket
 *   2. Ticket → HocuspocusProvider on ws://.../<flow_id>?token=<ticket>
 *   3. Provider syncs Y.Doc; we hand the Y.XmlFragment to BlockNote via
 *      its `collaboration` option.
 *   4. Edits propagate via the provider; the sidecar persists snapshots
 *      every 30s of idle and on last-disconnect (Phase 5 U1).
 *   5. Read-only mode passes through to BlockNote's editable=false.
 *
 * The editor mounts only after the provider's first sync so we never
 * render a half-empty document; the loading state has the same EmptyTab
 * shell DRDTab uses elsewhere.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { BlockNoteSchema } from "@blocknote/core";
import { useCreateBlockNote } from "@blocknote/react";
import { BlockNoteView } from "@blocknote/mantine";
import "@blocknote/core/fonts/inter.css";
import "@blocknote/mantine/style.css";
import * as Y from "yjs";
import {
  createDRDProvider,
  mintDRDTicket,
  userColor,
  type DRDCollabBundle,
} from "@/lib/drd/collab";
import { drdBlockSpecs } from "@/lib/drd/customBlocks";
import { useReducedMotion } from "@/lib/animations/context";
import EmptyTab from "./EmptyTab";
import ActivityRail from "@/components/drd/ActivityRail";

// Phase 5.1 P2 — schema with custom blocks (decisionRef, figmaLink,
// violationRef). Defaults preserved so paragraph/heading/list/etc.
// continue to work. Created at module scope so the schema reference
// is stable across renders.
const drdSchema = BlockNoteSchema.create({ blockSpecs: drdBlockSpecs });

interface Props {
  slug: string;
  flowID: string;
  readOnly?: boolean;
}

type ConnState =
  | { kind: "minting" }
  | { kind: "connecting" }
  | { kind: "synced" }
  | { kind: "error"; message: string }
  | { kind: "auth_failed" };

export default function DRDTabCollab({ slug, flowID, readOnly = false }: Props) {
  const [conn, setConn] = useState<ConnState>({ kind: "minting" });
  const bundleRef = useRef<DRDCollabBundle | null>(null);
  const [user, setUser] = useState<{ id: string; name: string; color: string } | null>(null);

  // Mount the provider once per (slug, flowID). Re-mount when either
  // changes; destroy on unmount.
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const ticket = await mintDRDTicket(slug, flowID);
      if (cancelled) return;
      if (!ticket.ok) {
        setConn({
          kind: "error",
          message: `${ticket.error} (status ${ticket.status})`,
        });
        return;
      }
      setUser({
        id: ticket.data.user_id,
        name: ticket.data.user_id.slice(0, 8),
        color: userColor(ticket.data.user_id),
      });
      setConn({ kind: "connecting" });
      const bundle = createDRDProvider({
        flowID: ticket.data.flow_id,
        ticket: ticket.data.ticket,
        user: { id: ticket.data.user_id },
        onAuthFailure: () => {
          if (!cancelled) setConn({ kind: "auth_failed" });
        },
        onSync: (synced) => {
          if (!cancelled && synced) setConn({ kind: "synced" });
        },
      });
      bundleRef.current = bundle;
    })();
    return () => {
      cancelled = true;
      bundleRef.current?.destroy();
      bundleRef.current = null;
    };
  }, [slug, flowID]);

  // Editor only mounts after first sync so BlockNote sees the bootstrap
  // state. Mount with the YjsFragment as a single shared document.
  if (conn.kind === "minting") {
    return <EmptyTab title="Connecting" description="Minting collaboration ticket…" />;
  }
  if (conn.kind === "connecting") {
    return <EmptyTab title="Connecting" description="Joining collaboration session…" />;
  }
  if (conn.kind === "auth_failed") {
    return (
      <EmptyTab
        title="Connection refused"
        description="Your collaboration ticket expired or was rejected. Reload the page to re-authenticate."
      />
    );
  }
  if (conn.kind === "error") {
    return <EmptyTab title="Couldn't open DRD" description={conn.message} />;
  }
  if (!bundleRef.current || !user) {
    return <EmptyTab title="Initialising" description="Spinning up the editor…" />;
  }

  return (
    <CollabEditor
      slug={slug}
      flowID={flowID}
      readOnly={readOnly}
      bundle={bundleRef.current}
      user={user}
    />
  );
}

interface CollabEditorProps {
  slug: string;
  flowID: string;
  readOnly: boolean;
  bundle: DRDCollabBundle;
  user: { id: string; name: string; color: string };
}

/**
 * CollabEditor mounts BlockNote with the Y.Doc fragment + provider's
 * awareness handle. Split out so the Yjs fragment + user identity are
 * stable across re-renders within a single connected session.
 */
function CollabEditor({ slug, flowID, readOnly, bundle, user }: CollabEditorProps) {
  // BlockNote consumes a Y.XmlFragment as the shared root. The fragment
  // name is arbitrary but must match across all peers; "blocknote" is
  // the convention BlockNote's docs use.
  const fragment = useMemo(
    () => bundle.doc.getXmlFragment("blocknote"),
    [bundle.doc],
  );

  // Phase 5.1 P3 — respect reduced-motion: when the user has the OS
  // setting on, BlockNote's "activity" cursor-label mode is too jumpy
  // (labels appear + fade on each remote keystroke). Switch to "always"
  // so labels are static — no animation, but presence is still legible.
  const reducedMotion = useReducedMotion();
  const editor = useCreateBlockNote({
    schema: drdSchema,
    collaboration: {
      fragment,
      user: { name: user.name, color: user.color },
      provider: { awareness: bundle.provider.awareness ?? undefined },
      showCursorLabels: reducedMotion ? "always" : "activity",
    },
  });

  return (
    <div data-anim="tab-content" style={containerStyle}>
      {readOnly && (
        <div role="status" style={readOnlyBannerStyle}>
          <span aria-hidden style={readOnlyIconStyle}>🔒</span>
          <div style={readOnlyTextStyle}>
            <strong style={readOnlyTitleStyle}>Read-only access</strong>
            <span style={readOnlySubtitleStyle}>
              You're viewing this in read-only mode. Edits don't propagate.
            </span>
          </div>
        </div>
      )}
      <header style={headerStyle}>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-3)" }}>
          DRD · live collab
        </span>
        <span style={{ flex: 1 }} />
        <PresenceBadge bundle={bundle} self={user.id} />
      </header>
      <div style={drdRowStyle}>
        <div style={editorWrapperStyle}>
          <BlockNoteView editor={editor} editable={!readOnly} />
        </div>
        <ActivityRail slug={slug} flowID={flowID} />
      </div>
    </div>
  );
}

/**
 * PresenceBadge — Phase 5.1 P3 stub. Renders the count of remote peers
 * currently in the document. Awareness state shape is `{ user: {...} }`
 * for each clientID; we count entries excluding our own clientID.
 */
function PresenceBadge({ bundle, self }: { bundle: DRDCollabBundle; self: string }) {
  const [peers, setPeers] = useState<Array<{ id: string; name: string; color: string }>>([]);
  useEffect(() => {
    const aw = bundle.provider.awareness;
    if (!aw) return;
    const tick = () => {
      const list: Array<{ id: string; name: string; color: string }> = [];
      aw.getStates().forEach((state, clientID) => {
        if (clientID === aw.clientID) return;
        const u = (state as { user?: { id: string; name: string; color: string } }).user;
        if (u && u.id !== self) list.push(u);
      });
      setPeers(list);
    };
    tick();
    aw.on("change", tick);
    return () => {
      aw.off("change", tick);
    };
  }, [bundle, self]);
  if (peers.length === 0) {
    return (
      <span
        style={{
          fontSize: 11,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
        }}
      >
        only you
      </span>
    );
  }
  return (
    <div
      data-testid="presence-badge"
      style={{ display: "flex", alignItems: "center", gap: 6 }}
    >
      {peers.slice(0, 5).map((p) => (
        <span
          key={p.id}
          title={p.name}
          aria-label={`${p.name} is editing`}
          style={{
            width: 18,
            height: 18,
            borderRadius: 999,
            background: p.color,
            color: "#fff",
            fontSize: 9,
            fontFamily: "var(--font-mono)",
            display: "grid",
            placeItems: "center",
            fontWeight: 600,
          }}
        >
          {p.name.slice(0, 2).toUpperCase()}
        </span>
      ))}
      {peers.length > 5 && (
        <span
          style={{
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          +{peers.length - 5}
        </span>
      )}
    </div>
  );
}

const containerStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 8,
  height: "100%",
  minHeight: 0,
};
const headerStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 12,
  padding: "8px 0",
  borderBottom: "1px solid var(--border)",
};
const drdRowStyle: React.CSSProperties = {
  display: "flex",
  gap: 16,
  alignItems: "flex-start",
  flex: 1,
  minHeight: 0,
};
const editorWrapperStyle: React.CSSProperties = {
  flex: 1,
  minWidth: 0,
  border: "1px solid var(--border)",
  borderRadius: 8,
  background: "var(--bg-base, #fff)",
  overflow: "auto",
};
const readOnlyBannerStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 12,
  padding: "10px 14px",
  background: "color-mix(in oklab, var(--bg-surface) 85%, var(--warning, #c80) 15%)",
  borderBottom: "1px solid var(--border)",
};
const readOnlyIconStyle: React.CSSProperties = { fontSize: 16 };
const readOnlyTextStyle: React.CSSProperties = { display: "flex", flexDirection: "column" };
const readOnlyTitleStyle: React.CSSProperties = { fontSize: 12 };
const readOnlySubtitleStyle: React.CSSProperties = { fontSize: 11, color: "var(--text-3)" };
