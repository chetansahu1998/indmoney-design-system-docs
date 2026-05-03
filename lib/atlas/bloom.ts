/**
 * lib/atlas/bloom.ts — entrance / pulse animation helpers.
 *
 * Every entity in the live store carries an optional `appearedAt: number`
 * (wall-clock ms). Entities present at first paint have it stripped so they
 * skip the entrance animation; entities arriving via SSE patches get
 * `Date.now()` stamped on insertion. The Canvas2D draw loop reads these
 * timestamps each frame to compute a 0..1 bloom progress value.
 *
 * Phases (ms after appearance):
 *   0 –  150 → scale 0 → 1.2 (overshoot in)
 *  150 –  400 → scale 1.2 → 1.0 (settle)
 *  400 – 1900 → halo radius r → 3r, opacity 0.6 → 0
 *  >1900     → fully integrated, no overlay
 */

export interface BloomState {
  /** Scale multiplier applied to the node sprite this frame. */
  scale: number;
  /** Halo radius multiplier (0 means no halo). */
  haloRadius: number;
  /** Halo alpha (0 means no halo). */
  haloAlpha: number;
  /** True while the entity is still in the entrance window. */
  active: boolean;
}

export const BLOOM_INACTIVE: Readonly<BloomState> = Object.freeze({
  scale: 1,
  haloRadius: 0,
  haloAlpha: 0,
  active: false,
});

const ENTER_DURATION_MS = 150;
const SETTLE_DURATION_MS = 250;
const HALO_DURATION_MS = 1500;
const TOTAL_DURATION_MS = ENTER_DURATION_MS + SETTLE_DURATION_MS + HALO_DURATION_MS;

/** Compute the per-frame bloom state for a given `appearedAt`. */
export function computeBloom(appearedAt: number | undefined, now: number): BloomState {
  if (!appearedAt) return BLOOM_INACTIVE;
  const elapsed = now - appearedAt;
  if (elapsed < 0 || elapsed >= TOTAL_DURATION_MS) return BLOOM_INACTIVE;

  // Phase 1 — scale from 0 to 1.2
  if (elapsed < ENTER_DURATION_MS) {
    const t = elapsed / ENTER_DURATION_MS;
    return {
      scale: easeOutCubic(t) * 1.2,
      haloRadius: 0,
      haloAlpha: 0,
      active: true,
    };
  }

  // Phase 2 — settle from 1.2 to 1.0
  if (elapsed < ENTER_DURATION_MS + SETTLE_DURATION_MS) {
    const t = (elapsed - ENTER_DURATION_MS) / SETTLE_DURATION_MS;
    return {
      scale: 1.2 - easeInOutCubic(t) * 0.2,
      haloRadius: 0,
      haloAlpha: 0,
      active: true,
    };
  }

  // Phase 3 — halo expand + fade
  const t = (elapsed - ENTER_DURATION_MS - SETTLE_DURATION_MS) / HALO_DURATION_MS;
  return {
    scale: 1,
    haloRadius: 1 + t * 2, // 1 → 3
    haloAlpha: 0.6 * (1 - easeInQuad(t)),
    active: true,
  };
}

/** Returns true if any entity in the array still has an active bloom — used
 *  by the renderer to decide whether to keep the rAF loop hot. */
export function anyBloomActive(
  entities: ReadonlyArray<{ appearedAt?: number }>,
  now: number,
): boolean {
  for (const e of entities) {
    if (!e.appearedAt) continue;
    if (now - e.appearedAt < TOTAL_DURATION_MS) return true;
  }
  return false;
}

// ─── easing ──────────────────────────────────────────────────────────────────

function easeOutCubic(t: number): number {
  return 1 - Math.pow(1 - t, 3);
}

function easeInOutCubic(t: number): number {
  return t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;
}

function easeInQuad(t: number): number {
  return t * t;
}

export const BLOOM_TOTAL_DURATION_MS = TOTAL_DURATION_MS;
