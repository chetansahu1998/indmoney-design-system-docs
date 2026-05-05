"use client";

import * as React from "react";
import type { BlockNoteEditor } from "@blocknote/core";
import {
  useActiveStyles,
  useEditorSelectionChange,
} from "@blocknote/react";

/**
 * DRDToolbar — persistent top strip of editor controls so designers landing
 * on a blank DRD see something to click. Sits above BlockNoteView; the
 * library's contextual menus (slash, side, formatting popover, link, file,
 * table) keep working underneath.
 *
 * Active state is derived live: inline-mark highlights from useActiveStyles,
 * block-type highlights re-derive on every selection change. Both editors
 * (DRDTab default schema, DRDTabCollab merged drdBlockSpecs) inherit from
 * defaultBlockSpecs, so the built-in types this toolbar inserts/updates
 * (heading/list/quote/codeBlock/image/table/paragraph) are always present.
 */

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyEditor = BlockNoteEditor<any, any, any>;

interface Props {
  editor: AnyEditor;
  disabled?: boolean;
}

export function DRDToolbar({ editor, disabled = false }: Props) {
  const styles = useActiveStyles(editor);
  const [, forceTick] = React.useState(0);
  // Selection-change re-render so the block-type buttons highlight as the
  // user moves the cursor across heading/list/paragraph runs.
  useEditorSelectionChange(() => forceTick((t) => t + 1), editor);

  const cursorBlock = safeCursorBlock(editor);
  const blockType = cursorBlock?.type as string | undefined;
  const headingLevel = (cursorBlock?.props as { level?: number } | undefined)?.level;

  const setBlockType = (type: string, props?: Record<string, unknown>) => {
    if (!cursorBlock) return;
    editor.updateBlock(cursorBlock.id, { type, props } as never);
    editor.focus();
  };

  const insertAfterCursor = (block: Record<string, unknown>) => {
    if (!cursorBlock) return;
    editor.insertBlocks([block as never], cursorBlock.id, "after");
    editor.focus();
  };

  const promptLink = () => {
    const url = window.prompt("Link URL", "https://");
    if (!url) return;
    editor.createLink(url);
    editor.focus();
  };

  const insertTable = () => {
    insertAfterCursor({
      type: "table",
      content: {
        type: "tableContent",
        rows: [
          { cells: ["", "", ""] },
          { cells: ["", "", ""] },
          { cells: ["", "", ""] },
        ],
      },
    });
  };

  const insertImage = () => {
    insertAfterCursor({ type: "image", props: {} });
  };

  return (
    <div
      role="toolbar"
      aria-label="DRD editor toolbar"
      style={toolbarStyle}
      data-disabled={disabled || undefined}
    >
      <Group>
        <Btn
          label="Bold"
          shortcut="⌘B"
          active={!!styles.bold}
          disabled={disabled}
          onClick={() => editor.toggleStyles({ bold: true })}
        >
          <strong style={glyphStyle}>B</strong>
        </Btn>
        <Btn
          label="Italic"
          shortcut="⌘I"
          active={!!styles.italic}
          disabled={disabled}
          onClick={() => editor.toggleStyles({ italic: true })}
        >
          <em style={glyphStyle}>I</em>
        </Btn>
        <Btn
          label="Underline"
          shortcut="⌘U"
          active={!!styles.underline}
          disabled={disabled}
          onClick={() => editor.toggleStyles({ underline: true })}
        >
          <span style={{ ...glyphStyle, textDecoration: "underline" }}>U</span>
        </Btn>
        <Btn
          label="Strikethrough"
          active={!!styles.strike}
          disabled={disabled}
          onClick={() => editor.toggleStyles({ strike: true })}
        >
          <span style={{ ...glyphStyle, textDecoration: "line-through" }}>S</span>
        </Btn>
        <Btn
          label="Inline code"
          active={!!styles.code}
          disabled={disabled}
          onClick={() => editor.toggleStyles({ code: true })}
        >
          <span style={{ ...glyphStyle, fontFamily: "var(--font-mono, ui-monospace)" }}>{"<>"}</span>
        </Btn>
      </Group>

      <Sep />

      <Group>
        <Btn
          label="Heading 1"
          active={blockType === "heading" && headingLevel === 1}
          disabled={disabled}
          onClick={() => setBlockType("heading", { level: 1 })}
        >
          H1
        </Btn>
        <Btn
          label="Heading 2"
          active={blockType === "heading" && headingLevel === 2}
          disabled={disabled}
          onClick={() => setBlockType("heading", { level: 2 })}
        >
          H2
        </Btn>
        <Btn
          label="Heading 3"
          active={blockType === "heading" && headingLevel === 3}
          disabled={disabled}
          onClick={() => setBlockType("heading", { level: 3 })}
        >
          H3
        </Btn>
        <Btn
          label="Paragraph"
          active={blockType === "paragraph"}
          disabled={disabled}
          onClick={() => setBlockType("paragraph")}
        >
          ¶
        </Btn>
      </Group>

      <Sep />

      <Group>
        <Btn
          label="Bulleted list"
          active={blockType === "bulletListItem"}
          disabled={disabled}
          onClick={() => setBlockType("bulletListItem")}
        >
          •
        </Btn>
        <Btn
          label="Numbered list"
          active={blockType === "numberedListItem"}
          disabled={disabled}
          onClick={() => setBlockType("numberedListItem")}
        >
          1.
        </Btn>
        <Btn
          label="Checklist"
          active={blockType === "checkListItem"}
          disabled={disabled}
          onClick={() => setBlockType("checkListItem")}
        >
          ☐
        </Btn>
        <Btn
          label="Quote"
          active={blockType === "quote"}
          disabled={disabled}
          onClick={() => setBlockType("quote")}
        >
          ❝
        </Btn>
        <Btn
          label="Code block"
          active={blockType === "codeBlock"}
          disabled={disabled}
          onClick={() => setBlockType("codeBlock")}
        >
          <span style={{ fontFamily: "var(--font-mono, ui-monospace)", fontSize: 11 }}>{ }</span>
        </Btn>
      </Group>

      <Sep />

      <Group>
        <Btn label="Link" shortcut="⌘K" disabled={disabled} onClick={promptLink}>
          🔗
        </Btn>
        <Btn label="Image" disabled={disabled} onClick={insertImage}>
          🖼
        </Btn>
        <Btn label="Table" disabled={disabled} onClick={insertTable}>
          ▦
        </Btn>
      </Group>

      <span style={{ flex: 1 }} />
      <span style={hintStyle} title="Type / inside the editor for the full insert menu">
        Type <kbd style={kbdStyle}>/</kbd> for more
      </span>
    </div>
  );
}

function safeCursorBlock(editor: AnyEditor) {
  try {
    return editor.getTextCursorPosition().block;
  } catch {
    return null;
  }
}

function Group({ children }: { children: React.ReactNode }) {
  return <div style={groupStyle}>{children}</div>;
}

function Sep() {
  return <span aria-hidden style={sepStyle} />;
}

function Btn({
  children,
  label,
  shortcut,
  active,
  disabled,
  onClick,
}: {
  children: React.ReactNode;
  label: string;
  shortcut?: string;
  active?: boolean;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      aria-pressed={active || undefined}
      title={shortcut ? `${label} (${shortcut})` : label}
      disabled={disabled}
      onMouseDown={(e) => e.preventDefault()}
      onClick={onClick}
      style={{
        ...btnStyle,
        ...(active ? btnActiveStyle : null),
        ...(disabled ? btnDisabledStyle : null),
      }}
    >
      {children}
    </button>
  );
}

const toolbarStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 4,
  padding: "6px 8px",
  borderBottom: "1px solid var(--border)",
  background: "var(--bg-surface, #f7f7f7)",
  position: "sticky",
  top: 0,
  zIndex: 2,
  flexWrap: "wrap",
};

const groupStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: 2,
};

const sepStyle: React.CSSProperties = {
  display: "inline-block",
  width: 1,
  height: 18,
  background: "var(--border)",
  margin: "0 4px",
};

const btnStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  minWidth: 28,
  height: 28,
  padding: "0 8px",
  border: "1px solid transparent",
  borderRadius: 6,
  background: "transparent",
  color: "var(--text-1, inherit)",
  cursor: "pointer",
  fontSize: 13,
  lineHeight: 1,
};

const btnActiveStyle: React.CSSProperties = {
  background: "color-mix(in oklab, var(--accent, #4a66ff) 18%, transparent)",
  borderColor: "color-mix(in oklab, var(--accent, #4a66ff) 35%, transparent)",
  color: "var(--accent-fg, var(--text-1))",
};

const btnDisabledStyle: React.CSSProperties = {
  opacity: 0.45,
  cursor: "not-allowed",
};

const glyphStyle: React.CSSProperties = {
  fontWeight: 600,
  fontStyle: "normal",
};

const hintStyle: React.CSSProperties = {
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  color: "var(--text-3)",
  paddingRight: 4,
};

const kbdStyle: React.CSSProperties = {
  display: "inline-block",
  padding: "1px 5px",
  border: "1px solid var(--border)",
  borderRadius: 4,
  background: "var(--bg-base, #fff)",
  fontFamily: "var(--font-mono)",
  fontSize: 10,
};
