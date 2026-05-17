/**
 * camera-actions.ts — module-level registry for the camera commands
 * the keymap module dispatches to (U3).
 *
 * Sister to `camera-snap.ts`'s `registerSnapTarget` pattern: LeafCanvas
 * registers its camera implementations on mount; the keymap reads from
 * this registry when a hotkey fires.
 *
 * Why a registry (not prop-drilling): the keymap lives one level up
 * (AtlasShellInner) and the leaf is dynamically mounted inside it.
 * Threading every camera command (fitAll, zoomIn, zoomOut, jumpToFrame,
 * …) through props would either bloat LeafCanvasProps or require a
 * context provider for what is essentially a single-target imperative
 * channel. The registry is one slot, single-target — same shape as
 * `registerSnapTarget` so the precedent is consistent.
 *
 * HMR / Vitest re-evaluation: the registered actions persist across
 * module re-eval but the LeafCanvas useEffect cleanup wipes the slot
 * on unmount, so a fresh mount overwrites cleanly.
 */

/**
 * One entry returned by `listNamedFrames`. The id is the canvas-level
 * frame id (the same value used by `onPickFrame` callbacks and the
 * frame strip); label is the human-readable name shown in the palette.
 */
export interface NamedFrameEntry {
  id: string;
  label: string;
}

export interface CameraActions {
  /** Shift+1 — fit every frame in the leaf to the viewport. */
  fitAll: () => void;
  /** Shift+2 — fit the current selection (delegates to existing camera-snap). */
  fitSelection: () => void;
  /** Cmd+0 — zoom to 100% (z=1) without changing pan. */
  zoom100: () => void;
  /** `+` / `=` — zoom in around viewport center. */
  zoomIn: () => void;
  /** `-` / `_` — zoom out around viewport center. */
  zoomOut: () => void;
  /**
   * N — fly camera to the next named frame in canvas-coordinate order.
   * "Named" means a top-level canvas frame (layout.frames item).
   * No-op when no frames exist.
   */
  nextNamedFrame: () => void;
  /** Shift+N — previous frame in canvas-coordinate order. */
  prevNamedFrame: () => void;
  /**
   * Snapshot of every named frame in canvas order. Consumed by the
   * Cmd+F name-search palette (U3b). The result is a fresh array on
   * every call — callers may not mutate the entries.
   */
  listNamedFrames: () => NamedFrameEntry[];
  /**
   * Fly the camera to a specific frame by id (the same id `onPickFrame`
   * carries and `listNamedFrames` returns). No-op when the id isn't
   * found in the current leaf.
   */
  jumpToFrame: (id: string) => void;
}

let registered: CameraActions | null = null;

/**
 * LeafCanvas calls this on mount. Returns an unregister fn. Idempotent:
 * re-registering replaces the prior slot (no listener accumulation).
 */
export function registerCameraActions(actions: CameraActions): () => void {
  registered = actions;
  return () => {
    if (registered === actions) registered = null;
  };
}

/**
 * Keymap (or any consumer that needs to fire a camera command) calls
 * this. Returns null when no canvas is active — caller should no-op
 * rather than throw.
 */
export function getCameraActions(): CameraActions | null {
  return registered;
}

/** Test helper — clears the registered slot between tests. */
export function __resetCameraActionsForTesting(): void {
  registered = null;
}

// ─── HMR / test re-evaluation guard ────────────────────────────────────
declare global {
  // eslint-disable-next-line no-var
  var __lcCameraActionsWired: boolean | undefined;
}
if (!globalThis.__lcCameraActionsWired) {
  globalThis.__lcCameraActionsWired = true;
}
