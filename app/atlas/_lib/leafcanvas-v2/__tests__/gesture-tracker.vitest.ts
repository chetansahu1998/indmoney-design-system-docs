/**
 * gesture-tracker.test.ts — exercise GestureTracker timing logic with
 * an injected fake clock. Vitest test, picked up by vitest.config.ts.
 */

import { describe, expect, it } from "vitest";

import { GestureTracker } from "../gesture-tracker";

// FakeClock — drives setTimer/clearTimer deterministically without
// touching the real event loop. Same shape as production setTimeout
// callback contract, just with explicit time advancement.
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
    setTimer: clk.setTimer,
    clearTimer: clk.clearTimer,
  });
  const events: boolean[] = [];
  t.subscribe((g) => events.push(g));
  return { t, clk, events };
}

describe("GestureTracker", () => {
  it("starts settled", () => {
    const { t } = make();
    expect(t.isGesturing).toBe(false);
  });

  it("flips to gesturing on first tick + fires listener once", () => {
    const { t, events } = make();
    t.tick();
    expect(t.isGesturing).toBe(true);
    expect(events).toEqual([true]);
  });

  it("burst of ticks does not refire (consumer cares about edges)", () => {
    const { t, clk, events } = make();
    t.tick();
    clk.advance(20);
    t.tick();
    clk.advance(20);
    t.tick();
    expect(events.length).toBe(1);
    expect(t.isGesturing).toBe(true);
  });

  it("settles after the debounce window", () => {
    const { t, clk, events } = make(150);
    t.tick();
    clk.advance(150);
    expect(t.isGesturing).toBe(false);
    expect(events).toEqual([true, false]);
  });

  it("each tick resets the settle timer (no settle until quiet window)", () => {
    const { t, clk } = make(150);
    t.tick();
    clk.advance(100);
    t.tick();
    clk.advance(100);
    t.tick();
    clk.advance(100);
    expect(t.isGesturing).toBe(true); // timer kept resetting
    clk.advance(150);
    expect(t.isGesturing).toBe(false); // settled after final 150ms idle
  });

  it("unsubscribe stops events", () => {
    const { t, clk } = make();
    let count = 0;
    const off = t.subscribe(() => count++);
    t.tick();
    clk.advance(150);
    off();
    t.tick();
    clk.advance(150);
    expect(count).toBe(2); // first tick produced (true, false); after off, none
  });

  it("reset clears state and cancels pending timer", () => {
    const { t, clk } = make();
    t.tick();
    expect(t.isGesturing).toBe(true);
    t.reset();
    expect(t.isGesturing).toBe(false);
    clk.advance(500);
    expect(t.isGesturing).toBe(false); // no zombie timer fired
  });

  it("listener exception does not sink other listeners", () => {
    const { t } = make();
    let goodFired = 0;
    t.subscribe(() => {
      throw new Error("first listener throws");
    });
    t.subscribe(() => {
      goodFired++;
    });
    t.tick();
    expect(goodFired).toBe(1);
  });
});
