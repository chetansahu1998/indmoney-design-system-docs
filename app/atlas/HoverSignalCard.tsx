"use client";

/**
 * Phase 6 U9 — floating signal card on node hover.
 *
 * Anchored near the cursor with screen-edge clamping. Surfaces type, parent
 * path, severity counts (Critical / High / Medium / Low / Info), persona
 * count, last-edited, and the "Open project →" CTA when the node has an
 * `open_url` (flows + decisions).
 *
 * No portal — rendered next to the canvas as a positioned absolute element
 * so it composes cleanly with Framer's layoutId animations.
 */

import { motion } from "framer-motion";

import type { GraphNode, GraphSeverityCounts } from "./types";

interface Props {
  node: GraphNode;
  anchor: { x: number; y: number };
}

export function HoverSignalCard({ node, anchor }: Props) {
  // Clamp to viewport: card is 280px × ~140px; flip to left of cursor when
  // we'd overflow the right edge.
  const cardW = 280;
  const cardH = 160;
  const margin = 12;
  let left = anchor.x + 16;
  let top = anchor.y + 16;
  if (typeof window !== "undefined") {
    if (left + cardW > window.innerWidth - margin) {
      left = anchor.x - cardW - 16;
    }
    if (top + cardH > window.innerHeight - margin) {
      top = anchor.y - cardH - 16;
    }
  }

  return (
    <motion.aside
      className="card"
      role="dialog"
      aria-label={`${node.type} signal: ${node.label}`}
      data-testid="hover-signal-card"
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.15 }}
      style={{ left, top }}
    >
      <header>
        <span className={`type type-${node.type}`}>{node.type}</span>
        <h2>{node.label}</h2>
      </header>
      <SeverityRow counts={node.signal.severity_counts} />
      <dl>
        {node.signal.persona_count > 0 && (
          <Row k="Personas" v={String(node.signal.persona_count)} />
        )}
        {node.signal.last_editor && (
          <Row k="Last editor" v={node.signal.last_editor} />
        )}
        {node.signal.last_updated_at && (
          <Row k="Updated" v={formatRelative(node.signal.last_updated_at)} />
        )}
      </dl>
      {node.signal.open_url && (
        <a className="cta" href={node.signal.open_url}>
          Open project →
        </a>
      )}
      <CardStyles />
    </motion.aside>
  );
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <>
      <dt>{k}</dt>
      <dd>{v}</dd>
    </>
  );
}

function SeverityRow({ counts }: { counts: GraphSeverityCounts }) {
  const total =
    counts.critical + counts.high + counts.medium + counts.low + counts.info;
  if (total === 0) {
    return <div className="sev-empty">No active violations</div>;
  }
  const tier = (n: number, color: string, label: string) =>
    n > 0 && (
      <span className="sev-tier" style={{ color }}>
        <span className="dot" style={{ background: color }} />
        {label} {n}
      </span>
    );
  return (
    <div className="sev">
      {tier(counts.critical, "#FF6B6B", "Critical")}
      {tier(counts.high, "#FFB347", "High")}
      {tier(counts.medium, "#FFD93D", "Medium")}
      {tier(counts.low, "#9F8FFF", "Low")}
      {tier(counts.info, "#7B9FFF", "Info")}
    </div>
  );
}

function formatRelative(ts: string): string {
  try {
    const d = new Date(ts);
    const diff = Date.now() - d.getTime();
    const sec = Math.round(diff / 1000);
    if (sec < 60) return `${sec}s ago`;
    const min = Math.round(sec / 60);
    if (min < 60) return `${min}m ago`;
    const hr = Math.round(min / 60);
    if (hr < 24) return `${hr}h ago`;
    const day = Math.round(hr / 24);
    return `${day}d ago`;
  } catch {
    return ts;
  }
}

function CardStyles() {
  return (
    <style jsx>{`
      .card {
        position: fixed;
        width: 280px;
        padding: 14px 16px;
        border-radius: 12px;
        border: 1px solid rgba(255, 255, 255, 0.08);
        background: rgba(10, 14, 24, 0.92);
        backdrop-filter: blur(12px);
        color: rgba(255, 255, 255, 0.92);
        font-family: var(--font-sans, "Inter Variable", sans-serif);
        font-size: 12px;
        z-index: 20;
        pointer-events: auto;
      }
      .card header {
        display: flex;
        flex-direction: column;
        gap: 4px;
        margin-bottom: 10px;
      }
      .type {
        font-size: 10px;
        text-transform: uppercase;
        letter-spacing: 0.08em;
        color: rgba(255, 255, 255, 0.5);
        font-weight: 600;
      }
      .type-product {
        color: #c8d6ff;
      }
      .type-flow {
        color: #9f8fff;
      }
      .type-decision {
        color: #ffb347;
      }
      h2 {
        margin: 0;
        font-size: 14px;
        font-weight: 600;
      }
      .sev {
        display: flex;
        flex-wrap: wrap;
        gap: 6px;
        margin-bottom: 10px;
      }
      .sev-empty {
        color: rgba(255, 255, 255, 0.42);
        font-size: 11px;
        margin-bottom: 10px;
      }
      .sev-tier {
        display: inline-flex;
        align-items: center;
        gap: 4px;
        font-size: 11px;
      }
      .dot {
        width: 6px;
        height: 6px;
        border-radius: 50%;
      }
      dl {
        display: grid;
        grid-template-columns: 80px 1fr;
        gap: 4px 8px;
        margin: 0 0 12px;
        font-size: 11px;
      }
      dt {
        color: rgba(255, 255, 255, 0.42);
      }
      dd {
        margin: 0;
        color: rgba(255, 255, 255, 0.85);
      }
      .cta {
        display: inline-block;
        color: #7b9fff;
        font-size: 12px;
        text-decoration: none;
      }
      .cta:hover {
        text-decoration: underline;
      }
    `}</style>
  );
}
