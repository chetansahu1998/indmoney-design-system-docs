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
 * This component now does one thing: observe `morphingNode`, rewrite the
 * /atlas history entry to carry `?from=<slug>` (so Esc/back from the
 * project view re-focuses the source leaf — U3's receiving-end contract),
 * then push the project URL.
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

/**
 * Extract the project slug from a flow node's open_url.
 * Mirrors the regex used by app/atlas/LeafLabelLayer.tsx (U2b).
 * Returns null when the URL doesn't match — caller skips the rewrite.
 */
function extractSlugFromOpenURL(url: string): string | null {
  const m = url.match(/^\/projects\/([^/?#]+)/);
  return m ? m[1] : null;
}

export function LeafMorphHandoff({ node, reducedMotion: _reducedMotion }: Props) {
  const router = useRouter();

  useEffect(() => {
    const url = node.signal.open_url;
    if (!url) return;

    // Phase 9 followup (#72) — write `?from=<slug>` onto the current
    // /atlas history entry BEFORE pushing the project URL. router.back()
    // from the project view returns to /atlas?from=<slug>, which U3's
    // page.tsx + useGraphView.morphFromProject pick up to refocus the
    // source leaf. router.replace mutates the current entry without
    // adding a new one to the stack, so the back-stack stays clean:
    //
    //   before: [ ..., /atlas ]
    //   after replace + push: [ ..., /atlas?from=<slug>, /projects/<slug> ]
    //   on Esc/back: [ ..., /atlas?from=<slug> ]  (current)
    const slug = extractSlugFromOpenURL(url);
    if (slug && typeof window !== "undefined") {
      // Read the current /atlas URL + merge ?from= without dropping
      // existing query params (e.g. ?platform=web that the user may
      // have set in the URL before clicking the leaf).
      const here = new URL(window.location.href);
      if (here.pathname === "/atlas") {
        here.searchParams.set("from", slug);
        router.replace(`${here.pathname}${here.search}`);
      }
    }

    router.push(url);
  }, [node, router]);

  return null;
}
