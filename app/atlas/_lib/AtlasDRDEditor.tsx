// @ts-nocheck
"use client";

/**
 * AtlasDRDEditor — Notion-grade DRD editor for the Atlas LeafInspector.
 *
 * Stack
 *   - BlockNote (Mantine renderer) — slash menu, drag handles, bubble menu,
 *     link toolbar, side menu, table handles, file/image embed, code blocks
 *     with syntax highlight, nested blocks, markdown shortcuts.
 *   - Yjs + Hocuspocus for real-time multiplayer + cursors + presence
 *     (when NEXT_PUBLIC_DRD_COLLAB === "1"). Block-level comments persist
 *     in the same Y.Doc.
 *   - REST single-author fallback with optimistic-concurrency 409 handling
 *     via lib/projects/client.ts.
 *
 * Visual layer
 *   - Wrapped in `.lc-ins-drd-host` so app/atlas/_styles/drd-editor.css can
 *     theme every BlockNote class to match the reference design tokens
 *     (Inter, --bg-2/3, --text-0/1/2/3, --accent, --select).
 *
 * Lifecycle
 *   1. Fetch DRD content + revision (REST). Always required — even in
 *      collab mode we want the initial server state for the first paint.
 *   2. If collab enabled, mint Hocuspocus ticket + open provider. The
 *      Y.Doc becomes BlockNote's source of truth and saves go through
 *      Hocuspocus snapshots.
 *   3. Otherwise wire `editor.onChange` → debounced `putDRD` (1500ms).
 *   4. Surface save state in the chrome header.
 */

import "@blocknote/core/fonts/inter.css";
import { BlockNoteView } from "@blocknote/mantine";

import { DRDAnchorChips } from "./DRDAnchorChips";
import "@blocknote/mantine/style.css";
import { filterSuggestionItems } from "@blocknote/core";
import {
  FormattingToolbarController,
  LinkToolbarController,
  SideMenuController,
  SuggestionMenuController,
  getDefaultReactSlashMenuItems,
  useCreateBlockNote,
} from "@blocknote/react";
import { useEffect, useMemo, useRef, useState } from "react";

import { DRDToolbar } from "../../../components/projects/tabs/DRDToolbar";
import { useAuth } from "../../../lib/auth-client";
import { fetchDRD, fetchProject, putDRD } from "../../../lib/projects/client";
import { createDRDProvider, mintDRDTicket, userColor } from "../../../lib/drd/collab";

import "../_styles/drd-editor.css";

const COLLAB_ENABLED = process.env.NEXT_PUBLIC_DRD_COLLAB === "1";

/** Languages registered for the code-block syntax highlighter. Extend as
 *  the docs surface needs them — every entry adds ~5–15KB to the bundle. */
const CODE_BLOCK_LANGUAGES = {
  text: { name: "Plain text", aliases: ["txt", "plain"] },
  ts: { name: "TypeScript", aliases: ["typescript"] },
  tsx: { name: "TSX", aliases: ["react"] },
  js: { name: "JavaScript", aliases: ["javascript"] },
  jsx: { name: "JSX" },
  json: { name: "JSON" },
  go: { name: "Go", aliases: ["golang"] },
  py: { name: "Python", aliases: ["python"] },
  sql: { name: "SQL" },
  bash: { name: "Bash", aliases: ["sh", "shell"] },
  yaml: { name: "YAML", aliases: ["yml"] },
  css: { name: "CSS" },
  html: { name: "HTML" },
  md: { name: "Markdown", aliases: ["markdown"] },
};

export interface AtlasDRDEditorProps {
  /** Parent project slug (= leaf.flow in the reference UI). */
  slug: string;
  /** Our DB flows.id (= leaf.id in the reference UI). */
  flowID: string;
  /** Plan 005 Phase B+ — sub_flow slug used by the anchor chip layer +
   *  /anchor slash command to scope drd_anchor rows. Null/undefined for
   *  legacy leaves without a sub_flow binding. */
  subFlowSlug?: string | null;
}

type SaveState = "idle" | "saving" | "saved" | "conflict" | "error";

export default function AtlasDRDEditor({ slug, flowID, subFlowSlug }: AtlasDRDEditorProps) {
  const auth = useAuth();
  const [initialContent, setInitialContent] = useState<any[] | undefined>(undefined);
  const [revision, setRevision] = useState<number>(0);
  const [loaded, setLoaded] = useState(false);
  const [saveState, setSaveState] = useState<SaveState>("idle");
  const [collabReady, setCollabReady] = useState<null | true | "failed">(null);
  const [resolvedFlowID, setResolvedFlowID] = useState<string>(flowID);
  const collabBundleRef = useRef<ReturnType<typeof createDRDProvider> | null>(null);
  const saveTimer = useRef<number | null>(null);

  // Post brain-products: callers pass flowID="" because the leaf is now a
  // whole project. Resolve the project's first flow once on mount and use
  // its UUID for every per-flow endpoint below. Independent of the leaf
  // canvas's screens fetch — the DRD editor only needs (project, flow_id)
  // and never blocks on canvas frame loading.
  useEffect(() => {
    if (flowID) { setResolvedFlowID(flowID); return; }
    let cancelled = false;
    void (async () => {
      try {
        const r = await fetchProject(slug);
        if (cancelled) return;
        if (r.ok && r.data.flows && r.data.flows.length > 0) {
          const first = r.data.flows.find((f: any) => !f.DeletedAt) ?? r.data.flows[0];
          setResolvedFlowID(first.ID);
        } else {
          // Project has no flows — stop spinning, render empty state.
          // Without this `loaded` would never flip and the spinner would
          // hang forever.
          setLoaded(true);
        }
      } catch {
        // Network blip — give up, render empty state. The user can refresh.
        if (!cancelled) setLoaded(true);
      }
    })();
    return () => { cancelled = true; };
  }, [slug, flowID]);

  // ─── 1. Load DRD content (REST) ────────────────────────────────────────────
  // Re-fires whenever resolvedFlowID changes (was previously keyed on flowID
  // which never changes after mount, causing a stale-closure DRD spin).
  useEffect(() => {
    if (!resolvedFlowID) return;
    let cancelled = false;
    void (async () => {
      const r = await fetchDRD(slug, resolvedFlowID);
      if (cancelled) return;
      if (r.ok) {
        const raw = r.data.content;
        let parsed: any[] | undefined;
        if (Array.isArray(raw)) parsed = raw;
        else if (typeof raw === "string" && raw.trim().length > 0) {
          try { parsed = JSON.parse(raw); } catch { parsed = undefined; }
        }
        setInitialContent(Array.isArray(parsed) && parsed.length > 0 ? parsed : undefined);
        setRevision(r.data.revision);
      } else {
        // 404 or error → empty doc. Still flip `loaded` so the spinner
        // gives way to the editor / empty state.
        setInitialContent(undefined);
      }
      setLoaded(true);
    })();
    return () => { cancelled = true; };
  }, [slug, resolvedFlowID]);

  // ─── 2. Mint collab ticket + provider ──────────────────────────────────────
  useEffect(() => {
    if (!COLLAB_ENABLED || !loaded || !resolvedFlowID) return;
    let cancelled = false;
    void (async () => {
      const t = await mintDRDTicket(slug, resolvedFlowID);
      if (cancelled || !t.ok) {
        setCollabReady("failed");
        return;
      }
      const userID = auth?.email || "anon";
      try {
        const bundle = createDRDProvider({
          flowID: resolvedFlowID,
          ticket: t.data.ticket,
          user: {
            id: userID,
            name: prettyName(auth?.email || userID),
            color: userColor(userID),
          },
          onAuthFailure: () => setCollabReady("failed"),
          onSync: () => setCollabReady(true),
        });
        collabBundleRef.current = bundle;
      } catch {
        setCollabReady("failed");
      }
    })();
    return () => {
      cancelled = true;
      collabBundleRef.current?.destroy();
      collabBundleRef.current = null;
    };
  }, [slug, resolvedFlowID, loaded, auth?.email]);

  // ─── 3. Editor instance with full Notion-feature surface ───────────────────
  const editorOptions = useMemo(() => {
    const opts: any = {
      // Code-block syntax highlight via Shiki (BlockNote's bundled engine).
      // Without this declaration the slash "/code" inserts a plain <pre>.
      codeBlock: {
        defaultLanguage: "text",
        indentLineWithTab: true,
        supportedLanguages: CODE_BLOCK_LANGUAGES,
      },
      // Image / file upload — base64 data URL fallback. Production should
      // replace this with an upload to ds-service / S3 and return the
      // public URL. Returning a dataURL keeps the editor functional today
      // without backend work; the Y.Doc grows but BlockNote handles it.
      uploadFile: async (file: File): Promise<string> => {
        const buf = await file.arrayBuffer();
        const b64 = btoa(String.fromCharCode(...new Uint8Array(buf)));
        return `data:${file.type};base64,${b64}`;
      },
      // Resolve user IDs in mentions / cursors / comments → display info.
      resolveUsers: async (userIDs: string[]) =>
        userIDs.map((id) => ({
          id,
          username: prettyName(id),
          avatarUrl: undefined,
        })),
    };
    if (COLLAB_ENABLED && collabReady === true && collabBundleRef.current) {
      opts.collaboration = {
        provider: collabBundleRef.current.provider,
        fragment: collabBundleRef.current.doc.getXmlFragment("blocknote"),
        user: {
          name: prettyName(auth?.email || "Anon"),
          color: userColor(auth?.email || "anon"),
        },
        showCursorLabels: "activity",
      };
      // initialContent is ignored when collab is on — Yjs is the truth.
    } else if (initialContent !== undefined) {
      opts.initialContent = initialContent;
    }
    return opts;
  }, [initialContent, collabReady, auth?.email]);

  const editor = useCreateBlockNote(editorOptions, [collabReady, initialContent]);

  // ─── 4. REST autosave (fallback) ───────────────────────────────────────────
  useEffect(() => {
    if (!editor) return;
    if (COLLAB_ENABLED && collabReady === true) return;
    if (!resolvedFlowID) return;
    const onChange = () => {
      if (saveTimer.current) window.clearTimeout(saveTimer.current);
      saveTimer.current = window.setTimeout(async () => {
        setSaveState("saving");
        const blocks = editor.document;
        const r = await putDRD(slug, resolvedFlowID, blocks, revision);
        if (r.ok) {
          setRevision(r.data.revision);
          setSaveState("saved");
          window.setTimeout(() => setSaveState("idle"), 1200);
        } else if ("conflict" in r) {
          setRevision(r.conflict.current_revision);
          setSaveState("conflict");
        } else {
          setSaveState("error");
        }
      }, 1500);
    };
    const off = editor.onChange(onChange);
    return () => {
      off?.();
      if (saveTimer.current) window.clearTimeout(saveTimer.current);
    };
  }, [editor, slug, resolvedFlowID, revision, collabReady]);

  if (!loaded) {
    return (
      <div className="lc-ins-drd-host">
        <div className="lc-drd-header">
          <span className="lc-drd-title">Design Requirement Doc</span>
          <span className="lc-drd-savestate lc-drd-savestate--idle">Loading…</span>
        </div>
        <div className="lc-drd--loading" />
      </div>
    );
  }

  const collabActive = COLLAB_ENABLED && collabReady === true;

  return (
    <div className="lc-ins-drd-host">
      <div className="lc-drd-header">
        <span className="lc-drd-title">Design Requirement Doc</span>
        <span className={`lc-drd-savestate lc-drd-savestate--${collabActive ? "saved" : saveState}`}>
          {labelForSaveState(saveState, collabActive)}
        </span>
      </div>
      <DRDToolbar editor={editor} />
      <div className="lc-drd-editor-host">
        <BlockNoteView
          editor={editor}
          theme="dark"
          // Default slash menu OFF — we replace it with a custom
          // <SuggestionMenuController> below that adds the
          // "Anchor to prototype" items underneath the BlockNote defaults.
          slashMenu={false}
          sideMenu
          formattingToolbar
          linkToolbar
          filePanel
          tableHandles
        >
          <SuggestionMenuController
            triggerCharacter="/"
            getItems={async (query) =>
              filterSuggestionItems(
                [
                  ...getDefaultReactSlashMenuItems(editor),
                  ...anchorSlashItems(editor, subFlowSlug ?? null),
                ],
                query,
              )
            }
          />
        </BlockNoteView>
        {subFlowSlug && <DRDAnchorChips subFlowSlug={subFlowSlug} />}
      </div>
    </div>
  );
}

/**
 * Build slash-menu items for "Anchor to <screen>" — one per prototype
 * screen the bridge advertised via `window.__atlasPrototypeScreens`.
 * Returns [] when no prototype is mounted yet (the menu just shows
 * BlockNote defaults).
 *
 * On select, the item POSTs to MCP `drd.attach_anchor` for the
 * currently-focused block, then dispatches `atlas:anchors-changed` so
 * <DRDAnchorChips> refetches and re-renders the chip layer.
 */
function anchorSlashItems(editor: any, subFlowSlug: string | null) {
  if (!subFlowSlug || typeof window === "undefined") return [];
  const cat = (window as any).__atlasPrototypeScreens as
    | { slug: string; screens: Array<{ screenId: string; label: string }> }
    | undefined;
  if (!cat || cat.slug !== subFlowSlug || !Array.isArray(cat.screens)) {
    return [];
  }
  return cat.screens.map((s) => ({
    title: `Anchor to ${s.screenId}`,
    subtext: s.label,
    aliases: ["anchor", "prototype", s.screenId.toLowerCase()],
    group: "Prototype anchors",
    onItemClick: () => {
      const block = editor.getTextCursorPosition?.()?.block;
      if (!block?.id) return;
      void postAttachAnchor(subFlowSlug, block.id, s.screenId);
    },
  }));
}

async function postAttachAnchor(
  subFlowSlug: string,
  blockID: string,
  screenID: string,
) {
  const dsBase =
    (process.env.NEXT_PUBLIC_DS_SERVICE_URL as string | undefined) ??
    "http://localhost:8080";
  let token = "";
  try {
    const raw = localStorage.getItem("indmoney-ds-auth");
    if (raw) token = JSON.parse(raw)?.state?.token ?? "";
  } catch {
    /* ignore */
  }
  try {
    const r = await fetch(`${dsBase}/v1/mcp/invoke/drd.attach_anchor`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({
        sub_flow_slug: subFlowSlug,
        block_id: blockID,
        screen_id: screenID,
      }),
    });
    if (!r.ok) return;
    window.dispatchEvent(new CustomEvent("atlas:anchors-changed"));
  } catch {
    /* surface via console only — the slash menu already closed */
  }
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

function labelForSaveState(s: SaveState, collab: boolean): string {
  if (collab) return "Live · multi-cursor";
  switch (s) {
    case "saving": return "Saving…";
    case "saved": return "Saved";
    case "conflict": return "Refresh — someone else edited";
    case "error": return "Save failed";
    default: return "Synced";
  }
}
