/**
 * pickLOD — Phase 3.5 U3 — pure-function tests.
 *
 * No test runner is wired in this repo (Playwright handles UI tests),
 * but this file is shape-correct for Vitest/Jest to pick up if/when a
 * runner lands. Each `t.true(...)` style assertion converts to
 * `expect(...).toBe(...)` for either runner.
 *
 * Until then this file documents pickLOD's expected behavior next to
 * the implementation — readers can sanity-check the threshold logic
 * without running anything.
 */

import { LOD_THRESHOLDS, pickLOD } from "./pickLOD";

interface Case {
  name: string;
  frameWidth: number;
  cameraZoom: number;
  viewportWidth: number;
  expected: "full" | "l1" | "l2";
}

const CASES: Case[] = [
  // density = (frame * zoom) / viewport
  // density >= 0.5 → "full"
  {
    name: "frame fills viewport at 1x zoom",
    frameWidth: 1024,
    cameraZoom: 1,
    viewportWidth: 1024,
    expected: "full",
  },
  {
    name: "frame at zoom=2 oversamples but stays full",
    frameWidth: 1024,
    cameraZoom: 2,
    viewportWidth: 1024,
    expected: "full",
  },
  // density between l2_max (0.25) and l1_max (0.5) → "l1"
  {
    name: "frame at 30% viewport density picks l1",
    frameWidth: 600,
    cameraZoom: 1,
    viewportWidth: 2000,
    expected: "l1",
  },
  {
    name: "exactly l1_max threshold (0.5) goes to full",
    frameWidth: 1000,
    cameraZoom: 1,
    viewportWidth: 2000,
    expected: "full",
  },
  // density < l2_max → "l2"
  {
    name: "thumbnail-density picks l2",
    frameWidth: 100,
    cameraZoom: 1,
    viewportWidth: 2000,
    expected: "l2",
  },
  // edge cases
  { name: "zero frame width returns full", frameWidth: 0, cameraZoom: 1, viewportWidth: 1024, expected: "full" },
  { name: "zero zoom returns full", frameWidth: 1024, cameraZoom: 0, viewportWidth: 1024, expected: "full" },
  { name: "zero viewport returns full", frameWidth: 1024, cameraZoom: 1, viewportWidth: 0, expected: "full" },
];

// When a runner lands, replace this loop with describe/test calls.
export function _runSelfChecks(): { name: string; passed: boolean; got?: string }[] {
  return CASES.map((c) => {
    const got = pickLOD(c.frameWidth, c.cameraZoom, c.viewportWidth);
    return {
      name: c.name,
      passed: got === c.expected,
      got: got !== c.expected ? got : undefined,
    };
  });
}

// Sanity exports so future tests can read the threshold constants
// directly when refactoring.
export const _LOD_THRESHOLDS = LOD_THRESHOLDS;
