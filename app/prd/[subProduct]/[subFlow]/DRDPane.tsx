"use client";

/**
 * DRDPane — U3 follow-up.
 *
 * Replaces the v1 read-only placeholder (see PRDShell.tsx history) with the
 * full BlockNote + Hocuspocus collab editor, keyed on `sub_flow_slug`.
 *
 * Wire-up:
 *   1. mintDRDTicketForSubFlow → POST /api/prd/{sp}/{sf}/drd/ticket.
 *      Server resolves sub_flow → flow_id (bootstrapping the synthetic
 *      project/flow/flow_drd chain on first open) and mints a 60s
 *      single-use ticket.
 *   2. createDRDProvider({flowID, ticket}) opens the Hocuspocus WebSocket
 *      against the resolved flow_id. BlockNote consumes the same Y.Doc
 *      via its `collaboration` extension.
 *   3. Editor only mounts AFTER the provider fires `onSync` — prevents
 *      flicker + stale content paint on first connect (see past learning:
 *      phase-5-1-collab-polish in docs/solutions/).
 *
 * Lifecycle states (matches AtlasDRDEditor's four-state pattern):
 *   - "minting"    — fetching the ticket
 *   - "connecting" — provider opening; awaiting first sync
 *   - "synced"     — editable BlockNote mounted
 *   - "error"      — generic upstream failure (5xx, 404, network)
 *   - "auth_failed"— 401 from the proxy / sidecar; user should refresh
 *
 * On unmount: destroy provider + Y.Doc to close the socket cleanly.
 */

import "@blocknote/core/fonts/inter.css";
import { BlockNoteSchema } from "@blocknote/core";
import { useCreateBlockNote } from "@blocknote/react";
import { BlockNoteView } from "@blocknote/mantine";
import "@blocknote/mantine/style.css";
import { useEffect, useMemo, useRef, useState } from "react";

import { useAuth } from "@/lib/auth-client";
import {
  createDRDProvider,
  mintDRDTicketForSubFlow,
  userColor,
  type DRDCollabBundle,
} from "@/lib/drd/collab";
import { drdBlockSpecs } from "@/lib/drd/customBlocks";

interface Props {
  subProductSlug: string;
  subFlowSlug: string;
}

type ConnectionState =
  | { kind: "minting" }
  | { kind: "connecting" }
  | { kind: "synced" }
  | { kind: "error"; message: string }
  | { kind: "auth_failed" };

export function DRDPane({ subProductSlug, subFlowSlug }: Props) {
  const email = useAuth((s) => s.email);
  const [state, setState] = useState<ConnectionState>({ kind: "minting" });
  const bundleRef = useRef<DRDCollabBundle | null>(null);

  // ─── Mint ticket + open provider ─────────────────────────────────────
  useEffect(() => {
    let cancelled = false;
    setState({ kind: "minting" });

    void (async () => {
      const result = await mintDRDTicketForSubFlow(subProductSlug, subFlowSlug);
      if (cancelled) return;

      if (!result.ok) {
        setState(
          result.status === 401
            ? { kind: "auth_failed" }
            : { kind: "error", message: result.error },
        );
        return;
      }

      const userID = email || "anon";
      try {
        const bundle = createDRDProvider({
          flowID: result.data.flow_id,
          ticket: result.data.ticket,
          user: {
            id: userID,
            name: prettyName(email || userID),
            color: userColor(userID),
          },
          onAuthFailure: () => {
            if (!cancelled) setState({ kind: "auth_failed" });
          },
          onSync: () => {
            if (!cancelled) setState({ kind: "synced" });
          },
        });
        bundleRef.current = bundle;
        setState({ kind: "connecting" });
      } catch (err) {
        if (!cancelled) {
          setState({
            kind: "error",
            message: err instanceof Error ? err.message : String(err),
          });
        }
      }
    })();

    return () => {
      cancelled = true;
      bundleRef.current?.destroy();
      bundleRef.current = null;
    };
  }, [subProductSlug, subFlowSlug, email]);

  if (state.kind !== "synced" || !bundleRef.current) {
    return (
      <div className="drd-pane">
        <header className="drd-pane__header">
          <h2>Design Requirements</h2>
          <span className="drd-pane__chip">
            {state.kind === "minting" && "Connecting…"}
            {state.kind === "connecting" && "Loading…"}
            {state.kind === "error" && "Error"}
            {state.kind === "auth_failed" && "Sign in expired"}
            {state.kind === "synced" && "Live"}
          </span>
        </header>
        <div className="drd-pane__body">
          {state.kind === "minting" && <p>Connecting to the DRD…</p>}
          {state.kind === "connecting" && <p>Loading DRD content…</p>}
          {state.kind === "error" && (
            <>
              <p>Could not load the DRD.</p>
              <p className="drd-pane__hint">
                {state.message ? state.message : "Refresh to retry."}
              </p>
            </>
          )}
          {state.kind === "auth_failed" && (
            <p>Sign-in expired. Refresh the page to reconnect.</p>
          )}
        </div>
        <style jsx>{paneStyles}</style>
      </div>
    );
  }

  return (
    <div className="drd-pane">
      <header className="drd-pane__header">
        <h2>Design Requirements</h2>
        <span className="drd-pane__chip drd-pane__chip--live">Live</span>
      </header>
      <div className="drd-pane__editor">
        <DRDEditor bundle={bundleRef.current} userEmail={email ?? ""} />
      </div>
      <style jsx>{paneStyles}</style>
    </div>
  );
}

// DRDEditor is a separate component so useCreateBlockNote re-fires only on
// genuine bundle changes (i.e. the parent rebuilds the provider, which is
// already a remount). Keeping the hook here avoids re-running it through
// the parent's status-machine re-renders.
function DRDEditor({
  bundle,
  userEmail,
}: {
  bundle: DRDCollabBundle;
  userEmail: string;
}) {
  // Custom BlockNote schema: register the DRD-specific block specs so
  // /decision /figma-link /violation slash items work the same way they
  // do in Atlas. Defaults (paragraph/heading/list/code/quote/table/etc.)
  // are inherited from BlockNoteSchema.create's defaultBlockSpecs.
  const schema = useMemo(
    () => BlockNoteSchema.create({ blockSpecs: drdBlockSpecs }),
    [],
  );

  // BlockNote's collaboration.provider type wants `awareness: Awareness |
  // undefined`, but HocuspocusProvider exposes `awareness: Awareness | null`.
  // The runtime contract matches; this is a pure TS upper-bound mismatch
  // that Atlas dodges with `// @ts-nocheck` at the file head. We localize
  // the cast to the one offending field instead so the rest of the file
  // stays type-checked.
  const editorOptions: Parameters<typeof useCreateBlockNote>[0] = {
    schema,
    collaboration: {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      provider: bundle.provider as any,
      fragment: bundle.doc.getXmlFragment("blocknote"),
      user: {
        name: prettyName(userEmail || "Anon"),
        color: userColor(userEmail || "anon"),
      },
      showCursorLabels: "activity",
    },
  };

  const editor = useCreateBlockNote(editorOptions, [bundle]);

  return (
    <BlockNoteView
      editor={editor}
      theme="dark"
      slashMenu
      sideMenu
      formattingToolbar
      linkToolbar
      filePanel
      tableHandles
    />
  );
}

function prettyName(emailOrID: string): string {
  if (!emailOrID) return "Anon";
  const local = emailOrID.split("@")[0] ?? emailOrID;
  return (
    local
      .split(/[._-]/)
      .filter(Boolean)
      .map((p) => p.charAt(0).toUpperCase() + p.slice(1))
      .join(" ")
      .trim() || "Anon"
  );
}

const paneStyles = `
  .drd-pane {
    display: flex;
    flex-direction: column;
    gap: 12px;
    height: 100%;
    min-height: 0;
  }
  .drd-pane__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .drd-pane__header h2 {
    margin: 0;
    font-size: 14px;
    font-weight: 600;
    letter-spacing: 0.02em;
    text-transform: uppercase;
    color: var(--text-2);
  }
  .drd-pane__chip {
    font-size: 11px;
    padding: 3px 8px;
    background: var(--surface-1, rgba(255, 255, 255, 0.04));
    border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
    border-radius: 999px;
    color: var(--text-3);
    font-variant-numeric: tabular-nums;
  }
  .drd-pane__chip--live {
    color: var(--accent);
    border-color: var(--accent);
  }
  .drd-pane__body {
    font-size: 13px;
    color: var(--text-2);
    line-height: 1.55;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .drd-pane__body p {
    margin: 0;
  }
  .drd-pane__hint {
    color: var(--text-3);
    font-size: 12px;
  }
  .drd-pane__editor {
    flex: 1;
    min-height: 0;
    overflow: auto;
  }
`;
