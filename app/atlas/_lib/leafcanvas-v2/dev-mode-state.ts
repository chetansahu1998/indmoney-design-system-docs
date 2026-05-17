/**
 * dev-mode-state.ts — module-level boolean signal for the global
 * Dev Mode render flag (U3 + U9).
 *
 * U3 (this unit) wires the Shift+D hotkey to toggle this flag. U9
 * adds the visual paint logic that reads it: when on, autolayout
 * frames render padding/gap fills always (independent of hover);
 * TEXT nodes render with baseline guides; image fills show their
 * constraint mode (FILL / FIT / STRETCH) as a corner annotation.
 *
 * Why module-level (not Zustand or a React context): the flag is
 * read on every chrome-layer rAF tick (when Dev Mode is on, the
 * chrome layer iterates spatial-store entries to paint per-frame
 * annotations). A Zustand subscriber would re-render every component
 * that consumes the flag on every toggle; module-level pub/sub with
 * useSyncExternalStore only re-renders subscribers when the value
 * changes (twice per session — once on enable, once on disable).
 *
 * Same pattern as hover-signal.ts and camera-state.ts.
 */

import { useSyncExternalStore } from "react";

let value = false;
const listeners = new Set<() => void>();

/**
 * Producer (keymap Shift+D handler) toggles the flag. Dedup is
 * value-equality — calling setDevMode(true) twice in a row is a no-op.
 */
export function setDevMode(next: boolean): void {
  if (value === next) return;
  value = next;
  notify();
}

/** Convenience — flip current value. Used by Shift+D handler. */
export function toggleDevMode(): void {
  setDevMode(!value);
}

/** Imperative read for non-React consumers (chrome-layer paint loop). */
export function getDevMode(): boolean {
  return value;
}

/** Imperative subscribe; returns unsubscribe. */
export function subscribeDevMode(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

/** React hook — re-renders on toggle. */
export function useDevMode(): boolean {
  return useSyncExternalStore(subscribeDevMode, getDevMode, () => false);
}

/** Test reset — clear value + listeners. */
export function __resetDevModeForTesting(): void {
  value = false;
  listeners.clear();
}

function notify(): void {
  const snapshot = [...listeners];
  for (const fn of snapshot) {
    try {
      fn();
    } catch {
      // Swallow listener errors so a single bad consumer can't sink the rest.
    }
  }
}

// ─── HMR / test re-evaluation guard ────────────────────────────────────
declare global {
  // eslint-disable-next-line no-var
  var __lcDevModeStateWired: boolean | undefined;
}
if (!globalThis.__lcDevModeStateWired) {
  globalThis.__lcDevModeStateWired = true;
}
