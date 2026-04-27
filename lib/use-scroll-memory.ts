"use client";

import { useEffect } from "react";
import { usePathname } from "next/navigation";

/**
 * Saves and restores window scroll position per route.
 *
 * Next App Router's default is to scroll to top on every navigation, which
 * is correct for fresh visits but wrong for back/forward — designers expect
 * to return to where they were on /files when bouncing back from
 * /files/[slug]. This hook persists scroll Y in sessionStorage keyed by
 * pathname + hash and restores it on mount.
 *
 * Mounts once at the root layout level. Hash-based scroll-spy still wins
 * for fresh anchor jumps; this hook only kicks in when no hash is present.
 */
export function useScrollMemory() {
  const pathname = usePathname();

  useEffect(() => {
    if (typeof window === "undefined") return;
    const key = `scroll:${pathname}`;

    // Restore on route change (or initial load).
    if (!window.location.hash) {
      const saved = sessionStorage.getItem(key);
      if (saved) {
        const y = parseInt(saved, 10);
        if (!Number.isNaN(y) && y > 0) {
          // Wait for content to render before scrolling — the page tree
          // may still be hydrating. Two rAFs gives layout a chance to
          // settle.
          requestAnimationFrame(() => {
            requestAnimationFrame(() => {
              window.scrollTo({ top: y, behavior: "instant" as ScrollBehavior });
            });
          });
        }
      }
    }

    // Save on scroll (throttled). beforeunload also saves so a refresh
    // mid-scroll doesn't lose position.
    let saveTimer: number | undefined;
    const saveScroll = () => {
      if (saveTimer) window.clearTimeout(saveTimer);
      saveTimer = window.setTimeout(() => {
        sessionStorage.setItem(key, String(window.scrollY));
      }, 120);
    };
    window.addEventListener("scroll", saveScroll, { passive: true });
    window.addEventListener("beforeunload", saveScroll);

    return () => {
      // Final flush — capture wherever the user left off.
      sessionStorage.setItem(key, String(window.scrollY));
      window.removeEventListener("scroll", saveScroll);
      window.removeEventListener("beforeunload", saveScroll);
      if (saveTimer) window.clearTimeout(saveTimer);
    };
  }, [pathname]);
}
