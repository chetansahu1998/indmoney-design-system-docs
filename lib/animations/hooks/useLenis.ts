"use client";

/**
 * `useLenis` — read access to the Lenis singleton + a smart `scrollTo` helper.
 *
 * Returns `null` when:
 *   - SSR (no window).
 *   - User has `prefers-reduced-motion: reduce` (Lenis is intentionally not
 *     instantiated; native scroll applies).
 *   - The app shell hasn't mounted `useLenisProvider()` yet.
 *
 * `scrollTo(target, opts)` smoothly scrolls when Lenis is available; falls
 * back to instant `window.scrollTo`/`element.scrollIntoView` when reduced
 * motion is on or Lenis isn't ready.
 */

import { useCallback, useSyncExternalStore } from "react";
import type Lenis from "lenis";
import { getLenis, getPrefersReducedMotion, subscribeLenis } from "../context";

export interface ScrollToOptions {
  /** Pixel offset added to the target position. */
  offset?: number;
  /** Override smoothness — default is `false` under reduced-motion. */
  immediate?: boolean;
  /** Lenis duration override (seconds). */
  duration?: number;
}

export interface UseLenisReturn {
  /** The Lenis instance, or null when unavailable. */
  lenis: Lenis | null;
  /** Smooth-scroll to a target — falls back to native instant scroll. */
  scrollTo: (
    target: number | string | HTMLElement,
    opts?: ScrollToOptions,
  ) => void;
}

export function useLenis(): UseLenisReturn {
  const lenis = useSyncExternalStore(
    subscribeLenis,
    () => getLenis(),
    () => null,
  );

  const scrollTo = useCallback(
    (target: number | string | HTMLElement, opts: ScrollToOptions = {}) => {
      const reduced = getPrefersReducedMotion();
      const immediate = opts.immediate ?? reduced;

      const instance = getLenis();
      if (instance && !immediate) {
        instance.scrollTo(target, {
          offset: opts.offset,
          duration: opts.duration,
        });
        return;
      }

      // Native fallback — instant under reduced motion or when Lenis is null.
      if (typeof window === "undefined") return;
      if (typeof target === "number") {
        window.scrollTo({
          top: target + (opts.offset ?? 0),
          behavior: immediate ? "auto" : "smooth",
        });
        return;
      }
      const el =
        typeof target === "string"
          ? (document.querySelector(target) as HTMLElement | null)
          : target;
      if (!el) return;
      const top = el.getBoundingClientRect().top + window.scrollY + (opts.offset ?? 0);
      window.scrollTo({
        top,
        behavior: immediate ? "auto" : "smooth",
      });
    },
    [],
  );

  return { lenis, scrollTo };
}
