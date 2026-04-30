"use client";

/**
 * DRD tab — U9.
 *
 * Notion-style editor backed by BlockNote 0.49+. Single-editor only — Yjs
 * collab + custom blocks (`/decision`, `/figma-link`, `/violation-ref`) ship
 * in Phase 5 of the plan. Phase 1's job is to prove the wiring:
 *   - Lazy-fetch DRD on tab mount via GET /v1/projects/:slug/flows/:flow_id/drd
 *   - BlockNote editor with the default schema
 *   - Debounced autosave (1.5s) via PUT with optimistic concurrency
 *     (revision-counter ETag, NOT updated_at — see plan DI C2)
 *   - 409 conflict shows banner + reload button (last-writer-wins-with-warn,
 *     not silent overwrite)
 *
 * The editor is the BlockNote default UI surface. Custom block specs live in
 * Phase 5; in Phase 1 the tab is intentionally lightweight (≤ 400KB gz when
 * the chunk lazy-loads via dynamic-import from ProjectShell — chunks/drd in
 * the bundle budget).
 *
 * Flow ID resolution: a flow lives under a project; ProjectShell currently
 * carries one Flow at a time (the flow whose slug the URL points at). When
 * future versions ship multi-flow projects, the active-flow selector will
 * surface in ProjectToolbar; we accept `flowID` as a prop so swapping later
 * doesn't churn this file.
 */

import { useEffect, useMemo, useRef, useState } from "react";
import { useCreateBlockNote } from "@blocknote/react";
import { BlockNoteView } from "@blocknote/mantine";
import "@blocknote/core/fonts/inter.css";
import "@blocknote/mantine/style.css";
import { fetchDRD, putDRD } from "@/lib/projects/client";
import EmptyTab from "./EmptyTab";

const AUTOSAVE_DEBOUNCE_MS = 1500;

type SaveStatus =
  | { kind: "idle" }
  | { kind: "saving" }
  | { kind: "saved"; at: Date }
  | { kind: "error"; message: string }
  | { kind: "conflict"; currentRevision: number };

interface DRDTabProps {
  slug: string;
  flowID: string | null;
}

export default function DRDTab({ slug, flowID }: DRDTabProps) {
  const [revision, setRevision] = useState<number>(0);
  const [status, setStatus] = useState<SaveStatus>({ kind: "idle" });
  const [loaded, setLoaded] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  // Debounce timer + latest content + in-flight token. Refs avoid stale
  // closures over editor.onChange.
  const debounceTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const inFlightSeq = useRef<number>(0);
  const latestRevision = useRef<number>(0);

  // Editor — default BlockNote schema. Custom blocks ship in Phase 5.
  const editor = useCreateBlockNote();

  // Initial load.
  useEffect(() => {
    if (!flowID) return;
    let cancelled = false;
    setLoaded(false);
    setLoadError(null);
    void fetchDRD(slug, flowID).then((res) => {
      if (cancelled) return;
      if (!res.ok) {
        setLoadError(res.error || "Failed to load DRD");
        return;
      }
      // Restore content. BlockNote stores documents as an array of blocks;
      // an empty `{}` payload (first-fetch) means no prior write — leave the
      // editor at its default empty paragraph.
      const content = res.data.content;
      if (Array.isArray(content) && content.length > 0) {
        try {
          editor.replaceBlocks(editor.document, content as never);
        } catch {
          // Defensive: malformed content from an older schema → start blank.
        }
      }
      latestRevision.current = res.data.revision;
      setRevision(res.data.revision);
      setLoaded(true);
    });
    return () => {
      cancelled = true;
      if (debounceTimer.current) clearTimeout(debounceTimer.current);
    };
  }, [slug, flowID, editor]);

  // Debounced autosave on every editor change. Captures editor.document via
  // the latest closure on save-fire (not on change-fire), so the request body
  // always reflects the most recent edit.
  useEffect(() => {
    if (!loaded || !flowID) return;
    const offChange = editor.onChange(() => {
      if (debounceTimer.current) clearTimeout(debounceTimer.current);
      debounceTimer.current = setTimeout(() => {
        void persistNow();
      }, AUTOSAVE_DEBOUNCE_MS);
    });
    return () => {
      // BlockNote's onChange returns an unsubscribe; older versions may not.
      if (typeof offChange === "function") offChange();
      if (debounceTimer.current) clearTimeout(debounceTimer.current);
    };
    // We only re-bind when the editor or flow changes; latestRevision moves
    // through a ref so it isn't a dep.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded, flowID, editor]);

  async function persistNow() {
    if (!flowID) return;
    const myseq = ++inFlightSeq.current;
    const sending = editor.document;
    setStatus({ kind: "saving" });
    const res = await putDRD(slug, flowID, sending, latestRevision.current);
    // Drop stale results — if a newer save started while this one was in
    // flight, ignore this response so it doesn't undo the newer status.
    if (myseq !== inFlightSeq.current) return;
    if ("conflict" in res) {
      setStatus({
        kind: "conflict",
        currentRevision: res.conflict.current_revision,
      });
      return;
    }
    if (!res.ok) {
      setStatus({ kind: "error", message: res.error || "save failed" });
      return;
    }
    latestRevision.current = res.data.revision;
    setRevision(res.data.revision);
    setStatus({ kind: "saved", at: new Date() });
  }

  async function reloadOnConflict() {
    if (!flowID) return;
    const res = await fetchDRD(slug, flowID);
    if (res.ok) {
      const content = res.data.content;
      if (Array.isArray(content) && content.length > 0) {
        try {
          editor.replaceBlocks(editor.document, content as never);
        } catch {
          /* fall through */
        }
      }
      latestRevision.current = res.data.revision;
      setRevision(res.data.revision);
      setStatus({ kind: "idle" });
    }
  }

  if (!flowID) {
    return (
      <EmptyTab
        title="No flow selected"
        description="DRDs are anchored to a flow. Once this project has a flow, the DRD editor opens here."
      />
    );
  }

  if (loadError) {
    return (
      <div role="alert" style={errorBoxStyle}>
        Couldn’t load DRD: {loadError}
      </div>
    );
  }

  return (
    <div data-anim="tab-content" style={containerStyle}>
      <header style={headerStyle}>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-3)" }}>
          DRD · revision {revision}
        </span>
        <span style={{ flex: 1 }} />
        <SaveStatusBadge status={status} onReload={reloadOnConflict} />
      </header>
      <div style={editorWrapperStyle} aria-busy={!loaded}>
        <BlockNoteView editor={editor} editable={loaded} />
      </div>
    </div>
  );
}

function SaveStatusBadge({
  status,
  onReload,
}: {
  status: SaveStatus;
  onReload: () => void;
}) {
  switch (status.kind) {
    case "idle":
      return (
        <span style={{ ...badgeBase, color: "var(--text-3)" }}>—</span>
      );
    case "saving":
      return <span style={{ ...badgeBase, color: "var(--text-2)" }}>saving…</span>;
    case "saved":
      return (
        <span style={{ ...badgeBase, color: "var(--text-3)" }}>
          saved · {status.at.toLocaleTimeString()}
        </span>
      );
    case "error":
      return (
        <span style={{ ...badgeBase, color: "var(--danger, #c00)" }}>
          save failed: {status.message}
        </span>
      );
    case "conflict":
      return (
        <span style={{ ...badgeBase, color: "var(--warn, #c80)" }}>
          edited elsewhere (rev {status.currentRevision}) ·
          <button
            onClick={onReload}
            style={{
              marginLeft: 6,
              background: "none",
              border: "none",
              color: "var(--accent)",
              cursor: "pointer",
              padding: 0,
              fontFamily: "inherit",
              fontSize: "inherit",
              textDecoration: "underline",
            }}
          >
            reload
          </button>
        </span>
      );
  }
}

const containerStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  height: "100%",
  minHeight: 360,
};
const headerStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 8,
  padding: "8px 12px",
  borderBottom: "1px solid var(--border)",
};
const editorWrapperStyle: React.CSSProperties = {
  flex: 1,
  overflowY: "auto",
  padding: 8,
};
const badgeBase: React.CSSProperties = {
  fontFamily: "var(--font-mono)",
  fontSize: 11,
};
const errorBoxStyle: React.CSSProperties = {
  padding: 16,
  margin: 16,
  border: "1px solid var(--border)",
  borderRadius: 8,
  background: "var(--bg-surface)",
  color: "var(--text-1)",
  fontFamily: "var(--font-mono)",
  fontSize: 12,
};
