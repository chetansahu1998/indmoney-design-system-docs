"use client";

/**
 * Phase 6 U7 — filter chips above the canvas.
 *
 * Four chips: [Hierarchy] [Components] [Tokens] [Decisions]. Hierarchy is
 * always-on (clicking does nothing in v1); the others toggle their
 * corresponding satellite-node class + edge class via the cull pass. The
 * d3 simulation re-settles when a filter flips because the node count
 * changes.
 */

import { motion } from "framer-motion";

import type { GraphFilters } from "./types";

interface Props {
  filters: GraphFilters;
  onChange: (next: GraphFilters) => void;
  reducedMotion: boolean;
}

export function FilterChips({ filters, onChange, reducedMotion }: Props) {
  const set = (patch: Partial<GraphFilters>) =>
    onChange({ ...filters, ...patch } as GraphFilters);

  return (
    <div className="chips">
      <Chip label="Hierarchy" active disabled reducedMotion={reducedMotion} />
      <Chip
        label="Components"
        active={filters.components}
        onClick={() => set({ components: !filters.components })}
        reducedMotion={reducedMotion}
      />
      <Chip
        label="Tokens"
        active={filters.tokens}
        onClick={() => set({ tokens: !filters.tokens })}
        reducedMotion={reducedMotion}
      />
      <Chip
        label="Decisions"
        active={filters.decisions}
        onClick={() => set({ decisions: !filters.decisions })}
        reducedMotion={reducedMotion}
      />
      <style jsx>{`
        .chips {
          position: fixed;
          top: 24px;
          left: 50%;
          transform: translateX(-50%);
          display: flex;
          gap: 8px;
          padding: 8px;
          background: rgba(0, 0, 0, 0.4);
          border: 1px solid rgba(255, 255, 255, 0.08);
          border-radius: 999px;
          backdrop-filter: blur(12px);
          z-index: 10;
        }
      `}</style>
    </div>
  );
}

interface ChipProps {
  label: string;
  active?: boolean;
  disabled?: boolean;
  reducedMotion: boolean;
  onClick?: () => void;
}

function Chip({ label, active, disabled, reducedMotion, onClick }: ChipProps) {
  return (
    <motion.button
      type="button"
      onClick={disabled ? undefined : onClick}
      disabled={disabled}
      className={`chip ${active ? "active" : ""} ${disabled ? "disabled" : ""}`}
      // Reduced-motion: remove the scale-on-press feedback.
      whileTap={reducedMotion || disabled ? undefined : { scale: 0.96 }}
      whileHover={reducedMotion || disabled ? undefined : { scale: 1.04 }}
      transition={{ duration: 0.15 }}
    >
      {label}
      <style jsx>{`
        .chip {
          padding: 6px 14px;
          border-radius: 999px;
          border: 1px solid rgba(255, 255, 255, 0.12);
          background: rgba(255, 255, 255, 0.02);
          color: rgba(255, 255, 255, 0.65);
          font-family: var(--font-sans, "Inter Variable", sans-serif);
          font-size: 12px;
          letter-spacing: 0.02em;
          cursor: pointer;
        }
        .chip.active {
          background: rgba(123, 159, 255, 0.18);
          border-color: rgba(123, 159, 255, 0.4);
          color: #c8d6ff;
        }
        .chip.disabled {
          cursor: default;
          opacity: 0.7;
        }
        .chip:focus-visible {
          outline: 2px solid #7b9fff;
          outline-offset: 2px;
        }
      `}</style>
    </motion.button>
  );
}
