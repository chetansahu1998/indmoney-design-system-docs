/**
 * preview-tier.test.ts — tier-selection math.
 *
 * No test runner is wired in this repo; following sibling-test convention,
 * tests are pure functions that throw on failure and a `runAll()` driver
 * exposes them for shape-correct Vitest/Jest pickup.
 */

import { pickPreviewTier, PREVIEW_TIER_MIN, PREVIEW_TIER_MAX } from "../preview-tier";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

// ─── Happy path ────────────────────────────────────────────────────────────

function test_typical_zoom_picks_512(): void {
  // Frame 375px wide, displayed at zoom 0.5, DPR 2 → required = 375 * 0.5 * 2 = 375
  // Smallest tier ≥ 375 is 512.
  const tier = pickPreviewTier(375, 0.5, 2);
  assert(tier === 512, `expected 512, got ${tier}`);
}

function test_high_zoom_picks_2048(): void {
  // Frame 800px at zoom 2 DPR 2 → required = 3200, larger than 2048 → cap.
  const tier = pickPreviewTier(800, 2, 2);
  assert(tier === 2048, `expected 2048 cap, got ${tier}`);
}

function test_low_zoom_picks_128(): void {
  // Frame 100px at zoom 0.1 DPR 2 → required = 20, smallest tier (128).
  const tier = pickPreviewTier(100, 0.1, 2);
  assert(tier === 128, `expected 128, got ${tier}`);
}

function test_exactly_at_boundary_512(): void {
  // required = 512 exactly → tier-512 (smallest tier ≥ 512).
  const tier = pickPreviewTier(256, 1, 2);
  assert(tier === 512, `expected 512, got ${tier}`);
}

function test_one_pixel_over_boundary_promotes(): void {
  // required = 513 → tier-1024.
  const tier = pickPreviewTier(257, 1, 2);
  assert(tier === 1024, `expected 1024, got ${tier}`);
}

// ─── Edge cases ────────────────────────────────────────────────────────────

function test_zero_displayPx_returns_min_tier(): void {
  const tier = pickPreviewTier(0, 1, 2);
  assert(tier === PREVIEW_TIER_MIN, `expected ${PREVIEW_TIER_MIN}, got ${tier}`);
}

function test_negative_inputs_clamp_safe(): void {
  // Negative or NaN inputs all coerce to safe defaults; never crash.
  const a = pickPreviewTier(-100, 1, 2);
  const b = pickPreviewTier(100, -1, 2);
  const c = pickPreviewTier(100, 1, -2);
  const d = pickPreviewTier(NaN, 1, 2);
  const e = pickPreviewTier(100, NaN, 2);
  for (const t of [a, b, c, d, e]) {
    assert(t === 128 || t === 512 || t === 1024 || t === 2048, `bad tier ${t}`);
  }
}

function test_extreme_zoom_caps_at_max(): void {
  const tier = pickPreviewTier(2000, 100, 4);
  assert(tier === PREVIEW_TIER_MAX, `expected ${PREVIEW_TIER_MAX}, got ${tier}`);
}

function test_dpr1_typical_panel(): void {
  // Non-retina display, frame 1024px at zoom 1 DPR 1 → required = 1024 → tier-1024.
  const tier = pickPreviewTier(1024, 1, 1);
  assert(tier === 1024, `expected 1024, got ${tier}`);
}

// ─── Driver ────────────────────────────────────────────────────────────────

export function runAll(): void {
  const tests: Array<[string, () => void]> = [
    ["typical zoom picks 512", test_typical_zoom_picks_512],
    ["high zoom picks 2048", test_high_zoom_picks_2048],
    ["low zoom picks 128", test_low_zoom_picks_128],
    ["exactly at boundary 512", test_exactly_at_boundary_512],
    ["one pixel over boundary promotes", test_one_pixel_over_boundary_promotes],
    ["zero displayPx returns min tier", test_zero_displayPx_returns_min_tier],
    ["negative inputs clamp safe", test_negative_inputs_clamp_safe],
    ["extreme zoom caps at max", test_extreme_zoom_caps_at_max],
    ["dpr1 typical panel", test_dpr1_typical_panel],
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
  if (failed > 0) throw new Error(`${failed} preview-tier test(s) failed`);
}
