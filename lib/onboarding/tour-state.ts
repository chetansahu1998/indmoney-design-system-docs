"use client";

/**
 * lib/onboarding/tour-state.ts — Phase 3 U11 — first-time-visitor
 * detection + tour persistence.
 *
 * Persistence model:
 *   - localStorage: "indmoney-projects-tour" = "completed" | "skipped"
 *     | undefined. Read on mount; written on dismiss / completion.
 *   - cookie: same key, same value. Phase 3 wires both because Phase 7
 *     SSR can read the cookie to decide whether to inline-render the
 *     tour-mount div without a client round-trip. Today only the local
 *     storage path is consulted; the cookie write is forward-looking.
 *
 * QA reset: append `?reset-tour=1` to any URL → tour-state cleared on
 * mount + tour mounts again.
 *
 * SSR safety: every accessor checks `typeof window`. The tour mount
 * point lives inside ProjectShell which is already client-only.
 */

const STORAGE_KEY = "indmoney-projects-tour";

export type TourState = "completed" | "skipped" | "unseen";

/** Read the tour state. SSR-safe (returns "unseen" when window is
 *  undefined). */
export function readTourState(): TourState {
  if (typeof window === "undefined") return "unseen";
  try {
    const v = window.localStorage.getItem(STORAGE_KEY);
    if (v === "completed" || v === "skipped") return v;
  } catch {
    // localStorage may throw in strict cookie modes; tolerate silently.
  }
  return "unseen";
}

/** Mark the tour as completed (user clicked Done on the last step). */
export function markCompleted(): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_KEY, "completed");
  } catch {
    // ignore
  }
  try {
    document.cookie = `${STORAGE_KEY}=completed; path=/; max-age=${
      60 * 60 * 24 * 365
    }; SameSite=Lax`;
  } catch {
    // ignore
  }
}

/** Mark the tour as dismissed without completion (user clicked Skip
 *  or closed mid-flow). */
export function markSkipped(): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_KEY, "skipped");
  } catch {
    // ignore
  }
}

/** QA helper: clear the tour state. Triggered by ?reset-tour=1. */
export function resetTourState(): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.removeItem(STORAGE_KEY);
  } catch {
    // ignore
  }
  try {
    document.cookie = `${STORAGE_KEY}=; path=/; max-age=0; SameSite=Lax`;
  } catch {
    // ignore
  }
}

/** Should the tour mount on this visit? */
export function shouldShowTour(searchParams: URLSearchParams | null): boolean {
  if (typeof window === "undefined") return false;
  if (searchParams?.get("reset-tour") === "1") {
    resetTourState();
    return true;
  }
  return readTourState() === "unseen";
}
