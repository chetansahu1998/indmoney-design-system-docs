"use client";

/**
 * Phase 9 U1 — leaf-click navigator.
 *
 * The user clicks a flow leaf → BrainGraph dispatches `morphTo(node)` →
 * this layer pushes the route. The visual morph itself happens via the
 * browser-native View Transitions API (CSS `view-transition-name` on the
 * leaf-label DOM in the overlay layer + the project view's title), with
 * Next.js 16.2's `experimental.viewTransition: true` (next.config.ts)
 * auto-wrapping the navigation in `document.startViewTransition()`.
 *
 * What this file used to do (pre-Phase-9): mount a Framer Motion
 * `motion.div` with `layoutId={`flow-${node.id}-label`}` and rely on
 * Framer to animate the layout shift across the route boundary. That
 * pattern does NOT work — Framer's `layoutId` does not bridge Next.js
 * App Router route changes (vercel/next.js#49279, still open 2026), and
 * React 19.2.4 stable does not export the `<ViewTransition>` component
 * (Canary-only). The new contract: leaf labels in the BrainGraph DOM
 * overlay and the ProjectToolbar title carry matching CSS
 * `view-transition-name` values; the browser handles the morph.
 *
 * This component now does one thing: observe `morphingNode` and trigger
 * `router.push` on the flow's URL. No Framer, no rendered output.
 *
 * Reduced-motion + Firefox-default + any browser without View Transitions:
 * instant route swap. The spatial-continuity cue for those users is the
 * static breadcrumb on the project toolbar (U2c), not an outline pulse.
 */

import { useRouter } from "next/navigation";
import { useEffect } from "react";

import type { GraphNode } from "./types";

interface Props {
  node: GraphNode;
  /** Carried for parity with prior callers; the View Transitions CSS
   *  handles reduced-motion via @media (prefers-reduced-motion: reduce)
   *  on the ::view-transition-old/new pseudo-elements (U2b). We don't
   *  branch on this prop here — the route push is the same either way. */
  reducedMotion: boolean;
}

export function LeafMorphHandoff({ node, reducedMotion: _reducedMotion }: Props) {
  const router = useRouter();

  useEffect(() => {
    const url = node.signal.open_url;
    if (!url) return;
    router.push(url);
  }, [node, router]);

  return null;
}
