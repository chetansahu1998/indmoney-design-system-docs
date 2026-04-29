"use client";

/**
 * `useGSAPContext` — component-scoped GSAP context with automatic cleanup.
 *
 * Why this hook exists:
 *   React 19 StrictMode mounts effects twice in dev. Without `gsap.context()`
 *   any tween created in `useEffect` would either run twice or leak across
 *   re-mounts. `gsap.context(fn, scope)` collects every tween/timeline/
 *   ScrollTrigger created inside `fn` (or registered later via `ctx.add`)
 *   and `ctx.revert()` reverses + kills all of them. We call `revert()` on
 *   unmount so the second StrictMode mount starts fresh.
 *
 * Usage:
 *   const ref = useRef<HTMLDivElement>(null);
 *   const ctx = useGSAPContext(ref);
 *   useEffect(() => {
 *     ctx?.add(() => {
 *       gsap.from(".child", { opacity: 0, duration: 0.4 });
 *     });
 *   }, [ctx]);
 *
 * The hook is SSR-safe (returns null on server). On the client, the context
 * is created on first effect run and revert()ed on unmount.
 */

import { type RefObject, useEffect, useRef, useState } from "react";
import gsap from "gsap";

/**
 * Returns a GSAP `Context` instance scoped to the given ref's element, or
 * null on the server / before mount. The context is reverted on unmount.
 */
export function useGSAPContext(
  scope: RefObject<HTMLElement | null>,
): gsap.Context | null {
  const [ctx, setCtx] = useState<gsap.Context | null>(null);
  // Stable ref to the latest context for cleanup-on-unmount even if the ref
  // element identity changes mid-life.
  const ctxRef = useRef<gsap.Context | null>(null);

  useEffect(() => {
    const el = scope.current;
    if (!el) return;
    const created = gsap.context(() => {
      // No-op default body — consumers register tweens via `created.add(...)`.
    }, el);
    ctxRef.current = created;
    setCtx(created);
    return () => {
      created.revert();
      if (ctxRef.current === created) {
        ctxRef.current = null;
      }
      setCtx(null);
    };
    // We intentionally re-run only when the ref element identity changes;
    // most refs are stable for a component's lifetime so this runs once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scope.current]);

  return ctx;
}
