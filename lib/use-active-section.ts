"use client";

import { useEffect } from "react";
import { useUIStore } from "@/lib/ui-store";

/**
 * Single source of truth for the "which section is active" question.
 *
 * Used by every shell (DocsShell, FilesShell, future shells) so the pill
 * animation, sidebar highlight, and URL hash all stay in lockstep. Before
 * this hook, DocsShell and FilesShell had two near-identical IntersectionObserver
 * blocks that drifted out of sync — most visibly, the pill on the active
 * sidebar entry only animated correctly on Foundations because the layoutId
 * was implicit and the duplicated observers raced.
 *
 * Args:
 *   sectionIds — every anchor id the sidebar references. The hook reads the
 *                DOM, observes only ids that actually exist, and (in dev)
 *                warns when an id is in the list but not in the DOM. That
 *                catches the most common nav-DOM drift bug: nav adds an
 *                anchor before the section component renders one with the
 *                matching id.
 */
export function useActiveSection(sectionIds: string[]) {
  const setActiveSection = useUIStore((s) => s.setActiveSection);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const els = sectionIds
      .map((id) => document.getElementById(id))
      .filter(Boolean) as HTMLElement[];

    if (process.env.NODE_ENV !== "production") {
      const present = new Set(els.map((e) => e.id));
      const missing = sectionIds.filter((id) => !present.has(id));
      if (missing.length > 0) {
        console.warn(
          "[useActiveSection] sidebar references anchor ids that do not exist in the DOM:",
          missing,
        );
      }
    }

    if (els.length === 0) return;

    const obs = new IntersectionObserver(
      (entries) => {
        const visible = entries.filter((e) => e.isIntersecting);
        if (visible.length === 0) return;
        visible.sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        const id = visible[0].target.id;
        setActiveSection(id);
        if (window.location.hash !== `#${id}`) {
          window.history.replaceState(null, "", `#${id}`);
        }
      },
      { rootMargin: "-15% 0px -75% 0px" },
    );
    els.forEach((el) => obs.observe(el));
    return () => obs.disconnect();
  }, [sectionIds, setActiveSection]);

  // Restore scroll position from URL hash on first mount.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const hash = window.location.hash.replace("#", "");
    if (!hash) return;
    requestAnimationFrame(() => {
      const el = document.getElementById(hash);
      if (el) el.scrollIntoView({ behavior: "instant" as ScrollBehavior, block: "start" });
    });
  }, []);
}
