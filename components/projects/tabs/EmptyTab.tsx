"use client";

/**
 * Empty-state placeholder shown by every U6 tab whose full implementation
 * lives in a later unit (DRD → U9, JSON → U8, Decisions → Phase 5). Anchors
 * the right-side ergonomics so designers don't see a blank pane.
 *
 * Visual language matches the existing `components/ui/Toast.tsx` palette:
 * `var(--bg-surface)` card, `var(--text-1)` heading, `var(--text-3)` mono
 * detail. No external icons — a 1-glyph wordmark keeps bundle weight zero.
 */

import type { ReactNode } from "react";

interface EmptyTabProps {
  /** Headline; e.g. "DRD coming in U9". */
  title: string;
  /** Optional secondary explanation / call-out copy. */
  description?: string;
  /** Optional slot for a CTA button or link from a future phase. */
  action?: ReactNode;
}

export default function EmptyTab({ title, description, action }: EmptyTabProps) {
  return (
    <div
      role="status"
      aria-live="polite"
      style={{
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 16,
        padding: "48px 32px",
        minHeight: 240,
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 12,
      }}
    >
      {/* Glyph rendered as a CSS-only sigil so we don't pull in lucide here. */}
      <div
        aria-hidden
        style={{
          width: 40,
          height: 40,
          borderRadius: 999,
          display: "grid",
          placeItems: "center",
          background:
            "color-mix(in srgb, var(--text-3) 12%, transparent)",
          color: "var(--text-3)",
          fontSize: 20,
          fontFamily: "var(--font-mono)",
        }}
      >
        ·
      </div>
      <div
        style={{
          fontSize: 14,
          fontWeight: 600,
          color: "var(--text-1)",
          textAlign: "center",
        }}
      >
        {title}
      </div>
      {description && (
        <div
          style={{
            fontSize: 12,
            color: "var(--text-3)",
            maxWidth: 360,
            textAlign: "center",
            lineHeight: 1.5,
            fontFamily: "var(--font-mono)",
          }}
        >
          {description}
        </div>
      )}
      {action}
    </div>
  );
}
