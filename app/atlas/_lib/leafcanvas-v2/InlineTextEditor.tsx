"use client";

/**
 * InlineTextEditor — canvas v2 / U8.
 *
 * Mounted in place of a TEXT atomic when the user double-clicks. Plain
 * `contenteditable` (NO BlockNote / NO Yjs by design — the override is a
 * single short string, never block content). The editor sits inside the
 * same DOM lineage as the rendered span so the parent's autolayout flexbox
 * keeps reflowing siblings as the text length changes — that's the whole
 * point of the U8 spike (D3a).
 *
 * Save state machine mirrors `app/atlas/_lib/AtlasDRDEditor.tsx`:
 *   idle → saving → saved → idle (1.2 s flash) → idle
 *   * → conflict (on 409 — banner with [Refresh])
 *   * → error   (on 5xx / network)
 *
 * Lifecycle:
 *   - Mount: place caret at click point (best-effort via Selection API).
 *   - oninput: 500 ms debounced PUT to `putTextOverride` with
 *     `expected_revision = currentRevision`. Each successful PUT bumps the
 *     revision so chained edits don't trigger spurious 409s.
 *   - Blur / Esc / click-outside: commit (if changed) and exit edit mode
 *     by calling onClose. Esc reverts to original (no PUT).
 *
 * Strict TS — no `// @ts-nocheck`.
 */

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
} from "react";

import {
  putTextOverride,
  type TextOverride,
  type TextOverridePutBody,
} from "../../../../lib/projects/client";

export type InlineTextEditorSaveState =
  | "idle"
  | "saving"
  | "saved"
  | "conflict"
  | "error";

/**
 * Props match the data the renderer has on hand at the moment a TEXT
 * atomic is double-clicked. Any state the editor needs to derive (e.g.
 * the bbox transform) it computes locally; the renderer just hands over
 * the identifying triple + canonical metadata.
 */
export interface InlineTextEditorProps {
  /** ds-service project slug (= leaf id post brain-products migration). */
  slug: string;
  /** Owning leaf id — needed by store mutators (setOverride etc.). */
  leafID: string;
  /** screens.id under which the override is pinned. */
  screenID: string;
  /** Figma node id — primary key for the override row. */
  figmaNodeID: string;
  /**
   * Original text from the canonical_tree node (= last_seen_original_text
   * sent to the server for orphan detection).
   */
  originalText: string;
  /**
   * canonical_path on the canonical_tree node — sent to the server so the
   * U2 reattach logic has a fallback when a future audit run renumbers
   * the figma_node_id.
   */
  canonicalPath: string;
  /**
   * Current revision of the override row, or 0 when there is no row yet
   * (first edit). The PUT echoes back the new revision; the editor keeps
   * its local copy in sync so chained edits don't 409 themselves.
   */
  currentRevision: number;
  /**
   * Optional starting value — e.g. the active override's `value`. When
   * absent we seed from `originalText`. Either way the user sees what the
   * canvas was rendering pre-edit.
   */
  initialValue?: string;
  /**
   * Inline style applied to the contenteditable span so it visually
   * matches the underlying rendered text node (font, size, color, etc.).
   * Caller passes the same CSSProperties the renderer applied.
   */
  textStyle?: CSSProperties;
  /**
   * Mirror of the live store mutators. Decoupled so unit tests can supply
   * stubs without spinning up a zustand instance.
   */
  onSavedOverride?: (override: TextOverride) => void;
  /**
   * Conflict reporter — called on 409 with the server-side current row.
   * Lets the surrounding inspector surface the live value.
   */
  onConflict?: (currentRevision: number, currentValue: string) => void;
  /** Save-state callback so an inspector pill can mirror the editor state. */
  onSaveStateChange?: (state: InlineTextEditorSaveState) => void;
  /**
   * Exit callback — fired on Esc, blur, or click-outside (after any
   * pending commit). The renderer un-mounts the editor and re-mounts the
   * static `<span>` once this fires.
   */
  onClose: () => void;
  /**
   * Override the network function — only used by tests so production code
   * never touches a global. Strictly typed against the real function so
   * stubs can't drift from the contract.
   */
  putOverrideFn?: typeof putTextOverride;
}

const DEBOUNCE_MS = 500;
const SAVED_FLASH_MS = 1200;

/**
 * Small helper exposed for tests so they can drive the save lifecycle
 * without faking React's microtask queue. Pure save action; no DOM.
 */
export interface SaveAttempt {
  slug: string;
  screenID: string;
  figmaNodeID: string;
  body: TextOverridePutBody;
}

export function InlineTextEditor(props: InlineTextEditorProps) {
  const {
    slug,
    screenID,
    figmaNodeID,
    originalText,
    canonicalPath,
    initialValue,
    textStyle,
    onSavedOverride,
    onConflict,
    onSaveStateChange,
    onClose,
  } = props;

  const seededValue = initialValue ?? originalText;
  const [saveState, setSaveStateInternal] = useState<InlineTextEditorSaveState>("idle");
  // Track current revision locally — a successful PUT bumps it so chained
  // edits don't 409 themselves.
  const revisionRef = useRef<number>(props.currentRevision);
  // Last value sent to the server. Used to short-circuit redundant PUTs
  // (blur with no change → no PUT, per plan §U8 edge case).
  const lastSentValueRef = useRef<string>(seededValue);
  // The value the user is currently editing (kept off-state to avoid React
  // round-tripping on every keystroke; we sync to DOM directly).
  const currentValueRef = useRef<string>(seededValue);

  const editorRef = useRef<HTMLSpanElement | null>(null);
  const debounceTimerRef = useRef<number | null>(null);
  const flashTimerRef = useRef<number | null>(null);

  // Network function — production caller passes nothing and we use the
  // real putTextOverride; tests inject a stub.
  const putFn = props.putOverrideFn ?? putTextOverride;

  const setSaveState = useCallback(
    (next: InlineTextEditorSaveState) => {
      setSaveStateInternal(next);
      onSaveStateChange?.(next);
    },
    [onSaveStateChange],
  );

  // ─── Commit ──────────────────────────────────────────────────────────────
  /**
   * Send the current buffered value as a PUT. Returns the server result so
   * the caller (debounced timer or blur handler) can chain post-commit
   * actions like onClose.
   */
  const commit = useCallback(
    async (value: string): Promise<void> => {
      if (value === lastSentValueRef.current) return; // no change → no PUT
      lastSentValueRef.current = value;
      setSaveState("saving");
      const body: TextOverridePutBody = {
        value,
        expected_revision: revisionRef.current,
        canonical_path: canonicalPath,
        last_seen_original_text: originalText,
      };
      const res = await putFn(slug, screenID, figmaNodeID, body);
      if (res.ok) {
        revisionRef.current = res.data.revision;
        setSaveState("saved");
        onSavedOverride?.({
          id: "", // not returned by PUT — populated next list refresh
          screen_id: screenID,
          figma_node_id: figmaNodeID,
          canonical_path: canonicalPath,
          last_seen_original_text: originalText,
          value,
          revision: res.data.revision,
          status: "active",
          updated_by_user_id: "",
          updated_at: res.data.updated_at,
        });
        // Flash "saved" briefly, then return to idle so the inspector pill
        // doesn't camp on the success state forever.
        if (flashTimerRef.current !== null) {
          window.clearTimeout(flashTimerRef.current);
        }
        flashTimerRef.current = window.setTimeout(() => {
          setSaveState("idle");
          flashTimerRef.current = null;
        }, SAVED_FLASH_MS);
      } else if (res.status === 409 && "conflict" in res) {
        revisionRef.current = res.conflict.current_revision;
        setSaveState("conflict");
        onConflict?.(res.conflict.current_revision, res.conflict.current_value);
      } else {
        setSaveState("error");
      }
    },
    [
      canonicalPath,
      figmaNodeID,
      onConflict,
      onSavedOverride,
      originalText,
      putFn,
      screenID,
      setSaveState,
      slug,
    ],
  );

  // ─── Input → debounced commit ─────────────────────────────────────────────
  const onInput = useCallback(() => {
    const el = editorRef.current;
    if (!el) return;
    const next = el.textContent ?? "";
    currentValueRef.current = next;
    if (debounceTimerRef.current !== null) {
      window.clearTimeout(debounceTimerRef.current);
    }
    debounceTimerRef.current = window.setTimeout(() => {
      debounceTimerRef.current = null;
      void commit(currentValueRef.current);
    }, DEBOUNCE_MS);
  }, [commit]);

  // ─── Esc → revert; Enter → commit-and-exit ────────────────────────────────
  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLSpanElement>) => {
      if (e.key === "Escape") {
        // Abort any pending debounced commit; revert local + DOM to original.
        if (debounceTimerRef.current !== null) {
          window.clearTimeout(debounceTimerRef.current);
          debounceTimerRef.current = null;
        }
        const el = editorRef.current;
        if (el) el.textContent = seededValue;
        currentValueRef.current = seededValue;
        e.preventDefault();
        e.stopPropagation();
        onClose();
        return;
      }
      if (e.key === "Enter" && !e.shiftKey) {
        // Enter commits and exits — Shift+Enter inserts a literal newline.
        e.preventDefault();
        e.stopPropagation();
        const el = editorRef.current;
        if (el) el.blur();
      }
    },
    [onClose, seededValue],
  );

  // ─── Blur → commit-and-exit ───────────────────────────────────────────────
  const onBlur = useCallback(() => {
    if (debounceTimerRef.current !== null) {
      window.clearTimeout(debounceTimerRef.current);
      debounceTimerRef.current = null;
    }
    const next = currentValueRef.current;
    // Fire-and-forget — onClose is called regardless so the static span
    // re-mounts immediately. The save-state pill keeps spinning until the
    // PUT resolves, courtesy of the inspector subscribing to the same
    // store the commit writes to.
    void commit(next).then(() => {
      // Nothing extra; commit already updates the store.
    });
    onClose();
  }, [commit, onClose]);

  // ─── Cleanup on unmount ──────────────────────────────────────────────────
  useEffect(() => {
    return () => {
      if (debounceTimerRef.current !== null) {
        window.clearTimeout(debounceTimerRef.current);
      }
      if (flashTimerRef.current !== null) {
        window.clearTimeout(flashTimerRef.current);
      }
    };
  }, []);

  // ─── Mount focus + caret placement ───────────────────────────────────────
  useEffect(() => {
    const el = editorRef.current;
    if (!el) return;
    el.focus();
    // Place caret at end (best-effort — exact click-point caret would need
    // document.caretRangeFromPoint which is non-standard). Caret-at-end
    // is the convention BlockNote / Notion / Figma all use too.
    if (typeof window !== "undefined" && window.getSelection) {
      const range = document.createRange();
      range.selectNodeContents(el);
      range.collapse(false);
      const sel = window.getSelection();
      if (sel) {
        sel.removeAllRanges();
        sel.addRange(range);
      }
    }
  }, []);

  // ─── Click-outside detection ─────────────────────────────────────────────
  useEffect(() => {
    function onDocMouseDown(e: MouseEvent) {
      const el = editorRef.current;
      if (!el) return;
      const target = e.target as Node | null;
      if (target && el.contains(target)) return;
      // Click outside — treat as blur.
      el.blur();
    }
    document.addEventListener("mousedown", onDocMouseDown, { capture: true });
    return () => {
      document.removeEventListener("mousedown", onDocMouseDown, {
        capture: true,
      } as EventListenerOptions);
    };
  }, []);

  // Compose the editor span style from the caller's textStyle plus a
  // narrow set of editor-only overrides (outline, caret-color).
  const composedStyle = useMemo<CSSProperties>(
    () => ({
      ...textStyle,
      outline: "1px solid var(--ds-color-accent, #0a84ff)",
      outlineOffset: "1px",
      cursor: "text",
      // Allow the editor to wrap if the user pastes long content; the
      // surrounding flexbox container reflows accordingly per the D3a
      // spike. Keep nowrap when textStyle says so.
      whiteSpace: textStyle?.whiteSpace ?? "nowrap",
      // contenteditable spans need at least 1ch of width even when empty
      // so the caret has a target.
      minWidth: "1ch",
    }),
    [textStyle],
  );

  return (
    <span
      ref={editorRef}
      className="leafcv2-inline-editor"
      contentEditable
      suppressContentEditableWarning
      data-figma-id={figmaNodeID}
      data-figma-type="TEXT"
      data-save-state={saveState}
      style={composedStyle}
      onInput={onInput}
      onKeyDown={onKeyDown}
      onBlur={onBlur}
      // Stop click + dblclick propagation so the underlying canvas doesn't
      // try to re-trigger selection while the user is editing.
      onClick={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
    >
      {seededValue}
    </span>
  );
}
