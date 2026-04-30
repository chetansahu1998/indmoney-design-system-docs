/**
 * Tab-switch curtain-wipe.
 *
 * Per Animation Philosophy: outgoing fades + slides up; incoming slides up
 * from below. ~300ms total. Suitable for DRD ↔ Violations ↔ JSON tab swaps;
 * the active-tab indicator on the tablist is handled separately via Framer
 * Motion `layoutId` (per the philosophy table).
 *
 * Reduced-motion: outgoing snaps to hidden, incoming snaps to visible.
 */

import gsap from "gsap";
import { EASE_TAB_SWITCH } from "../easings";
import { getPrefersReducedMotion } from "../context";

const DURATION_OUT = 0.15;
const DURATION_IN = 0.18;
const SLIDE_DISTANCE = 12; // px

/** Final/initial states for incoming and outgoing nodes. */
const HIDDEN_OUT = { opacity: 0, y: -SLIDE_DISTANCE };
const HIDDEN_IN = { opacity: 0, y: SLIDE_DISTANCE };
const VISIBLE = { opacity: 1, y: 0 };

/**
 * Builds a paused timeline that animates `outgoing` out (up + fade) and
 * `incoming` in (up from below + fade). Caller plays the timeline.
 *
 * If `outgoing` and `incoming` are the same element, the function still
 * returns a valid paused timeline that does nothing meaningful (no-op).
 */
export function tabSwitch(
  outgoing: HTMLElement | null,
  incoming: HTMLElement | null,
): gsap.core.Timeline {
  const tl = gsap.timeline({ paused: true });

  if (getPrefersReducedMotion()) {
    if (outgoing) gsap.set(outgoing, HIDDEN_OUT);
    if (incoming) gsap.set(incoming, VISIBLE);
    return tl;
  }

  if (outgoing) {
    tl.to(
      outgoing,
      { ...HIDDEN_OUT, duration: DURATION_OUT, ease: EASE_TAB_SWITCH },
      0,
    );
  }

  if (incoming) {
    tl.set(incoming, HIDDEN_IN, 0);
    tl.to(
      incoming,
      { ...VISIBLE, duration: DURATION_IN, ease: EASE_TAB_SWITCH },
      // Slight overlap so the swap feels like a single curtain.
      DURATION_OUT * 0.5,
    );
  }

  return tl;
}
