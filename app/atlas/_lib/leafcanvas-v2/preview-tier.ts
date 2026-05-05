/**
 * preview-tier.ts — frontend half of U1 (preview pyramid).
 *
 * Backend ships 4 cached PNG tiers per node (128/512/1024/2048
 * longest-edge px). The frontend selects the smallest tier whose px
 * count satisfies the display requirement at the current zoom + DPR.
 *
 * Math (DesignBrain-AI ImageTileManager.ts:111-130):
 *
 *   requiredPx = displayPx × zoom × DPR
 *   tier       = smallest of [128, 512, 1024, 2048] with tier ≥ requiredPx
 *
 * `displayPx` is the source frame's longest edge in canonical_tree px
 * (i.e., the natural size at zoom=1). `zoom` is the canvas's current
 * pan/zoom scale; `dpr` is window.devicePixelRatio (or 2 if unknown).
 *
 * Why discrete tiers (vs continuous `?w=N`): caching efficiency. With
 * 4 tiers we have at most 4 cache rows per node + tenant + version;
 * with a continuous size knob we'd thrash the cache and never hit.
 */

/** Tier px ladder. Ascending so the picker can early-exit on first match. */
export const PREVIEW_TIERS = [128, 512, 1024, 2048] as const;
export type PreviewTier = (typeof PREVIEW_TIERS)[number];

/** Minimum tier — smallest available. Always serves as the level-0 fallback. */
export const PREVIEW_TIER_MIN: PreviewTier = PREVIEW_TIERS[0];

/** Maximum tier — biggest available. Used when zoom × DPR overshoots the ladder. */
export const PREVIEW_TIER_MAX: PreviewTier = PREVIEW_TIERS[PREVIEW_TIERS.length - 1];

/**
 * Pick the smallest tier where `tier ≥ displayPx × zoom × DPR`.
 *
 * - If the math overshoots tier-2048 (very high zoom, very large frame),
 *   we cap at tier-2048 — going bigger would just waste bandwidth on a
 *   detail nobody can see at the zoom/DPR they're using anyway.
 *
 * - If the math is < tier-128 (microscopic zoom or DPR), we floor at
 *   tier-128 — never request a tier that doesn't exist on disk.
 *
 * - Defensive: clamps inputs to non-negative + non-zero so `0 × zoom`
 *   doesn't return tier-128 for a degenerate frame; returns tier-128
 *   in that case so the renderer at least gets *something* to display.
 */
export function pickPreviewTier(
  displayPx: number,
  zoom: number,
  dpr: number,
): PreviewTier {
  const safePx = Number.isFinite(displayPx) && displayPx > 0 ? displayPx : 1;
  const safeZoom = Number.isFinite(zoom) && zoom > 0 ? zoom : 1;
  const safeDpr = Number.isFinite(dpr) && dpr > 0 ? dpr : 2;
  const required = safePx * safeZoom * safeDpr;
  for (const tier of PREVIEW_TIERS) {
    if (tier >= required) return tier;
  }
  return PREVIEW_TIER_MAX;
}

/** Browser DPR with a safe fallback for SSR / non-browser contexts. */
export function getDeviceDPR(): number {
  if (typeof window === "undefined") return 2;
  const v = window.devicePixelRatio;
  return Number.isFinite(v) && v > 0 ? v : 2;
}
