/**
 * idle-tracker.test.ts — unit tests for the IdleTracker pure-logic core.
 *
 * No test runner is wired in this repo; following the convention of
 * sibling tests, we throw on assertion failure and expose `runAll`
 * for shape-correct Vitest/Jest pickup.
 *
 * Tests use injected fake clock + setTimer so timing is deterministic.
 */

import { IdleTracker } from "../idle-tracker";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

class FakeClock {
  private t = 0;
  private next = 1;
  private timers = new Map<number, { fireAt: number; cb: () => void }>();

  now(): number {
    return this.t;
  }

  setTimeout(cb: () => void, ms: number): number {
    const id = this.next++;
    this.timers.set(id, { fireAt: this.t + ms, cb });
    return id;
  }

  clearTimeout(handle: unknown): void {
    if (typeof handle === "number") this.timers.delete(handle);
  }

  /** Advance the clock and fire any timers whose deadline has passed. */
  advance(ms: number): void {
    const target = this.t + ms;
    while (true) {
      const due = [...this.timers.entries()]
        .filter(([, v]) => v.fireAt <= target)
        .sort((a, b) => a[1].fireAt - b[1].fireAt);
      if (due.length === 0) break;
      const [id, { fireAt, cb }] = due[0];
      this.t = fireAt;
      this.timers.delete(id);
      cb();
    }
    this.t = target;
  }
}

function tracker(idleMs: number = 500): { t: IdleTracker; clock: FakeClock } {
  const clock = new FakeClock();
  const t = new IdleTracker(idleMs, {
    now: () => clock.now(),
    setTimer: (cb, ms) => clock.setTimeout(cb, ms),
    clearTimer: (h) => clock.clearTimeout(h),
  });
  return { t, clock };
}

// ─── Happy path ────────────────────────────────────────────────────────────

function test_initial_state_is_idle(): void {
  const { t } = tracker();
  assert(t.idle === true, "tracker starts idle");
}

function test_wakeUp_flips_to_active_and_notifies(): void {
  const { t } = tracker();
  let lastSeen: boolean | undefined;
  t.subscribe((idle) => {
    lastSeen = idle;
  });
  t.wakeUp();
  assert(t.idle === false, "wakeUp flips to active");
  assert(lastSeen === false, "listener saw active=false");
}

function test_idle_after_threshold(): void {
  const { t, clock } = tracker(500);
  let lastSeen: boolean | undefined;
  t.subscribe((idle) => {
    lastSeen = idle;
  });
  t.wakeUp();
  assert(t.idle === false, "wakeUp made tracker active");
  clock.advance(499);
  assert(t.idle === false, "still active 1ms before threshold");
  clock.advance(2);
  assert(t.idle === true, "idle after threshold elapses");
  assert(lastSeen === true, "listener notified on flip-to-idle");
}

// ─── Edge cases ────────────────────────────────────────────────────────────

function test_repeated_wakeUp_resets_timer(): void {
  const { t, clock } = tracker(500);
  t.wakeUp();
  clock.advance(400);
  t.wakeUp(); // 100ms before idle would have fired — restart timer
  clock.advance(400);
  assert(t.idle === false, "repeat wakeUp keeps tracker active beyond original 500ms");
  clock.advance(200);
  assert(t.idle === true, "once activity stops, idle fires after 500ms from last wakeUp");
}

function test_pointermove_throttle(): void {
  const { t, clock } = tracker(500);
  t.wakeUp("pointermove");
  // Burst of pointermove within the 100ms throttle window — should NOT extend the timer.
  for (let i = 0; i < 10; i++) {
    clock.advance(5);
    t.wakeUp("pointermove");
  }
  // Total elapsed inside the burst = 50ms. After throttle window passes, next call counts.
  clock.advance(100); // total 150ms since first wakeUp; pointer-move throttle ended.
  t.wakeUp("pointermove"); // this one DOES restart
  clock.advance(450);
  assert(t.idle === false, "still active 450ms after the throttled second wakeUp");
  clock.advance(100);
  assert(t.idle === true, "idle after the second wakeUp's idleMs window elapses");
}

function test_other_events_are_not_throttled(): void {
  const { t, clock } = tracker(500);
  t.wakeUp("other");
  clock.advance(50);
  t.wakeUp("other"); // immediate 2nd wakeUp — must restart timer regardless of throttle
  clock.advance(450);
  assert(t.idle === false, "two close 'other' wakeUps still extend the active window past idleMs");
}

function test_listener_unsubscribe_during_callback(): void {
  const { t } = tracker();
  let count = 0;
  const unsub = t.subscribe(() => {
    count++;
    unsub(); // unsubscribe ourselves mid-callback
  });
  t.wakeUp();
  t.wakeUp();
  // The first notification triggered unsubscribe; subsequent flips don't notify.
  assert(count === 1, "self-unsubscribe during callback doesn't crash the iteration");
}

function test_listener_throw_doesnt_kill_others(): void {
  const { t } = tracker();
  // `let bSeen` is `false` initially; TS narrows the literal type. Use a
  // mutable ref-shaped object so the assignment inside the closure widens
  // it to a generic boolean and the post-condition check type-checks.
  const flags = { bSeen: false };
  t.subscribe(() => {
    throw new Error("listener A blew up");
  });
  t.subscribe(() => {
    flags.bSeen = true;
  });
  t.wakeUp();
  assert(flags.bSeen === true, "listener B still fires after listener A threw");
}

function test_reset_clears_state(): void {
  const { t, clock } = tracker(500);
  t.wakeUp();
  assert(t.idle === false, "active before reset");
  t.reset();
  assert(t.idle === true, "reset returns to idle state");
  // Timer cleared — advancing past idleMs doesn't fire anything (no listeners anyway).
  clock.advance(1000);
  assert(t.idle === true, "still idle, no spurious flip from reset's pending timer");
}

// ─── Driver ────────────────────────────────────────────────────────────────

export function runAll(): void {
  const tests: Array<[string, () => void]> = [
    ["initial state is idle", test_initial_state_is_idle],
    ["wakeUp flips to active and notifies", test_wakeUp_flips_to_active_and_notifies],
    ["idle after threshold", test_idle_after_threshold],
    ["repeated wakeUp resets timer", test_repeated_wakeUp_resets_timer],
    ["pointermove throttle", test_pointermove_throttle],
    ["other events are not throttled", test_other_events_are_not_throttled],
    ["listener unsubscribe during callback", test_listener_unsubscribe_during_callback],
    ["listener throw doesn't kill others", test_listener_throw_doesnt_kill_others],
    ["reset clears state", test_reset_clears_state],
  ];
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      fn();
      // eslint-disable-next-line no-console
      console.log(`ok  ${name}`);
    } catch (err) {
      failed++;
      // eslint-disable-next-line no-console
      console.error(`fail ${name}: ${err instanceof Error ? err.message : String(err)}`);
    }
  }
  if (failed > 0) throw new Error(`${failed} idle-tracker test(s) failed`);
}
