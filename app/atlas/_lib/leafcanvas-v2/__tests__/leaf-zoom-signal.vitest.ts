/**
 * leaf-zoom-signal.test.ts — exercise the live/settled split for U7 of
 * plan 2026-05-06-003. Self-rolling assertions, runAll() driver,
 * matches gesture-tracker.test.ts shape.
 *
 * Scope: synchronous setLeafZoom behavior under different gesturing
 * states. The settle bridge (canvasGestureTracker → settledListeners)
 * is timing-dependent (150ms debounce in the real tracker) — testing
 * it cleanly requires a runner with timer mocking (vi.useFakeTimers
 * etc.). When that runner lands, add a "settle bridge syncs settledZoom
 * to last liveZoom after debounce" case here.
 *
 * The HMR guard (globalThis.__lcLeafZoomSignalWired) is similarly
 * hard to exercise without runner-level vi.resetModules() — module
 * caching means a second import in this file won't re-execute the
 * module body. Documented in leaf-zoom-signal.ts itself.
 */

import { canvasGestureTracker } from "../gesture-tracker";
import {
  getLeafZoomLive,
  getLeafZoomSettled,
  setLeafZoom,
} from "../leaf-zoom-signal";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

// Reset both signals + gesture tracker between tests so each runs in
// isolation. Direct mutation of the module-level state isn't exposed,
// so we re-zero by setting a known value, then stripping the gesture-
// tracker's internal flag via reset().
function resetState(): void {
  canvasGestureTracker.reset();
  // After reset, gesture-tracker is in settled state; setLeafZoom(1)
  // will update both signals back to 1.
  setLeafZoom(1);
}

function test_setLeafZoom_updates_live_when_settled(): void {
  resetState();
  setLeafZoom(0.5);
  assert(getLeafZoomLive() === 0.5, `live = ${getLeafZoomLive()}, want 0.5`);
}

function test_setLeafZoom_updates_settled_when_not_gesturing(): void {
  resetState();
  setLeafZoom(0.75);
  assert(
    getLeafZoomSettled() === 0.75,
    `settled = ${getLeafZoomSettled()}, want 0.75`,
  );
}

function test_setLeafZoom_skips_settled_during_gesture(): void {
  resetState();
  setLeafZoom(1.0); // baseline
  canvasGestureTracker.tick(); // flips gesturingFlag = true
  assert(canvasGestureTracker.isGesturing, "tracker should be gesturing");
  setLeafZoom(0.4);
  assert(getLeafZoomLive() === 0.4, `live = ${getLeafZoomLive()}, want 0.4`);
  assert(
    getLeafZoomSettled() === 1.0,
    `settled = ${getLeafZoomSettled()}, want 1.0 (skipped during gesture)`,
  );
}

function test_setLeafZoom_rejects_nan(): void {
  resetState();
  setLeafZoom(0.5); // baseline
  setLeafZoom(Number.NaN);
  assert(
    getLeafZoomLive() === 0.5,
    `live = ${getLeafZoomLive()}, want 0.5 (NaN rejected)`,
  );
}

function test_setLeafZoom_rejects_zero_and_negative(): void {
  resetState();
  setLeafZoom(0.5); // baseline
  setLeafZoom(0);
  setLeafZoom(-1);
  assert(
    getLeafZoomLive() === 0.5,
    `live = ${getLeafZoomLive()}, want 0.5 (0 and negative rejected)`,
  );
}

function test_setLeafZoom_idempotent_same_value(): void {
  resetState();
  let liveCallCount = 0;
  // Subscribe to liveZoom updates to count notifications.
  // (Module-level subscribeLive is internal; instead read the value
  // before/after to verify same-value calls don't wastefully notify.)
  setLeafZoom(0.6);
  const before = getLeafZoomLive();
  setLeafZoom(0.6); // same value — should be a no-op for state and listeners
  const after = getLeafZoomLive();
  assert(before === after, `same-value call should not change live signal`);
  // Counter would increment if internal notify fired; we'd need module
  // surgery to assert that directly. Skip that assertion until a runner
  // with module-mocking lands.
  void liveCallCount;
}

export function runAll(): void {
  const tests: Array<[string, () => void]> = [
    ["setLeafZoom updates live when settled", test_setLeafZoom_updates_live_when_settled],
    ["setLeafZoom updates settled when not gesturing", test_setLeafZoom_updates_settled_when_not_gesturing],
    ["setLeafZoom skips settled during gesture", test_setLeafZoom_skips_settled_during_gesture],
    ["setLeafZoom rejects NaN", test_setLeafZoom_rejects_nan],
    ["setLeafZoom rejects zero and negative", test_setLeafZoom_rejects_zero_and_negative],
    ["setLeafZoom is idempotent on same value", test_setLeafZoom_idempotent_same_value],
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
  if (failed > 0) throw new Error(`${failed} leaf-zoom-signal test(s) failed`);
}
