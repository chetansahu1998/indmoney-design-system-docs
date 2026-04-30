/**
 * viewportRing — Phase 3.5 follow-up #3 — HOT / WARM viewport ring with
 * hysteresis. Adopted from DesignBrain's `engine/scene/ViewportCuller.ts`.
 *
 * Concept:
 *   HOT  = viewport + 800px padding. Frames inside HOT load full texture
 *          + render at full opacity.
 *   WARM = HOT × 2 (so viewport + 1600px padding). Frames inside WARM
 *          but outside HOT pre-load their texture (so promotion to HOT
 *          on pan is instant) but render at opacity 0 — they hold a
 *          GPU slot without painting pixels.
 *   COLD = anything outside WARM. Frames don't load textures + don't
 *          render. They re-enter the cycle when the viewport moves
 *          enough to bring them inside WARM.
 *
 * Hysteresis: re-classification only fires when the camera has moved
 * by more than 5% of the viewport (HYSTERESIS_FRACTION). Below that,
 * the previous classification holds. Prevents per-frame re-query
 * thrash when the user idle-jiggles the canvas.
 *
 * Pinned set: frames whose ID is in the pinned set are always HOT,
 * regardless of distance. Used by the click-to-snap selection so the
 * actively-selected frame can't accidentally cull itself off-screen
 * during the camera dolly.
 *
 * Returned tier ("hot" | "warm" | "cold") feeds AtlasFrame's render
 * decisions:
 *   hot  → load texture, render at opacity 1
 *   warm → load texture, render at opacity 0 (GPU slot, invisible)
 *   cold → don't load texture, don't render
 *
 * Pure function — easy to unit-test against synthetic camera poses.
 */

export type ViewportRingTier = "hot" | "warm" | "cold";

export const HOT_PADDING_PX = 800;
export const WARM_MULTIPLIER = 2; // WARM ring extends to HOT × this
export const HYSTERESIS_FRACTION = 0.05;

/**
 * Frame's screen-space bounding box. The atlas stores frames at preserved
 * (x, y) Figma coordinates; the viewport ring computes against world-
 * space coordinates which the AtlasFrame already has.
 */
export interface FrameBounds {
  /** Frame ID — used for pinned-set lookup. */
  id: string;
  /** Frame center in world space. The atlas's coordinate system has
   *  Y inverted from Figma (Figma's positive y = down; r3f's = up). */
  centerX: number;
  centerY: number;
  /** Frame half-width and half-height in world space. */
  halfWidth: number;
  halfHeight: number;
}

/** Viewport in world space. Caller computes via the camera's frustum. */
export interface ViewportRect {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

/**
 * Classifies a frame's tier given the current viewport. The viewport
 * is expanded by HOT_PADDING_PX for the HOT ring; the WARM ring is
 * HOT × WARM_MULTIPLIER. The padding is in WORLD-space pixels —
 * the caller is responsible for scaling by the camera zoom (1 / zoom).
 *
 * Returns "hot" when the frame's bounding box intersects the HOT ring;
 * "warm" when it intersects WARM but not HOT; "cold" otherwise.
 */
export function classify(
  frame: FrameBounds,
  viewport: ViewportRect,
  cameraZoom: number,
  pinned: Set<string>,
): ViewportRingTier {
  if (pinned.has(frame.id)) return "hot";
  if (cameraZoom <= 0) return "cold";

  const hotPad = HOT_PADDING_PX / cameraZoom;
  const warmPad = (HOT_PADDING_PX * WARM_MULTIPLIER) / cameraZoom;

  const hotRect: ViewportRect = {
    minX: viewport.minX - hotPad,
    minY: viewport.minY - hotPad,
    maxX: viewport.maxX + hotPad,
    maxY: viewport.maxY + hotPad,
  };
  if (intersects(frame, hotRect)) return "hot";

  const warmRect: ViewportRect = {
    minX: viewport.minX - warmPad,
    minY: viewport.minY - warmPad,
    maxX: viewport.maxX + warmPad,
    maxY: viewport.maxY + warmPad,
  };
  if (intersects(frame, warmRect)) return "warm";

  return "cold";
}

function intersects(frame: FrameBounds, rect: ViewportRect): boolean {
  const fxMin = frame.centerX - frame.halfWidth;
  const fxMax = frame.centerX + frame.halfWidth;
  const fyMin = frame.centerY - frame.halfHeight;
  const fyMax = frame.centerY + frame.halfHeight;
  return fxMax >= rect.minX && fxMin <= rect.maxX && fyMax >= rect.minY && fyMin <= rect.maxY;
}

/**
 * Hysteresis check. Returns true when the camera has moved enough to
 * justify re-classifying frames. AtlasCanvas calls this once per frame
 * inside useFrame and skips the per-frame .classify() loop when it
 * returns false — saves O(N) work per frame on idle-jiggle inputs.
 *
 * The threshold compares the viewport's diagonal against the previous
 * viewport's diagonal — a 5% diagonal-distance change triggers
 * re-classification.
 */
export function shouldReclassify(
  prev: ViewportRect | null,
  next: ViewportRect,
): boolean {
  if (!prev) return true;
  const prevW = prev.maxX - prev.minX;
  const prevH = prev.maxY - prev.minY;
  const dx = (next.minX + next.maxX) / 2 - (prev.minX + prev.maxX) / 2;
  const dy = (next.minY + next.maxY) / 2 - (prev.minY + prev.maxY) / 2;
  const dist = Math.sqrt(dx * dx + dy * dy);
  const threshold = Math.sqrt(prevW * prevW + prevH * prevH) * HYSTERESIS_FRACTION;
  return dist > threshold;
}
