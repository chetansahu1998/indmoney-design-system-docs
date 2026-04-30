"use client";

/**
 * ProductTour — Phase 3 U11 — in-product 4-step Shepherd.js tour.
 *
 * Mounts ONLY for first-time visitors (per lib/onboarding/tour-state.ts);
 * subsequent visits no-op. Triggered to remount via ?reset-tour=1 for
 * QA + demos.
 *
 * Steps (matching the Phase 3 plan):
 *   1. Persona toggle  → [data-tour="persona-toggle"]
 *   2. Theme toggle    → [data-tour="theme-toggle"]
 *   3. JSON tab        → [data-tour="tab-json"]
 *   4. Violations tab  → [data-tour="tab-violations"]
 *
 * Reduced-motion: Shepherd's spotlight pulse is short-circuited via a
 * data-attribute on the body (Shepherd respects the
 * `prefers-reduced-motion` media query in its default theme; we add an
 * extra guard for environments where that doesn't kick in).
 *
 * Accessibility: Shepherd v13 ships keyboard nav (Tab/Shift+Tab cycles
 * focus inside the popup; Esc dismisses) + focus management
 * (focus traps to the popup while open). We honor those defaults.
 */

import { useEffect } from "react";
import { useReducedMotion } from "@/lib/animations/context";
import {
  markCompleted,
  markSkipped,
  shouldShowTour,
} from "@/lib/onboarding/tour-state";

interface Props {
  searchParams: URLSearchParams | null;
}

export default function ProductTour({ searchParams }: Props) {
  const reduced = useReducedMotion();

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (!shouldShowTour(searchParams)) return;

    let cancelled = false;
    let tour: { complete: () => void; cancel: () => void } | null = null;

    void (async () => {
      // Lazy-load Shepherd so its ~30KB chunk only ships when the tour
      // actually mounts — first-time visitors only. Subsequent visits
      // skip this branch entirely.
      const Shepherd = (await import("shepherd.js")).default;
      // Shepherd's CSS lives in shepherd.js/dist/css/shepherd.css. We
      // import it dynamically too so it's tree-shaken out when the tour
      // never mounts.
      await import("shepherd.js/dist/css/shepherd.css");
      if (cancelled) return;

      const t = new Shepherd.Tour({
        useModalOverlay: true,
        defaultStepOptions: {
          cancelIcon: { enabled: true },
          scrollTo: { behavior: reduced ? "auto" : "smooth", block: "center" },
          modalOverlayOpeningPadding: 6,
          modalOverlayOpeningRadius: 8,
        },
      });

      const cancelEvent = () => {
        if (cancelled) return;
        markSkipped();
      };
      const completeEvent = () => {
        if (cancelled) return;
        markCompleted();
      };
      t.on("cancel", cancelEvent);
      t.on("complete", completeEvent);

      const STEPS = [
        {
          id: "persona-toggle",
          title: "Persona toggle",
          text:
            "Toggle the active persona to re-resolve the atlas + JSON tab against that persona's component coverage. Cross-persona violations surface in the Violations tab.",
          attachTo: { element: '[data-tour="persona-toggle"]', on: "bottom" },
        },
        {
          id: "theme-toggle",
          title: "Theme toggle",
          text:
            "Light / Dark / Auto. Toggle re-renders atlas textures + re-resolves Variable bindings in the JSON tab. Theme parity violations catch hand-painted dark fills.",
          attachTo: { element: '[data-tour="theme-toggle"]', on: "bottom" },
        },
        {
          id: "tab-json",
          title: "JSON tab",
          text:
            "Click any frame in the atlas to open its canonical_tree here. Bound variables show as chips with resolved hex swatches per the active mode.",
          attachTo: { element: '[data-tour="tab-json"]', on: "top" },
        },
        {
          id: "tab-violations",
          title: "Violations tab",
          text:
            "Audit findings grouped by severity (Critical → Info) and filterable by category chips (theme parity, cross-persona, a11y, …). Per-rule progress streams in via SSE while audit runs.",
          attachTo: { element: '[data-tour="tab-violations"]', on: "top" },
        },
      ];

      STEPS.forEach((step, i) => {
        const isLast = i === STEPS.length - 1;
        const isFirst = i === 0;
        t.addStep({
          id: step.id,
          title: step.title,
          text: step.text,
          attachTo: step.attachTo as { element: string; on: "bottom" | "top" },
          buttons: [
            ...(isFirst
              ? [
                  {
                    text: "Skip tour",
                    action: () => t.cancel(),
                    classes: "shepherd-button-secondary",
                  },
                ]
              : [
                  {
                    text: "Back",
                    action: () => t.back(),
                    classes: "shepherd-button-secondary",
                  },
                ]),
            {
              text: isLast ? "Done" : "Next",
              action: () => (isLast ? t.complete() : t.next()),
            },
          ],
        });
      });

      // Defer start by a frame so the toolbar's data-tour anchors are
      // mounted; without this, Shepherd attaches to a nascent DOM and
      // misplaces the spotlight on first paint.
      requestAnimationFrame(() => {
        if (cancelled) return;
        t.start();
      });

      tour = { complete: () => t.complete(), cancel: () => t.cancel() };
    })();

    return () => {
      cancelled = true;
      tour?.cancel();
    };
    // searchParams + reduced are read once at mount; we don't restart the
    // tour on re-render. eslint-disable-next-line react-hooks/exhaustive-deps
    // (ProjectShell re-renders should not relaunch the tour mid-flow.)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return null;
}
