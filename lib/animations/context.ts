"use client";

/**
 * Animation context — singletons + reduced-motion detection.
 *
 * Responsibilities:
 *   1. Register GSAP plugins exactly once on first import (ScrollTrigger).
 *   2. Provide a Lenis singleton with a single rAF loop owned by the lib.
 *   3. Expose `useReducedMotion()` for components/timelines to short-circuit.
 *   4. Expose `useLenisProvider()` for the app shell to mount the singleton
 *      once (e.g. in a top-level layout client component).
 *
 * Bundle posture:
 *   - We import GSAP core via `import gsap from "gsap"` and the ScrollTrigger
 *     plugin via subpath so other consumers get treeshaken bundles.
 *   - Lenis 1.x is ~10KB gz; GSAP core+ScrollTrigger ~30KB gz. Total fits the
 *     `chunks/animations` ≤50KB budget documented in the Phase 1 plan.
 *
 * Cleanup model (read this if you touch this file):
 *   - The Lenis singleton is created lazily on first `useLenisProvider()` call
 *     and destroyed when the last provider unmounts (refcount). The rAF loop
 *     is the property of Lenis itself — we drive it by calling
 *     `lenis.raf(time)` from a single rAF chain. We never start a second loop.
 *   - Under `prefers-reduced-motion: reduce` Lenis is destroyed entirely so
 *     native scroll behaviour applies; no scroll virtualization happens.
 */

import { useEffect, useState, useSyncExternalStore } from "react";
import gsap from "gsap";
import { ScrollTrigger } from "gsap/ScrollTrigger";
import Lenis from "lenis";

// ---------------------------------------------------------------------------
// One-time plugin registration. Module-level so it runs on first import only.
// ---------------------------------------------------------------------------
let pluginsRegistered = false;
function registerPluginsOnce(): void {
  if (pluginsRegistered) return;
  if (typeof window === "undefined") return; // SSR no-op
  gsap.registerPlugin(ScrollTrigger);
  pluginsRegistered = true;
}

// Side-effect on import (browser only); idempotent.
registerPluginsOnce();

// ---------------------------------------------------------------------------
// Reduced-motion detection
// ---------------------------------------------------------------------------

const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

/**
 * SSR-safe check; defaults to `false` outside the browser so server-rendered
 * markup matches the optimistic "animations enabled" state. Hydration corrects
 * it via `useReducedMotion`.
 */
export function getPrefersReducedMotion(): boolean {
  if (typeof window === "undefined") return false;
  if (typeof window.matchMedia !== "function") return false;
  return window.matchMedia(REDUCED_MOTION_QUERY).matches;
}

/**
 * `useReducedMotion()` — subscribes to `prefers-reduced-motion: reduce`.
 * Returns `true` when the user has explicitly opted out of motion.
 *
 * Pattern follows lib/use-mobile.ts (typed hook, default false on SSR).
 */
export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState<boolean>(false);

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (typeof window.matchMedia !== "function") return;
    const mq = window.matchMedia(REDUCED_MOTION_QUERY);
    setReduced(mq.matches);
    const handler = (e: MediaQueryListEvent) => setReduced(e.matches);
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, []);

  return reduced;
}

// ---------------------------------------------------------------------------
// Lenis singleton (refcounted)
// ---------------------------------------------------------------------------

let lenisInstance: Lenis | null = null;
let lenisRefs = 0;
let rafId: number | null = null;

const lenisListeners = new Set<() => void>();
function emitLenisChanged(): void {
  for (const l of lenisListeners) l();
}

function startRafLoop(): void {
  if (rafId !== null) return;
  const tick = (time: number) => {
    if (lenisInstance) {
      // Lenis expects ms timestamps from rAF.
      lenisInstance.raf(time);
      rafId = window.requestAnimationFrame(tick);
    } else {
      rafId = null;
    }
  };
  rafId = window.requestAnimationFrame(tick);
}

function stopRafLoop(): void {
  if (rafId !== null) {
    window.cancelAnimationFrame(rafId);
    rafId = null;
  }
}

function ensureLenis(): Lenis | null {
  if (typeof window === "undefined") return null;
  if (getPrefersReducedMotion()) return null;
  if (lenisInstance) return lenisInstance;
  lenisInstance = new Lenis({
    // Conservative defaults — feel similar to native momentum on macOS.
    duration: 1.0,
    smoothWheel: true,
  });
  startRafLoop();
  emitLenisChanged();
  return lenisInstance;
}

function teardownLenis(): void {
  stopRafLoop();
  if (lenisInstance) {
    lenisInstance.destroy();
    lenisInstance = null;
    emitLenisChanged();
  }
}

/** Internal accessor — exported for `useLenis` hook. */
export function getLenis(): Lenis | null {
  return lenisInstance;
}

/** Internal subscription — exported for `useLenis` hook. */
export function subscribeLenis(listener: () => void): () => void {
  lenisListeners.add(listener);
  return () => lenisListeners.delete(listener);
}

/**
 * `useLenisProvider()` — mount this once at the app shell to opt into the
 * Lenis smooth-scroll singleton. Refcounted: nested calls are safe and only
 * the last unmount tears the instance down.
 *
 * Disabled entirely under `prefers-reduced-motion: reduce` (no instance, no
 * rAF loop) so native scroll behaviour applies.
 */
export function useLenisProvider(): Lenis | null {
  const reduced = useReducedMotion();
  const lenis = useSyncExternalStore(
    subscribeLenis,
    () => lenisInstance,
    () => null, // SSR snapshot
  );

  useEffect(() => {
    if (reduced) {
      // Reduced-motion: ensure no instance is alive.
      if (lenisInstance && lenisRefs === 0) {
        teardownLenis();
      }
      return;
    }
    lenisRefs += 1;
    ensureLenis();
    return () => {
      lenisRefs = Math.max(0, lenisRefs - 1);
      if (lenisRefs === 0) {
        teardownLenis();
      }
    };
  }, [reduced]);

  return lenis;
}
