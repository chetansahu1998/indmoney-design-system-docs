"use client";

/**
 * SupersessionChain — renders a vertical thread connecting a decision
 * to its predecessor / successor cards. Phase 5 ships a simple visual:
 * a thin dashed line between two cards rendered top-to-bottom in
 * `made_at DESC` order. The line lives in the gap between cards via
 * an absolutely-positioned segment.
 *
 * The component takes pre-grouped cards (DecisionsTab does the grouping);
 * the cards' DOM is rendered by the parent so this component is purely a
 * visual decoration that wraps a chain in a marker container.
 */

import type { ReactNode } from "react";

interface Props {
  children: ReactNode;
  /** When true, the chain has at least 2 visible decisions and we draw the line. */
  decorate: boolean;
}

export default function SupersessionChain({ children, decorate }: Props) {
  return (
    <div
      style={{
        position: "relative",
        display: "flex",
        flexDirection: "column",
        gap: 8,
      }}
    >
      {decorate && (
        <span
          aria-hidden
          style={{
            position: "absolute",
            left: 8,
            top: 22,
            bottom: 22,
            width: 0,
            borderLeft: "1px dashed var(--border)",
            pointerEvents: "none",
          }}
        />
      )}
      {children}
    </div>
  );
}
