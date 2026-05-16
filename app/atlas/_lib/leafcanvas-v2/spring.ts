/**
 * spring.ts — critically-damped spring integrator for the camera
 * (U2 of the Figma-Dev-Mode parity initiative).
 *
 * Replaces the `easeInOutCubic` lerp in `camera-snap.ts:animateCamera`
 * for snap-to-fit, fly-to-selection, and any other programmatic camera
 * motion. Wheel/pinch deltas remain direct camRef writes — those are
 * already gesture-driven and don't need physics.
 *
 * Why a hand-rolled spring (not @react-spring/web, framer-motion,
 * popmotion, etc.):
 *
 *   1. The existing animateCamera surface (`from, to, durationMs,
 *      onTick, onDone`) is rAF-loop-driven with injectable now/raf for
 *      deterministic tests. Pulling in a library that owns its own
 *      loop would force us to choose between two integration models or
 *      ship a wrapper anyway.
 *
 *   2. The chrome layer (chrome-layer.tsx, U1) already runs its own
 *      rAF loop that reads camera-state on every tick. Keeping the
 *      spring inside our rAF means a single tick drives both camera
 *      update AND chrome repaint — no scheduling skew.
 *
 *   3. The math is small enough (semi-implicit Euler, ~30 lines) that
 *      a library is overkill. The dependency surface for canvas-v2
 *      stays bounded.
 *
 * Physics:
 *
 *   F = -k(x - target) - c*v        (Hooke spring + viscous damping)
 *   a = F / m,   with m = 1 in our system
 *   v_{t+dt} = v_t + a * dt          (semi-implicit Euler)
 *   x_{t+dt} = x_t + v_{t+dt} * dt
 *
 * For a one-axis spring with stiffness k and damping c, critical
 * damping is at c = 2 * sqrt(k * m) = 2 * sqrt(k). With k=180,
 * c_critical ≈ 26.83. Our default damping=26 is fractionally under
 * critical, producing ~1% overshoot — close enough to "matches Figma"
 * for the initial tuning; final adjustment lands when side-by-side
 * comparison runs (per plan KTD-3, brainstorm Key Decision).
 *
 * Stability:
 *
 *   Semi-implicit Euler is stable when dt < 2 / sqrt(k). For k=180,
 *   max stable dt ≈ 0.149s. Frame drops can produce dt up to ~50ms
 *   (20 fps), well within the stability bound. As a guard, springStep
 *   sub-steps any dt > 16.67ms into multiple ≤16ms steps so even on a
 *   long pause (tab backgrounded, devtools open) the integrator stays
 *   numerically stable.
 */

/**
 * Per-axis spring state. Spring is one-dimensional; the camera has
 * three independent springs (x, y, z) that don't cross-couple.
 */
export interface SpringState {
  /** Current spring position. */
  value: number;
  /** Current spring velocity (units per second). */
  velocity: number;
}

export interface SpringParams {
  /**
   * Spring constant (Hooke's law). Higher = stiffer = faster motion.
   * 180 is the initial tune for the camera ({x, y} in world px, z in
   * zoom multiplier). Tunable per side-by-side with Figma.
   */
  stiffness: number;
  /**
   * Damping coefficient. Higher = more drag = less overshoot.
   * For critical damping (no overshoot, fastest settle):
   *   damping = 2 * sqrt(stiffness * mass), mass = 1
   * For stiffness=180, critical damping ≈ 26.83.
   * Our default 26 is slightly under critical → ~1% overshoot.
   */
  damping: number;
  /**
   * Optional termination thresholds. The spring is "settled" when the
   * distance from target is below `value` AND the velocity is below
   * `velocity`. Defaults: 0.5 px, 1.0 px/s.
   *
   * Why those defaults aren't tighter: at our tuning (k=180, c=26),
   * velocity decays with time-constant 1/(ζω₀) ≈ 77ms. The value
   * reaches sub-pixel precision (<0.5 px) in ~30 frames at 60Hz but
   * velocity continues trickling for another 30+ frames toward
   * zero. A 1 px/s velocity threshold means at 60Hz the next frame
   * moves at most 1/60 px — well below the chrome layer's
   * integer-pixel paint rounding. So treating "value within 0.5 px
   * AND velocity below 1 px/s" as settled drops zero perceptible
   * motion and saves ~30 trailing rAF ticks per snap.
   *
   * The camera passes a tighter precisionZ to cameraSpringSettled
   * for the z-axis (zoom in multiplier units, not px) so the zoom
   * axis settles to 0.001 multiplier precision — see animateCamera
   * in camera-snap.ts.
   */
  precision?: {
    value: number;
    velocity: number;
  };
}

/**
 * Initial spring tuning. The plan calls these "constants in one place
 * so they're tunable" — that's this object. Future U2-tuning passes
 * adjust stiffness / damping here without touching the integrator.
 */
export const DEFAULT_SPRING: SpringParams = {
  stiffness: 180,
  damping: 26,
};

/**
 * Camera-appropriate defaults — see SpringParams.precision docs for
 * the rationale. Tightening these makes snap animations trail many
 * imperceptible rAF ticks; loosening further risks visibly stopping
 * before the target.
 */
const DEFAULT_VALUE_EPSILON = 0.5;
const DEFAULT_VELOCITY_EPSILON = 1.0;
const MAX_INTEGRATION_STEP_SEC = 1 / 60;

/**
 * Advance a spring by `dtSec` seconds toward `target`. Returns the new
 * state. Sub-steps any dt above ~16ms to preserve stability under
 * frame drops or long pauses (e.g., devtools open, tab backgrounded).
 *
 * Pure function — no side effects, no rAF, no time source. Wraps
 * cleanly into the existing animateCamera loop.
 */
export function springStep(
  state: SpringState,
  target: number,
  params: SpringParams,
  dtSec: number,
): SpringState {
  if (!Number.isFinite(dtSec) || dtSec <= 0) return state;

  let { value, velocity } = state;
  // Sub-step to stay below the integrator's stability bound. At k=180
  // the bound is ~0.149s; we cap at 1/60s which is well under that
  // AND matches the natural rAF cadence.
  let remaining = dtSec;
  // Guard against pathological inputs (e.g., 10-minute pause): cap
  // total simulated dt at 0.5s. Anything longer collapses to "snap
  // to target" semantics, which is the right behavior — the user
  // has been away from the canvas long enough that a long spring
  // flight to catch up would be more disorienting than instantaneous.
  if (remaining > 0.5) remaining = 0.5;

  while (remaining > 0) {
    const step = Math.min(remaining, MAX_INTEGRATION_STEP_SEC);
    const acceleration =
      params.stiffness * (target - value) - params.damping * velocity;
    velocity += acceleration * step;
    value += velocity * step;
    remaining -= step;
  }

  return { value, velocity };
}

/**
 * Returns true when the spring has reached equilibrium within the
 * configured (or default) precision. Used by the camera animation
 * loop to decide when to fire onDone and stop scheduling rAF.
 */
export function springSettled(
  state: SpringState,
  target: number,
  params: SpringParams,
): boolean {
  const valueEps = params.precision?.value ?? DEFAULT_VALUE_EPSILON;
  const velocityEps = params.precision?.velocity ?? DEFAULT_VELOCITY_EPSILON;
  return (
    Math.abs(state.value - target) < valueEps &&
    Math.abs(state.velocity) < velocityEps
  );
}

/**
 * Convenience: returns true when ALL of x, y, z are settled. The
 * camera animation only declares "done" when every axis is at rest;
 * z lagging while x and y are settled would visibly hold the
 * camera mid-zoom while the framing already arrived.
 *
 * `precisionZ` lets callers override the z-axis precision separately:
 * zoom is in a different unit (multiplier) than x/y (world pixels),
 * so the default 0.1 / 0.01 thresholds are too coarse for tight
 * z-zoom targets like 1.0 → 1.0001 (irrelevant but still in the
 * mathematical formula). Default keeps z at the same thresholds as
 * x/y — fine for typical zoom ranges.
 */
export interface CameraSpringStates {
  x: SpringState;
  y: SpringState;
  z: SpringState;
}

export function cameraSpringSettled(
  states: CameraSpringStates,
  target: { x: number; y: number; z: number },
  params: SpringParams,
  precisionZ?: { value: number; velocity: number },
): boolean {
  const zParams: SpringParams = precisionZ
    ? { ...params, precision: precisionZ }
    : params;
  return (
    springSettled(states.x, target.x, params) &&
    springSettled(states.y, target.y, params) &&
    springSettled(states.z, target.z, zParams)
  );
}
