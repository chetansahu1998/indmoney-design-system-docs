"use client";

import { useEffect } from "react";
import { usePathname } from "next/navigation";
import { useUIStore } from "@/lib/ui-store";

/**
 * Single source of truth for the "which section is active" question.
 *
 * Used by every shell (DocsShell, FilesShell, future shells) so the pill
 * animation, sidebar highlight, and URL hash all stay in lockstep.
 *
 * Behavior:
 *   • Resets activeSection on every route change so state from one page
 *     can't leak onto the next (the sidebar pill used to stay stuck on
 *     "color-surface" after navigating to /icons because the store was
 *     persistent).
 *   • At scroll=0 with no IntersectionObserver hit, picks the first
 *     section in sectionIds that exists in the DOM as the initial active
 *     entry. Without this the rootMargin band misses the top-of-page case
 *     and the pill defaults to whatever the store last held.
 *   • Throttles history.replaceState so micro-scrolls don't thrash the
 *     URL bar. Only updates when the active id actually changes.
 *   • Hash restore is gated on the hash matching one of sectionIds for
 *     the current shell; a stale hash from another route is ignored
 *     instead of triggering a getElementById on a non-existent id.
 *   • In dev, warns when the sidebar references anchor ids that don't
 *     exist in the DOM — catches nav/DOM drift early.
 */
export function useActiveSection(sectionIds: string[]) {
  const setActiveSection = useUIStore((s) => s.setActiveSection);
  const pathname = usePathname();

  // Reset active state on route change so the previous page's pill
  // position can't bleed into the new shell.
  useEffect(() => {
    setActiveSection("");
  }, [pathname, setActiveSection]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (sectionIds.length === 0) return;

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

    // Initial highlight: pick the first section that exists in the DOM.
    // The IntersectionObserver may not fire at scroll=0 (its rootMargin
    // band excludes the top of the viewport), so without this seed the
    // sidebar pill is invisible until the user scrolls.
    const initialActive = sectionIds.find((id) => document.getElementById(id));
    if (initialActive) {
      setActiveSection(initialActive);
    }

    let lastActive = initialActive ?? "";

    const obs = new IntersectionObserver(
      (entries) => {
        const visible = entries.filter((e) => e.isIntersecting);
        if (visible.length === 0) return;
        visible.sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        const id = visible[0].target.id;
        if (id === lastActive) return; // dedup — no-op when nothing changed
        lastActive = id;
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

  // Restore scroll position from URL hash on first mount, but only when
  // the hash references a section the current shell actually has. A stale
  // #color-surface from a Foundations visit shouldn't trigger anything
  // on /icons.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const hash = window.location.hash.replace("#", "");
    if (!hash || !sectionIds.includes(hash)) return;
    requestAnimationFrame(() => {
      const el = document.getElementById(hash);
      if (el) el.scrollIntoView({ behavior: "instant" as ScrollBehavior, block: "start" });
    });
    // sectionIds intentionally excluded from deps — restoration runs once
    // per route mount, not on every sectionIds change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}
