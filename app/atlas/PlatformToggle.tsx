"use client";

/**
 * Phase 6 U8 — Mobile ↔ Web universal toggle.
 *
 * Top-right pill with an animated indicator. On change, the parent
 * BrainGraph swaps the platform prop on useGraphAggregate which triggers
 * a re-fetch + scene re-mount. The crossfade between scenes is implemented
 * at the BrainGraph level via opacity tween (not in this component).
 *
 * Reduced-motion: the indicator slides without spring; transitions cut.
 */

import { motion } from "framer-motion";

import type { GraphPlatform } from "./types";

interface Props {
  value: GraphPlatform;
  onChange: (next: GraphPlatform) => void;
  reducedMotion: boolean;
}

export function PlatformToggle({ value, onChange, reducedMotion }: Props) {
  const transition = reducedMotion
    ? { duration: 0 }
    : { type: "spring" as const, stiffness: 360, damping: 28 };

  return (
    <div className="toggle" role="tablist" aria-label="Platform">
      <button
        type="button"
        role="tab"
        aria-selected={value === "mobile"}
        onClick={() => onChange("mobile")}
        className={value === "mobile" ? "tab active" : "tab"}
      >
        Mobile
      </button>
      <button
        type="button"
        role="tab"
        aria-selected={value === "web"}
        onClick={() => onChange("web")}
        className={value === "web" ? "tab active" : "tab"}
      >
        Web
      </button>
      <motion.div
        className="indicator"
        aria-hidden
        layoutId="atlas-platform-toggle"
        transition={transition}
        style={{ left: value === "mobile" ? 4 : "calc(50% + 0px)" }}
      />
      <style jsx>{`
        .toggle {
          position: fixed;
          top: 24px;
          right: 24px;
          display: flex;
          padding: 4px;
          background: rgba(0, 0, 0, 0.4);
          border: 1px solid rgba(255, 255, 255, 0.08);
          border-radius: 999px;
          backdrop-filter: blur(12px);
          z-index: 10;
        }
        .tab {
          position: relative;
          z-index: 1;
          padding: 6px 16px;
          border: none;
          background: transparent;
          color: rgba(255, 255, 255, 0.55);
          font-family: var(--font-sans, "Inter Variable", sans-serif);
          font-size: 12px;
          letter-spacing: 0.02em;
          cursor: pointer;
          border-radius: 999px;
        }
        .tab.active {
          color: #ffffff;
        }
        .tab:focus-visible {
          outline: 2px solid #7b9fff;
          outline-offset: 2px;
        }
        .indicator {
          position: absolute;
          top: 4px;
          bottom: 4px;
          width: calc(50% - 4px);
          border-radius: 999px;
          background: rgba(123, 159, 255, 0.18);
          border: 1px solid rgba(123, 159, 255, 0.4);
          z-index: 0;
        }
      `}</style>
    </div>
  );
}
