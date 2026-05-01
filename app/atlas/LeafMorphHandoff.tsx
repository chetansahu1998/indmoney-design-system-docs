"use client";

/**
 * Phase 6 U12 — leaf-morph hand-off to project view.
 *
 * The user clicks a flow leaf → BrainGraph dispatches `morphTo(node)` →
 * this layer renders a Framer Motion `motion.div` with a `layoutId` that
 * matches a target on the project view's title bar. router.push to the
 * flow URL; Framer auto-tweens the layout shift across the route boundary
 * (~600ms cubic-out per R27).
 *
 * In parallel: the r3f scene fades out (handled by BrainGraph via opacity
 * tween on its outer container) and the project view's atlas fades in.
 *
 * Reduced-motion: instant route swap, no morph.
 */

import { motion } from "framer-motion";
import { useRouter } from "next/navigation";
import { useEffect } from "react";

import type { GraphNode } from "./types";

interface Props {
  node: GraphNode;
  reducedMotion: boolean;
}

export function LeafMorphHandoff({ node, reducedMotion }: Props) {
  const router = useRouter();

  useEffect(() => {
    if (!node.signal.open_url) return;
    if (reducedMotion) {
      // Instant route swap. The morph layer still renders for one frame
      // but the route push happens immediately.
      router.push(node.signal.open_url);
      return;
    }
    // Give Framer one render frame to anchor the layoutId before pushing.
    const t = window.setTimeout(() => {
      router.push(node.signal.open_url ?? "");
    }, 50);
    return () => window.clearTimeout(t);
  }, [node, reducedMotion, router]);

  if (reducedMotion) return null;

  return (
    <motion.div
      layoutId={`flow-${node.id}-label`}
      className="morph"
      initial={{ opacity: 1 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.6, ease: [0.22, 1, 0.36, 1] }}
    >
      {node.label}
      <style jsx>{`
        .morph {
          position: fixed;
          top: 50%;
          left: 50%;
          transform: translate(-50%, -50%);
          padding: 12px 24px;
          background: rgba(123, 159, 255, 0.18);
          border: 1px solid rgba(123, 159, 255, 0.4);
          border-radius: 999px;
          color: #ffffff;
          font-family: var(--font-sans, "Inter Variable", sans-serif);
          font-size: 18px;
          font-weight: 600;
          z-index: 30;
          pointer-events: none;
        }
      `}</style>
    </motion.div>
  );
}
