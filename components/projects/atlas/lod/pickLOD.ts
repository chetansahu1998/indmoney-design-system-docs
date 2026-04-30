/**
 * pickLOD — Phase 3.5 U3 — screen-space-density LOD picker.
 *
 * For each atlas frame, returns the LOD tier the renderer should
 * request:
 *
 *   "full" — 100% of the persisted long-edge cap (Phase 1 max
 *            4096px). Used when the frame fills a meaningful chunk of
 *            the viewport.
 *   "l1"   — 50% downsample (`<id>@2x.l1.ktx2` / `.l1.png` once the
 *            backend LOD-tier generation lands). Used when the frame
 *            is small relative to the viewport but still visible
 *            without obvious aliasing.
 *   "l2"   — 25% downsample. Used at extreme zoom-out when the frame
 *            is closer to a thumbnail than a screen.
 *
 * Today (Phase 3.5) the backend ships a single PNG (4096px max) per
 * screen and a KTX2 sidecar with a mipmap chain. The pickLOD helper
 * is the contract the URL-picking site reads — when backend LOD-tier
 * generation lands (deferred unit; needs png.go pipeline extension),
 * this helper decides which tier URL to fetch. Until then the frontend
 * routes every tier to the same single PNG/KTX2 and the GPU's mipmap
 * sampling does the actual work.
 *
 * Threshold rationale (per Phase 3 plan U3):
 *   density < 0.25  → l2
 *   density < 0.50  → l1
 *   else            → full
 *
 * Where density is `(frame_width_px / viewport_width_px)`. Below 0.25
 * a 4096px frame is rendered to <1024 viewport pixels — l2 (1024px
 * input) fully covers the on-screen area without artifacts.
 */

export type LODTier = "full" | "l1" | "l2";

export const LOD_THRESHOLDS = {
  l2_max: 0.25,
  l1_max: 0.5,
} as const;

/**
 * pickLOD returns the right tier for a given frame at the given camera
 * zoom and viewport dimensions.
 *
 * Inputs match what r3f exposes in `useFrame`:
 *   frameWidth — the frame's source-space width (pre-zoom)
 *   cameraZoom — three.OrthographicCamera.zoom
 *   viewportWidth — gl.domElement.clientWidth (pixels)
 *
 * Pure function — no side effects, fully unit-testable.
 */
export function pickLOD(
  frameWidth: number,
  cameraZoom: number,
  viewportWidth: number,
): LODTier {
  if (frameWidth <= 0 || viewportWidth <= 0 || cameraZoom <= 0) {
    return "full";
  }
  // Screen-space density = how much viewport the frame currently
  // occupies (in 0..N range; >1 means the frame is larger than the
  // viewport, i.e. zoomed in).
  const density = (frameWidth * cameraZoom) / viewportWidth;
  if (density < LOD_THRESHOLDS.l2_max) return "l2";
  if (density < LOD_THRESHOLDS.l1_max) return "l1";
  return "full";
}

/**
 * Builds the URL for a given LOD tier. Backend serves tier-specific
 * downsamples via the `?tier=l1` / `?tier=l2` query param on the
 * existing PNG + KTX2 routes; "full" → no query param.
 *
 * When the backend doesn't have a tier file on disk (LOD generation
 * failed / pre-LOD screen / ops cleared sidecars), the handler
 * returns 404 and the frontend's getTextureKTX2OrPNG falls back
 * cleanly to the .png path WITHOUT the tier suffix. So tier=l2 →
 * 404 ktx2 → 404 png → useEffect onError, and AtlasFrame can re-
 * render with tier=full to recover.
 */
export function lodURL(baseURL: string, tier: LODTier): string {
  if (tier === "full") return baseURL;
  const sep = baseURL.includes("?") ? "&" : "?";
  return `${baseURL}${sep}tier=${tier}`;
}
