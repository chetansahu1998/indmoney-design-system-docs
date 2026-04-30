/**
 * Project-shell page-open timeline.
 *
 * Per the Phase 1 plan's "Animation timeline sketch (project view open)":
 *
 *   0ms     toolbar.opacity=0  y=-12          (initial)
 *   0ms     atlas-canvas.opacity=0
 *   0ms     tab-strip.opacity=0  y=8
 *   0ms     tab-content.opacity=0
 *
 *   100ms   toolbar { opacity:1, y:0 }              ease=expo.out  duration=400ms
 *   300ms   atlas-canvas { opacity:1 }              ease=cubic.out duration=500ms
 *   300ms   atlas-frames stagger { opacity:1, y:0 } ease=back.out(1.2) per-frame=80ms (max 600ms total)
 *   500ms   tab-strip { opacity:1, y:0 }            ease=cubic.out duration=400ms
 *   600ms   tab-content { opacity:1 }               ease=cubic.out duration=300ms
 *
 *   Total: ~900ms (cinematic but not slow).
 *
 * Reduced-motion: short-circuits — every target is set to its final state via
 * `gsap.set` with no duration; the returned timeline is empty (still a valid
 * `gsap.core.Timeline` so callers can `.play()` / `.kill()` uniformly).
 *
 * Targets are looked up inside `scope` via `data-anim-*` attributes:
 *
 *   [data-anim="toolbar"]
 *   [data-anim="atlas-canvas"]
 *   [data-anim="atlas-frame"]    (zero or more — staggered)
 *   [data-anim="tab-strip"]
 *   [data-anim="tab-content"]
 *
 * Caller is responsible for `.play()` (timeline returns paused) and for
 * killing it when their component unmounts (use `useGSAPContext` for auto-
 * cleanup in React components).
 */

import gsap from "gsap";
import {
  EASE_HOVER,
  EASE_PAGE_OPEN,
  EASE_THEME_TOGGLE,
  STAGGER_MAX_MS,
  STAGGER_PER_FRAME_MS,
} from "../easings";
import { getPrefersReducedMotion } from "../context";

const SEL = {
  toolbar: '[data-anim="toolbar"]',
  atlasCanvas: '[data-anim="atlas-canvas"]',
  atlasFrame: '[data-anim="atlas-frame"]',
  tabStrip: '[data-anim="tab-strip"]',
  tabContent: '[data-anim="tab-content"]',
} as const;

/** Final-state property maps reused for both reduced-motion and full timelines. */
const FINAL = {
  toolbar: { opacity: 1, y: 0 },
  atlasCanvas: { opacity: 1 },
  atlasFrame: { opacity: 1, y: 0 },
  tabStrip: { opacity: 1, y: 0 },
  tabContent: { opacity: 1 },
} as const;

/** Initial-state property maps — applied at timeline start. */
const INITIAL = {
  toolbar: { opacity: 0, y: -12 },
  atlasCanvas: { opacity: 0 },
  atlasFrame: { opacity: 0, y: 12 },
  tabStrip: { opacity: 0, y: 8 },
  tabContent: { opacity: 0 },
} as const;

/**
 * Returns a paused `gsap.core.Timeline`. Caller plays / scrubs / kills.
 *
 * Under `prefers-reduced-motion: reduce` we don't animate at all — we set
 * every target to its final state synchronously and return an empty paused
 * timeline so the calling code path stays uniform.
 */
export function projectShellOpen(scope: HTMLElement): gsap.core.Timeline {
  const tl = gsap.timeline({ paused: true });

  if (getPrefersReducedMotion()) {
    // Snap to final state — no animation.
    const toolbar = scope.querySelector(SEL.toolbar);
    const atlasCanvas = scope.querySelector(SEL.atlasCanvas);
    const atlasFrames = scope.querySelectorAll(SEL.atlasFrame);
    const tabStrip = scope.querySelector(SEL.tabStrip);
    const tabContent = scope.querySelector(SEL.tabContent);

    if (toolbar) gsap.set(toolbar, FINAL.toolbar);
    if (atlasCanvas) gsap.set(atlasCanvas, FINAL.atlasCanvas);
    if (atlasFrames.length > 0) gsap.set(atlasFrames, FINAL.atlasFrame);
    if (tabStrip) gsap.set(tabStrip, FINAL.tabStrip);
    if (tabContent) gsap.set(tabContent, FINAL.tabContent);
    return tl;
  }

  // Full motion path.
  const toolbar = scope.querySelector(SEL.toolbar);
  const atlasCanvas = scope.querySelector(SEL.atlasCanvas);
  const atlasFrames = scope.querySelectorAll(SEL.atlasFrame);
  const tabStrip = scope.querySelector(SEL.tabStrip);
  const tabContent = scope.querySelector(SEL.tabContent);

  // Initial state at t=0 — synchronous, ensures no flash even before play().
  if (toolbar) tl.set(toolbar, INITIAL.toolbar, 0);
  if (atlasCanvas) tl.set(atlasCanvas, INITIAL.atlasCanvas, 0);
  if (atlasFrames.length > 0) tl.set(atlasFrames, INITIAL.atlasFrame, 0);
  if (tabStrip) tl.set(tabStrip, INITIAL.tabStrip, 0);
  if (tabContent) tl.set(tabContent, INITIAL.tabContent, 0);

  // 100ms → toolbar reveal — snappy mhdyousuf cadence.
  if (toolbar) {
    tl.to(
      toolbar,
      { ...FINAL.toolbar, duration: 0.4, ease: EASE_PAGE_OPEN },
      0.1,
    );
  }

  // 300ms → atlas canvas fades in.
  if (atlasCanvas) {
    tl.to(
      atlasCanvas,
      { ...FINAL.atlasCanvas, duration: 0.5, ease: EASE_THEME_TOGGLE },
      0.3,
    );
  }

  // 300ms → atlas frames staggered. Per-frame 80ms, total clamped to 600ms.
  if (atlasFrames.length > 0) {
    const perFrameSec = STAGGER_PER_FRAME_MS / 1000;
    const maxStaggerSec = STAGGER_MAX_MS / 1000;
    const desiredTotal = atlasFrames.length * perFrameSec;
    const totalStagger = Math.min(desiredTotal, maxStaggerSec);
    // GSAP's `stagger.amount` distributes total across all elements.
    tl.to(
      atlasFrames,
      {
        ...FINAL.atlasFrame,
        duration: 0.4,
        ease: EASE_HOVER,
        stagger: { amount: totalStagger, from: "start" },
      },
      0.3,
    );
  }

  // 500ms → tab-strip slides in.
  if (tabStrip) {
    tl.to(
      tabStrip,
      { ...FINAL.tabStrip, duration: 0.4, ease: EASE_THEME_TOGGLE },
      0.5,
    );
  }

  // 600ms → tab-content fades in.
  if (tabContent) {
    tl.to(
      tabContent,
      { ...FINAL.tabContent, duration: 0.3, ease: EASE_THEME_TOGGLE },
      0.6,
    );
  }

  return tl;
}
