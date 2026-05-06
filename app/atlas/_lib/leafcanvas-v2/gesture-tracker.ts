/**
 * gesture-tracker.ts — module-level singleton that tracks an active
 * pan/zoom gesture and emits a `gestureend` signal after a short
 * debounce window. Sister to idle-tracker, but with a much tighter
 * window because the contract is different:
 *
 *   idle-tracker  →  "user has not touched the canvas for ~500 ms"
 *                    Used to detach IntersectionObservers and stop
 *                    background work.
 *
 *   gesture-tracker → "user is currently mid-pan/mid-zoom"
 *                     Used to defer expensive work (mounting frames,
 *                     swapping <img> URLs across preview tiers, firing
 *                     useImageRefs HTTP calls) so it doesn't compete
 *                     with the wheel→RAF→paint pipeline. Settles after
 *                     ~150 ms with no further pan/zoom activity.
 *
 * Why a dedicated tracker rather than reusing idle-tracker's events:
 * idle-tracker listens to *every* activity event (keydown, click,
 * pointermove…). That's the right set for "should we keep observers
 * armed" — but for "is a zoom in flight right now" we only care about
 * wheel events and pan-drags, and we need a much tighter debounce so
 * lazy hydration kicks back in promptly when the gesture ends.
 *
 * Producers: `leafcanvas.tsx` calls `canvasGestureTracker.tick()` from
 * its wheel handler and from the pointer-drag pan handler.
 *
 * Consumers: `LeafFrameRenderer` (and any other component that
 * mutates state / fires HTTP calls in response to viewport changes)
 * gates on `useIsGesturing()` — when true, queue the work; when the
 * gesture settles, drain. `useIconClusterURLs` reads the *settled*
 * zoom signal so preview-tier transitions don't fire mid-gesture.
 */

import { useSyncExternalStore } from "react";

export type GestureListener = (gesturing: boolean) => void;

/**
 * 150ms matches the @use-gesture / Figma "settled" pattern — long
 * enough that a single trackpad pinch (which fires ~60 small events
 * over ~half a second) is treated as one continuous gesture, short
 * enough that lazy hydration kicks back in before the user notices a
 * stutter when they finish zooming.
 */
export const DEFAULT_GESTURE_END_MS = 150;

/**
 * Pure-logic gesture tracker. `tick()` records ongoing gesture
 * activity; after `endMs` of no further ticks the tracker flips
 * back to "settled" and notifies listeners.
 *
 * Mirrors IdleTracker's shape (injectable clock + timer) so unit
 * tests can exercise the timing without a real browser.
 */
export class GestureTracker {
  private endMs: number;
  private now: () => number;
  private setTimer: (cb: () => void, ms: number) => unknown;
  private clearTimer: (handle: unknown) => void;
  private listeners = new Set<GestureListener>();
  private currentTimer: unknown = null;
  private gesturingFlag = false;

  constructor(
    endMs: number = DEFAULT_GESTURE_END_MS,
    deps?: {
      now?: () => number;
      setTimer?: (cb: () => void, ms: number) => unknown;
      clearTimer?: (handle: unknown) => void;
    },
  ) {
    this.endMs = endMs;
    this.now = deps?.now ?? (() => Date.now());
    this.setTimer = deps?.setTimer ?? ((cb, ms) => globalThis.setTimeout(cb, ms));
    this.clearTimer =
      deps?.clearTimer ??
      ((handle) => {
        if (handle != null) globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>);
      });
  }

  /** True while a gesture is in flight (within the debounce window). */
  get isGesturing(): boolean {
    return this.gesturingFlag;
  }

  /**
   * Mark gesture activity now. If we were settled, listeners are
   * notified that a gesture has begun. The settle timer is restarted.
   */
  tick(): void {
    if (!this.gesturingFlag) {
      this.gesturingFlag = true;
      this.notify();
      // canvas-log import is intentionally inline-deferred — keeping
      // this module dep-free for the unit-test runner.
      if (typeof window !== "undefined" && window.__CANVAS_LOG) {
        // eslint-disable-next-line no-console
        console.log("%c[gesture] begin", "color:#7eb8ff");
      }
    }
    if (this.currentTimer !== null) this.clearTimer(this.currentTimer);
    this.currentTimer = this.setTimer(() => this.flipToSettled(), this.endMs);
  }

  /** Subscribe; returns unsubscribe. */
  subscribe(cb: GestureListener): () => void {
    this.listeners.add(cb);
    return () => {
      this.listeners.delete(cb);
    };
  }

  /** Tear down (used for hot-reload / tests). */
  reset(): void {
    if (this.currentTimer !== null) this.clearTimer(this.currentTimer);
    this.currentTimer = null;
    this.gesturingFlag = false;
    this.listeners.clear();
  }

  private flipToSettled(): void {
    if (!this.gesturingFlag) return;
    this.gesturingFlag = false;
    this.currentTimer = null;
    this.notify();
    if (typeof window !== "undefined" && window.__CANVAS_LOG) {
      // eslint-disable-next-line no-console
      console.log("%c[gesture] settle", "color:#7eb8ff");
    }
  }

  private notify(): void {
    const snapshot = [...this.listeners];
    for (const cb of snapshot) {
      try {
        cb(this.gesturingFlag);
      } catch {
        // Swallow listener errors so a single bad consumer can't sink the rest.
      }
    }
  }
}

/** Shared instance — one per app. Producers (camera-store / leafcanvas) call .tick(). */
export const canvasGestureTracker = new GestureTracker();

/**
 * React hook — re-renders the consumer on gesture begin and gesture
 * end. Use sparingly: most consumers should *gate work* on this
 * (read-then-decide), not subscribe and re-render on every flip.
 */
export function useIsGesturing(): boolean {
  return useSyncExternalStore(
    (cb) => canvasGestureTracker.subscribe(cb),
    () => canvasGestureTracker.isGesturing,
    () => false,
  );
}

/** Imperative read for non-React contexts (IntersectionObserver callbacks). */
export function getIsGesturing(): boolean {
  return canvasGestureTracker.isGesturing;
}
