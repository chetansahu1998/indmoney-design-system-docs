/**
 * gesture-tracker.test.ts — exercise GestureTracker timing logic with
 * an injected fake clock. Mirrors idle-tracker.test.ts shape; throws
 * on assertion failure and exposes runAll() for Vitest/Jest pickup.
 */

import { GestureTracker } from "../gesture-tracker";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

class FakeClock {
  t = 0;
  pending: Array<{ id: number; due: number; cb: () => void }> = [];
  nextId = 1;
  now = (): number => this.t;
  setTimer = (cb: () => void, ms: number): unknown => {
    const id = this.nextId++;
    this.pending.push({ id, due: this.t + ms, cb });
    return id;
  };
  clearTimer = (handle: unknown): void => {
    this.pending = this.pending.filter((p) => p.id !== handle);
  };
  advance(ms: number): void {
    const target = this.t + ms;
    while (true) {
      const next = this.pending
        .filter((p) => p.due <= target)
        .sort((a, b) => a.due - b.due)[0];
      if (!next) break;
      this.t = next.due;
      this.pending = this.pending.filter((p) => p.id !== next.id);
      next.cb();
    }
    this.t = target;
  }
}

function make(endMs = 150): { t: GestureTracker; clk: FakeClock; events: boolean[] } {
  const clk = new FakeClock();
  const t = new GestureTracker(endMs, {
    now: clk.now,
    setTimer: clk.setTimer,
    clearTimer: clk.clearTimer,
  });
  const events: boolean[] = [];
  t.subscribe((g) => events.push(g));
  return { t, clk, events };
}

// ─── lifecycle ────────────────────────────────────────────────────────

function test_initially_settled(): void {
  const { t } = make();
  assert(t.isGesturing === false, "starts settled");
}

function test_tick_flips_to_gesturing(): void {
  const { t, events } = make();
  t.tick();
  assert(t.isGesturing === true, "tick → gesturing");
  assert(events.length === 1 && events[0] === true, "fires listener once with true");
}

function test_tick_burst_does_not_refire(): void {
  // Many ticks in rapid succession should produce ONE 'true' event,
  // not one per tick. (The consumer cares about edges, not every input.)
  const { t, clk, events } = make();
  t.tick();
  clk.advance(20);
  t.tick();
  clk.advance(20);
  t.tick();
  assert(events.length === 1, `expected 1 event, got ${events.length}`);
  assert(t.isGesturing === true, "still gesturing");
}

function test_settle_after_debounce(): void {
  const { t, clk, events } = make(150);
  t.tick();
  clk.advance(150);
  assert(t.isGesturing === false, "settled after 150ms");
  assert(events.length === 2, `expected [true,false], got len=${events.length}`);
  assert(events[1] === false, "second event is false");
}

function test_tick_resets_settle_timer(): void {
  // 100ms tick, 100ms tick, 100ms tick → after 200ms total still
  // gesturing because each tick reset the 150ms timer.
  const { t, clk } = make(150);
  t.tick();
  clk.advance(100);
  t.tick();
  clk.advance(100);
  t.tick();
  clk.advance(100);
  assert(t.isGesturing === true, "still gesturing — timer kept resetting");
  // Now go quiet for the full window.
  clk.advance(150);
  assert(t.isGesturing === false, "settled after final 150ms idle");
}

function test_unsubscribe(): void {
  const { t, clk } = make();
  let count = 0;
  const off = t.subscribe(() => count++);
  t.tick();
  clk.advance(150);
  off();
  t.tick();
  clk.advance(150);
  // First tick produced 2 events (true, false). After unsubscribe, no more.
  assert(count === 2, `expected 2 events, got ${count}`);
}

function test_reset_clears_state(): void {
  const { t, clk } = make();
  t.tick();
  assert(t.isGesturing === true, "gesturing after tick");
  t.reset();
  assert(t.isGesturing === false, "reset → settled");
  // No timer should fire after reset.
  clk.advance(500);
  assert(t.isGesturing === false, "still settled");
}

function test_listener_error_does_not_sink_others(): void {
  const { t } = make();
  let goodFired = 0;
  t.subscribe(() => {
    throw new Error("first listener throws");
  });
  t.subscribe(() => {
    goodFired++;
  });
  t.tick();
  // Two listeners + the events-recorder from make() = 3 total. Two should fire.
  assert(goodFired === 1, `good listener should fire despite bad one — got ${goodFired}`);
}

// ─── Driver ───────────────────────────────────────────────────────────

export function runAll(): void {
  const tests: Array<[string, () => void]> = [
    ["initially settled", test_initially_settled],
    ["tick flips to gesturing", test_tick_flips_to_gesturing],
    ["tick burst does not refire", test_tick_burst_does_not_refire],
    ["settle after debounce", test_settle_after_debounce],
    ["tick resets settle timer", test_tick_resets_settle_timer],
    ["unsubscribe stops events", test_unsubscribe],
    ["reset clears state", test_reset_clears_state],
    ["listener error does not sink others", test_listener_error_does_not_sink_others],
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
  if (failed > 0) throw new Error(`${failed} gesture-tracker test(s) failed`);
}
