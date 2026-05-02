/**
 * Phase 6 — Frozen d3-force-3d configuration.
 *
 * These constants were tuned during U5 prototype against a 1000-node
 * fixture on an M1 MacBook. The pair (alphaDecay 0.022, velocityDecay 0.4)
 * was the lowest-energy state we found that still felt "alive" rather than
 * rigid. See plan U4 for full rationale.
 *
 * Don't twiddle these without a perf trace — Phase 6 plan Risk Table flags
 * "react-force-graph-3d performance regression at >1000 nodes" as High
 * severity, and the constants below are the first knobs to turn if it
 * regresses.
 */
export const FORCE_CONFIG = {
  /** Node-node repulsion. More negative = more spread. */
  charge: -120,
  /** Link force — distance is target inter-node spacing; strength is the
   *  spring constant. */
  link: { distance: 60, strength: 0.4 },
  /** Pull toward origin. Low values keep the graph from drifting offscreen
   *  while still allowing local clusters to form. */
  center: { strength: 0.05 },
  /** Collision radius — keeps node bodies from overlapping. */
  collide: { radius: 12 },
  /** Higher alphaDecay = settle faster but with more abrupt stops. 0.022
   *  reaches alpha < 0.01 in ~1.7s on the production fixture. */
  alphaDecay: 0.022,
  /** Velocity damping per tick. 0.4 dampens overshoot without making the
   *  simulation feel sluggish. */
  velocityDecay: 0.4,
} as const;

/** Node visual presets per type. Read by BrainGraph's nodeThreeObject
 *  factory (U5). Emissive colors are tuned for the bloom postprocessing
 *  chain (U6/U8) — they look duller without bloom.
 *
 *  Phase 8 U8 — Per-type emissive hierarchy. The bloom pass uses a
 *  luminance threshold ≈ 1.0; only materials whose `color × emissiveIntensity`
 *  produces channels above 1.0 contribute meaningful glow. With base colours
 *  in [0, 1], an `emissiveIntensity > ~1.5` lifts the brightest channel
 *  above the threshold and produces visible bloom; lower values fall under
 *  the threshold and remain crisp/dim. The values below were chosen so
 *  products glow strongest, folders/flows dimmer, and components/tokens/
 *  decisions only carry minimal HDR signal — letting the filter-driven
 *  opacity gate do the rest of the visual weighting. */
export const NODE_VISUAL = {
  product: {
    radius: 8,
    color: "#7B9FFF",
    emissiveIntensity: 3.5,
    /** Always render the label; products are the brain-view anchors. */
    labelMinZoom: 0,
  },
  folder: {
    radius: 5,
    color: "#5C6FA8",
    emissiveIntensity: 1.8,
    labelMinZoom: 0.6,
  },
  flow: {
    radius: 4,
    color: "#3D4F7A",
    emissiveIntensity: 1.2,
    labelMinZoom: 0.9,
  },
  persona: {
    radius: 4,
    color: "#9F8FFF",
    emissiveIntensity: 1.0,
    labelMinZoom: 0.7,
  },
  component: {
    radius: 3,
    color: "#5C6FA8",
    emissiveIntensity: 1.0,
    labelMinZoom: 0.95,
  },
  token: {
    radius: 2.5,
    color: "#FFB347",
    emissiveIntensity: 0.8,
    labelMinZoom: 1.0,
  },
  decision: {
    radius: 4,
    color: "#FFB347",
    emissiveIntensity: 0.8,
    labelMinZoom: 0.85,
  },
} as const;

/** Edge style presets per class. Width + alpha are read by BrainGraph's
 *  link width / particle / arrow accessors (U7). */
export const EDGE_STYLE = {
  hierarchy: { color: "#3D4F7A", alpha: 0.4, width: 0.8 },
  uses: { color: "#5C6FA8", alpha: 0.6, width: 1.0 },
  "binds-to": { color: "#9F8FFF", alpha: 0.7, width: 1.0, dashed: true },
  supersedes: { color: "#FFB347", alpha: 0.8, width: 1.2, directed: true },
} as const;

/** Background color of the r3f Canvas — near-black so bloom emissives pop. */
export const BACKGROUND_COLOR = "#000814";
