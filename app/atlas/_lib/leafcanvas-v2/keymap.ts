/**
 * keymap.ts — centralized keyboard dispatcher for the leaf canvas (U3).
 *
 * Consolidates the previously-scattered keyboard handlers into one
 * action table and one window-level keydown listener. AtlasShellInner
 * mounts the dispatcher once on leaf-open; the dispatcher fires the
 * registered action callback when a hotkey matches AND the canvas
 * (or one of its non-editable descendants) is the focused element.
 *
 * Action coverage (U3):
 *
 *   Camera-only (fully wired in U3):
 *     Shift+1            canvas.fit-all
 *     Shift+2            canvas.fit-selection
 *     Cmd/Ctrl+0         canvas.zoom-100
 *     + / =              canvas.zoom-in
 *     - / _              canvas.zoom-out
 *     N                  canvas.next-named-frame
 *     Shift+N            canvas.prev-named-frame
 *
 *   Mode flag (fully wired):
 *     Shift+D            mode.toggle-dev-mode
 *
 *   Layered close (ported from AtlasShellInner.tsx:177-195):
 *     Escape             selection.escape-layered
 *
 *   Selection-dependent (registered, U4 wires the action body):
 *     Tab                selection.next-sibling
 *     Shift+Tab          selection.prev-sibling
 *     Cmd/Ctrl+A         selection.select-all
 *     Enter              selection.descend
 *     Shift+Enter / \    selection.ascend
 *
 *   Search (U3b commit ships this in the same unit):
 *     Cmd/Ctrl+F         search.open-palette
 *
 *   Transient modes (registered, leafcanvas pointer handlers consume the flags):
 *     Z (held)           mode.zoom-region (drag-rectangle)
 *     Space (held)       mode.pan
 *     H                  mode.toggle-hand-tool
 *
 * Focus model (per doc-review P1, Security F4 + Feasibility F4):
 *
 *   Canvas hotkeys fire only when the active focus target is INSIDE
 *   the .lc-stage subtree AND is not an editable element (input,
 *   textarea, contenteditable). This means:
 *
 *     - User clicks the canvas (or tabs to it): focus lands on .lc-stage
 *       which has tabindex=0 (added in U3's leafcanvas.tsx wiring).
 *       Canvas hotkeys fire.
 *     - User focuses an InlineTextEditor input INSIDE the canvas:
 *       isEditableActive() returns true, canvas hotkeys do NOT fire.
 *       The text-edit input gets its native Cmd+A / Cmd+F behavior.
 *     - User focuses an inspector input or any element OUTSIDE
 *       .lc-stage: closest(".lc-stage") returns null, canvas hotkeys
 *       do NOT fire. Browser defaults take over.
 *
 *   Pointer-over-canvas is NOT a gate — relying on cursor position
 *   would let a user mid-edit-in-inspector accidentally trigger
 *   canvas hotkeys when their pointer happened to rest over the canvas.
 *
 * Held-key transient mode tracking:
 *
 *   For Z+drag and Space+drag, we expose getHeldKey() so the leafcanvas
 *   pointer-handlers can branch on the current modifier mode. The
 *   keymap tracks held state via keydown / keyup pairs; window-blur
 *   clears all flags so a user that alt-tabs away mid-drag doesn't
 *   come back with Z still "held" stuck.
 */

import { canvasGestureTracker } from "./gesture-tracker";

/** Canonical action names dispatched by the keymap. */
export type KeymapAction =
  // camera
  | "canvas.fit-all"
  | "canvas.fit-selection"
  | "canvas.zoom-100"
  | "canvas.zoom-in"
  | "canvas.zoom-out"
  | "canvas.next-named-frame"
  | "canvas.prev-named-frame"
  // selection (U4 wires the action body)
  | "selection.escape-layered"
  | "selection.next-sibling"
  | "selection.prev-sibling"
  | "selection.select-all"
  | "selection.descend"
  | "selection.ascend"
  // mode
  | "mode.toggle-dev-mode"
  | "mode.toggle-hand-tool"
  // search (U3b)
  | "search.open-palette";

export type ActionHandler = () => void;
export type ActionTable = Partial<Record<KeymapAction, ActionHandler>>;

/**
 * Transient-mode keys that the leafcanvas pointer handlers consume.
 * These are not actions — they're modifier flags polled while a drag
 * is in flight.
 */
export type HeldKey = "z" | "space";

let actionTable: ActionTable = {};
const heldKeys = new Set<HeldKey>();
const heldListeners = new Set<() => void>();

/** Producer (AtlasShellInner mount) registers the action table. */
export function registerKeymap(table: ActionTable): () => void {
  actionTable = table;
  return () => {
    actionTable = {};
  };
}

/** Imperative read of the current action table — exposed for tests. */
export function __getActionTableForTesting(): ActionTable {
  return actionTable;
}

/**
 * Returns true when the supplied keyboard event should be dispatched
 * to a canvas action. False means "let the browser handle it" (or
 * "the editable target gets it").
 *
 * Exported so tests can pin the focus predicate without going through
 * full DOM mounting.
 */
export function isCanvasKeymapEligible(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) {
    // Document-level events (no specific target). Fall back to the
    // active element — fine in practice; tests can override by
    // dispatching against a known element.
    target = document.activeElement;
    if (!(target instanceof Element)) return false;
  }
  if (isEditableElement(target)) return false;
  return target.closest(".lc-stage") !== null;
}

function isEditableElement(el: Element): boolean {
  const tag = el.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  const htmlEl = el as HTMLElement;
  if (htmlEl.isContentEditable) return true;
  return false;
}

/** Imperative read of held transient-mode keys. */
export function getHeldKey(): HeldKey | null {
  if (heldKeys.has("space")) return "space";
  if (heldKeys.has("z")) return "z";
  return null;
}

/** Subscribe to held-key transitions (for pointer-handlers that re-arm). */
export function subscribeHeldKey(cb: () => void): () => void {
  heldListeners.add(cb);
  return () => {
    heldListeners.delete(cb);
  };
}

/** Test helper — clear held keys and listeners. */
export function __resetKeymapForTesting(): void {
  actionTable = {};
  heldKeys.clear();
  heldListeners.clear();
}

/**
 * Compute the action name for a keyboard event, or null if no match.
 * Exported for unit tests; the dispatcher just calls this then looks
 * up the action in the table.
 */
export function matchAction(e: KeyboardEvent): KeymapAction | null {
  const mod = e.metaKey || e.ctrlKey;
  // Camera fits + zoom.
  if (e.shiftKey && e.code === "Digit1") return "canvas.fit-all";
  if (e.shiftKey && e.code === "Digit2") return "canvas.fit-selection";
  if (mod && e.code === "Digit0") return "canvas.zoom-100";
  if (!mod && !e.shiftKey && (e.key === "+" || e.key === "=")) return "canvas.zoom-in";
  if (!mod && !e.shiftKey && (e.key === "-" || e.key === "_")) return "canvas.zoom-out";

  // Named-frame nav.
  if (!mod && e.shiftKey && e.code === "KeyN") return "canvas.prev-named-frame";
  if (!mod && !e.shiftKey && e.code === "KeyN") return "canvas.next-named-frame";

  // Mode flag.
  if (!mod && e.shiftKey && e.code === "KeyD") return "mode.toggle-dev-mode";
  if (!mod && !e.shiftKey && e.code === "KeyH") return "mode.toggle-hand-tool";

  // Search palette (U3b's Cmd+F).
  if (mod && e.code === "KeyF") return "search.open-palette";

  // Selection-dependent (U4 fills the action body; the dispatch wiring
  // is here so muscle memory works the moment U4 lands).
  if (e.key === "Escape") return "selection.escape-layered";
  if (mod && e.code === "KeyA") return "selection.select-all";
  if (!mod && !e.shiftKey && e.key === "Tab") return "selection.next-sibling";
  if (!mod && e.shiftKey && e.key === "Tab") return "selection.prev-sibling";
  if (!mod && !e.shiftKey && e.key === "Enter") return "selection.descend";
  if (!mod && e.shiftKey && e.key === "Enter") return "selection.ascend";
  // Backslash is the second key Figma binds to "ascend"; honor it.
  if (!mod && !e.shiftKey && e.key === "\\") return "selection.ascend";

  return null;
}

/**
 * Window-level keydown handler. Mounted once by AtlasShellInner via
 * `installKeymap`. Returns the install/uninstall pair so AtlasShellInner
 * can manage lifecycle inside its existing leaf-open useEffect.
 */
export function installKeymap(): () => void {
  function handleKeydown(e: KeyboardEvent): void {
    // Track held transient modes regardless of focus — releasing Z
    // outside the canvas should still clear the flag so a subsequent
    // pointer-drag doesn't re-arm a stale mode.
    if (e.code === "KeyZ") {
      if (!heldKeys.has("z")) {
        heldKeys.add("z");
        notifyHeld();
      }
    }
    if (e.code === "Space") {
      if (!heldKeys.has("space")) {
        heldKeys.add("space");
        notifyHeld();
      }
    }

    if (!isCanvasKeymapEligible(e.target)) return;

    const action = matchAction(e);
    if (!action) return;
    const handler = actionTable[action];
    if (!handler) return;

    // canvasGestureTracker.tick() so any in-flight gesture-end work
    // (e.g., MeasurementOverlay paint) treats the hotkey as part of
    // the gesture. This matches the pattern in leafcanvas.tsx's wheel
    // handler — every input route ticks the tracker so settle-after
    // logic kicks in uniformly.
    canvasGestureTracker.tick();

    // Hotkeys preventDefault so the browser's default never collides:
    // Cmd+0 (browser reset zoom), Cmd+F (browser find), Cmd+A (select
    // page text), +/- (no browser default usually), Shift+1/2 (most
    // browsers no-op). Always-preventDefault is safe because we
    // already pass the focus gate.
    e.preventDefault();
    handler();
  }

  function handleKeyup(e: KeyboardEvent): void {
    if (e.code === "KeyZ" && heldKeys.has("z")) {
      heldKeys.delete("z");
      notifyHeld();
    }
    if (e.code === "Space" && heldKeys.has("space")) {
      heldKeys.delete("space");
      notifyHeld();
    }
  }

  function handleBlur(): void {
    if (heldKeys.size === 0) return;
    heldKeys.clear();
    notifyHeld();
  }

  window.addEventListener("keydown", handleKeydown);
  window.addEventListener("keyup", handleKeyup);
  window.addEventListener("blur", handleBlur);

  return () => {
    window.removeEventListener("keydown", handleKeydown);
    window.removeEventListener("keyup", handleKeyup);
    window.removeEventListener("blur", handleBlur);
  };
}

function notifyHeld(): void {
  const snapshot = [...heldListeners];
  for (const fn of snapshot) {
    try {
      fn();
    } catch {
      // Swallow listener errors.
    }
  }
}

// ─── HMR / test re-evaluation guard ────────────────────────────────────
declare global {
  // eslint-disable-next-line no-var
  var __lcKeymapWired: boolean | undefined;
}
if (!globalThis.__lcKeymapWired) {
  globalThis.__lcKeymapWired = true;
}
