/**
 * idle-tracker.ts — module-level singleton that tracks canvas-wide
 * activity (wheel / pointer / keydown / touch) and flips an `idle` flag
 * after a debounce window of inactivity.
 *
 * Adapted from DesignBrain-AI's `RenderEngine.ts:646-651` self-pause
 * pattern. Their loop stops scheduling rAF after N idle frames; ours
 * pauses IntersectionObservers after N idle ms because that's where
 * our cost lives — Figma DOM walker + React reconciliation are dirty-
 * driven via the existing fetch-queue, not RAF.
 *
 * Why module-level singleton: every LeafFrameRenderer instance benefits
 * from the same idle signal, and the underlying activity events are
 * canvas-wide (any wheel anywhere wakes everyone). N subscribers on
 * one tracker is cheaper than N independent timers each listening to
 * the same events.
 *
 * Testability: the clock and DOM bindings are injected. Unit tests can
 * exercise the timer logic without a real browser. Production wiring
 * uses `attachToDOM()` once at app boot.
 */

export type IdleListener = (idle: boolean) => void;

/** Default idle threshold matches DesignBrain's 500ms RAF self-pause window. */
export const DEFAULT_IDLE_MS = 500;

/** DOM events that count as "user is interacting with the canvas". */
const ACTIVITY_EVENTS: readonly (keyof DocumentEventMap)[] = [
  "wheel",
  "pointermove",
  "pointerdown",
  "keydown",
  "touchstart",
  "touchmove",
];

/** Pointer-move debounce — these fire on every mouse-pixel; throttle so we don't reset the timer 60×/sec. */
const POINTERMOVE_THROTTLE_MS = 100;

/**
 * Pure-logic activity tracker. `wakeUp()` records activity; after
 * `idleMs` of no further wakeUp calls, listeners are notified that
 * the tracker is now idle. Re-arms on the next `wakeUp`.
 */
export class IdleTracker {
  private idleMs: number;
  private now: () => number;
  private setTimer: (cb: () => void, ms: number) => unknown;
  private clearTimer: (handle: unknown) => void;
  private listeners = new Set<IdleListener>();
  private currentTimer: unknown = null;
  private lastWakeAt = 0;
  private isIdle = true;
  private pointerMoveLastWakeAt = 0;

  constructor(
    idleMs: number = DEFAULT_IDLE_MS,
    deps?: {
      now?: () => number;
      setTimer?: (cb: () => void, ms: number) => unknown;
      clearTimer?: (handle: unknown) => void;
    },
  ) {
    this.idleMs = idleMs;
    this.now = deps?.now ?? (() => Date.now());
    this.setTimer = deps?.setTimer ?? ((cb, ms) => globalThis.setTimeout(cb, ms));
    this.clearTimer =
      deps?.clearTimer ??
      ((handle) => {
        if (handle != null) globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>);
      });
  }

  /** True when the tracker is currently in its idle state. */
  get idle(): boolean {
    return this.isIdle;
  }

  /**
   * Mark activity now. If the tracker was idle, listeners are notified
   * that we became active. The idle-flip timer is restarted.
   *
   * `kind === "pointermove"` enables the per-event-type throttle so
   * dragging the cursor doesn't restart the timer 60×/sec.
   */
  wakeUp(kind?: "pointermove" | "other"): void {
    const t = this.now();
    if (kind === "pointermove") {
      if (t - this.pointerMoveLastWakeAt < POINTERMOVE_THROTTLE_MS) return;
      this.pointerMoveLastWakeAt = t;
    }
    this.lastWakeAt = t;

    if (this.isIdle) {
      this.isIdle = false;
      this.notify();
    }

    if (this.currentTimer !== null) this.clearTimer(this.currentTimer);
    this.currentTimer = this.setTimer(() => this.flipToIdle(), this.idleMs);
  }

  /** Subscribe; returns unsubscribe. */
  subscribe(cb: IdleListener): () => void {
    this.listeners.add(cb);
    return () => {
      this.listeners.delete(cb);
    };
  }

  /** Tear down (used for hot-reload / tests). */
  reset(): void {
    if (this.currentTimer !== null) this.clearTimer(this.currentTimer);
    this.currentTimer = null;
    this.lastWakeAt = 0;
    this.pointerMoveLastWakeAt = 0;
    this.isIdle = true;
    this.listeners.clear();
  }

  private flipToIdle(): void {
    if (this.isIdle) return;
    this.isIdle = true;
    this.currentTimer = null;
    this.notify();
  }

  private notify(): void {
    // Snapshot so unsubscribe-during-callback doesn't mutate iteration.
    const snapshot = [...this.listeners];
    for (const cb of snapshot) {
      try {
        cb(this.isIdle);
      } catch {
        // Listener errors are swallowed — one bad listener shouldn't sink the rest.
      }
    }
  }
}

/** Shared instance — one per app, listens to document-wide activity events. */
export const canvasIdleTracker = new IdleTracker();

let domAttached = false;

/**
 * Wire the singleton tracker to document events. Idempotent — calling
 * twice is a no-op. Returns a detach fn for tests / hot-reload.
 */
export function attachIdleTrackerToDOM(): () => void {
  if (domAttached || typeof document === "undefined") {
    return () => {
      /* no-op */
    };
  }
  domAttached = true;

  const handlers: Array<{ event: keyof DocumentEventMap; handler: EventListener }> = [];
  for (const event of ACTIVITY_EVENTS) {
    const handler = (): void => {
      canvasIdleTracker.wakeUp(event === "pointermove" ? "pointermove" : "other");
    };
    document.addEventListener(event, handler, { passive: true });
    handlers.push({ event, handler });
  }
  // Initial wakeUp so the singleton starts in "active" state and only
  // flips to idle after the first uninterrupted idleMs window.
  canvasIdleTracker.wakeUp();
  return () => {
    for (const { event, handler } of handlers) {
      document.removeEventListener(event, handler);
    }
    domAttached = false;
  };
}
