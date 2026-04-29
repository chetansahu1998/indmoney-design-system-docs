/**
 * Theme-toggle pulse — JSON tab "bound variable" chips flash on theme swap.
 *
 * Per Animation Philosophy: when light↔dark toggles, atlas textures crossfade
 * (~400ms cubic-bezier) and JSON-tab values pulse the bound chips. This
 * timeline handles the *chip pulse* portion; the atlas crossfade is owned by
 * the AtlasFrame component (U7) which knows about texture URLs.
 *
 * Animation: brief scale + opacity flash (~400ms cubic-out) per chip,
 * staggered slightly so a row of chips ripples.
 *
 * Reduced-motion: returns an empty paused timeline (no pulse, the chip
 * already shows its new bound value via the normal re-render).
 */

import gsap from "gsap";
import { EASE_THEME_TOGGLE } from "../easings";
import { getPrefersReducedMotion } from "../context";

const PULSE_SCALE = 1.06;
const PULSE_DURATION_S = 0.2;

/**
 * Returns a paused timeline that pulses each chip's scale + opacity.
 * Chips snap back to their resting state at the end. Safe to call with an
 * empty array (returns a no-op timeline).
 */
export function themeToggle(boundChips: HTMLElement[]): gsap.core.Timeline {
  const tl = gsap.timeline({ paused: true });

  if (getPrefersReducedMotion() || boundChips.length === 0) {
    return tl;
  }

  // Scale+opacity up (200ms) → back to rest (200ms) — total ~400ms.
  tl.to(
    boundChips,
    {
      scale: PULSE_SCALE,
      opacity: 0.85,
      duration: PULSE_DURATION_S,
      ease: EASE_THEME_TOGGLE,
      stagger: { amount: 0.12, from: "start" },
      transformOrigin: "center center",
    },
    0,
  );

  tl.to(
    boundChips,
    {
      scale: 1,
      opacity: 1,
      duration: PULSE_DURATION_S,
      ease: EASE_THEME_TOGGLE,
      stagger: { amount: 0.12, from: "start" },
    },
    PULSE_DURATION_S,
  );

  return tl;
}
