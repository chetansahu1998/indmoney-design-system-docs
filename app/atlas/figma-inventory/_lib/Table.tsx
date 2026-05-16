"use client";

/**
 * Shared table primitives for /atlas/figma-inventory.
 *
 * Mirrors the Th/Td shapes used in organisms/page.tsx and the inline-style
 * conventions across /atlas/* — kept here so the figma-inventory
 * page can reuse them without duplicating the body, and so the sortable
 * variant (SortableTh) lives next to the static Th it's based on.
 *
 * Uses CSS variables from app/globals.css (--text-2, --text-3, --border)
 * with hex fallbacks for environments where the token cascade hasn't
 * loaded yet.
 */

import type React from "react";

export function Th({
  children,
  align,
  width,
}: {
  children: React.ReactNode;
  align?: "left" | "right" | "center";
  width?: string | number;
}) {
  return (
    <th
      style={{
        textAlign: align ?? "left",
        padding: "8px 12px",
        fontWeight: 500,
        color: "var(--text-2, #aaa)",
        fontSize: 11,
        textTransform: "uppercase",
        letterSpacing: 0.5,
        width,
        whiteSpace: "nowrap",
        userSelect: "none",
      }}
    >
      {children}
    </th>
  );
}

export type SortDirection = "asc" | "desc";

export function SortableTh<K extends string>({
  field,
  current,
  direction,
  onClick,
  align,
  width,
  children,
}: {
  field: K;
  current: K | null;
  direction: SortDirection;
  onClick: (field: K) => void;
  align?: "left" | "right" | "center";
  width?: string | number;
  children: React.ReactNode;
}) {
  const isActive = current === field;
  const arrow = !isActive ? "" : direction === "asc" ? " ↑" : " ↓";
  return (
    <th
      onClick={() => onClick(field)}
      style={{
        textAlign: align ?? "left",
        padding: "8px 12px",
        fontWeight: 500,
        color: isActive ? "var(--text-1, #f7f7f7)" : "var(--text-2, #aaa)",
        fontSize: 11,
        textTransform: "uppercase",
        letterSpacing: 0.5,
        width,
        whiteSpace: "nowrap",
        cursor: "pointer",
        userSelect: "none",
      }}
      title={`Sort by ${String(field)}`}
    >
      {children}
      <span style={{ opacity: isActive ? 1 : 0.3, marginLeft: 4 }}>
        {arrow || "↕"}
      </span>
    </th>
  );
}

export function Td({
  children,
  align,
  muted,
  mono,
  colSpan,
  style,
  onClick,
  title,
}: {
  children: React.ReactNode;
  align?: "left" | "right" | "center";
  muted?: boolean;
  mono?: boolean;
  colSpan?: number;
  style?: React.CSSProperties;
  onClick?: () => void;
  title?: string;
}) {
  return (
    <td
      colSpan={colSpan}
      onClick={onClick}
      title={title}
      style={{
        padding: "10px 12px",
        textAlign: align ?? "left",
        color: muted ? "var(--text-3, #707070)" : undefined,
        fontFamily: mono ? "ui-monospace, monospace" : undefined,
        fontSize: mono ? 12 : undefined,
        verticalAlign: "top",
        ...style,
      }}
    >
      {children}
    </td>
  );
}

/** Empty-state box matching the dashboard convention. */
export function EmptyBox({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        padding: 40,
        textAlign: "center",
        color: "var(--text-3, #707070)",
        background: "var(--bg-surface, rgba(255,255,255,0.02))",
        border: "1px dashed var(--border, rgba(255,255,255,0.1))",
        borderRadius: 8,
      }}
    >
      {children}
    </div>
  );
}

/** Inline pill button used for filter chips. */
export function Pill({
  active,
  onClick,
  children,
  count,
  title,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
  count?: number;
  title?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      title={title}
      style={{
        padding: "6px 14px",
        background: active
          ? "var(--accent-soft, rgba(80, 180, 255, 0.15))"
          : "transparent",
        border: `1px solid ${
          active
            ? "var(--accent, rgba(80, 180, 255, 0.5))"
            : "var(--border, rgba(255,255,255,0.15))"
        }`,
        color: active
          ? "var(--accent, #7b9fff)"
          : "var(--text-2, rgba(255,255,255,0.6))",
        borderRadius: 6,
        cursor: "pointer",
        fontSize: 13,
        whiteSpace: "nowrap",
      }}
    >
      {children}
      {typeof count === "number" && (
        <span style={{ opacity: 0.6, marginLeft: 6 }}>{count}</span>
      )}
    </button>
  );
}

/** Plain text button (transparent bg, subtle border). */
export function GhostBtn({
  onClick,
  disabled,
  children,
  title,
}: {
  onClick: () => void;
  disabled?: boolean;
  children: React.ReactNode;
  title?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      style={{
        padding: "6px 12px",
        background: "transparent",
        border: "1px solid var(--border, rgba(255,255,255,0.2))",
        color: "var(--text-2, rgba(255,255,255,0.7))",
        borderRadius: 6,
        cursor: disabled ? "wait" : "pointer",
        fontSize: 13,
        whiteSpace: "nowrap",
      }}
    >
      {children}
    </button>
  );
}

/** Status badge — small uppercase pill with semantic color. */
export function StatusBadge({
  kind,
  children,
  title,
}: {
  kind: "ok" | "pending" | "warn" | "danger" | "muted";
  children: React.ReactNode;
  title?: string;
}) {
  const palette: Record<typeof kind, { fg: string; bg: string }> = {
    ok: { fg: "#4ade80", bg: "rgba(34, 197, 94, 0.15)" },
    pending: { fg: "#a5b4fc", bg: "rgba(99, 102, 241, 0.15)" },
    warn: { fg: "#fde047", bg: "rgba(234, 179, 8, 0.15)" },
    danger: { fg: "#fca5a5", bg: "rgba(239, 68, 68, 0.15)" },
    muted: { fg: "var(--text-3, #888)", bg: "rgba(255,255,255,0.04)" },
  };
  const { fg, bg } = palette[kind];
  return (
    <span
      title={title}
      style={{
        display: "inline-block",
        fontSize: 10,
        padding: "1px 8px",
        borderRadius: 999,
        textTransform: "uppercase",
        letterSpacing: "0.06em",
        fontWeight: 600,
        background: bg,
        color: fg,
      }}
    >
      {children}
    </span>
  );
}

/** Relative-time formatter shared across panels. */
export function formatAgo(iso: string | undefined): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const seconds = Math.floor((Date.now() - t) / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  if (seconds < 86400 * 7) return `${Math.floor(seconds / 86400)}d ago`;
  return `${Math.floor(seconds / (86400 * 7))}w ago`;
}
