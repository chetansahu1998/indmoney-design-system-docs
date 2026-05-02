/**
 * Shared easing constants for the Projects animation language.
 *
 * Animation philosophy refs:
 *   - mhdyousuf.me — snappy reveals, sub-400ms easing, terminal aesthetic.
 *   - resn.co.nz   — soothing curves, cinematic 800-1200ms transitions.
 *
 * GSAP eases are passed as strings. See https://gsap.com/docs/v3/Eases for
 * the parametrized variants (e.g. `back.out(1.2)` overshoot factor).
 *
 * U10 — these constants are the *canonical* curves for sequenced DOM
 * choreography (GSAP timelines). For triggered 3D transitions we use
 * `@react-spring/three` springs instead of named eases — see
 * `lib/animations/conventions.md` for the three-tool motion grammar.
 *
 * `EASE_DOLLY` is the standard camera-tween curve. When a GSAP-driven
 * camera move is unavoidable, prefer this constant over an inline string;
 * for the canonical r3f / react-force-graph-3d camera dolly, use a spring
 * with `{ tension: 170, friction: 26 }` (canonical react-spring default).
 */

/** Page-load reveal — `expo.out` for snappy mhdyousuf-style cadence. */
export const EASE_PAGE_OPEN = "expo.out" as const;

/** Tab swap — symmetrical `cubic.inOut` so outgoing/incoming feel paired. */
export const EASE_TAB_SWITCH = "cubic.inOut" as const;

/** Hover micro-interactions — `back.out(1.2)` adds subtle playful overshoot. */
export const EASE_HOVER = "back.out(1.2)" as const;

/** Theme toggle crossfade — soothing `cubic.out`. */
export const EASE_THEME_TOGGLE = "cubic.out" as const;

/** Camera dolly / zoom-into — `expo.inOut` for resn-style cinematic. */
export const EASE_DOLLY = "expo.inOut" as const;

/** Type-on / character-stagger reveal duration — for breadcrumbs & code. */
export const EASE_TYPE_ON = "none" as const;

/** Default per-frame stagger for atlas reveal in milliseconds. */
export const STAGGER_PER_FRAME_MS = 80;
/** Maximum total stagger window before clamping. */
export const STAGGER_MAX_MS = 600;
