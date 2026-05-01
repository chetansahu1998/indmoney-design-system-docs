"use client";

/**
 * Phase 6 U11 — click-and-hold signal-animation state machine.
 *
 * The user's frozen contract:
 *   - mousedown (onPointerDown) on any node sets heldNodeID
 *   - mouseup OR pointer-leave clears it
 *   - the animation layer subscribes to heldNodeID and draws particle +
 *     glow + edge pulse while it's non-null
 *   - **NO camera move**, **NO expansion**, **NO sibling dim**
 *
 * The hook is dead simple — almost no state, no debouncing, no timers. The
 * "soothing" quality comes from the natural ease-in/out of the particles
 * (rendered by SignalAnimationLayer), not from clever logic here.
 */

import { useCallback, useState } from "react";

interface SignalHold {
  heldNodeID: string | null;
  start: (id: string) => void;
  end: () => void;
}

export function useSignalHold(): SignalHold {
  const [heldNodeID, setHeldNodeID] = useState<string | null>(null);
  const start = useCallback((id: string) => {
    setHeldNodeID(id);
  }, []);
  const end = useCallback(() => {
    setHeldNodeID(null);
  }, []);
  return { heldNodeID, start, end };
}
